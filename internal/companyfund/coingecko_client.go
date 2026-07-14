package companyfund

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// CoinGeckoClient is intentionally a narrow REST client. Request reservation,
// durable snapshots, and cache scheduling belong to their own layers.
type CoinGeckoClient struct {
	baseURL    *url.URL
	demoAPIKey string
	httpClient *http.Client
	now        func() time.Time
}

func NewCoinGeckoClient(config CoinGeckoClientConfig) (*CoinGeckoClient, error) {
	baseURL := strings.TrimSpace(config.BaseURL)
	if baseURL == "" {
		baseURL = CoinGeckoDefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("coingecko base URL must be an absolute URL without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("coingecko base URL must use https")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultCoinGeckoHTTPTimeout}
	}
	now := config.Clock
	if now == nil {
		now = time.Now
	}
	return &CoinGeckoClient{
		baseURL:    parsed,
		demoAPIKey: config.DemoAPIKey,
		httpClient: coinGeckoHTTPClientWithoutRedirects(httpClient),
		now:        now,
	}, nil
}

func coinGeckoHTTPClientWithoutRedirects(source *http.Client) *http.Client {
	clone := *source
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func (c *CoinGeckoClient) FetchSimplePrices(ctx context.Context, request CoinGeckoSimplePriceRequest) (CoinGeckoPriceBatch, error) {
	if len(request.CoinIDs) > maxCoinGeckoBatchAssets {
		return CoinGeckoPriceBatch{}, fmt.Errorf("coingecko coin IDs must contain at most %d values", maxCoinGeckoBatchAssets)
	}
	coinIDs, err := normalizeCoinGeckoValues("coin IDs", request.CoinIDs, false)
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	quotes, err := normalizeCoinGeckoValues("quote currencies", request.QuoteCurrencies, true)
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	payload, fetchedAt, digest, err := c.fetchPricePayload(ctx, []string{"simple", "price"}, url.Values{
		"ids":                     []string{strings.Join(coinIDs, ",")},
		"vs_currencies":           []string{strings.Join(quotes, ",")},
		"include_last_updated_at": []string{"true"},
		"precision":               []string{"full"},
	})
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	return buildCoinGeckoPriceBatch(coinIDs, quotes, "", nil, payload, fetchedAt, digest), nil
}

func (c *CoinGeckoClient) FetchTokenPrices(ctx context.Context, request CoinGeckoTokenPriceRequest) (CoinGeckoPriceBatch, error) {
	platformID, err := normalizeCoinGeckoValue("platform ID", request.PlatformID, true)
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	if len(request.ContractAddresses) > maxCoinGeckoBatchAssets {
		return CoinGeckoPriceBatch{}, fmt.Errorf("coingecko contract addresses must contain at most %d values", maxCoinGeckoBatchAssets)
	}
	contracts, err := normalizeCoinGeckoValues("contract addresses", request.ContractAddresses, false)
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	quotes, err := normalizeCoinGeckoValues("quote currencies", request.QuoteCurrencies, true)
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	payload, fetchedAt, digest, err := c.fetchPricePayload(ctx, []string{"simple", "token_price", platformID}, url.Values{
		"contract_addresses":      []string{strings.Join(contracts, ",")},
		"vs_currencies":           []string{strings.Join(quotes, ",")},
		"include_last_updated_at": []string{"true"},
		"precision":               []string{"full"},
	})
	if err != nil {
		return CoinGeckoPriceBatch{}, err
	}
	return buildCoinGeckoPriceBatch(contracts, quotes, platformID, contracts, payload, fetchedAt, digest), nil
}

// FetchExchangeRates obtains the provider's BTC-relative exchange-rate table.
// It is intentionally a separate narrow surface from /simple/price because
// fiat-to-USD calculation requires the USD and fiat values to originate from
// the same response snapshot. The endpoint has no usable provider timestamp,
// so callers receive FetchedAt and must retain CURRENT/PROVISIONAL semantics.
func (c *CoinGeckoClient) FetchExchangeRates(ctx context.Context) (CoinGeckoExchangeRatesBatch, error) {
	body, fetchedAt, digest, err := c.fetchPayload(ctx, []string{"exchange_rates"}, nil)
	if err != nil {
		return CoinGeckoExchangeRatesBatch{}, err
	}
	rates, err := decodeCoinGeckoExchangeRatesPayload(body)
	if err != nil {
		return CoinGeckoExchangeRatesBatch{}, err
	}
	return CoinGeckoExchangeRatesBatch{Rates: rates, FetchedAt: fetchedAt, ResponseDigest: digest}, nil
}

func (c *CoinGeckoClient) fetchPricePayload(ctx context.Context, pathParts []string, query url.Values) (map[string]map[string]any, time.Time, string, error) {
	body, fetchedAt, digest, err := c.fetchPayload(ctx, pathParts, query)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	payload, err := decodeCoinGeckoPricePayload(body)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	return payload, fetchedAt, digest, nil
}

func (c *CoinGeckoClient) fetchPayload(ctx context.Context, pathParts []string, query url.Values) ([]byte, time.Time, string, error) {
	if c == nil || c.baseURL == nil || c.httpClient == nil || c.now == nil {
		return nil, time.Time{}, "", fmt.Errorf("coingecko client is not configured")
	}
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + strings.Join(pathParts, "/")
	endpoint.RawPath = ""
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, time.Time{}, "", fmt.Errorf("create coingecko request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.demoAPIKey != "" {
		request.Header.Set("x-cg-demo-api-key", c.demoAPIKey)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, time.Time{}, "", err
		}
		return nil, time.Time{}, "", ErrCoinGeckoNetwork
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		var retryAfter *time.Duration
		if response.StatusCode == http.StatusTooManyRequests {
			retryAfter = parseCoinGeckoRetryAfter(response.Header.Get("Retry-After"), c.now().UTC())
		}
		return nil, time.Time{}, "", &CoinGeckoHTTPError{
			StatusCode: response.StatusCode,
			RetryAfter: retryAfter,
		}
	}
	body, err := readCoinGeckoResponseBody(response.Body)
	if err != nil {
		return nil, time.Time{}, "", err
	}
	digest := sha256.Sum256(body)
	return body, c.now().UTC(), hex.EncodeToString(digest[:]), nil
}

func buildCoinGeckoPriceBatch(assets, quotes []string, platformID string, contracts []string, payload map[string]map[string]any, fetchedAt time.Time, digest string) CoinGeckoPriceBatch {
	prices := make([]CoinGeckoPrice, 0, len(assets)*len(quotes))
	for index, asset := range assets {
		fields, assetFound := payload[strings.ToLower(asset)]
		for _, quoteCurrency := range quotes {
			price := CoinGeckoPrice{QuoteCurrency: quoteCurrency}
			if platformID == "" {
				price.CoinID = asset
			} else {
				price.PlatformID = platformID
				price.ContractAddress = contracts[index]
			}
			price.Quote, price.UnavailableReason = parseCoinGeckoQuote(fields, assetFound, quoteCurrency, fetchedAt, digest)
			prices = append(prices, price)
		}
	}
	return CoinGeckoPriceBatch{Prices: prices, FetchedAt: fetchedAt, ResponseDigest: digest}
}

func parseCoinGeckoQuote(fields map[string]any, assetFound bool, quoteCurrency string, fetchedAt time.Time, digest string) (*CoinGeckoQuote, CoinGeckoPriceUnavailableReason) {
	if !assetFound || fields == nil {
		return nil, CoinGeckoPriceUnavailableMissingAsset
	}
	rawPrice, found := fields[quoteCurrency]
	if !found || rawPrice == nil {
		return nil, CoinGeckoPriceUnavailableMissingQuote
	}
	price, ok := parseCoinGeckoDecimal(rawPrice)
	if !ok {
		return nil, CoinGeckoPriceUnavailableInvalidPrice
	}
	if !price.GreaterThan(decimal.Zero) {
		return nil, CoinGeckoPriceUnavailableNonPositive
	}
	updatedAt, ok := parseCoinGeckoUnixTime(fields["last_updated_at"])
	if !ok {
		return nil, CoinGeckoPriceUnavailableMissingTimestamp
	}
	return &CoinGeckoQuote{Price: price, ProviderUpdatedAt: updatedAt, FetchedAt: fetchedAt, ResponseDigest: digest}, ""
}
