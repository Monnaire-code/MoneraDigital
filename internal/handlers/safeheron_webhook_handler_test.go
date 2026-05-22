package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

type fakeVerifier struct {
	convertFn func([]byte) (*safeheron.WebhookEvent, error)
}

func (f *fakeVerifier) WebhookConvert(body []byte) (*safeheron.WebhookEvent, error) {
	evt, err := f.convertFn(body)
	// Simulate real SDK: populate RawBody with the full decrypted plaintext so
	// the handler can store lossless payload (AML fields etc.).
	if err == nil && evt != nil && len(evt.RawBody) == 0 {
		if raw, merr := json.Marshal(evt); merr == nil {
			evt.RawBody = raw
		}
	}
	return evt, err
}

type fakeRecorder struct {
	insertFn func(ctx context.Context, evt *deposit.Event) (bool, error)
}

func (f *fakeRecorder) InsertEventOrSkip(ctx context.Context, evt *deposit.Event) (bool, error) {
	return f.insertFn(ctx, evt)
}

func newWebhookReq(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/webhooks/safeheron", strings.NewReader(body))
}

func runWebhook(h *SafeheronWebhookHandler, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = newWebhookReq(body)
	h.Receive(c)
	return w
}

func TestWebhook_AckBodyVerbatim(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventType: "TRANSACTION_STATUS_CHANGED",
				EventDetail: safeheron.EventDetail{
					TxKey:             "tx-1",
					TransactionStatus: "COMPLETED",
				},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return true, nil
		}},
		nil,
	)

	w := runWebhook(h, `{"any":"envelope"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != SafeheronAckBody {
		t.Fatalf("ack body mismatch: got %q want %q", w.Body.String(), SafeheronAckBody)
	}
}

// TestWebhook_RawPayloadPreservesOriginalBody verifies that raw_payload is the
// verbatim webhook body (so forensic replay can re-verify signatures and recover
// fields the SDK schema doesn't model: replaceTxHash, destinationAddressList,
// custom Safeheron metadata). Regression: T7-I-4 — previously the handler
// re-marshalled the SDK's stripped WebhookEvent struct, dropping unknown fields.
func TestWebhook_RawPayloadPreservesOriginalBody(t *testing.T) {
	originalBody := `{"timestamp":"1734567890123","sig":"abc","bizContent":"ciphertext","unknown_field":"forensic-data","destinationAddressList":[{"addr":"0xabc"}]}`
	var capturedRaw []byte
	h := NewSafeheronWebhookHandler(
		&rawVerifier{evt: &safeheron.WebhookEvent{
			EventDetail: safeheron.EventDetail{TxKey: "tx-1", TransactionStatus: "COMPLETED"},
			RawBody:     []byte(originalBody),
		}},
		&fakeRecorder{insertFn: func(_ context.Context, evt *deposit.Event) (bool, error) {
			capturedRaw = evt.RawPayload
			return true, nil
		}},
		nil,
	)

	w := runWebhook(h, originalBody)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if string(capturedRaw) != originalBody {
		t.Errorf("raw_payload must preserve original webhook body verbatim\n  got: %s\n want: %s",
			string(capturedRaw), originalBody)
	}
}

func TestWebhook_DuplicateStillAcks(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx-2", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return false, nil // duplicate
		}},
		nil,
	)
	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK || w.Body.String() != SafeheronAckBody {
		t.Fatalf("duplicate must still ack 200 + standard body, got %d %q", w.Code, w.Body.String())
	}
}

func TestWebhook_VerifyFailReturns401(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return nil, errors.New("bad signature")
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			t.Fatal("recorder should not be called on verify fail")
			return false, nil
		}},
		nil,
	)
	w := runWebhook(h, `{"sig":"bad"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SUCCESS") {
		t.Errorf("must not send SUCCESS body on auth fail")
	}
}

func TestWebhook_MissingTxKeyReturns400(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{EventType: "T", EventDetail: safeheron.EventDetail{}}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return true, nil
		}},
		nil,
	)
	w := runWebhook(h, `{}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWebhook_EmptyBodyReturns400(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			t.Fatal("verifier must not be called on empty body")
			return nil, nil
		}},
		&fakeRecorder{insertFn: nil},
		nil,
	)
	w := runWebhook(h, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWebhook_RecorderErrorReturns500(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return false, errors.New("db down")
		}},
		nil,
	)
	w := runWebhook(h, `{}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestWebhook_NilHandlerReturns503(t *testing.T) {
	var h *SafeheronWebhookHandler
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = newWebhookReq(`{}`)
	h.Receive(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// TestWebhook_BodyTooLargeReturns413 verifies that plan D-12's body limit uses
// http.MaxBytesReader semantics: a 2MB body must be rejected with 413 Payload
// Too Large, NOT silently truncated and passed to the verifier.
// Regression: T7-I-3.
func TestWebhook_BodyTooLargeReturns413(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 2*1024*1024)
	verifyCalled := false
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(b []byte) (*safeheron.WebhookEvent, error) {
			verifyCalled = true
			return nil, errors.New("must not be called for oversize body")
		}},
		&fakeRecorder{insertFn: nil},
		nil,
	)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/webhooks/safeheron",
		io.NopCloser(bytes.NewReader(big)))
	h.Receive(c)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 Payload Too Large, got %d", w.Code)
	}
	if verifyCalled {
		t.Errorf("verifier must not run for oversize body (defence-in-depth)")
	}
}

// TestWebhook_BodyExactlyAtLimitAccepted verifies the 1MB boundary is inclusive
// — a body exactly MaxWebhookBodyBytes long must NOT be rejected. Real-world
// envelopes are well under 16KB, but the boundary still matters.
func TestWebhook_BodyExactlyAtLimitAccepted(t *testing.T) {
	// Construct a JSON-ish payload that's exactly MaxWebhookBodyBytes.
	payload := make([]byte, MaxWebhookBodyBytes)
	// We don't care about the content; the verifier short-circuits.
	for i := range payload {
		payload[i] = 'a'
	}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(b []byte) (*safeheron.WebhookEvent, error) {
			if len(b) != MaxWebhookBodyBytes {
				t.Errorf("expected exactly %d bytes at verifier, got %d", MaxWebhookBodyBytes, len(b))
			}
			return nil, errors.New("body received OK, verify fails as expected")
		}},
		&fakeRecorder{insertFn: nil},
		nil,
	)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/webhooks/safeheron",
		io.NopCloser(bytes.NewReader(payload)))
	h.Receive(c)
	// Verify fails (we made it fail), so 401 — not 413.
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 verify-fail after accepting 1MB body, got %d", w.Code)
	}
}

// TestWebhook_BenchAckTime samples ack latency across many requests and asserts
// that the P99 stays well within plan §6 F-13 budget (real-world target < 2s).
// Without real RSA verification the synthetic ceiling is dramatically tighter
// (100ms) so a regression that adds blocking work to the hot path shows up.
// Regression: T7-S-5.
func TestWebhook_BenchAckTime(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return true, nil
		}},
		nil,
	)

	const samples = 100
	latencies := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		w := runWebhook(h, `{}`)
		latencies = append(latencies, time.Since(start))
		if w.Code != http.StatusOK {
			t.Fatalf("sample %d: expected 200, got %d", i, w.Code)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p99Idx := int(float64(samples)*0.99) - 1
	if p99Idx < 0 {
		p99Idx = 0
	}
	p99 := latencies[p99Idx]
	p50 := latencies[samples/2]

	// Plan §6 F-13 target is 2s P99 in production. The synthetic stack here
	// has no RSA / network / DB, so anything > 100ms suggests a regression.
	const budget = 100 * time.Millisecond
	if p99 > budget {
		t.Errorf("webhook ack P99 regressed: p50=%v p99=%v budget=%v", p50, p99, budget)
	}
}

// I-1 regression: AML_KYT_ALERT eventID must derive from body content (sha256),
// not wall-clock time. Two deliveries of the same encrypted body must collapse
// to a single eventID so Safeheron's retry storm is dedup'd by ON CONFLICT.
// Two deliveries of *different* body must produce *different* eventIDs so
// genuine follow-up alerts still land.
func TestWebhook_AMLAlertEventIDIsContentHash(t *testing.T) {
	var capturedEventIDs []string
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventType:   "AML_KYT_ALERT",
				EventDetail: safeheron.EventDetail{TxKey: "tx-aml-dedup"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, evt *deposit.Event) (bool, error) {
			capturedEventIDs = append(capturedEventIDs, evt.EventID)
			return true, nil
		}},
		nil,
	)

	bodyA := `{"timestamp":"1","sig":"sigA","bizContent":"ciphertextA"}`
	bodyB := `{"timestamp":"2","sig":"sigB","bizContent":"ciphertextB"}`

	// Two deliveries of identical body — should produce identical eventID
	runWebhook(h, bodyA)
	runWebhook(h, bodyA)
	if len(capturedEventIDs) != 2 {
		t.Fatalf("expected 2 webhook calls, got %d", len(capturedEventIDs))
	}
	if capturedEventIDs[0] != capturedEventIDs[1] {
		t.Errorf("I-1 regression: identical bodies must produce same eventID for dedup; got %q vs %q",
			capturedEventIDs[0], capturedEventIDs[1])
	}
	// And the eventID must start with the txKey + ":AML_KYT_ALERT:" prefix
	prefix := "tx-aml-dedup:AML_KYT_ALERT:"
	if !strings.HasPrefix(capturedEventIDs[0], prefix) {
		t.Errorf("eventID missing AML alert prefix: got %q", capturedEventIDs[0])
	}

	// Different body content — must produce a different eventID
	runWebhook(h, bodyB)
	if len(capturedEventIDs) != 3 {
		t.Fatalf("expected 3 webhook calls after bodyB, got %d", len(capturedEventIDs))
	}
	if capturedEventIDs[2] == capturedEventIDs[0] {
		t.Errorf("different bodies must produce different eventIDs: bodyA=%q bodyB=%q",
			capturedEventIDs[0], capturedEventIDs[2])
	}
}

// ---------------------------------------------------------------------------
// Empty RawBody after verify → 500
// ---------------------------------------------------------------------------

type rawVerifier struct {
	evt *safeheron.WebhookEvent
	err error
}

func (r *rawVerifier) WebhookConvert(_ []byte) (*safeheron.WebhookEvent, error) {
	return r.evt, r.err
}

func TestWebhook_EmptyRawBodyReturns500(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&rawVerifier{evt: &safeheron.WebhookEvent{
			EventType:   "TRANSACTION_STATUS_CHANGED",
			EventDetail: safeheron.EventDetail{TxKey: "tx-empty", TransactionStatus: "COMPLETED"},
			RawBody:     nil,
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			t.Fatal("recorder must not be called when RawBody is empty")
			return false, nil
		}},
		nil,
	)
	w := runWebhook(h, `{"sig":"ok"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty RawBody, got %d", w.Code)
	}
}

func TestWebhook_EmptyRawBodySliceReturns500(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&rawVerifier{evt: &safeheron.WebhookEvent{
			EventType:   "TRANSACTION_STATUS_CHANGED",
			EventDetail: safeheron.EventDetail{TxKey: "tx-zero", TransactionStatus: "COMPLETED"},
			RawBody:     []byte{},
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			t.Fatal("recorder must not be called when RawBody is empty slice")
			return false, nil
		}},
		nil,
	)
	w := runWebhook(h, `{"sig":"ok"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for empty RawBody slice, got %d", w.Code)
	}
}

// === T11.2 D-42: Webhook IP whitelist ===

func runWebhookWithIP(h *SafeheronWebhookHandler, body, clientIP string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = newWebhookReq(body)
	c.Request.RemoteAddr = clientIP + ":12345"
	h.Receive(c)
	return w
}

func TestWebhookIPWhitelist_BlockedIP(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			t.Fatal("verifier must not be called for blocked IP")
			return nil, nil
		}},
		&fakeRecorder{insertFn: nil},
		[]string{"1.2.3.4", "5.6.7.8"},
	)
	w := runWebhookWithIP(h, `{"any":"body"}`, "9.9.9.9")
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for blocked IP, got %d", w.Code)
	}
}

func TestWebhookIPWhitelist_AllowedIP(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx-ip", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return true, nil
		}},
		[]string{"1.2.3.4", "5.6.7.8"},
	)
	w := runWebhookWithIP(h, `{"any":"body"}`, "1.2.3.4")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for allowed IP, got %d", w.Code)
	}
}

func TestWebhookIPWhitelist_EmptyListAllowsAll(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx-noip", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) {
			return true, nil
		}},
		nil,
	)
	w := runWebhookWithIP(h, `{"any":"body"}`, "99.99.99.99")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 when no IP whitelist, got %d", w.Code)
	}
}

// SEC-1: when Gin is configured with no trusted proxies (production-safe
// default), a forged X-Forwarded-For header must not let an off-allowlist
// client pass the IP whitelist check.
func TestWebhookIPWhitelist_RejectsForgedXForwardedFor(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			t.Fatal("verifier must not be called when forged XFF is rejected")
			return nil, nil
		}},
		&fakeRecorder{insertFn: nil},
		[]string{"1.2.3.4"},
	)

	// Configure a real Gin engine with no trusted proxies — matches the
	// production main.go default (cfg.TrustedProxies=nil).
	gin.SetMode(gin.TestMode)
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	r.POST("/api/webhooks/safeheron", h.Receive)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/safeheron", strings.NewReader(`{"any":"body"}`))
	req.RemoteAddr = "9.9.9.9:443"               // off-allowlist direct peer
	req.Header.Set("X-Forwarded-For", "1.2.3.4") // forged spoof of allowlist member
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 (XFF must be ignored when no trusted proxies), got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// SEND-direction sweep update tests
// ---------------------------------------------------------------------------

type fakeSweepUpdater struct {
	updateFn   func(ctx context.Context, txKey, status, subStatus, txHash string, completedAt *time.Time) error
	called     bool
	lastKey    string
	lastStatus string
	lastSub    string
	lastHash   string
}

func (f *fakeSweepUpdater) UpdateSweepStatus(ctx context.Context, txKey, status, subStatus, txHash string, completedAt *time.Time) error {
	f.called = true
	f.lastKey = txKey
	f.lastStatus = status
	f.lastSub = subStatus
	f.lastHash = txHash
	return f.updateFn(ctx, txKey, status, subStatus, txHash, completedAt)
}

func sendWebhookEvent(txKey, status, direction string) *safeheron.WebhookEvent {
	return &safeheron.WebhookEvent{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: safeheron.EventDetail{
			TxKey:                txKey,
			TransactionStatus:    status,
			TransactionDirection: direction,
			TxHash:               "0xhash123",
		},
	}
}

func TestWebhookSweep_SendUpdatesStatus(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, completedAt *time.Time) error {
		if completedAt == nil {
			t.Error("completedAt should be set for COMPLETED status")
		}
		return nil
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-sweep-1", "COMPLETED", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !updater.called {
		t.Error("SweepUpdater should be called for SEND direction")
	}
	if updater.lastKey != "tx-sweep-1" {
		t.Errorf("txKey = %q, want tx-sweep-1", updater.lastKey)
	}
	if updater.lastStatus != "COMPLETED" {
		t.Errorf("status = %q, want COMPLETED", updater.lastStatus)
	}
	if updater.lastHash != "0xhash123" {
		t.Errorf("txHash = %q, want 0xhash123", updater.lastHash)
	}
}

func TestWebhookSweep_SendNotFound_StillAcks(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) error {
		return sql.ErrNoRows
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-unknown", "CONFIRMING", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even when sweep not found, got %d", w.Code)
	}
}

func TestWebhookSweep_SendDBError_StillAcks(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) error {
		return errors.New("connection refused")
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-err", "SIGNING", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even on sweep DB error, got %d", w.Code)
	}
}

// M-1: Sweep update DB error should trigger an alert
func TestWebhookSweep_SendDBError_SendsAlert(t *testing.T) {
	var alertCalled bool
	var alertTitle string
	var alertFields map[string]string

	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) error {
		return errors.New("connection refused")
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-alert", "COMPLETED", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)
	h.SetAlertFn(func(level, title string, fields map[string]string) {
		alertCalled = true
		alertTitle = title
		alertFields = fields
	})

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 even on sweep DB error, got %d", w.Code)
	}
	if !alertCalled {
		t.Fatal("AlertFn should be called when sweep update fails with DB error")
	}
	if alertFields["txKey"] != "tx-alert" {
		t.Errorf("alert txKey = %q, want tx-alert", alertFields["txKey"])
	}
	if alertFields["error"] != "connection refused" {
		t.Errorf("alert error = %q, want 'connection refused'", alertFields["error"])
	}
	_ = alertTitle
}

func TestWebhookSweep_ReceiveDirection_SkipsSweep(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, _ *time.Time) error {
		t.Fatal("SweepUpdater must NOT be called for RECEIVE direction")
		return nil
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-recv", "COMPLETED", "RECEIVE"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestWebhookSweep_NilUpdater_SkipsSweep(t *testing.T) {
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-nil", "COMPLETED", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	// No SetSweepUpdater call — SweepUpdater is nil

	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil SweepUpdater, got %d", w.Code)
	}
}

func TestWebhookSweep_FailedStatus_SetsCompletedAt(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, completedAt *time.Time) error {
		if completedAt == nil {
			t.Error("completedAt should be set for FAILED status")
		}
		return nil
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-fail", "FAILED", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)
	runWebhook(h, `{}`)
}

func TestWebhookSweep_SigningStatus_NoCompletedAt(t *testing.T) {
	updater := &fakeSweepUpdater{updateFn: func(_ context.Context, _, _, _, _ string, completedAt *time.Time) error {
		if completedAt != nil {
			t.Error("completedAt should be nil for SIGNING status")
		}
		return nil
	}}
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return sendWebhookEvent("tx-sign", "SIGNING", "SEND"), nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		nil,
	)
	h.SetSweepUpdater(updater)
	runWebhook(h, `{}`)
}
