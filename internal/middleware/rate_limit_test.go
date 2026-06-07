// internal/middleware/rate_limit_test.go
package middleware

import (
	"testing"
	"time"
)

// B1 regression: the previous call site used `NewRateLimiter(5, 60)` where
// 60 was interpreted as 60 nanoseconds, making the global limiter a no-op.
// These tests pin the contract: a 5/60s limiter must reject the 6th
// request inside the window and re-admit after the window elapses.
func TestRateLimiter_AllowsUpToLimitInsideWindow(t *testing.T) {
	rl := NewRateLimiter(5, 60*time.Second)
	defer rl.Stop()

	for i := 1; i <= 5; i++ {
		if !rl.IsAllowed("client-A") {
			t.Fatalf("request %d should be allowed within limit", i)
		}
	}
}

func TestRateLimiter_RejectsOverLimitInsideWindow(t *testing.T) {
	rl := NewRateLimiter(5, 60*time.Second)
	defer rl.Stop()

	for i := 1; i <= 5; i++ {
		rl.IsAllowed("client-A")
	}

	if rl.IsAllowed("client-A") {
		t.Fatal("6th request inside the window must be rejected")
	}
}

func TestRateLimiter_PerKeyIsolation(t *testing.T) {
	rl := NewRateLimiter(2, 60*time.Second)
	defer rl.Stop()

	rl.IsAllowed("client-A")
	rl.IsAllowed("client-A")
	if rl.IsAllowed("client-A") {
		t.Fatal("client-A 3rd request should be rejected")
	}

	if !rl.IsAllowed("client-B") {
		t.Fatal("client-B 1st request should be allowed (per-key isolation)")
	}
}

func TestRateLimiter_AdmitsAgainAfterWindow(t *testing.T) {
	rl := NewRateLimiter(2, 30*time.Millisecond)
	defer rl.Stop()

	rl.IsAllowed("client-A")
	rl.IsAllowed("client-A")
	if rl.IsAllowed("client-A") {
		t.Fatal("3rd request inside window should be rejected")
	}

	time.Sleep(40 * time.Millisecond)

	if !rl.IsAllowed("client-A") {
		t.Fatal("request after window elapsed must be allowed again")
	}
}

func TestRateLimiter_DefaultWindowIsSubSecond_NoLonger(t *testing.T) {
	// B1: the previous default was 60ns (interpreted from bare int 60).
	// Verify a freshly-constructed limiter with a 60s window does NOT
	// admit a burst of 1000 requests in 1ms.
	rl := NewRateLimiter(5, 60*time.Second)
	defer rl.Stop()

	for i := 1; i <= 5; i++ {
		rl.IsAllowed("burst")
	}

	start := time.Now()
	allowed := 0
	for i := 0; i < 1000; i++ {
		if rl.IsAllowed("burst") {
			allowed++
		}
	}
	elapsed := time.Since(start)

	if allowed != 0 {
		t.Fatalf("expected 0 admits after limit reached in 1ms, got %d", allowed)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("1000 IsAllowed calls should be fast; took %s", elapsed)
	}
}
