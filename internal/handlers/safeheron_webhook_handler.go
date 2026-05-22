package handlers

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/approval"
	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

// SafeheronAckBody is the exact body Safeheron requires for a successful ack.
// Any deviation (different message, non-200 status) triggers the 6-attempt
// retry storm — see SPEC §6.4 and sandbox findings.
const SafeheronAckBody = `{"code":"200","message":"SUCCESS"}`

// MaxWebhookBodyBytes caps the inbound webhook body (defence in depth — the
// SDK envelope is well under 16KB; 1MB leaves comfortable headroom).
const MaxWebhookBodyBytes = 1 << 20

// WebhookVerifier verifies + decrypts the Safeheron envelope.
type WebhookVerifier interface {
	WebhookConvert(rawBody []byte) (*safeheron.WebhookEvent, error)
}

// WebhookEventRecorder stores the raw decrypted envelope idempotently.
type WebhookEventRecorder interface {
	InsertEventOrSkip(ctx context.Context, evt *deposit.Event) (inserted bool, err error)
}

// SweepUpdater updates sweep_transactions status from outgoing webhook events.
type SweepUpdater interface {
	UpdateSweepStatus(ctx context.Context, txKey, status, subStatus, txHash string, completedAt *time.Time) error
}

// WebhookAlertFn is called to send operational alerts from the webhook handler.
type WebhookAlertFn func(level, title string, fields map[string]string)

// SafeheronWebhookHandler is the sync side of the deposit pipeline.
type SafeheronWebhookHandler struct {
	Verifier     WebhookVerifier
	Recorder     WebhookEventRecorder
	SweepUpdater SweepUpdater
	AllowedIPs   []string
	AlertFn      WebhookAlertFn
}

// NewSafeheronWebhookHandler wires the public webhook receiver.
func NewSafeheronWebhookHandler(v WebhookVerifier, r WebhookEventRecorder, allowedIPs []string) *SafeheronWebhookHandler {
	return &SafeheronWebhookHandler{Verifier: v, Recorder: r, AllowedIPs: allowedIPs}
}

// SetSweepUpdater injects the sweep_transactions updater (optional, added by WithCosignerCallback).
func (h *SafeheronWebhookHandler) SetSweepUpdater(u SweepUpdater) {
	h.SweepUpdater = u
}

// SetAlertFn injects the operational alert function (optional).
func (h *SafeheronWebhookHandler) SetAlertFn(fn WebhookAlertFn) {
	h.AlertFn = fn
}

// Receive handles POST /api/webhooks/safeheron. It:
//
//  1. Reads at most MaxWebhookBodyBytes from the body
//  2. Verifies signature + decrypts via Safeheron SDK
//  3. INSERTs into safeheron_webhook_events (ON CONFLICT DO NOTHING)
//  4. Returns the exact ack body Safeheron requires
//
// Any failure prior to the INSERT returns 401 and skips DB writes. Insert
// failures still ack the webhook (already-stored events return inserted=false
// and we still ack), but other DB errors return 500 so Safeheron retries.
func (h *SafeheronWebhookHandler) Receive(c *gin.Context) {
	if h == nil || h.Verifier == nil || h.Recorder == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "WEBHOOK_UNAVAILABLE",
			"message": "Safeheron webhook handler not initialised",
		})
		return
	}

	clientIP := c.ClientIP()

	// D-42: IP whitelist — reject before reading body or running RSA verify
	if len(h.AllowedIPs) > 0 {
		allowed := false
		for _, ip := range h.AllowedIPs {
			if ip == clientIP {
				allowed = true
				break
			}
		}
		if !allowed {
			log.Printf("safeheron webhook rejected: IP %s not in allowlist", clientIP)
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}

	log.Printf("[webhook] received ip=%s", clientIP)

	// Plan D-12: http.MaxBytesReader caps body at 1MB AND surfaces an explicit
	// *http.MaxBytesError on overflow — unlike io.LimitReader which silently
	// truncates and would let attackers slip past the verifier with garbage.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxWebhookBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			log.Printf("[webhook] REJECT ip=%s body exceeds %d bytes", clientIP, MaxWebhookBodyBytes)
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		log.Printf("[webhook] REJECT ip=%s read body error: %v", clientIP, err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		log.Printf("[webhook] REJECT ip=%s empty body", clientIP)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	log.Printf("[webhook] verifying ip=%s bytes=%d", clientIP, len(body))
	evt, err := h.Verifier.WebhookConvert(body)
	if err != nil {
		sum := sha256.Sum256(body)
		log.Printf("[webhook] REJECT ip=%s verify failed bodyHash=%s err=%v", clientIP, hex.EncodeToString(sum[:8]), err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	eventID := evt.EventDetail.TxKey + ":" + evt.EventDetail.TransactionStatus
	if evt.EventType == "AML_KYT_ALERT" {
		// AML alerts share txKey across multiple distinct events (provider rescans,
		// follow-up findings). A wall-clock suffix would break dedup entirely — every
		// Safeheron retry of an identical body would slip past ON CONFLICT and
		// inflate storage. A content hash keeps Safeheron's own retries idempotent
		// while letting genuinely new alert content through.
		sum := sha256.Sum256(body)
		eventID = evt.EventDetail.TxKey + ":AML_KYT_ALERT:" + hex.EncodeToString(sum[:8])
	}
	if evt.EventDetail.TxKey == "" {
		log.Printf("[webhook] REJECT ip=%s eventType=%s missing txKey", clientIP, evt.EventType)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	log.Printf("[webhook] OK ip=%s eventType=%s txKey=%s status=%s bodyLen=%d",
		clientIP, evt.EventType, evt.EventDetail.TxKey, evt.EventDetail.TransactionStatus, len(evt.RawBody))

	// Use the raw decrypted plaintext as the stored payload so that fields
	// not captured by safeheron.EventDetail (e.g. AML_KYT_ALERT's amlList,
	// amlScreeningTriggeredState) are preserved for the async worker.
	decryptedPayload := evt.RawBody
	if len(decryptedPayload) == 0 {
		log.Printf("[webhook] REJECT ip=%s empty RawBody after verify", clientIP)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	inserted, err := h.Recorder.InsertEventOrSkip(c.Request.Context(), &deposit.Event{
		EventID:        eventID,
		EventType:      evt.EventType,
		SafeheronTxKey: evt.EventDetail.TxKey,
		CustomerRefID:  evt.EventDetail.CustomerRefID,
		RawPayload:     decryptedPayload,
	})
	if err != nil {
		// A DB outage is the only reasonable cause; let Safeheron retry by
		// returning 5xx (does not match the ack body, deliberately).
		log.Printf("[webhook] ERROR ip=%s insert failed eventId=%s: %v", clientIP, eventID, err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !inserted {
		log.Printf("[webhook] DUPLICATE ip=%s eventType=%s txKey=%s eventId=%s — ack SUCCESS",
			clientIP, evt.EventType, evt.EventDetail.TxKey, eventID)
	} else {
		log.Printf("[webhook] STORED ip=%s eventType=%s txKey=%s eventId=%s",
			clientIP, evt.EventType, evt.EventDetail.TxKey, eventID)
	}

	// 出向交易（归集）状态更新 — 竞态待验证 spec §5.2
	if h.SweepUpdater != nil && evt.EventDetail.TransactionDirection == "SEND" {
		var completedAt *time.Time
		if evt.EventDetail.TransactionStatus == "COMPLETED" || evt.EventDetail.TransactionStatus == "FAILED" {
			now := time.Now()
			completedAt = &now
		}
		err := h.SweepUpdater.UpdateSweepStatus(
			c.Request.Context(),
			evt.EventDetail.TxKey,
			evt.EventDetail.TransactionStatus,
			evt.EventDetail.TransactionSubStatus,
			evt.EventDetail.TxHash,
			completedAt,
		)
		if err != nil {
			if errors.Is(err, approval.ErrSweepNotFound) {
				log.Printf("[webhook] WARN: sweep txKey=%s not in sweep_transactions (unexpected — cosigner callback may not have arrived yet)", evt.EventDetail.TxKey)
			} else if errors.Is(err, sql.ErrNoRows) {
				log.Printf("[webhook] sweep txKey=%s already terminal, ignoring", evt.EventDetail.TxKey)
			} else {
				log.Printf("[webhook] ERROR updating sweep txKey=%s: %v", evt.EventDetail.TxKey, err)
				if h.AlertFn != nil {
					h.AlertFn("ERROR", "归集状态更新失败", map[string]string{
						"txKey":  evt.EventDetail.TxKey,
						"status": evt.EventDetail.TransactionStatus,
						"error":  err.Error(),
					})
				}
			}
		} else {
			log.Printf("[webhook] sweep updated txKey=%s status=%s", evt.EventDetail.TxKey, evt.EventDetail.TransactionStatus)
		}
	}

	c.Header("Content-Type", "application/json")
	c.String(http.StatusOK, SafeheronAckBody)
}
