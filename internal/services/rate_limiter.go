package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	RateLimitWindowMinutes    = 3 // 3 minutes between activation code sends
	RateLimitMaxAttemptsIP    = 10
	RateLimitMaxAttemptsEmail = 10 // Same email can request multiple times in window
)

type RateLimiter struct {
	db *sql.DB
}

func NewRateLimiter(db *sql.DB) *RateLimiter {
	return &RateLimiter{db: db}
}

type RateLimitResult struct {
	Allowed     bool
	Remaining   int
	RetryAfter  int
	WindowStart time.Time
}

func (r *RateLimiter) CheckAndIncrement(ctx context.Context, keyType, keyValue, action string, limit int) (*RateLimitResult, error) {
	now := time.Now()
	windowStart := now.Add(-time.Duration(RateLimitWindowMinutes) * time.Minute)

	var result RateLimitResult

	query := `
		INSERT INTO rate_limits (key_type, key_value, action, attempt_count, window_start, updated_at)
		VALUES ($1, $2, $3, 1, $4, NOW())
		ON CONFLICT (key_type, key_value, action) 
		DO UPDATE SET 
			attempt_count = CASE 
				WHEN rate_limits.window_start < $4 THEN 1
				ELSE rate_limits.attempt_count + 1
			END,
			window_start = CASE
				WHEN rate_limits.window_start < $4 THEN $4
				ELSE rate_limits.window_start
			END,
			updated_at = NOW()
		RETURNING attempt_count, window_start
	`

	err := r.db.QueryRowContext(ctx, query, keyType, keyValue, action, windowStart).Scan(
		&result.Remaining, &result.WindowStart,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to check rate limit: %w", err)
	}

	if result.Remaining > limit {
		result.Allowed = false
		result.Remaining = 0
		retryAfter := int(time.Until(result.WindowStart.Add(time.Duration(RateLimitWindowMinutes) * time.Minute)).Seconds())
		if retryAfter < 0 {
			retryAfter = 0
		}
		result.RetryAfter = retryAfter
	} else {
		result.Allowed = true
		result.Remaining = limit - result.Remaining
		result.RetryAfter = 0
	}

	return &result, nil
}

func (r *RateLimiter) GetAttempts(ctx context.Context, keyType, keyValue, action string) (int, time.Time, error) {
	now := time.Now()
	windowStart := now.Add(-time.Duration(RateLimitWindowMinutes) * time.Minute)

	var attemptCount int
	var windowStartDB time.Time

	query := `
		SELECT attempt_count, window_start 
		FROM rate_limits 
		WHERE key_type = $1 AND key_value = $2 AND action = $3 AND window_start > $4
	`

	err := r.db.QueryRowContext(ctx, query, keyType, keyValue, action, windowStart).Scan(&attemptCount, &windowStartDB)
	if err == sql.ErrNoRows {
		return 0, now, nil
	}
	if err != nil {
		return 0, now, fmt.Errorf("failed to get rate limit attempts: %w", err)
	}

	return attemptCount, windowStartDB, nil
}

func (r *RateLimiter) Reset(ctx context.Context, keyType, keyValue, action string) error {
	query := `DELETE FROM rate_limits WHERE key_type = $1 AND key_value = $2 AND action = $3`
	_, err := r.db.ExecContext(ctx, query, keyType, keyValue, action)
	if err != nil {
		return fmt.Errorf("failed to reset rate limit: %w", err)
	}
	return nil
}

func GetClientIP(c context.Context) string {
	if ip, ok := c.Value("clientIP").(string); ok {
		return ip
	}
	return "unknown"
}

func GenerateRateLimitKey(rateType, value string) string {
	return fmt.Sprintf("%s:%s", rateType, value)
}
