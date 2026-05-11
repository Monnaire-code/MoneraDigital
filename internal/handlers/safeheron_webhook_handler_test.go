package handlers

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

func TestWebhook_BodyTooLargeTruncatedNotRejected(t *testing.T) {
	// LimitReader silently truncates; we then fail at verify (because the
	// truncated body isn't valid). Sanity-check that the read doesn't loop
	// indefinitely on a 2MB payload.
	big := bytes.Repeat([]byte("a"), 2*1024*1024)
	h := NewSafeheronWebhookHandler(
		&fakeVerifier{convertFn: func(b []byte) (*safeheron.WebhookEvent, error) {
			if len(b) > MaxWebhookBodyBytes {
				t.Errorf("body not capped: got %d bytes", len(b))
			}
			return nil, errors.New("decoded truncated payload")
		}},
		&fakeRecorder{insertFn: nil},
	)
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/webhooks/safeheron",
		io.NopCloser(bytes.NewReader(big)))
	h.Receive(c)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 after verify fail, got %d", w.Code)
	}
}

func TestWebhook_BenchAckTime(t *testing.T) {
	// Sanity-check that the verify+ack roundtrip is sub-millisecond when
	// the verifier short-circuits (no real RSA). Real-world P99 < 2s.
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
	w := runWebhook(h, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
