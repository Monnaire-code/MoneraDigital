package handlers

import (
	"bytes"
	"context"
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
	return f.convertFn(body)
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
		&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
			return &safeheron.WebhookEvent{
				EventDetail: safeheron.EventDetail{TxKey: "tx-1", TransactionStatus: "COMPLETED"},
			}, nil
		}},
		&fakeRecorder{insertFn: func(_ context.Context, evt *deposit.Event) (bool, error) {
			capturedRaw = evt.RawPayload
			return true, nil
		}},
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
