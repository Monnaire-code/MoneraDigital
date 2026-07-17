package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
)

type companyFundFinanceStoreStub struct {
	dashboard func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error)
	details   func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error)
	update    func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error)
}

func (s companyFundFinanceStoreStub) GetFinanceDashboard(ctx context.Context, filter companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
	return s.dashboard(ctx, filter)
}

func (s companyFundFinanceStoreStub) ListFinanceTransactionDetails(ctx context.Context, request companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
	return s.details(ctx, request)
}

func (s companyFundFinanceStoreStub) UpdateFinanceTransactionClassification(ctx context.Context, input companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
	return s.update(ctx, input)
}

func newCompanyFundFinanceHandlerRouter(t *testing.T, store companyfund.CompanyFundFinanceStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, err := NewCompanyFundFinanceHandler(CompanyFundFinanceHandlerConfig{Store: store, AdminKey: "company-fund-admin-key"})
	if err != nil {
		t.Fatalf("NewCompanyFundFinanceHandler() error = %v", err)
	}
	router := gin.New()
	router.Use(handler.RequireAdminKey())
	router.GET("/dashboard", handler.GetDashboard)
	router.GET("/transactions", handler.ListTransactions)
	router.PUT("/transactions/:transactionID/classification", handler.UpdateClassification)
	return router
}

func TestCompanyFundFinanceHandler_RequiresDedicatedAdminKeyNotUserJWT(t *testing.T) {
	calls := 0
	store := companyFundFinanceStoreStub{
		dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			calls++
			return companyfund.FinanceDashboardSummary{}, nil
		},
		details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			return nil, nil
		},
		update: func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			return companyfund.FinanceClassificationResult{}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)

	for _, request := range []*http.Request{
		httpRequest(http.MethodGet, "/dashboard", "", map[string]string{"Authorization": "Bearer customer-jwt"}),
		httpRequest(http.MethodGet, "/dashboard", "", map[string]string{companyFundAdminKeyHeader: "wrong-key"}),
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized company-fund response = %d, want 401", response.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("unauthorized requests reached store %d times", calls)
	}
}

func TestCompanyFundFinanceHandler_DashboardParsesFilterAndPreservesExactDecimals(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	operating := true
	store := companyFundFinanceStoreStub{
		dashboard: func(_ context.Context, filter companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			if filter.DateFrom == nil || !filter.DateFrom.Equal(from) || filter.DateTo == nil || !filter.DateTo.Equal(to) ||
				len(filter.Channels) != 2 || filter.Channels[0] != companyfund.ChannelAirwallex || filter.Channels[1] != companyfund.ChannelSafeheron ||
				len(filter.AccountIDs) != 2 || filter.AccountIDs[0] != 4 || filter.AccountIDs[1] != 8 ||
				filter.OperatingIncomeExpense == nil || *filter.OperatingIncomeExpense != operating {
				t.Fatalf("dashboard filter = %#v", filter)
			}
			return companyfund.FinanceDashboardSummary{Aggregates: []companyfund.FinanceDashboardAggregate{{
				Direction:        companyfund.DirectionInflow,
				Currency:         "USDT",
				TransactionCount: 1,
				Amount:           "10.000000000000000001",
				USDValue:         stringPointer("10.123456789012345678"),
				Drilldown:        filter,
			}}}, nil
		},
		details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			return nil, nil
		},
		update: func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			return companyfund.FinanceClassificationResult{}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)
	request := httpRequest(http.MethodGet, "/dashboard?dateFrom=2026-07-01T00:00:00Z&dateTo=2026-07-02T00:00:00Z&channel=SAFEHERON,AIRWALLEX&accountId=4&accountId=8&direction=INFLOW&currency=usdt&financeCategoryLevel1Id=11&financeCategoryLevel2Id=22&operatingIncomeExpense=true", "", map[string]string{companyFundAdminKeyHeader: "company-fund-admin-key"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("dashboard response = %d body=%s", response.Code, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	data := decoded["data"].(map[string]any)
	aggregates := data["aggregates"].([]any)
	aggregate := aggregates[0].(map[string]any)
	if amount, ok := aggregate["amount"].(string); !ok || amount != "10.000000000000000001" {
		t.Fatalf("exact amount JSON = %#v", aggregate["amount"])
	}
	if strings.Contains(response.Body.String(), "rawPayload") || strings.Contains(response.Body.String(), "ownedPayload") {
		t.Fatalf("finance dashboard response leaked provider payload field: %s", response.Body.String())
	}
}

func TestCompanyFundFinanceHandler_ClassificationUsesAdminActorAndManualFieldsOnly(t *testing.T) {
	level1 := int64(11)
	level2 := int64(22)
	operating := true
	override := false
	counterpartyName := "Vendor alias"
	store := companyFundFinanceStoreStub{
		dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			return companyfund.FinanceDashboardSummary{}, nil
		},
		details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			return nil, nil
		},
		update: func(_ context.Context, input companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			if input.TransactionID != 77 || input.FinanceCategoryLevel1ID == nil || *input.FinanceCategoryLevel1ID != level1 ||
				input.FinanceCategoryLevel2ID == nil || *input.FinanceCategoryLevel2ID != level2 ||
				input.IsOperatingIncomeExpense == nil || *input.IsOperatingIncomeExpense != operating ||
				input.Applicant == nil || *input.Applicant != "finance@monera" ||
				input.BusinessDescription == nil || *input.BusinessDescription != "July settlement" ||
				!input.CounterpartyNameOverrideSet || input.CounterpartyNameOverride == nil || *input.CounterpartyNameOverride != counterpartyName ||
				input.SummaryInclusionOverride == nil || *input.SummaryInclusionOverride != override || input.UpdatedBy != "finance-admin" {
				t.Fatalf("classification input = %#v", input)
			}
			return companyfund.FinanceClassificationResult{
				TransactionID:            input.TransactionID,
				FinanceCategoryLevel1ID:  input.FinanceCategoryLevel1ID,
				FinanceCategoryLevel2ID:  input.FinanceCategoryLevel2ID,
				IsOperatingIncomeExpense: input.IsOperatingIncomeExpense,
				Applicant:                *input.Applicant,
				BusinessDescription:      *input.BusinessDescription,
				SummaryInclusionOverride: input.SummaryInclusionOverride,
				CounterpartyNameOverride: input.CounterpartyNameOverride,
				ClassificationStatus:     "CLASSIFIED",
				UpdatedBy:                input.UpdatedBy,
				UpdatedAt:                time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)
	body := `{"financeCategoryLevel1Id":11,"financeCategoryLevel2Id":22,"isOperatingIncomeExpense":true,"applicant":"finance@monera","businessDescription":"July settlement","summaryInclusionOverride":false,"counterpartyNameOverride":"Vendor alias"}`
	request := httpRequest(http.MethodPut, "/transactions/77/classification", body, map[string]string{
		companyFundAdminKeyHeader:   "company-fund-admin-key",
		companyFundAdminActorHeader: "finance-admin",
		"Content-Type":              "application/json",
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("classification response = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"transactionId":77`) || !strings.Contains(response.Body.String(), `"counterpartyNameOverride":"Vendor alias"`) || strings.Contains(response.Body.String(), `"TransactionID"`) {
		t.Fatalf("classification JSON must be camelCase: %s", response.Body.String())
	}
}

func TestCompanyFundFinanceHandler_ClassificationCounterpartyOverridePresence(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		body      string
		wantSet   bool
		wantValue *string
	}{
		{name: "omitted preserves existing override", body: `{}`, wantSet: false},
		{name: "explicit null clears override", body: `{"counterpartyNameOverride":null}`, wantSet: true},
		{name: "blank clears override", body: `{"counterpartyNameOverride":"   "}`, wantSet: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			called := false
			store := companyFundFinanceStoreStub{
				dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
					return companyfund.FinanceDashboardSummary{}, nil
				},
				details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
					return nil, nil
				},
				update: func(_ context.Context, input companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
					called = true
					if input.CounterpartyNameOverrideSet != testCase.wantSet || input.CounterpartyNameOverride != testCase.wantValue {
						t.Fatalf("counterparty override presence = %#v", input)
					}
					return companyfund.FinanceClassificationResult{TransactionID: input.TransactionID}, nil
				},
			}
			router := newCompanyFundFinanceHandlerRouter(t, store)
			request := httpRequest(http.MethodPut, "/transactions/77/classification", testCase.body, map[string]string{
				companyFundAdminKeyHeader:   "company-fund-admin-key",
				companyFundAdminActorHeader: "finance-admin",
				"Content-Type":              "application/json",
			})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !called {
				t.Fatalf("classification response = %d called=%v body=%s", response.Code, called, response.Body.String())
			}
		})
	}
}

func TestCompanyFundFinanceHandler_ListTransactionsUsesSharedFilterAndPagination(t *testing.T) {
	store := companyFundFinanceStoreStub{
		dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			return companyfund.FinanceDashboardSummary{}, nil
		},
		details: func(_ context.Context, request companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			if request.Limit != 25 || request.Offset != 50 || !request.Filter.IncludeSummaryExcluded || len(request.Filter.Currencies) != 1 || request.Filter.Currencies[0] != "USDT" {
				t.Fatalf("detail request = %#v", request)
			}
			return []companyfund.FinanceTransactionDetail{{
				ID:                      99,
				Date:                    time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
				Channel:                 companyfund.ChannelAirwallex,
				Direction:               companyfund.DirectionInflow,
				Currency:                "USDT",
				Amount:                  "0.000000000000000001",
				USDValue:                stringPointer("0.000000000000000001"),
				ProviderTransactionID:   "provider-99",
				SummaryIncluded:         false,
				AutoExcludedFromSummary: true,
			}}, nil
		},
		update: func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			return companyfund.FinanceClassificationResult{}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)
	request := httpRequest(http.MethodGet, "/transactions?currency=usdt&includeSummaryExcluded=true&limit=25&offset=50", "", map[string]string{companyFundAdminKeyHeader: "company-fund-admin-key"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"amount":"0.000000000000000001"`) || strings.Contains(response.Body.String(), "rawPayload") {
		t.Fatalf("detail response = %d %s", response.Code, response.Body.String())
	}
}

func TestCompanyFundFinanceHandler_RejectsUnknownClassificationFieldsBeforeStore(t *testing.T) {
	called := false
	store := companyFundFinanceStoreStub{
		dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			return companyfund.FinanceDashboardSummary{}, nil
		},
		details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			return nil, nil
		},
		update: func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			called = true
			return companyfund.FinanceClassificationResult{}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)
	request := httpRequest(http.MethodPut, "/transactions/77/classification", `{"providerStatus":"must-not-be-writable"}`, map[string]string{
		companyFundAdminKeyHeader:   "company-fund-admin-key",
		companyFundAdminActorHeader: "finance-admin",
		"Content-Type":              "application/json",
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("unknown provider field response=%d called=%v, want 400 false", response.Code, called)
	}
}

func TestCompanyFundFinanceHandler_RejectsInvalidFilterBeforeStore(t *testing.T) {
	called := false
	store := companyFundFinanceStoreStub{
		dashboard: func(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
			called = true
			return companyfund.FinanceDashboardSummary{}, nil
		},
		details: func(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
			return nil, nil
		},
		update: func(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
			return companyfund.FinanceClassificationResult{}, nil
		},
	}
	router := newCompanyFundFinanceHandlerRouter(t, store)
	request := httpRequest(http.MethodGet, "/dashboard?channel=unsupported", "", map[string]string{companyFundAdminKeyHeader: "company-fund-admin-key"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || called {
		t.Fatalf("invalid filter response=%d called=%v, want 400 false", response.Code, called)
	}
}

func httpRequest(method, target, body string, headers map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	return request
}

func stringPointer(value string) *string { return &value }
