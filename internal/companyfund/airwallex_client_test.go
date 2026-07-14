package companyfund

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAirwallexClient_ReusesAndRefreshesTokenBeforeSkew(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	loginCalls := 0
	bearerTokens := make([]string, 0, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			if request.Method != http.MethodPost || request.Header.Get("x-client-id") != "client-id-secret" || request.Header.Get("x-api-key") != "api-key-secret" {
				t.Errorf("unexpected authentication request: %#v", request.Header)
			}
			mu.Lock()
			loginCalls++
			token := "token-" + strconv.Itoa(loginCalls)
			mu.Unlock()
			_, _ = response.Write([]byte(airwallexTestLoginResponse(token, now.Add(10*time.Minute))))
		case "/api/v1/financial_transactions":
			mu.Lock()
			bearerTokens = append(bearerTokens, request.Header.Get("Authorization"))
			mu.Unlock()
			if request.Header.Get("x-api-key") != "" || request.Header.Get("x-client-id") != "" {
				t.Error("business request must not carry long-lived credentials")
			}
			_, _ = response.Write([]byte(`{"items":[],"has_more":false}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	for range 2 {
		if _, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest()); err != nil {
			t.Fatalf("ListFinancialTransactions() error = %v", err)
		}
	}
	now = now.Add(9 * time.Minute)
	if _, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest()); err != nil {
		t.Fatalf("refresh ListFinancialTransactions() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 2 || len(bearerTokens) != 3 || bearerTokens[0] != "Bearer token-1" || bearerTokens[1] != "Bearer token-1" || bearerTokens[2] != "Bearer token-2" {
		t.Fatalf("token reuse/refresh = calls:%d bearer:%#v", loginCalls, bearerTokens)
	}
}

func TestAirwallexClient_CoalescesConcurrentTokenRefresh(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var startedOnce sync.Once
	loginCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			mu.Lock()
			loginCalls++
			mu.Unlock()
			startedOnce.Do(func() { close(started) })
			<-release
			_, _ = response.Write([]byte(airwallexTestLoginResponse("shared-token", now.Add(10*time.Minute))))
		case "/api/v1/financial_transactions":
			if request.Header.Get("Authorization") != "Bearer shared-token" {
				t.Errorf("Authorization = %q, want cached bearer token", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"items":[]}`))
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	const callers = 12
	errs := make(chan error, callers)
	var waitGroup sync.WaitGroup
	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
			errs <- err
		}()
	}
	<-started
	close(release)
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ListFinancialTransactions() error = %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 1 {
		t.Fatalf("authentication calls = %d, want one single-flight request", loginCalls)
	}
}

func TestParseAirwallexTokenExpiry_AcceptsOfficialISO8601Offsets(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  time.Time
	}{
		{value: "2021-06-18T16:30:00+0000", want: time.Date(2021, 6, 18, 16, 30, 0, 0, time.UTC)},
		{value: "2026-07-10T11:00:00.123+0800", want: time.Date(2026, 7, 10, 3, 0, 0, 123000000, time.UTC)},
		{value: "2026-07-10T03:00:00.123Z", want: time.Date(2026, 7, 10, 3, 0, 0, 123000000, time.UTC)},
		{value: "2026-07-10T03:00:00+00:00", want: time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)},
	} {
		t.Run(testCase.value, func(t *testing.T) {
			got, err := parseAirwallexTokenExpiry(testCase.value)
			if err != nil || !got.Equal(testCase.want) {
				t.Fatalf("parseAirwallexTokenExpiry(%q) = %s, %v; want %s", testCase.value, got, err, testCase.want)
			}
		})
	}
}

func TestAirwallexClient_AcceptsNoColonExpiryAndScopesOnlyLogin(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			if request.Header.Get("x-login-as") != "account_123" {
				t.Errorf("x-login-as = %q, want configured account", request.Header.Get("x-login-as"))
			}
			if request.Header.Get("x-api-version") != "" {
				t.Errorf("authentication x-api-version = %q, want absent because the official authentication request contract does not include it", request.Header.Get("x-api-version"))
			}
			_, _ = response.Write([]byte(`{"token":"offset-token","expires_at":"2026-07-10T03:30:00+0000"}`))
		case "/api/v1/financial_transactions":
			if request.Header.Get("Authorization") != "Bearer offset-token" || request.Header.Get("x-login-as") != "" || request.Header.Get("x-client-id") != "" || request.Header.Get("x-api-key") != "" || request.Header.Get("x-api-version") != airwallexTestAPIVersion {
				t.Errorf("business headers = %#v, want bearer only", request.Header)
			}
			_, _ = response.Write([]byte(`{"items":[]}`))
		}
	}))
	defer server.Close()
	client := newTestAirwallexClientWithLoginAs(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute, " account_123 ")
	if _, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest()); err != nil {
		t.Fatalf("ListFinancialTransactions() error = %v", err)
	}
}

func TestAirwallexClient_RetriesIdempotentGETOnceAfterUnauthorized(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	loginCalls := 0
	getCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			mu.Lock()
			loginCalls++
			call := loginCalls
			mu.Unlock()
			token := "stale-token"
			if call == 2 {
				token = "fresh-token"
			}
			_, _ = response.Write([]byte(airwallexTestLoginResponse(token, now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			mu.Lock()
			getCalls++
			mu.Unlock()
			if request.Header.Get("Authorization") == "Bearer stale-token" {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Header.Get("Authorization") != "Bearer fresh-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"items":[],"has_more":false}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	if _, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest()); err != nil {
		t.Fatalf("ListFinancialTransactions() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 2 || getCalls != 2 {
		t.Fatalf("calls = login:%d get:%d, want exactly two of each", loginCalls, getCalls)
	}
}

func TestAirwallexClient_RetriesFinancialTransactionDetailGETOnceAfterUnauthorized(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	loginCalls := 0
	getCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			mu.Lock()
			loginCalls++
			call := loginCalls
			mu.Unlock()
			token := "stale-detail-token"
			if call == 2 {
				token = "fresh-detail-token"
			}
			_, _ = response.Write([]byte(airwallexTestLoginResponse(token, now.Add(time.Hour))))
		case "/api/v1/financial_transactions/ftx_1":
			mu.Lock()
			getCalls++
			mu.Unlock()
			if request.Header.Get("Authorization") == "Bearer stale-detail-token" {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			if request.Header.Get("Authorization") != "Bearer fresh-detail-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			_, _ = response.Write([]byte(`{"id":"ftx_1","amount":1,"fee":0,"net":1,"currency":"USD","status":"SETTLED"}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	if _, err := client.GetFinancialTransaction(context.Background(), "ftx_1"); err != nil {
		t.Fatalf("GetFinancialTransaction() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 2 || getCalls != 2 {
		t.Fatalf("calls = login:%d get:%d, want exactly two of each", loginCalls, getCalls)
	}
}

func TestAirwallexClient_StopsAfterSecondUnauthorizedGET(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	loginCalls := 0
	getCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			mu.Lock()
			loginCalls++
			call := loginCalls
			mu.Unlock()
			_, _ = response.Write([]byte(airwallexTestLoginResponse("token-"+strconv.Itoa(call), now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			mu.Lock()
			getCalls++
			mu.Unlock()
			response.WriteHeader(http.StatusForbidden)
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
	if !errors.Is(err, ErrAirwallexUnauthorized) {
		t.Fatalf("ListFinancialTransactions() error = %v, want ErrAirwallexUnauthorized", err)
	}
	if strings.Contains(err.Error(), "token-1") || strings.Contains(err.Error(), "token-2") {
		t.Fatalf("unauthorized error leaked bearer token: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 2 || getCalls != 2 {
		t.Fatalf("calls = login:%d get:%d, want one retry only", loginCalls, getCalls)
	}
}

func TestAirwallexClient_CoalescesConcurrentUnauthorizedRefresh(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	const callers = 12
	var mu sync.Mutex
	loginCalls := 0
	staleGetCalls := 0
	freshGetCalls := 0
	staleRequestsArrived := make(chan struct{})
	releaseStaleRequests := make(chan struct{})
	var closeStaleRequests sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			mu.Lock()
			loginCalls++
			call := loginCalls
			mu.Unlock()
			token := "stale-token"
			if call == 2 {
				token = "fresh-token"
			}
			_, _ = response.Write([]byte(airwallexTestLoginResponse(token, now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			switch request.Header.Get("Authorization") {
			case "Bearer stale-token":
				mu.Lock()
				staleGetCalls++
				if staleGetCalls == callers {
					closeStaleRequests.Do(func() { close(staleRequestsArrived) })
				}
				mu.Unlock()
				<-releaseStaleRequests
				response.WriteHeader(http.StatusUnauthorized)
			case "Bearer fresh-token":
				mu.Lock()
				freshGetCalls++
				mu.Unlock()
				_, _ = response.Write([]byte(`{"items":[],"has_more":false}`))
			default:
				t.Errorf("unexpected Authorization = %q", request.Header.Get("Authorization"))
				response.WriteHeader(http.StatusUnauthorized)
			}
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	errs := make(chan error, callers)
	var waitGroup sync.WaitGroup
	for range callers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest())
			errs <- err
		}()
	}
	<-staleRequestsArrived
	close(releaseStaleRequests)
	waitGroup.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ListFinancialTransactions() error = %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if loginCalls != 2 || staleGetCalls != callers || freshGetCalls != callers {
		t.Fatalf("calls = login:%d stale:%d fresh:%d, want login:2 stale:%d fresh:%d", loginCalls, staleGetCalls, freshGetCalls, callers, callers)
	}
}

func TestAirwallexClient_InvalidatingOldTokenDoesNotClearNewToken(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	client := newTestAirwallexClient(t, AirwallexDefaultBaseURL, nil, func() time.Time { return now }, time.Minute)
	client.tokenMu.Lock()
	client.token = airwallexAccessToken{value: "fresh-token", expiresAt: now.Add(time.Hour), generation: 2}
	client.tokenMu.Unlock()

	client.invalidateAccessToken(airwallexAccessToken{value: "stale-token", generation: 1})

	client.tokenMu.Lock()
	defer client.tokenMu.Unlock()
	if client.token.value != "fresh-token" || !client.token.expiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("new token was cleared by stale rejection: %#v", client.token)
	}
}
