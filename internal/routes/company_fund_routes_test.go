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
}

func (s *companyFundRouteStore) GetFinanceDashboard(context.Context, companyfund.FinanceTransactionFilter) (companyfund.FinanceDashboardSummary, error) {
	s.dashboardCalls++
	return companyfund.FinanceDashboardSummary{}, nil
}

func (s *companyFundRouteStore) ListFinanceTransactionDetails(context.Context, companyfund.FinanceTransactionDetailRequest) ([]companyfund.FinanceTransactionDetail, error) {
	return []companyfund.FinanceTransactionDetail{}, nil
}

func (s *companyFundRouteStore) UpdateFinanceTransactionClassification(context.Context, companyfund.FinanceClassificationUpdate) (companyfund.FinanceClassificationResult, error) {
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

func TestSetupRoutes_CompanyFundFinanceUsesDedicatedAdminKeyWithoutJWT(t *testing.T) {
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
	router := gin.New()
	SetupRoutes(router, cont)

	request := httptest.NewRequest(http.MethodGet, "/api/company-fund/finance/dashboard", nil)
	request.Header.Set("X-Company-Fund-Admin-Key", "company-fund-admin-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || store.dashboardCalls != 1 {
		t.Fatalf("dedicated admin request status=%d calls=%d, want 200/1", response.Code, store.dashboardCalls)
	}

	jwtOnly := httptest.NewRequest(http.MethodGet, "/api/company-fund/finance/dashboard", nil)
	jwtOnly.Header.Set("Authorization", "Bearer customer-jwt")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, jwtOnly)
	if response.Code != http.StatusUnauthorized || store.dashboardCalls != 1 {
		t.Fatalf("ordinary JWT must not authorize company-fund route: status=%d calls=%d", response.Code, store.dashboardCalls)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/webhooks/airwallex", strings.NewReader(`{"id":"evt_1","name":"deposit.created"}`))
	request.Header.Set("x-timestamp", "1")
	request.Header.Set("x-signature", "accepted-by-test-verifier")
	response = httptest.NewRecorder()
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
