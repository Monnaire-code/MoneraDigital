// internal/cache/token_blacklist.go
package cache

import (
	"context"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// TokenBlacklist 令牌黑名单
type TokenBlacklist struct {
	tokens map[string]time.Time
	mu     sync.RWMutex
	ticker *time.Ticker
	done   chan bool
	ctx    context.Context
	cancel context.CancelFunc
}

// NewTokenBlacklist 创建令牌黑名单
func NewTokenBlacklist() *TokenBlacklist {
	ctx, cancel := context.WithCancel(context.Background())
	tb := &TokenBlacklist{
		tokens: make(map[string]time.Time),
		ticker: time.NewTicker(1 * time.Hour),
		done:   make(chan bool, 1),
		ctx:    ctx,
		cancel: cancel,
	}

	// 启动清理过期令牌的后台任务
	go tb.cleanupExpiredTokens()

	return tb
}

// Add 添加令牌到黑名单
func (tb *TokenBlacklist) Add(token string, expiry time.Time) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.tokens[token] = expiry
}

// IsBlacklisted 检查令牌是否在黑名单中
func (tb *TokenBlacklist) IsBlacklisted(token string) bool {
	tb.mu.RLock()
	defer tb.mu.RUnlock()

	expiry, exists := tb.tokens[token]
	if !exists {
		return false
	}

	// 检查令牌是否已过期
	if time.Now().After(expiry) {
		// 令牌已过期，不需要在黑名单中
		return false
	}

	return true
}

// cleanupExpiredTokens 清理过期的令牌
func (tb *TokenBlacklist) cleanupExpiredTokens() {
	defer func() {
		if rv := recover(); rv != nil {
			log.Printf("token blacklist cleanup panic recovered: %v\n%s", rv, debug.Stack())
		}
	}()
	for {
		select {
		case <-tb.ticker.C:
			tb.mu.Lock()
			now := time.Now()
			for token, expiry := range tb.tokens {
				if now.After(expiry) {
					delete(tb.tokens, token)
				}
			}
			tb.mu.Unlock()

		case <-tb.ctx.Done():
			tb.ticker.Stop()
			return
		}
	}
}

// Close 关闭黑名单
func (tb *TokenBlacklist) Close() {
	tb.cancel()
}

// Size 获取黑名单中的令牌数量
func (tb *TokenBlacklist) Size() int {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	return len(tb.tokens)
}
