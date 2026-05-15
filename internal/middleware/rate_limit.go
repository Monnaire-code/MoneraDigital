// internal/middleware/rate_limit.go
package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter 速率限制器
type RateLimiter struct {
	store  map[string][]time.Time
	mu     sync.RWMutex
	limit  int
	window time.Duration
	ctx    context.Context
	cancel context.CancelFunc
}

// NewRateLimiter 创建速率限制器
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	rl := &RateLimiter{
		store:  make(map[string][]time.Time),
		limit:  limit,
		window: window,
		ctx:    ctx,
		cancel: cancel,
	}

	// 启动清理过期时间戳的后台任务
	go rl.cleanupExpiredTimestamps()

	return rl
}

func (rl *RateLimiter) Stop() {
	rl.cancel()
}

func (rl *RateLimiter) IsAllowed(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	timestamps := rl.store[key]

	valid := make([]time.Time, 0, len(timestamps))
	for _, ts := range timestamps {
		if now.Sub(ts) < rl.window {
			valid = append(valid, ts)
		}
	}

	if len(valid) >= rl.limit {
		rl.store[key] = valid
		return false
	}

	valid = append(valid, now)
	rl.store[key] = valid
	return true
}

// cleanupExpiredTimestamps 清理过期的时间戳
func (rl *RateLimiter) cleanupExpiredTimestamps() {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("rate limiter cleanup panic recovered: %v\n%s", rv, debug.Stack())
		}
	}()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			keysToDelete := []string{}
			for key, timestamps := range rl.store {
				var valid []time.Time
				for _, ts := range timestamps {
					if now.Sub(ts) < rl.window {
						valid = append(valid, ts)
					}
				}

				if len(valid) == 0 {
					keysToDelete = append(keysToDelete, key)
				} else {
					rl.store[key] = valid
				}
			}
			for _, key := range keysToDelete {
				delete(rl.store, key)
			}
			rl.mu.Unlock()

		case <-rl.ctx.Done():
			return
		}
	}
}

// RateLimitMiddleware 速率限制中间件
func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取客户端 IP
		clientIP := c.ClientIP()

		// 检查是否允许请求
		if !limiter.IsAllowed(clientIP) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests",
				"code":  "RATE_LIMIT_EXCEEDED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// PerEndpointRateLimiter 每个端点的速率限制器
type PerEndpointRateLimiter struct {
	limiters map[string]*RateLimiter
	mu       sync.RWMutex
}

// NewPerEndpointRateLimiter 创建每个端点的速率限制器
func NewPerEndpointRateLimiter() *PerEndpointRateLimiter {
	return &PerEndpointRateLimiter{
		limiters: make(map[string]*RateLimiter),
	}
}

// AddEndpoint 添加端点限制
func (p *PerEndpointRateLimiter) AddEndpoint(endpoint string, limit int, window time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.limiters[endpoint] = NewRateLimiter(limit, window)
}

// Middleware 返回中间件
func (p *PerEndpointRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p.mu.RLock()
		limiter, exists := p.limiters[c.Request.URL.Path]
		p.mu.RUnlock()

		if !exists {
			// 使用默认限制（5 请求/分钟）
			limiter = NewRateLimiter(5, 1*time.Minute)
		}

		clientIP := c.ClientIP()
		key := fmt.Sprintf("%s:%s", c.Request.URL.Path, clientIP)

		if !limiter.IsAllowed(key) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests",
				"code":  "RATE_LIMIT_EXCEEDED",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}
