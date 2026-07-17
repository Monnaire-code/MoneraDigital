// internal/middleware/rate_limit_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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

// L2: whitelisted paths must bypass the limiter entirely. The homepage
// /api/fund/stats public read endpoint is exactly this case: a 60s
// in-memory cache (L1) is the primary fix, but the limiter should
// never even see the request — otherwise a misconfigured cache TTL or
// a route that forgets to wrap the cache would put us right back at
// "too many requests" for any user refreshing the homepage.
func TestRateLimitMiddleware_WhitelistedPathBypassesLimiter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewRateLimiter(2, 60*time.Second)
	defer rl.Stop()
	rl.SkipPath("/api/fund/stats")

	r := gin.New()
	r.Use(RateLimitMiddleware(rl))
	r.GET("/api/fund/stats", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// Hit the whitelisted path 10 times in a row — every request must
	// pass through, even though the limiter is configured for 2/60s.
	for i := 1; i <= 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/fund/stats", nil)
		req.RemoteAddr = "203.0.113.7:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("whitelisted request %d should be 200, got %d", i, w.Code)
		}
	}
}

// L2: non-whitelisted paths must still be limited. Guards against
// accidentally whitelisting everything via a sloppy config.
func TestRateLimitMiddleware_NonWhitelistedPathStillLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rl := NewRateLimiter(2, 60*time.Second)
	defer rl.Stop()
	rl.SkipPath("/api/fund/stats")

	r := gin.New()
	r.Use(RateLimitMiddleware(rl))
	r.GET("/api/auth/login", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
		req.RemoteAddr = "203.0.113.8:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("non-whitelisted request %d should be 200, got %d", i, w.Code)
		}
	}

	// 3rd request from same IP — must hit 429.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	req.RemoteAddr = "203.0.113.8:1234"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("3rd request should be 429, got %d", w.Code)
	}
}

// L2: different IPs are still isolated for whitelisted paths —
// whitelisting a path must not collapse the per-IP counter.
func TestRateLimiter_SkipPath_DoesNotAffectPerKeyCounter(t *testing.T) {
	rl := NewRateLimiter(2, 60*time.Second)
	defer rl.Stop()

	// Without SkipPath, two IPs are independent.
	rl.IsAllowed("ip-A")
	rl.IsAllowed("ip-A")
	if rl.IsAllowed("ip-A") {
		t.Fatal("ip-A 3rd request should be rejected")
	}
	if !rl.IsAllowed("ip-B") {
		t.Fatal("ip-B 1st request should be allowed (per-IP isolation)")
	}

	if rl.IsPathWhitelisted("/api/fund/stats") {
		t.Fatal("/api/fund/stats should not be whitelisted yet")
	}
	rl.SkipPath("/api/fund/stats")
	if !rl.IsPathWhitelisted("/api/fund/stats") {
		t.Fatal("/api/fund/stats should be whitelisted after SkipPath")
	}
}

// Provider webhook exemptions must be exact paths. A prefix exemption would
// accidentally remove the global limiter from unrelated future endpoints
// under /api/webhooks.
func TestRateLimiter_CompanyFundWebhookExemptionsAreExact(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	defer rl.Stop()
	rl.SkipPath("/api/webhooks/safeheron")
	rl.SkipPath("/api/webhooks/airwallex")

	for _, path := range []string{"/api/webhooks/safeheron", "/api/webhooks/airwallex"} {
		if !rl.IsPathWhitelisted(path) {
			t.Fatalf("%s should be an exact webhook exemption", path)
		}
	}
	for _, path := range []string{"/api/webhooks", "/api/webhooks/safeheron/replay", "/api/webhooks/airwallex/replay"} {
		if rl.IsPathWhitelisted(path) {
			t.Fatalf("%s must not be exempted by a provider webhook path", path)
		}
	}
}
