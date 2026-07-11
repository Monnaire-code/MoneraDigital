package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
)

// AirwallexWebhookSignatureVerifier validates timestamp/signature headers over
// the exact raw request bytes before the handler parses a JSON envelope.
type AirwallexWebhookSignatureVerifier interface {
	Verify(timestamp, signature string, rawBody []byte) error
}

// AirwallexWebhookPayloadIngestor is satisfied by
// companyfund.OwnedProviderPayloadService. Keeping this narrow permits the
// HTTP boundary to persist one encrypted, idempotent delivery without doing
// provider enrichment or transaction normalization synchronously.
type AirwallexWebhookPayloadIngestor interface {
	Ingest(ctx context.Context, input companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error)
}

// CompanyFundAirwallexWebhookHandlerConfig contains only the encrypted inbox
// settings. Routing and dependency injection are deliberately left to a later
// composition step.
type CompanyFundAirwallexWebhookHandlerConfig struct {
	Verifier AirwallexWebhookSignatureVerifier
	Ingestor AirwallexWebhookPayloadIngestor
	// Wake is an optional, nonblocking notification owned by the runtime. It
	// is called only after a verified delivery has been durably inserted (or
	// idempotently found) in the encrypted inbox. It deliberately receives no
	// webhook account or organisation identifier: reconciliation chooses its
	// account scope exclusively from trusted management configuration.
	Wake func()
	// Eligible is an optional dynamic ownership gate. When the configured
	// account cache no longer proves a single x-login-as scope, the endpoint
	// must not accept or enqueue a webhook that could later be attributed to
	// an unproven company account.
	Eligible             func() bool
	ProviderEventVersion string
	KeyVersion           string
	Retention            time.Duration
	LegalHold            bool
}

const maxCompanyFundAirwallexProviderEventVersionBytes = 64

// CompanyFundAirwallexWebhookHandler validates and durably stores raw provider
// deliveries. It deliberately contains no provider HTTP calls or normalizer.
type CompanyFundAirwallexWebhookHandler struct {
	verifier             AirwallexWebhookSignatureVerifier
	ingestor             AirwallexWebhookPayloadIngestor
	wake                 func()
	eligible             func() bool
	providerEventVersion string
	keyVersion           string
	retention            time.Duration
	legalHold            bool
}

func NewCompanyFundAirwallexWebhookHandler(config CompanyFundAirwallexWebhookHandlerConfig) (*CompanyFundAirwallexWebhookHandler, error) {
	if config.Verifier == nil || config.Ingestor == nil {
		return nil, fmt.Errorf("airwallex webhook verifier and ingestor are required")
	}
	keyVersion := strings.TrimSpace(config.KeyVersion)
	if keyVersion == "" {
		return nil, fmt.Errorf("airwallex webhook payload key version is required")
	}
	providerEventVersion := strings.TrimSpace(config.ProviderEventVersion)
	if config.ProviderEventVersion != providerEventVersion || len(providerEventVersion) > maxCompanyFundAirwallexProviderEventVersionBytes {
		return nil, fmt.Errorf("airwallex webhook provider event version must be a bounded exact value")
	}
	if config.Retention <= 0 || config.Retention.Microseconds() <= 0 {
		return nil, fmt.Errorf("airwallex webhook payload retention must be at least one microsecond")
	}
	return &CompanyFundAirwallexWebhookHandler{
		verifier:             config.Verifier,
		ingestor:             config.Ingestor,
		wake:                 config.Wake,
		eligible:             config.Eligible,
		providerEventVersion: providerEventVersion,
		keyVersion:           keyVersion,
		retention:            config.Retention,
		legalHold:            config.LegalHold,
	}, nil
}

// Receive accepts a raw Airwallex delivery. Every non-200 response is a safe
// status-only reply; neither raw body nor verification/storage details escape.
func (h *CompanyFundAirwallexWebhookHandler) Receive(c *gin.Context) {
	if h == nil || h.verifier == nil || h.ingestor == nil || c == nil || c.Request == nil || c.Request.Body == nil {
		if c != nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
		return
	}
	if h.eligible != nil && !h.eligible() {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	timestamp := c.GetHeader("x-timestamp")
	signature := c.GetHeader("x-signature")
	if timestamp == "" || signature == "" {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, companyfund.MaxOwnedProviderPayloadPlaintextBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxError *http.MaxBytesError
		if errors.As(err, &maxError) {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if len(body) == 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if h.verifier.Verify(timestamp, signature, body) != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	envelope, valid := decodeAirwallexWebhookEnvelope(body)
	if !valid {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	_, err = h.ingestor.Ingest(c.Request.Context(), companyfund.OwnedProviderPayloadInput{
		Channel:              companyfund.ChannelAirwallex,
		ProviderEventID:      envelope.ID,
		EventType:            envelope.Name,
		ProviderEventVersion: h.providerEventVersion,
		ProviderOrgKey:       envelope.OrgID,
		ProviderAccountKey:   envelope.AccountID,
		Body:                 append([]byte(nil), body...),
		KeyVersion:           h.keyVersion,
		Retention:            h.retention,
		LegalHold:            h.legalHold,
	})
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if h.wake != nil {
		h.wake()
	}
	c.Status(http.StatusOK)
}

type airwallexWebhookEnvelope struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
	OrgID     string `json:"org_id"`
}

func decodeAirwallexWebhookEnvelope(body []byte) (airwallexWebhookEnvelope, bool) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	var envelope airwallexWebhookEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return airwallexWebhookEnvelope{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return airwallexWebhookEnvelope{}, false
	}
	if strings.TrimSpace(envelope.ID) == "" || strings.TrimSpace(envelope.Name) == "" {
		return airwallexWebhookEnvelope{}, false
	}
	return envelope, true
}
