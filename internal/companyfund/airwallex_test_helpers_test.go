package companyfund

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const airwallexTestAPIVersion = "2026-05-29"

func newTestAirwallexClient(t *testing.T, baseURL string, httpClient *http.Client, clock func() time.Time, skew time.Duration) *AirwallexClient {
	return newTestAirwallexClientWithLoginAs(t, baseURL, httpClient, clock, skew, "")
}

func newTestAirwallexClientWithLoginAs(t *testing.T, baseURL string, httpClient *http.Client, clock func() time.Time, skew time.Duration, loginAs string) *AirwallexClient {
	t.Helper()
	client, err := NewAirwallexClient(AirwallexClientConfig{
		BaseURL:          baseURL,
		ClientID:         "client-id-secret",
		APIKey:           "api-key-secret",
		APIVersion:       airwallexTestAPIVersion,
		LoginAs:          loginAs,
		HTTPClient:       httpClient,
		Clock:            clock,
		TokenRefreshSkew: skew,
	})
	if err != nil {
		t.Fatalf("NewAirwallexClient() error = %v", err)
	}
	return client
}

func newAirwallexTestTLSClient(t *testing.T, servers ...*httptest.Server) *http.Client {
	t.Helper()
	roots := x509.NewCertPool()
	for _, server := range servers {
		roots.AddCert(server.Certificate())
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	return &http.Client{Transport: transport}
}

func airwallexTestLoginResponse(token string, expiresAt time.Time) string {
	return fmt.Sprintf(`{"token":%q,"expires_at":%q}`, token, expiresAt.UTC().Format(time.RFC3339Nano))
}

func airwallexTestListRequest() AirwallexFinancialTransactionsRequest {
	return AirwallexFinancialTransactionsRequest{
		FromCreatedAt: time.Date(2026, 7, 9, 16, 0, 0, 0, time.UTC),
		ToCreatedAt:   time.Date(2026, 7, 10, 16, 0, 0, 0, time.UTC),
		PageNum:       0,
	}
}
