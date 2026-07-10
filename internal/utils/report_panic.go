package utils

import (
	"errors"
	"fmt"
	"log"
	"runtime/debug"
)

// ErrServerPanic is returned (wrapped) when ReportPanicTo recovers a panic in
// the HTTP server goroutine. Callers can use errors.Is to distinguish a crash
// from a ListenAndServe startup failure.
var ErrServerPanic = errors.New("server goroutine panic")

// ReportPanicTo recovers a panic in the calling goroutine, logs the full stack
// trace (same pattern as deposit worker / pool replenisher), and forwards a
// short wrapped ErrServerPanic on errCh — stack stays in logs only.
//
//	go func() {
//	    defer utils.ReportPanicTo(errCh)
//	    ...
//	}()
func ReportPanicTo(errCh chan<- error) {
	if r := recover(); r != nil {
		log.Printf("server goroutine panic recovered: %v\n%s", r, debug.Stack())
		errCh <- fmt.Errorf("%w: %v", ErrServerPanic, r)
	}
}

// FatalLabelForServerErr returns the log message for a serverErrCh failure.
// Panic recoveries use "Server crashed"; ListenAndServe / other errors use
// "Server failed to start".
func FatalLabelForServerErr(err error) string {
	if errors.Is(err, ErrServerPanic) {
		return "Server crashed"
	}
	return "Server failed to start"
}
