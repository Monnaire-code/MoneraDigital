package companyfund

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	AirwallexDefaultBaseURL                      = "https://api.airwallex.com"
	defaultAirwallexHTTPTimeout                  = 10 * time.Second
	defaultAirwallexTokenRefreshSkew             = time.Minute
	maxAirwallexResponseBytes                    = 1 << 20
	defaultAirwallexFinancialTransactionPageSize = 100
	maxAirwallexFinancialTransactionPageSize     = 1000
	maxAirwallexFinancialTransactionPageNumber   = 2000
	maxAirwallexFinancialTransactionIDBytes      = 256
	defaultAirwallexWebhookTimestampTolerance    = 5 * time.Minute
)

var (
	ErrAirwallexNetwork                       = errors.New("airwallex network request failed")
	ErrAirwallexUnauthorized                  = errors.New("airwallex authentication failed")
	ErrAirwallexClientResponse                = errors.New("airwallex client response error")
	ErrAirwallexServerResponse                = errors.New("airwallex server response error")
	ErrAirwallexMalformedResponse             = errors.New("airwallex malformed response")
	ErrAirwallexResponseRead                  = errors.New("airwallex response read failed")
	ErrAirwallexResponseTooLarge              = errors.New("airwallex response exceeds size limit")
	ErrAirwallexWebhookInvalidSignature       = errors.New("airwallex webhook signature is invalid")
	ErrAirwallexWebhookInvalidTimestamp       = errors.New("airwallex webhook timestamp is invalid")
	ErrAirwallexWebhookTimestampOutsideWindow = errors.New("airwallex webhook timestamp is outside the allowed window")
)

// AirwallexClientConfig keeps long-lived credentials inside the Go backend.
// HTTPClient is cloned to disable redirects before any credential-bearing call.
type AirwallexClientConfig struct {
	BaseURL  string
	ClientID string
	APIKey   string
	// APIVersion pins the date-versioned contract used by business API calls.
	// It must be an exact YYYY-MM-DD value; the authentication endpoint uses
	// the separately documented credential-header contract.
	APIVersion       string
	LoginAs          string
	HTTPClient       *http.Client
	Clock            func() time.Time
	TokenRefreshSkew time.Duration
}

// AirwallexHTTPError classifies a status without retaining provider bodies,
// request URLs, API credentials, or bearer tokens.
type AirwallexHTTPError struct {
	StatusCode int
}

func (e *AirwallexHTTPError) Error() string {
	return fmt.Sprintf("airwallex HTTP response status %d", e.StatusCode)
}

func (e *AirwallexHTTPError) Unwrap() error {
	switch {
	case e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden:
		return ErrAirwallexUnauthorized
	case e.StatusCode >= http.StatusInternalServerError:
		return ErrAirwallexServerResponse
	default:
		return ErrAirwallexClientResponse
	}
}

// AirwallexFinancialTransactionsRequest intentionally requires a bounded time
// window. The Financial Transactions API uses zero-based page_num pagination;
// it does not expose a cursor contract.
type AirwallexFinancialTransactionsRequest struct {
	FromCreatedAt time.Time
	ToCreatedAt   time.Time
	PageNum       int
	PageSize      int
}

// AirwallexFinancialTransaction preserves provider values before a later
// normalizer maps them into company-fund movement semantics. Numeric fields
// remain raw JSON so neither the REST boundary nor json decoding introduces a
// floating-point conversion. Raw keeps the complete provider object for
// encrypted reconciliation persistence.
type AirwallexFinancialTransaction struct {
	ProviderID         string
	Amount             json.RawMessage
	BatchID            string
	ClientRate         json.RawMessage
	CreatedAt          string
	Currency           string
	CurrencyPair       string
	Description        string
	EstimatedSettledAt string
	Fee                json.RawMessage
	FundingSourceID    string
	Net                json.RawMessage
	SettledAt          string
	SourceID           string
	SourceType         string
	Status             string
	TransactionType    string
	Raw                json.RawMessage
}

// AirwallexFinancialTransactionsPage mirrors the documented list envelope.
type AirwallexFinancialTransactionsPage struct {
	Items   []AirwallexFinancialTransaction
	HasMore bool
}

// AirwallexWebhookVerifierConfig validates the webhook delivery boundary only;
// it performs no HTTP handling, parsing, storage, or retry acknowledgement.
type AirwallexWebhookVerifierConfig struct {
	Secret string
	MaxAge time.Duration
	Clock  func() time.Time
}
