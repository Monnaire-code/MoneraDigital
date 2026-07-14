package companyfund

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewAirwallexClient_RejectsInsecureBaseWithoutLeakingCredentials(t *testing.T) {
	_, err := NewAirwallexClient(AirwallexClientConfig{
		BaseURL:  "http://127.0.0.1:8080",
		ClientID: "client-id-secret",
		APIKey:   "api-key-secret",
	})
	if err == nil {
		t.Fatal("http base URL must be rejected")
	}
	if strings.Contains(err.Error(), "client-id-secret") || strings.Contains(err.Error(), "api-key-secret") {
		t.Fatalf("insecure-base error leaked credential: %v", err)
	}
}

func TestNewAirwallexClient_RequiresStrictPinnedAPIVersion(t *testing.T) {
	base := AirwallexClientConfig{
		BaseURL:    AirwallexDefaultBaseURL,
		ClientID:   "client-id-secret",
		APIKey:     "api-key-secret",
		APIVersion: airwallexTestAPIVersion,
	}
	for _, version := range []string{"", " " + airwallexTestAPIVersion, "2026-5-29", "2026-02-29", "2026-05-29\nother"} {
		config := base
		config.APIVersion = version
		if _, err := NewAirwallexClient(config); err == nil {
			t.Fatalf("NewAirwallexClient(APIVersion=%q) error = nil, want strict rejection", version)
		}
	}
	client, err := NewAirwallexClient(base)
	if err != nil {
		t.Fatalf("NewAirwallexClient(valid version) error = %v", err)
	}
	if client.apiVersion != airwallexTestAPIVersion {
		t.Fatalf("client API version = %q, want %q", client.apiVersion, airwallexTestAPIVersion)
	}
}

func TestAirwallexClient_ReturnsTypedRedirectWithoutFollowingCredentials(t *testing.T) {
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

	client := newTestAirwallexClient(t, source.URL, newAirwallexTestTLSClient(t, source, target), time.Now, time.Minute)
	_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
	if !errors.Is(err, ErrAirwallexClientResponse) {
		t.Fatalf("redirect error = %v, want typed client-response error", err)
	}
	var httpError *AirwallexHTTPError
	if !errors.As(err, &httpError) || httpError.StatusCode != http.StatusFound {
		t.Fatalf("redirect error = %v, want typed 302", err)
	}
	if strings.Contains(err.Error(), "client-id-secret") || strings.Contains(err.Error(), "api-key-secret") {
		t.Fatalf("redirect error leaked credentials: %v", err)
	}
	select {
	case headers := <-targetHeaders:
		t.Fatalf("redirect target received credential headers %#v", headers)
	default:
	}
}

func TestAirwallexClient_SanitizesProviderErrorsAndBoundsBodies(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		statusCode int
		body       string
		wantTarget error
	}{
		{name: "unauthorized", statusCode: http.StatusUnauthorized, body: "api-key-secret token-body", wantTarget: ErrAirwallexUnauthorized},
		{name: "oversized", statusCode: http.StatusOK, body: strings.Repeat("x", maxAirwallexResponseBytes+1), wantTarget: ErrAirwallexResponseTooLarge},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/api/v1/authentication/login" {
					t.Errorf("unexpected path %q", request.URL.Path)
				}
				response.WriteHeader(testCase.statusCode)
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client := newTestAirwallexClient(t, server.URL, server.Client(), time.Now, time.Minute)
			_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
			if !errors.Is(err, testCase.wantTarget) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, testCase.wantTarget)
			}
			if strings.Contains(err.Error(), "api-key-secret") || strings.Contains(err.Error(), "token-body") {
				t.Fatalf("provider error leaked credential/body: %v", err)
			}
		})
	}
}

func TestAirwallexClient_DoesNotLeakBearerTokenOrBusinessErrorBody(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			_, _ = response.Write([]byte(airwallexTestLoginResponse("bearer-token-secret", time.Now().Add(time.Hour))))
		case "/api/v1/financial_transactions":
			response.WriteHeader(http.StatusServiceUnavailable)
			_, _ = response.Write([]byte("bearer-token-secret provider diagnostic body"))
		}
	}))
	defer server.Close()
	client := newTestAirwallexClient(t, server.URL, server.Client(), time.Now, time.Minute)
	_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
	if !errors.Is(err, ErrAirwallexServerResponse) {
		t.Fatalf("error = %v, want ErrAirwallexServerResponse", err)
	}
	if strings.Contains(err.Error(), "bearer-token-secret") || strings.Contains(err.Error(), "provider diagnostic body") {
		t.Fatalf("business error leaked bearer/body: %v", err)
	}
}
