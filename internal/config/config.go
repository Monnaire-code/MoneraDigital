package config

import (
	"sync"
	"time"

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
}

// 全局时区配置
var (
	defaultLocation *time.Location
	locationOnce    sync.Once
	locationErr     error
)

func Load() *Config {
	viper.SetConfigFile(".env")
	viper.ReadInConfig() // Ignore error if .env doesn't exist

	// Use PORT from environment, default to 80 for Cloud Run compatibility
	viper.SetDefault("PORT", "80")
	viper.SetDefault("DATABASE_URL", "postgres://user:password@localhost/monera?sslmode=disable")
	viper.SetDefault("REDIS_URL", "redis://localhost:6379")
	viper.SetDefault("JWT_SECRET", "your-secret-key")
	viper.SetDefault("TIME_ZONE", "Asia/Shanghai")

	viper.AutomaticEnv()

	cfg := &Config{
		Port:          viper.GetString("PORT"),
		DatabaseURL:   viper.GetString("DATABASE_URL"),
		RedisURL:      viper.GetString("REDIS_URL"),
		JWTSecret:     viper.GetString("JWT_SECRET"),
		EncryptionKey: viper.GetString("ENCRYPTION_KEY"),
		TimeZone:      viper.GetString("TIME_ZONE"),
		CoreAPIURL:    viper.GetString("MONNAIRE_CORE_API_URL"),
	}

	return cfg
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
