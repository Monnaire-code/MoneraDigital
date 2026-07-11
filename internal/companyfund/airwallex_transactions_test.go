package companyfund

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAirwallexClient_ListsFinancialTransactionsWithBoundedPagination(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			_, _ = response.Write([]byte(airwallexTestLoginResponse("transaction-token", now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			if request.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", request.Method)
			}
			query := request.URL.Query()
			if query.Get("from_created_at") != "2026-07-09T16:00:00Z" || query.Get("to_created_at") != "2026-07-10T15:59:59.999999999Z" || query.Get("page_num") != "3" || query.Get("page_size") != "25" {
				t.Errorf("unexpected transaction query: %s", request.URL.RawQuery)
			}
			if query.Has("page") {
				t.Errorf("financial transaction request must not use an undocumented cursor: %s", request.URL.RawQuery)
			}
			if request.Header.Get("Authorization") != "Bearer transaction-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get("x-api-version") != airwallexTestAPIVersion {
				t.Errorf("x-api-version = %q, want configured pinned version", request.Header.Get("x-api-version"))
			}
			_, _ = response.Write([]byte(`{"has_more":true,"items":[{"id":"ftx_1","amount":123.456789012345678,"fee":0.123456789012345678,"net":123.333332223333333,"client_rate":157.123456789012345678,"batch_id":"batch_1","created_at":"2026-07-10T11:00:00+08:00","currency":"JPY","currency_pair":"USDJPY","description":"company settlement","estimated_settled_at":"2026-07-10T12:00:00+08:00","funding_source_id":"fs_1","settled_at":"2026-07-10T12:01:00+08:00","source_id":"deposit_1","source_type":"DEPOSIT","status":"SETTLED","transaction_type":"DEPOSIT"}]}`))
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	request := airwallexTestListRequest()
	request.PageNum = 3
	request.PageSize = 25
	page, err := client.ListFinancialTransactions(context.Background(), request)
	if err != nil {
		t.Fatalf("ListFinancialTransactions() error = %v", err)
	}
	if !page.HasMore || len(page.Items) != 1 {
		t.Fatalf("page = %#v", page)
	}
	item := page.Items[0]
	if item.ProviderID != "ftx_1" ||
		string(item.Amount) != "123.456789012345678" ||
		string(item.Fee) != "0.123456789012345678" ||
		string(item.Net) != "123.333332223333333" ||
		string(item.ClientRate) != "157.123456789012345678" ||
		item.BatchID != "batch_1" ||
		item.CreatedAt != "2026-07-10T11:00:00+08:00" ||
		item.Currency != "JPY" ||
		item.CurrencyPair != "USDJPY" ||
		item.Description != "company settlement" ||
		item.EstimatedSettledAt != "2026-07-10T12:00:00+08:00" ||
		item.FundingSourceID != "fs_1" ||
		item.SettledAt != "2026-07-10T12:01:00+08:00" ||
		item.SourceID != "deposit_1" ||
		item.SourceType != "DEPOSIT" ||
		item.TransactionType != "DEPOSIT" ||
		item.Status != "SETTLED" ||
		len(item.Raw) == 0 {
		t.Fatalf("provider-preserving item = %#v", item)
	}
}

func TestAirwallexClient_UsesDocumentedDefaultPagination(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			_, _ = response.Write([]byte(airwallexTestLoginResponse("default-page-token", now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			query := request.URL.Query()
			if query.Get("page_num") != "0" || query.Get("page_size") != "100" || query.Has("page") {
				t.Errorf("default pagination query = %s", request.URL.RawQuery)
			}
			_, _ = response.Write([]byte(`{"has_more":false,"items":[]}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	if _, err := client.ListFinancialTransactions(context.Background(), airwallexTestListRequest()); err != nil {
		t.Fatalf("ListFinancialTransactions() error = %v", err)
	}
}

func TestAirwallexClient_RejectsUnsafeFinancialTransactionsPagination(t *testing.T) {
	client := newTestAirwallexClient(t, AirwallexDefaultBaseURL, nil, time.Now, time.Minute)
	request := airwallexTestListRequest()
	request.PageNum = maxAirwallexFinancialTransactionPageNumber + 1
	if _, err := client.ListFinancialTransactions(context.Background(), request); err == nil {
		t.Fatal("page number above the official bound must be rejected before HTTP")
	}
	request = airwallexTestListRequest()
	request.PageSize = maxAirwallexFinancialTransactionPageSize + 1
	if _, err := client.ListFinancialTransactions(context.Background(), request); err == nil {
		t.Fatal("page size above configured bound must be rejected before HTTP")
	}
	request = airwallexTestListRequest()
	request.ToCreatedAt = request.FromCreatedAt
	if _, err := client.ListFinancialTransactions(context.Background(), request); err == nil {
		t.Fatal("empty time window must be rejected before HTTP")
	}
}

func TestAirwallexClient_MapsAdjacentInternalWindowsToDisjointInclusiveProviderQueries(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	queries := make([]string, 0, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			_, _ = response.Write([]byte(airwallexTestLoginResponse("adjacent-window-token", now.Add(time.Hour))))
		case "/api/v1/financial_transactions":
			queries = append(queries, request.URL.Query().Get("from_created_at")+"|"+request.URL.Query().Get("to_created_at"))
			_, _ = response.Write([]byte(`{"has_more":false,"items":[]}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	first := airwallexTestListRequest()
	first.FromCreatedAt = time.Date(2026, time.July, 9, 16, 0, 0, 123456789, time.UTC)
	first.ToCreatedAt = time.Date(2026, time.July, 10, 16, 0, 0, 123456789, time.UTC)
	second := first
	second.FromCreatedAt = first.ToCreatedAt
	second.ToCreatedAt = first.ToCreatedAt.Add(24 * time.Hour)
	if _, err := client.ListFinancialTransactions(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListFinancialTransactions(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2026-07-09T16:00:00.123456789Z|2026-07-10T16:00:00.123456788Z",
		"2026-07-10T16:00:00.123456789Z|2026-07-11T16:00:00.123456788Z",
	}
	if len(queries) != len(want) {
		t.Fatalf("provider queries = %#v, want %#v", queries, want)
	}
	for index := range want {
		if queries[index] != want[index] {
			t.Fatalf("provider query %d = %q, want %q", index, queries[index], want[index])
		}
	}
}

func TestAirwallexClient_GetsFinancialTransactionDetail(t *testing.T) {
	now := time.Date(2026, 7, 10, 3, 0, 0, 0, time.UTC)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/authentication/login":
			_, _ = response.Write([]byte(airwallexTestLoginResponse("detail-token", now.Add(time.Hour))))
		case "/api/v1/financial_transactions/ftx_1":
			if request.Method != http.MethodGet || request.URL.RawQuery != "" {
				t.Errorf("detail request = %s %s", request.Method, request.URL.String())
			}
			if request.Header.Get("Authorization") != "Bearer detail-token" {
				t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
			}
			if request.Header.Get("x-api-version") != airwallexTestAPIVersion {
				t.Errorf("x-api-version = %q, want configured pinned version", request.Header.Get("x-api-version"))
			}
			_, _ = response.Write([]byte(`{"id":"ftx_1","amount":19.75,"fee":0.25,"net":19.5,"client_rate":1,"created_at":"2026-07-10T03:00:00Z","currency":"USD","source_id":"transfer_1","source_type":"PAYOUT","status":"SETTLED","transaction_type":"PAYOUT"}`))
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestAirwallexClient(t, server.URL, server.Client(), func() time.Time { return now }, time.Minute)
	item, err := client.GetFinancialTransaction(context.Background(), "ftx_1")
	if err != nil {
		t.Fatalf("GetFinancialTransaction() error = %v", err)
	}
	if item.ProviderID != "ftx_1" || string(item.Amount) != "19.75" || string(item.Fee) != "0.25" || string(item.Net) != "19.5" || string(item.ClientRate) != "1" || item.SourceID != "transfer_1" || item.SourceType != "PAYOUT" || item.Status != "SETTLED" || item.TransactionType != "PAYOUT" || len(item.Raw) == 0 {
		t.Fatalf("detail = %#v", item)
	}
}

func TestAirwallexClient_RejectsUnsafeFinancialTransactionDetailID(t *testing.T) {
	client := newTestAirwallexClient(t, AirwallexDefaultBaseURL, nil, time.Now, time.Minute)
	for _, providerID := range []string{"../unexpected", "%2fapi%2fv1%2fauthentication%2flogin"} {
		if _, err := client.GetFinancialTransaction(context.Background(), providerID); err == nil {
			t.Fatalf("unsafe detail ID %q must be rejected before HTTP", providerID)
		}
	}
}
