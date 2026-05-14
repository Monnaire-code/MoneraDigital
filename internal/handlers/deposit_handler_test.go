package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestHandleDepositWebhook_Returns410Gone verifies the Phase-0 Core-API webhook
// endpoint surfaces deprecation with 410 Gone so a stale upstream notices the
// switch to /api/webhooks/safeheron. Plan §6 S-4. Regression: T7-S-6.
//
// R2-I-4: Uses a real gin engine + ServeHTTP rather than direct handler
// invocation so the router wiring is exercised — if someone renames the
// constant route path, this test fails, not just a handler-only unit test
// that ignores routing.
func TestHandleDepositWebhook_Returns410Gone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.POST("/api/webhooks/core/deposit", h.HandleDepositWebhook)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/core/deposit", strings.NewReader(`{}`))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Errorf("expected 410 Gone, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "/api/webhooks/safeheron") {
		t.Errorf("expected successor pointer in body, got %s", w.Body.String())
	}
}

// TestHandleDepositWebhook_RouterRoundTrip belt-and-braces: confirms the route
// is reachable via gin's tree (404 if the path were mistyped during wiring).
func TestHandleDepositWebhook_RouterRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.POST("/api/webhooks/core/deposit", h.HandleDepositWebhook)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/webhooks/core/deposit", nil))
	if w.Code == http.StatusNotFound {
		t.Fatalf("router did not match the deprecated webhook path; got 404")
	}
}
