package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type captureEmailer struct {
	mu    sync.Mutex
	calls [][2]string // [to, body]
	fail  bool
}

func (c *captureEmailer) SendActivationEmail(_ context.Context, to, body string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, [2]string{to, body})
	if c.fail {
		return errAlertEmailFailed
	}
	return nil
}

var errAlertEmailFailed = &emailFailErr{}

type emailFailErr struct{}

func (e *emailFailErr) Error() string { return "email failed" }

func TestAlertService_Nil_Safe(t *testing.T) {
	var a *AlertService
	a.Send("INFO", "title", nil) // should be no-op, not panic
}

func TestAlertService_FormatAlert_Deterministic(t *testing.T) {
	out := formatAlert("ERROR", "Deposit manual review", map[string]string{
		"userId":             "42",
		"reason":             "ADDRESS_UNASSIGNED",
		"destinationAddress": "0xabc",
	})
	want := strings.Join([]string{
		"【Phase1告警】level=ERROR",
		"title=Deposit manual review",
		"destinationAddress=0xabc",
		"reason=ADDRESS_UNASSIGNED",
		"userId=42",
		"",
	}, "\n")
	if out != want {
		t.Errorf("format mismatch:\nwant=%q\n got=%q", want, out)
	}
}

func TestAlertService_FeishuPOSTsJSON(t *testing.T) {
	var captured atomic.Pointer[map[string]any]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected JSON content-type, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		captured.Store(&m)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := NewAlertService(srv.URL, nil, nil)
	a.Send("ERROR", "Test", map[string]string{"reason": "X"})

	got := captured.Load()
	if got == nil {
		t.Fatal("feishu endpoint not hit")
	}
	if (*got)["msg_type"] != "text" {
		t.Errorf("expected msg_type=text, got %v", (*got)["msg_type"])
	}
	content, ok := (*got)["content"].(map[string]any)
	if !ok || !strings.Contains(content["text"].(string), "reason=X") {
		t.Errorf("content missing reason: %+v", (*got)["content"])
	}
}

func TestAlertService_EmailFanoutNonBlocking(t *testing.T) {
	emailer := &captureEmailer{}
	a := NewAlertService("", []string{"a@x.com", "b@x.com"}, emailer)
	a.Send("INFO", "Test", map[string]string{"k": "v"})
	if len(emailer.calls) != 2 {
		t.Errorf("expected 2 email calls, got %d", len(emailer.calls))
	}
}

func TestAlertService_EmailFailureSwallowed(t *testing.T) {
	emailer := &captureEmailer{fail: true}
	a := NewAlertService("", []string{"a@x.com"}, emailer)
	a.Send("INFO", "Test", nil) // must not panic / return error
	if len(emailer.calls) != 1 {
		t.Errorf("expected 1 email call even when it fails, got %d", len(emailer.calls))
	}
}

func TestAlertService_FeishuFailureSwallowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	a := NewAlertService(srv.URL, nil, nil)
	a.Send("INFO", "Test", nil) // must not error
}

func TestAlertService_EmptyFeishuURL_NoCall(t *testing.T) {
	a := NewAlertService("", nil, nil)
	a.Send("INFO", "Test", nil) // no panic on nil-everything
}
