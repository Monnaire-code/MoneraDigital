package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	sdkCosigner "github.com/Safeheron/safeheron-api-sdk-go/safeheron/cosigner"

	"monera-digital/internal/approval"
	"monera-digital/internal/safeheron"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type stubParser struct {
	parseResult *safeheron.CoSignerBizContentV3
	parseErr    error
	buildResult map[string]string
	buildErr    error
}

func (s *stubParser) ParseRequest(_ sdkCosigner.CoSignerCallBackV3) (*safeheron.CoSignerBizContentV3, error) {
	return s.parseResult, s.parseErr
}

func (s *stubParser) BuildResponse(_, _ string) (map[string]string, error) {
	return s.buildResult, s.buildErr
}

type stubEvaluator struct {
	result *approval.ApprovalDecision
	err    error
}

func (s *stubEvaluator) Evaluate(_ context.Context, _ *safeheron.CoSignerBizContentV3) (*approval.ApprovalDecision, error) {
	return s.result, s.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func cosignerRoute(h *CosignerCallbackHandler) *gin.Engine {
	r := gin.New()
	r.POST("/api/cosigner/callback", h.Handle)
	return r
}

func cosignerReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/cosigner/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func validBody() string {
	b, _ := json.Marshal(sdkCosigner.CoSignerCallBackV3{
		Timestamp: "1716300000000", Sig: "sig", Version: "v3", BizContent: "enc",
	})
	return string(b)
}

// ---------------------------------------------------------------------------
// 503 — nil / uninitialized
// ---------------------------------------------------------------------------

func TestCosignerCallback_NilHandler(t *testing.T) {
	var h *CosignerCallbackHandler
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = cosignerReq("{}")
	h.Handle(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestCosignerCallback_NilParser(t *testing.T) {
	h := &CosignerCallbackHandler{Evaluator: &stubEvaluator{}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = cosignerReq("{}")
	h.Handle(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 403 — IP whitelist
// ---------------------------------------------------------------------------

func TestCosignerCallback_IPBlocked(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser:     &stubParser{},
		Evaluator:  &stubEvaluator{},
		AllowedIPs: []string{"10.0.0.1"},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq("{}"))
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 413 — body too large
// ---------------------------------------------------------------------------

func TestCosignerCallback_BodyTooLarge(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser:    &stubParser{},
		Evaluator: &stubEvaluator{},
	}
	r := cosignerRoute(h)
	bigBody := strings.Repeat("x", 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/api/cosigner/callback", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 400 — invalid JSON
// ---------------------------------------------------------------------------

func TestCosignerCallback_InvalidJSON(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser:    &stubParser{},
		Evaluator: &stubEvaluator{},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq("not-json"))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 401 — verify fails + alert
// ---------------------------------------------------------------------------

func TestCosignerCallback_VerifyFails(t *testing.T) {
	var alertCalled bool
	h := &CosignerCallbackHandler{
		Parser:    &stubParser{parseErr: errors.New("bad sig")},
		Evaluator: &stubEvaluator{},
		AlertFn: func(_, _ string, _ map[string]string) {
			alertCalled = true
		},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if !alertCalled {
		t.Error("verify failure should trigger alert")
	}
}

func TestCosignerCallback_VerifyFails_NilAlert(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser:    &stubParser{parseErr: errors.New("bad sig")},
		Evaluator: &stubEvaluator{},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 500 — service error
// ---------------------------------------------------------------------------

func TestCosignerCallback_ServiceError(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser: &stubParser{parseResult: &safeheron.CoSignerBizContentV3{
			ApprovalId: "ap-1", Type: "TRANSACTION",
		}},
		Evaluator: &stubEvaluator{err: errors.New("db down")},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 500 — build response error
// ---------------------------------------------------------------------------

func TestCosignerCallback_BuildResponseError(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser: &stubParser{
			parseResult: &safeheron.CoSignerBizContentV3{ApprovalId: "ap-1", Type: "CALLBACK_TEST"},
			buildErr:    errors.New("sign failed"),
		},
		Evaluator: &stubEvaluator{
			result: &approval.ApprovalDecision{Action: "APPROVE"},
		},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 200 — happy path APPROVE
// ---------------------------------------------------------------------------

func TestCosignerCallback_HappyPath_Approve(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser: &stubParser{
			parseResult: &safeheron.CoSignerBizContentV3{
				ApprovalId: "ap-1", Type: "CALLBACK_TEST", Detail: json.RawMessage(`{}`),
			},
			buildResult: map[string]string{"code": "200", "sig": "ok", "bizContent": "enc"},
		},
		Evaluator: &stubEvaluator{
			result: &approval.ApprovalDecision{Action: "APPROVE", Reason: "test"},
		},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "200" {
		t.Errorf("resp code = %q, want 200", resp["code"])
	}
	if resp["sig"] != "ok" {
		t.Errorf("resp sig = %q, want ok", resp["sig"])
	}
}

// ---------------------------------------------------------------------------
// 200 — happy path REJECT
// ---------------------------------------------------------------------------

func TestCosignerCallback_HappyPath_Reject(t *testing.T) {
	h := &CosignerCallbackHandler{
		Parser: &stubParser{
			parseResult: &safeheron.CoSignerBizContentV3{
				ApprovalId: "ap-2", Type: "MPC_SIGN", Detail: json.RawMessage(`{}`),
			},
			buildResult: map[string]string{"code": "200", "sig": "rej"},
		},
		Evaluator: &stubEvaluator{
			result: &approval.ApprovalDecision{Action: "REJECT", Reason: "unsupported"},
		},
	}
	r := cosignerRoute(h)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, cosignerReq(validBody()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestCosignerClient_ImplementsParser(t *testing.T) {
	var _ CosignerParser = (*safeheron.CosignerClient)(nil)
}

func TestApprovalService_ImplementsEvaluator(t *testing.T) {
	var _ CosignerEvaluator = (*approval.ApprovalService)(nil)
}

func TestNewCosignerCallbackHandler(t *testing.T) {
	p := &stubParser{}
	e := &stubEvaluator{}
	ips := []string{"1.2.3.4"}
	fn := func(_, _ string, _ map[string]string) {}

	h := NewCosignerCallbackHandler(p, e, ips, fn)
	if h.Parser != p {
		t.Error("parser not set")
	}
	if h.Evaluator != e {
		t.Error("evaluator not set")
	}
	if len(h.AllowedIPs) != 1 {
		t.Errorf("allowedIPs len = %d, want 1", len(h.AllowedIPs))
	}
	if h.AlertFn == nil {
		t.Error("alertFn should not be nil")
	}
}
