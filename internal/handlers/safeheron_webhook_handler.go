package handlers

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

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

// SafeheronWebhookHandler is the sync side of the deposit pipeline.
type SafeheronWebhookHandler struct {
	Verifier WebhookVerifier
	Recorder WebhookEventRecorder
}

// NewSafeheronWebhookHandler wires the public webhook receiver.
func NewSafeheronWebhookHandler(v WebhookVerifier, r WebhookEventRecorder) *SafeheronWebhookHandler {
	return &SafeheronWebhookHandler{Verifier: v, Recorder: r}
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

	// Plan D-12: http.MaxBytesReader caps body at 1MB AND surfaces an explicit
	// *http.MaxBytesError on overflow — unlike io.LimitReader which silently
	// truncates and would let attackers slip past the verifier with garbage.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxWebhookBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			log.Printf("safeheron webhook body exceeds %d bytes: %v", MaxWebhookBodyBytes, err)
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		log.Printf("safeheron webhook read body error: %v", err)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	evt, err := h.Verifier.WebhookConvert(body)
	if err != nil {
		log.Printf("safeheron webhook verify failed: %v", err)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	eventID := evt.EventDetail.TxKey + ":" + evt.EventDetail.TransactionStatus
	if evt.EventDetail.TxKey == "" {
		log.Printf("safeheron webhook missing txKey, eventType=%s", evt.EventType)
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// T7-I-4: store the verbatim webhook body — preserves the outer envelope
	// (timestamp / signature / bizContent ciphertext) AND every inner field
	// the SDK's WebhookEvent struct doesn't model (replaceTxHash,
	// destinationAddressList, custom metadata). Forensic replay can re-verify
	// signatures or replay decryption against the stored body.
	inserted, err := h.Recorder.InsertEventOrSkip(c.Request.Context(), &deposit.Event{
		EventID:        eventID,
		EventType:      evt.EventType,
		SafeheronTxKey: evt.EventDetail.TxKey,
		CustomerRefID:  evt.EventDetail.CustomerRefID,
		RawPayload:     body,
	})
	if err != nil {
		// A DB outage is the only reasonable cause; let Safeheron retry by
		// returning 5xx (does not match the ack body, deliberately).
		log.Printf("safeheron webhook insert failed eventId=%s: %v", eventID, err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if !inserted {
		log.Printf("safeheron webhook duplicate eventId=%s — replying SUCCESS", eventID)
	}

	c.Header("Content-Type", "application/json")
	c.String(http.StatusOK, SafeheronAckBody)
}
