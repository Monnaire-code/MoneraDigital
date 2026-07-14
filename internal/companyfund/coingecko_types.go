package companyfund

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
)

const (
	CoinGeckoDefaultBaseURL     = "https://api.coingecko.com/api/v3"
	defaultCoinGeckoHTTPTimeout = 10 * time.Second
	maxCoinGeckoResponseBytes   = 1 << 20
	maxCoinGeckoBatchAssets     = 515
)

var (
	ErrCoinGeckoNetwork           = errors.New("coingecko network request failed")
	ErrCoinGeckoRateLimited       = errors.New("coingecko rate limited")
	ErrCoinGeckoClientResponse    = errors.New("coingecko client response error")
	ErrCoinGeckoServerResponse    = errors.New("coingecko server response error")
	ErrCoinGeckoMalformedResponse = errors.New("coingecko malformed response")
	ErrCoinGeckoResponseTooLarge  = errors.New("coingecko response exceeds size limit")
)

// CoinGeckoClientConfig configures the small REST-only current-price client.
// The Demo key is deliberately sent only in the documented request header and
// never appended to a URL or included in returned error text.
type CoinGeckoClientConfig struct {
	BaseURL    string
	DemoAPIKey string
	HTTPClient *http.Client
	Clock      func() time.Time
}

// CoinGeckoSimplePriceRequest is one batch request over explicitly configured
// CoinGecko IDs. IDs are not inferred from display symbols.
type CoinGeckoSimplePriceRequest struct {
	CoinIDs         []string
	QuoteCurrencies []string
}

// CoinGeckoTokenPriceRequest targets contract assets using the provider's
// platform and contract mapping rather than an ambiguous ticker symbol.
type CoinGeckoTokenPriceRequest struct {
	PlatformID        string
	ContractAddresses []string
	QuoteCurrencies   []string
}

// CoinGeckoExchangeRate is one BTC-relative rate returned by CoinGecko's
// /exchange_rates endpoint. Value means units of Code per one BTC; it is not
// itself a USD value. Current fiat valuation derives USD-per-fiat only by
// dividing the USD and configured-fiat values from the same response.
type CoinGeckoExchangeRate struct {
	Code  string
	Unit  string
	Type  string
	Value decimal.Decimal
}

// CoinGeckoExchangeRatesBatch carries one exact response snapshot. The
// endpoint does not provide a usable per-rate provider timestamp, so FetchedAt
// is explicitly the observation time for CURRENT/PROVISIONAL use only; it
// must never be interpreted as a transaction-time historical observation.
type CoinGeckoExchangeRatesBatch struct {
	Rates          map[string]CoinGeckoExchangeRate
	FetchedAt      time.Time
	ResponseDigest string
}

// CoinGeckoPriceUnavailableReason makes a missing or bad provider field
// explicit. A nil Quote always means unavailable; numeric zero is never used
// as a fallback value.
type CoinGeckoPriceUnavailableReason string

const (
	CoinGeckoPriceUnavailableMissingAsset     CoinGeckoPriceUnavailableReason = "MISSING_ASSET"
	CoinGeckoPriceUnavailableMissingQuote     CoinGeckoPriceUnavailableReason = "MISSING_QUOTE"
	CoinGeckoPriceUnavailableInvalidPrice     CoinGeckoPriceUnavailableReason = "INVALID_PRICE"
	CoinGeckoPriceUnavailableNonPositive      CoinGeckoPriceUnavailableReason = "NON_POSITIVE_PRICE"
	CoinGeckoPriceUnavailableMissingTimestamp CoinGeckoPriceUnavailableReason = "MISSING_TIMESTAMP"
)

// CoinGeckoQuote is an exact positive price plus the provider and local audit
// times. It contains no float and can be copied safely into an immutable cache
// snapshot.
type CoinGeckoQuote struct {
	Price               decimal.Decimal
	ProviderUpdatedAt   time.Time
	FetchedAt           time.Time
	ResponseDigest      string
	Method              USDValuationMethod
	BTCCrossNumerator   decimal.Decimal
	BTCCrossDenominator decimal.Decimal
	// RateSnapshotID is zero only for an explicitly non-persistent test or
	// standalone cache. The company-fund runtime persists provider facts before
	// publishing a quote and carries the resulting audit FK with every read.
	RateSnapshotID int64
}

func (quote CoinGeckoQuote) valuationMethod() USDValuationMethod {
	if quote.Method == "" {
		return USDValuationMethodCoinGeckoDirect
	}
	return quote.Method
}

// CoinGeckoPrice represents one requested asset/currency pair. Quote is nil
// for a partial, missing, malformed, or non-positive provider value.
type CoinGeckoPrice struct {
	CoinID            string
	PlatformID        string
	ContractAddress   string
	QuoteCurrency     string
	Quote             *CoinGeckoQuote
	UnavailableReason CoinGeckoPriceUnavailableReason
}

// CoinGeckoPriceBatch preserves the full response digest and one local fetch
// time for all pairs returned by one provider response.
type CoinGeckoPriceBatch struct {
	Prices         []CoinGeckoPrice
	FetchedAt      time.Time
	ResponseDigest string
}

// CoinGeckoHTTPError classifies HTTP status without retaining a provider body
// or request URL. RetryAfter is populated only for a valid 429 header.
type CoinGeckoHTTPError struct {
	StatusCode int
	RetryAfter *time.Duration
}

func (e *CoinGeckoHTTPError) Error() string {
	return fmt.Sprintf("coingecko HTTP response status %d", e.StatusCode)
}

func (e *CoinGeckoHTTPError) Unwrap() error {
	switch {
	case e.StatusCode == 429:
		return ErrCoinGeckoRateLimited
	case e.StatusCode >= 500:
		return ErrCoinGeckoServerResponse
	default:
		return ErrCoinGeckoClientResponse
	}
}
