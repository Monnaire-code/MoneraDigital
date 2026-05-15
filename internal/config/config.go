package config

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

type Config struct {
	Port          string
	DatabaseURL   string
	RedisURL      string
	JWTSecret     string
	EncryptionKey string
	TimeZone      string
	CoreAPIURL    string
	// TrustedProxies controls which client addresses Gin trusts for X-Forwarded-For
	// inspection (SEC-1). Empty means trust nothing — c.ClientIP() returns the
	// direct peer address. Configure to LB CIDRs (e.g. "10.0.0.0/8") in production
	// when running behind a reverse proxy.
	TrustedProxies []string
}

// 全局时区配置
var (
	defaultLocation *time.Location
	locationOnce    sync.Once
	locationErr     error
)

func Load() *Config {
	// D-51: 非 production 环境，用 godotenv.Overload 把 .env 写入 os.Environ
	// 以覆盖 shell 残留的同名变量。viper.AutomaticEnv 随后从 os.Environ 读取，
	// 无需再 viper.ReadInConfig 二次加载。
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	// Use PORT from environment, default to 80 for Cloud Run compatibility
	viper.SetDefault("PORT", "80")
	viper.SetDefault("DATABASE_URL", "postgres://user:password@localhost/monera?sslmode=disable")
	viper.SetDefault("REDIS_URL", "redis://localhost:6379")
	viper.SetDefault("JWT_SECRET", "your-secret-key")
	viper.SetDefault("TIME_ZONE", "Asia/Shanghai")

	viper.AutomaticEnv()

	cfg := &Config{
		Port:           viper.GetString("PORT"),
		DatabaseURL:    viper.GetString("DATABASE_URL"),
		RedisURL:       viper.GetString("REDIS_URL"),
		JWTSecret:      viper.GetString("JWT_SECRET"),
		EncryptionKey:  viper.GetString("ENCRYPTION_KEY"),
		TimeZone:       viper.GetString("TIME_ZONE"),
		CoreAPIURL:     viper.GetString("MONNAIRE_CORE_API_URL"),
		TrustedProxies: splitTrustedProxies(viper.GetString("TRUSTED_PROXIES")),
	}

	return cfg
}

// splitTrustedProxies parses a comma-separated TRUSTED_PROXIES env var into a
// slice. Empty input returns nil — Gin treats nil as "trust no proxies".
func splitTrustedProxies(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetLocation returns the configured timezone location.
// Falls back to UTC+8 (Asia/Shanghai) if timezone is invalid or unavailable.
func GetLocation() *time.Location {
	locationOnce.Do(func() {
		cfg := Load()
		timeZone := cfg.TimeZone

		if timeZone == "" {
			timeZone = "Asia/Shanghai"
		}

		loc, err := time.LoadLocation(timeZone)
		if err != nil {
			locationErr = err
			// Fallback to fixed UTC+8
			defaultLocation = time.FixedZone(timeZone, 8*60*60)
			return
		}

		defaultLocation = loc
	})

	return defaultLocation
}

// GetLocationWithTimezone returns the timezone location for the given timezone string.
// Used when you need a specific timezone different from the default.
func GetLocationWithTimezone(timeZone string) *time.Location {
	if timeZone == "" {
		return GetLocation()
	}

	loc, err := time.LoadLocation(timeZone)
	if err != nil {
		return time.FixedZone(timeZone, 8*60*60)
	}

	return loc
}

// NowInDefaultZone returns the current time in the configured timezone.
func NowInDefaultZone() time.Time {
	return time.Now().In(GetLocation())
}

// TodayInDefaultZone returns today's date string (YYYY-MM-DD) in the configured timezone.
func TodayInDefaultZone() string {
	return NowInDefaultZone().Format("2006-01-02")
}

// GetLocationError returns any error that occurred while loading the timezone.
func GetLocationError() error {
	return locationErr
}
