package companyfund

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCoinGeckoClientFetchSimplePrices_UsesHeaderAndExactDecimals(t *testing.T) {
	const demoAPIKey = "demo-secret-key"
	const body = `{"bitcoin":{"usd":65000.123456789012345678,"jpy":10000000.000000000000000001,"last_updated_at":1720000000},"usd-coin":{"usd":0.999999999999999999,"last_updated_at":1720000001}}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/simple/price" {
			t.Errorf("path = %q, want /simple/price", request.URL.Path)
		}
		if got := request.Header.Get("x-cg-demo-api-key"); got != demoAPIKey {
			t.Errorf("demo key header = %q, want configured value", got)
		}
		query := request.URL.Query()
		if query.Get("ids") != "bitcoin,usd-coin" || query.Get("vs_currencies") != "usd,jpy" {
			t.Errorf("unexpected batch query: %s", request.URL.RawQuery)
		}
		if query.Get("include_last_updated_at") != "true" || query.Get("precision") != "full" {
			t.Errorf("missing required precision/timestamp query: %s", request.URL.RawQuery)
		}
		if query.Get("x-cg-demo-api-key") != "" || strings.Contains(request.URL.RawQuery, demoAPIKey) {
			t.Error("demo API key must never be encoded in the URL")
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()

	fetchedAt := time.Date(2026, 7, 10, 3, 4, 5, 0, time.UTC)
	client := newTestCoinGeckoClientWithHTTP(t, server.URL, demoAPIKey, func() time.Time { return fetchedAt }, server.Client())
	batch, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{
		CoinIDs:         []string{"bitcoin", "usd-coin"},
		QuoteCurrencies: []string{"USD", "JPY"},
	})
	if err != nil {
		t.Fatalf("FetchSimplePrices() error = %v", err)
	}
	if len(batch.Prices) != 4 || !batch.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("batch = %#v, want four exact quotes at %s", batch, fetchedAt)
	}
	wantDigest := sha256.Sum256([]byte(body))
	if batch.ResponseDigest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("response digest = %q, want SHA-256 of body", batch.ResponseDigest)
	}

	bitcoinUSD := findCoinGeckoPrice(t, batch, "bitcoin", "usd")
	if bitcoinUSD.Quote == nil || !bitcoinUSD.Quote.Price.Equal(decimal.RequireFromString("65000.123456789012345678")) {
		t.Fatalf("bitcoin USD lost exact precision: %#v", bitcoinUSD)
	}
	if !bitcoinUSD.Quote.ProviderUpdatedAt.Equal(time.Unix(1720000000, 0).UTC()) || !bitcoinUSD.Quote.FetchedAt.Equal(fetchedAt) || bitcoinUSD.Quote.ResponseDigest != batch.ResponseDigest {
		t.Fatalf("bitcoin USD audit metadata = %#v", bitcoinUSD.Quote)
	}
	missingJPY := findCoinGeckoPrice(t, batch, "usd-coin", "jpy")
	if missingJPY.Quote != nil || missingJPY.UnavailableReason != CoinGeckoPriceUnavailableMissingQuote {
		t.Fatalf("missing quote = %#v, want unavailable without zero", missingJPY)
	}
}

func TestCoinGeckoClientFetchTokenPrices_EncodesContractAndNeverTreatsNonPositiveAsZero(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/simple/token_price/ethereum" {
			t.Errorf("path = %q, want platform endpoint", request.URL.Path)
		}
		query := request.URL.Query()
		if query.Get("contract_addresses") != "0xAbC,0xDef" || query.Get("vs_currencies") != "usd" {
			t.Errorf("token query = %s", request.URL.RawQuery)
		}
		_, _ = response.Write([]byte(`{"0xabc":{"usd":0,"last_updated_at":1720000000}}`))
	}))
	defer server.Close()

	client := newTestCoinGeckoClientWithHTTP(t, server.URL, "", func() time.Time { return time.Unix(1720000100, 0).UTC() }, server.Client())
	batch, err := client.FetchTokenPrices(context.Background(), CoinGeckoTokenPriceRequest{
		PlatformID:        "ethereum",
		ContractAddresses: []string{"0xAbC", "0xDef"},
		QuoteCurrencies:   []string{"usd"},
	})
	if err != nil {
		t.Fatalf("FetchTokenPrices() error = %v", err)
	}
	nonPositive := findCoinGeckoTokenPrice(t, batch, "ethereum", "0xAbC", "usd")
	if nonPositive.Quote != nil || nonPositive.UnavailableReason != CoinGeckoPriceUnavailableNonPositive {
		t.Fatalf("zero token quote = %#v, want unavailable rather than numeric zero", nonPositive)
	}
	missing := findCoinGeckoTokenPrice(t, batch, "ethereum", "0xDef", "usd")
	if missing.Quote != nil || missing.UnavailableReason != CoinGeckoPriceUnavailableMissingAsset {
		t.Fatalf("missing token = %#v, want unavailable", missing)
	}
}

func TestCoinGeckoClientFetchExchangeRates_PreservesExactBTCRelativeDecimals(t *testing.T) {
	fetchedAt := time.Date(2026, time.July, 11, 4, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/exchange_rates" {
			t.Fatalf("path = %q, want /exchange_rates", request.URL.Path)
		}
		if request.URL.RawQuery != "" {
			t.Fatalf("exchange_rates must not add a query string: %q", request.URL.RawQuery)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"rates": {
				"btc": {"unit":"BTC","type":"crypto","value":1},
				"usd": {"unit":"$","type":"fiat","value":60000.123456789012345678},
				"jpy": {"unit":"¥","type":"fiat","value":9000000.000000000000000123},
				"sgd": {"unit":"S$","type":"fiat","value":"80000.123456789012345678"}
			}
		}`))
	}))
	defer server.Close()
	client := newTestCoinGeckoClientWithHTTP(t, server.URL, "", func() time.Time { return fetchedAt }, server.Client())

	batch, err := client.FetchExchangeRates(context.Background())
	if err != nil {
		t.Fatalf("FetchExchangeRates() error = %v", err)
	}
	if !batch.FetchedAt.Equal(fetchedAt) || !isLowerSHA256Hex(batch.ResponseDigest) || len(batch.Rates) != 4 {
		t.Fatalf("exchange-rates batch metadata = %#v", batch)
	}
	if rate := batch.Rates["JPY"]; rate.Type != "fiat" || !rate.Value.Equal(decimal.RequireFromString("9000000.000000000000000123")) {
		t.Fatalf("JPY rate lost exact value/type: %#v", rate)
	}
	if rate := batch.Rates["SGD"]; !rate.Value.Equal(decimal.RequireFromString("80000.123456789012345678")) {
		t.Fatalf("string decimal exchange rate lost exact value: %#v", rate)
	}
}

func newTestCoinGeckoClient(t *testing.T, baseURL, demoAPIKey string, clock func() time.Time) *CoinGeckoClient {
	return newTestCoinGeckoClientWithHTTP(t, baseURL, demoAPIKey, clock, nil)
}

func newTestCoinGeckoClientWithHTTP(t *testing.T, baseURL, demoAPIKey string, clock func() time.Time, httpClient *http.Client) *CoinGeckoClient {
	t.Helper()
	client, err := NewCoinGeckoClient(CoinGeckoClientConfig{
		BaseURL:    baseURL,
		DemoAPIKey: demoAPIKey,
		HTTPClient: httpClient,
		Clock:      clock,
	})
	if err != nil {
		t.Fatalf("NewCoinGeckoClient() error = %v", err)
	}
	return client
}

func findCoinGeckoPrice(t *testing.T, batch CoinGeckoPriceBatch, coinID, quoteCurrency string) CoinGeckoPrice {
	t.Helper()
	for _, price := range batch.Prices {
		if price.CoinID == coinID && price.QuoteCurrency == quoteCurrency {
			return price
		}
	}
	t.Fatalf("coin price %s/%s not found in %#v", coinID, quoteCurrency, batch.Prices)
	return CoinGeckoPrice{}
}

func findCoinGeckoTokenPrice(t *testing.T, batch CoinGeckoPriceBatch, platformID, contractAddress, quoteCurrency string) CoinGeckoPrice {
	t.Helper()
	for _, price := range batch.Prices {
		if price.PlatformID == platformID && price.ContractAddress == contractAddress && price.QuoteCurrency == quoteCurrency {
			return price
		}
	}
	t.Fatalf("token price %s/%s/%s not found in %#v", platformID, contractAddress, quoteCurrency, batch.Prices)
	return CoinGeckoPrice{}
}
