package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
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

const safeheronWebhookEventIdentityVersion = "v2"

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
	Verifier                  WebhookVerifier
	Recorder                      WebhookEventRecorder
	AllowedIPs                    []string
	companyFundSourceLookup       SafeheronEventSourceLookup
	companyFundProviderBridge     SafeheronCompanyFundProviderBridge
	companyFundEligibility        companyfund.SafeheronWebhookEligibility
	companyFundProviderEventWake  func()
	depositWorkerWake             func()
}

// NewSafeheronWebhookHandler wires the public webhook receiver.
func NewSafeheronWebhookHandler(v WebhookVerifier, r WebhookEventRecorder, allowedIPs []string) *SafeheronWebhookHandler {
	return &SafeheronWebhookHandler{Verifier: v, Recorder: r, AllowedIPs: allowedIPs}
}

// SetCompanyFundBridge attaches the optional independent company-fund
// consumer. It is intentionally configured separately so the existing deposit
// pipeline remains source-compatible and exclusively owns process_status.
func (h *SafeheronWebhookHandler) SetCompanyFundBridge(sourceLookup SafeheronEventSourceLookup, bridge SafeheronCompanyFundProviderBridge) {
	if h == nil {
		return
	}
	h.companyFundSourceLookup = sourceLookup
	h.companyFundProviderBridge = bridge
}

// SetCompanyFundEligibility enables the independent company-fund consumer for
// verified Safeheron raw events. Without this service, bridge delivery fails
// closed and the deposit webhook remains exclusively a deposit-owned flow.
func (h *SafeheronWebhookHandler) SetCompanyFundEligibility(eligibility companyfund.SafeheronWebhookEligibility) {
	if h == nil {
		return
	}
	h.companyFundEligibility = eligibility
}

// SetCompanyFundProviderEventWake attaches an optional process-local wake used
// only after a provider event has been durably inserted. The callback is
// advisory: durable event state remains the source of truth.
func (h *SafeheronWebhookHandler) SetCompanyFundProviderEventWake(wake func()) {
	if h == nil {
		return
	}
	h.companyFundProviderEventWake = wake
}

// SetDepositWorkerWake attaches an optional process-local wake used only after
// a Safeheron webhook event has been durably inserted into the deposit inbox.
func (h *SafeheronWebhookHandler) SetDepositWorkerWake(wake func()) {
	if h == nil {
		return
	}
	h.depositWorkerWake = wake
}

// Receive handles POST /api/webhooks/safeheron. It:
//
//  1. Reads at most MaxWebhookBodyBytes from the body
//  2. Verifies signature + decrypts via Safeheron SDK
//  3. Persists the decrypted raw event and its SHA-256 digest idempotently
//  4. Records negative company-fund eligibility or creates an eligible provider-event reference
//  5. Returns the exact ack body Safeheron requires
//
// Signature/decryption failures return 401 and skip DB writes. Raw-event
// persistence failures return 5xx so Safeheron retries. Once the raw event is
// durable, an eligibility-marker failure returns 5xx so Safeheron can retry.
// A positive candidate bridge failure is left for the collector and still
// acknowledges the provider's delivery.
func (h *SafeheronWebhookHandler) Receive(c *gin.Context) {
	if h == nil || h.Verifier == nil || h.Recorder == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "WEBHOOK_UNAVAILABLE",
			"message": "Safeheron webhook handler not initialised",
		})
		return
	}

	// D-42: IP whitelist — reject before reading body or running RSA verify
	if len(h.AllowedIPs) > 0 {
		clientIP := c.ClientIP()
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

	clientIP := c.ClientIP()
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
		log.Printf("[webhook] REJECT ip=%s verify failed bodyHash=%s", clientIP, hex.EncodeToString(sum[:8]))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
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
	payloadDigestSum := sha256.Sum256(decryptedPayload)
	payloadDigest := hex.EncodeToString(payloadDigestSum[:])
	// The event ID is a fixed SHA-256 content address. Including txKey, type,
	// status, and the digest of the actual decrypted bytes keeps delivery
	// retries stable while allowing different payload revisions (including AML
	// rescans for one txKey) to coexist. The version is inside the hashed tuple
	// so the persisted value remains exactly lowercase 64-hex characters.
	eventID := safeheronWebhookContentEventID(
		evt.EventDetail.TxKey,
		evt.EventType,
		evt.EventDetail.TransactionStatus,
		payloadDigest,
	)

	inserted, err := h.Recorder.InsertEventOrSkip(c.Request.Context(), &deposit.Event{
		EventID:        eventID,
		EventType:      evt.EventType,
		SafeheronTxKey: evt.EventDetail.TxKey,
		CustomerRefID:  evt.EventDetail.CustomerRefID,
		RawPayload:     decryptedPayload,
		PayloadDigest:  payloadDigest,
	})
	if err != nil {
		// A DB outage is the only reasonable cause; let Safeheron retry by
		// returning 5xx (does not match the ack body, deliberately).
		log.Printf("[webhook] ERROR ip=%s insert failed eventId=%s", clientIP, eventID)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	// Wake deposit/routing consumers only after durable persistence. Idempotent
	// duplicates still wake so a prior lost signal can be repaired.
	if h.depositWorkerWake != nil {
		h.depositWorkerWake()
	}
	if h.companyFundEligibility != nil {
		source, sourceErr := h.lookupCompanyFundSafeheronSource(c.Request.Context(), eventID, payloadDigest)
		if sourceErr != nil {
			// The eligibility decision cannot be durably tied to the persisted raw
			// event. Return 5xx so Safeheron's webhook retry policy repairs it.
			log.Printf("[webhook] ERROR ip=%s company-fund eligibility source eventIdHash=%s payloadDigest=%s",
				clientIP, safeheronWebhookHashSummary(eventID), safeheronWebhookHashSummary(payloadDigest))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		decision, eligibilityErr := h.companyFundEligibility.AssessAndRecord(c.Request.Context(), companyfund.SafeheronWebhookEligibilityInput{
			SafeheronWebhookEventID: source.ID,
			EventType:               evt.EventType,
			PayloadDigest:           payloadDigest,
			RawPayload:              decryptedPayload,
		})
		if eligibilityErr != nil {
			// A negative marker is part of the acknowledged delivery contract. Do
			// not acknowledge an event whose exclusion could not be made durable.
			log.Printf("[webhook] ERROR ip=%s company-fund eligibility marker eventIdHash=%s payloadDigest=%s",
				clientIP, safeheronWebhookHashSummary(eventID), safeheronWebhookHashSummary(payloadDigest))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if decision.Candidate {
			if err := h.bridgeCompanyFundProviderEvent(c.Request.Context(), eventID, evt.EventType, payloadDigest, source); err != nil {
				// The raw event is already durable. Do not expose bridge internals or raw
				// content in logs; the anti-join collector will repair this delivery.
				log.Printf("[webhook] DEFER ip=%s company-fund bridge eventIdHash=%s payloadDigest=%s",
					clientIP, safeheronWebhookHashSummary(eventID), safeheronWebhookHashSummary(payloadDigest))
			}
		}
	}
	if !inserted {
		log.Printf("[webhook] DUPLICATE ip=%s eventType=%s txKey=%s eventId=%s — ack SUCCESS",
			clientIP, evt.EventType, evt.EventDetail.TxKey, eventID)
	} else {
		log.Printf("[webhook] STORED ip=%s eventType=%s txKey=%s eventId=%s",
			clientIP, evt.EventType, evt.EventDetail.TxKey, eventID)
	}

	c.Header("Content-Type", "application/json")
	c.String(http.StatusOK, SafeheronAckBody)
}

func safeheronWebhookContentEventID(txKey, eventType, transactionStatus, payloadDigest string) string {
	parts := []string{
		"safeheron-webhook-event",
		safeheronWebhookEventIdentityVersion,
		txKey,
		eventType,
		transactionStatus,
		payloadDigest,
	}
	var canonical strings.Builder
	for _, part := range parts {
		canonical.WriteString(strconv.Itoa(len(part)))
		canonical.WriteByte(':')
		canonical.WriteString(part)
	}
	sum := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(sum[:])
}

func safeheronWebhookHashSummary(value string) string {
	const prefixLength = 12
	if len(value) <= prefixLength {
		return value
	}
	return value[:prefixLength]
}
