package utils

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReportPanicTo_SendsFormattedError(t *testing.T) {
	ch := make(chan error, 1)
	func() {
		defer ReportPanicTo(ch)
		panic("boom")
	}()

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected non-nil error after panic")
		}
		if !errors.Is(err, ErrServerPanic) {
			t.Errorf("expected errors.Is(err, ErrServerPanic), got %v", err)
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("error should include panic value, got %q", err.Error())
		}
		// Stack stays in logs only — error must stay a short single-line wrap.
		if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "runtime/debug") {
			t.Errorf("error must not embed stack trace (log-only); got %q", err.Error())
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for panic error on channel")
	}
}

func TestReportPanicTo_NoPanic_NoSend(t *testing.T) {
	ch := make(chan error, 1)
	func() {
		defer ReportPanicTo(ch)
	}()

	select {
	case err := <-ch:
		t.Fatalf("expected no error on channel when no panic, got %v", err)
	default:
	}
}

func TestFatalLabelForServerErr_PanicVsStartFailure(t *testing.T) {
	if got := FatalLabelForServerErr(ErrServerPanic); got != "Server crashed" {
		t.Errorf("panic label: got %q, want Server crashed", got)
	}

	ch := make(chan error, 1)
	func() {
		defer ReportPanicTo(ch)
		panic("x")
	}()
	err := <-ch
	if got := FatalLabelForServerErr(err); got != "Server crashed" {
		t.Errorf("wrapped panic label: got %q, want Server crashed", got)
	}

	startErr := errors.New("listen tcp :8081: bind: address already in use")
	if got := FatalLabelForServerErr(startErr); got != "Server failed to start" {
		t.Errorf("start failure label: got %q, want Server failed to start", got)
	}
	if got := FatalLabelForServerErr(nil); got != "Server failed to start" {
		t.Errorf("nil err label: got %q", got)
	}
}
