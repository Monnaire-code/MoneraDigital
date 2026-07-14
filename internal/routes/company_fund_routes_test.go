package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/container"
	"monera-digital/internal/handlers"
	"monera-digital/internal/middleware"
	"monera-digital/internal/repository"
)

type companyFundRouteStore struct {
	dashboardCalls int
	listCalls      int
	updateCalls    int
}

func (s *companyFundRouteStore) GetFinanceDashboard(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
	s.dashboardCalls++
	return companyfund.FinanceDashboardSummary{}, nil
}

func (s *companyFundRouteStore) ListFinanceTransactionDetails(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
	s.listCalls++
	return []companyfund.FinanceTransactionDetail{}, nil
}

func (s *companyFundRouteStore) UpdateFinanceTransactionClassification(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
	s.updateCalls++
	return companyfund.FinanceClassificationResult{}, nil
}

type companyFundRouteAirwallexVerifier struct{}

func (companyFundRouteAirwallexVerifier) Verify(string, string, []byte) error { return nil }

type companyFundRouteAirwallexIngestor struct {
	calls int
}

func (s *companyFundRouteAirwallexIngestor) Ingest(context.Context, companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
	s.calls++
	return companyfund.ProviderEventInsertResult{ID: 1, Inserted: true}, nil
}

func TestSetupRoutes_CompanyFundManagementRoutesStayDisabledWhileWebhooksRemainActive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &companyFundRouteStore{}
	finance, err := handlers.NewCompanyFundFinanceHandler(handlers.CompanyFundFinanceHandlerConfig{
		Store:    store,
		AdminKey: "company-fund-admin-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	cont := newCompanyFundRouteContainer()
	defer cont.RateLimiter.Stop()
	cont.CompanyFundFinanceHandler = finance
	ingestor := &companyFundRouteAirwallexIngestor{}
	webhook, err := handlers.NewCompanyFundAirwallexWebhookHandler(handlers.CompanyFundAirwallexWebhookHandlerConfig{
		Verifier:             companyFundRouteAirwallexVerifier{},
		Ingestor:             ingestor,
		ProviderEventVersion: "2026-05-29",
		KeyVersion:           "payload-v1",
		Retention:            time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	cont.CompanyFundAirwallexWebhookHandler = webhook
	cont.SafeheronWebhookHandler = handlers.NewSafeheronWebhookHandler(nil, nil, nil)
	router := gin.New()
	SetupRoutes(router, cont)

	managementRoutes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "dashboard", method: http.MethodGet, path: "/api/company-fund/finance/dashboard"},
		{name: "transactions", method: http.MethodGet, path: "/api/company-fund/finance/transactions"},
		{
			name:   "classification",
			method: http.MethodPut,
			path:   "/api/company-fund/finance/transactions/77/classification",
			body:   `{"isOperatingIncomeExpense":true}`,
		},
	}
	for _, route := range managementRoutes {
		t.Run(route.name, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
			request.Header.Set("X-Company-Fund-Admin-Key", "company-fund-admin-key")
			request.Header.Set("X-Company-Fund-Admin-Actor", "finance-admin")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Errorf("%s management route status=%d, want 404", route.name, response.Code)
			}
		})
	}
	if store.dashboardCalls != 0 || store.listCalls != 0 || store.updateCalls != 0 {
		t.Fatalf(
			"disabled management routes reached store: dashboard=%d list=%d update=%d",
			store.dashboardCalls,
			store.listCalls,
			store.updateCalls,
		)
	}

	foundSafeheronWebhook := false
	foundAirwallexWebhook := false
	for _, route := range router.Routes() {
		if strings.HasPrefix(route.Path, "/api/company-fund/finance") {
			t.Errorf("company-fund management route must not be registered: %s %s", route.Method, route.Path)
		}
		if route.Method == http.MethodPost && route.Path == "/api/webhooks/safeheron" {
			foundSafeheronWebhook = true
		}
		if route.Method == http.MethodPost && route.Path == "/api/webhooks/airwallex" {
			foundAirwallexWebhook = true
		}
	}
	if !foundSafeheronWebhook || !foundAirwallexWebhook {
		t.Fatalf("webhook routes missing: safeheron=%v airwallex=%v", foundSafeheronWebhook, foundAirwallexWebhook)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/webhooks/airwallex", strings.NewReader(`{"id":"evt_1","name":"deposit.created"}`))
	request.Header.Set("x-timestamp", "1")
	request.Header.Set("x-signature", "accepted-by-test-verifier")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || ingestor.calls != 1 {
		t.Fatalf("Airwallex Go webhook status=%d calls=%d, want 200/1", response.Code, ingestor.calls)
	}
}

func newCompanyFundRouteContainer() *container.Container {
	return &container.Container{
		JWTSecret:   "customer-jwt-secret",
		RateLimiter: middleware.NewRateLimiter(100, time.Minute),
		Repository:  &repository.Repository{},
	}
}
