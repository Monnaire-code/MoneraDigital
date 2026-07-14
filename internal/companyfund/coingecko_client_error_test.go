package companyfund

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCoinGeckoClient_ClassifiesHTTPFailuresWithoutLeakingDemoKey(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statusCode int
		retryAfter string
		wantTarget error
		wantRetry  *time.Duration
	}{
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			retryAfter: "7",
			wantTarget: ErrCoinGeckoRateLimited,
			wantRetry:  durationPointer(7 * time.Second),
		},
		{
			name:       "client error",
			statusCode: http.StatusBadRequest,
			wantTarget: ErrCoinGeckoClientResponse,
		},
		{
			name:       "server error",
			statusCode: http.StatusServiceUnavailable,
			retryAfter: "7",
			wantTarget: ErrCoinGeckoServerResponse,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if testCase.retryAfter != "" {
					response.Header().Set("Retry-After", testCase.retryAfter)
				}
				response.WriteHeader(testCase.statusCode)
				_, _ = response.Write([]byte("provider diagnostic body must not leak"))
			}))
			defer server.Close()

			client := newTestCoinGeckoClientWithHTTP(t, server.URL, "demo-secret-key", func() time.Time { return time.Unix(1720000000, 0).UTC() }, server.Client())
			_, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{
				CoinIDs:         []string{"bitcoin"},
				QuoteCurrencies: []string{"usd"},
			})
			if !errors.Is(err, testCase.wantTarget) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, testCase.wantTarget)
			}
			if strings.Contains(err.Error(), "demo-secret-key") || strings.Contains(err.Error(), "provider diagnostic body") {
				t.Fatalf("error leaked sensitive request/body data: %v", err)
			}
			var httpError *CoinGeckoHTTPError
			if !errors.As(err, &httpError) || httpError.StatusCode != testCase.statusCode {
				t.Fatalf("error = %v, want typed HTTP status %d", err, testCase.statusCode)
			}
			if !equalDurationPointer(httpError.RetryAfter, testCase.wantRetry) {
				t.Fatalf("RetryAfter = %#v, want %#v", httpError.RetryAfter, testCase.wantRetry)
			}
		})
	}
}

func TestCoinGeckoClient_RejectsMalformedAndOversizedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		body       string
		wantTarget error
	}{
		{name: "malformed", body: `{"bitcoin":`, wantTarget: ErrCoinGeckoMalformedResponse},
		{name: "oversized", body: strings.Repeat("x", maxCoinGeckoResponseBytes+1), wantTarget: ErrCoinGeckoResponseTooLarge},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client := newTestCoinGeckoClientWithHTTP(t, server.URL, "", time.Now, server.Client())
			_, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{CoinIDs: []string{"bitcoin"}, QuoteCurrencies: []string{"usd"}})
			if !errors.Is(err, testCase.wantTarget) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, testCase.wantTarget)
			}
		})
	}
}

func TestCoinGeckoClientFetchExchangeRates_RejectsMalformedAndOversizedResponse(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		body       string
		wantTarget error
	}{
		{name: "malformed", body: `{"rates":{"usd":`, wantTarget: ErrCoinGeckoMalformedResponse},
		{name: "missing rates", body: `{"unexpected":{}}`, wantTarget: ErrCoinGeckoMalformedResponse},
		{name: "oversized", body: strings.Repeat("x", maxCoinGeckoResponseBytes+1), wantTarget: ErrCoinGeckoResponseTooLarge},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/exchange_rates" {
					t.Errorf("path = %q, want /exchange_rates", request.URL.Path)
				}
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client := newTestCoinGeckoClientWithHTTP(t, server.URL, "", time.Now, server.Client())
			_, err := client.FetchExchangeRates(context.Background())
			if !errors.Is(err, testCase.wantTarget) {
				t.Fatalf("FetchExchangeRates() error = %v, want errors.Is(%v)", err, testCase.wantTarget)
			}
		})
	}
}

func TestCoinGeckoClient_ReportsNetworkFailureWithoutExposingKey(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	baseURL := server.URL
	httpClient := server.Client()
	server.Close()
	client := newTestCoinGeckoClientWithHTTP(t, baseURL, "demo-secret-key", time.Now, httpClient)
	_, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{CoinIDs: []string{"bitcoin"}, QuoteCurrencies: []string{"usd"}})
	if !errors.Is(err, ErrCoinGeckoNetwork) {
		t.Fatalf("network error = %v, want ErrCoinGeckoNetwork", err)
	}
	if strings.Contains(err.Error(), "demo-secret-key") {
		t.Fatalf("network error leaked demo API key: %v", err)
	}
}

func TestNewCoinGeckoClient_RejectsInsecureBaseWithoutLeakingDemoKey(t *testing.T) {
	_, err := NewCoinGeckoClient(CoinGeckoClientConfig{
		BaseURL:    "http://127.0.0.1:8080/api/v3",
		DemoAPIKey: "demo-secret-key",
	})
	if err == nil {
		t.Fatal("http base URL must be rejected")
	}
	if strings.Contains(err.Error(), "demo-secret-key") {
		t.Fatalf("insecure-base error leaked demo API key: %v", err)
	}
}

func TestCoinGeckoClient_ReturnsTyped302WithoutFollowingCrossOriginRedirect(t *testing.T) {
	targetHeaders := make(chan http.Header, 1)
	target := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		targetHeaders <- request.Header.Clone()
		response.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	source := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Location", target.URL)
		response.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	client := newTestCoinGeckoClientWithHTTP(t, source.URL, "demo-secret-key", time.Now, newTestTLSClient(t, source, target))
	_, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{
		CoinIDs:         []string{"bitcoin"},
		QuoteCurrencies: []string{"usd"},
	})
	if !errors.Is(err, ErrCoinGeckoClientResponse) {
		t.Fatalf("redirect error = %v, want typed client-response error", err)
	}
	var httpError *CoinGeckoHTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != http.StatusFound {
		t.Fatalf("redirect error = %v, want typed 302", err)
	}
	if strings.Contains(err.Error(), "demo-secret-key") {
		t.Fatalf("redirect error leaked demo API key: %v", err)
	}
	select {
	case headers := <-targetHeaders:
		t.Fatalf("redirect target received request with demo header %q", headers.Get("x-cg-demo-api-key"))
	default:
	}
}

func TestCoinGeckoClient_RejectsBatchAboveProviderLimitBeforeHTTP(t *testing.T) {
	client := newTestCoinGeckoClient(t, CoinGeckoDefaultBaseURL, "", time.Now)
	tooMany := make([]string, maxCoinGeckoBatchAssets+1)
	for index := range tooMany {
		tooMany[index] = "asset-" + strconv.Itoa(index)
	}
	if _, err := client.FetchSimplePrices(context.Background(), CoinGeckoSimplePriceRequest{CoinIDs: tooMany, QuoteCurrencies: []string{"usd"}}); err == nil {
		t.Fatalf("simple-price batch over %d IDs must be rejected before HTTP", maxCoinGeckoBatchAssets)
	}
	if _, err := client.FetchTokenPrices(context.Background(), CoinGeckoTokenPriceRequest{PlatformID: "ethereum", ContractAddresses: tooMany, QuoteCurrencies: []string{"usd"}}); err == nil {
		t.Fatalf("token-price batch over %d contracts must be rejected before HTTP", maxCoinGeckoBatchAssets)
	}
}

func TestParseCoinGeckoRetryAfter_UsesHTTPDatesAndRejectsDurationOverflow(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	if got := parseCoinGeckoRetryAfter(now.Add(9*time.Second).Format(http.TimeFormat), now); got == nil || *got != 9*time.Second {
		t.Fatalf("HTTP-date Retry-After = %#v, want 9 seconds", got)
	}
	if got := parseCoinGeckoRetryAfter("901", now); got != nil {
		t.Fatalf("overlong numeric Retry-After must be rejected, got %#v", got)
	}
	if got := parseCoinGeckoRetryAfter(time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC).Format(http.TimeFormat), now); got != nil {
		t.Fatalf("overlong HTTP-date Retry-After must be rejected, got %#v", got)
	}
}

func durationPointer(value time.Duration) *time.Duration { return &value }

func equalDurationPointer(left, right *time.Duration) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func newTestTLSClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport}
}
