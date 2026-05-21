package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"monera-digital/internal/cache"
	"monera-digital/internal/config"
	"monera-digital/internal/container"
	"monera-digital/internal/db"
	"monera-digital/internal/logger"
	"monera-digital/internal/middleware"
	"monera-digital/internal/routes"
	"monera-digital/internal/scheduler"
	"monera-digital/internal/services"
	"monera-digital/internal/utils"

	"github.com/gin-gonic/gin"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Initialize logger - use ENV variable for proper environment detection
	env := os.Getenv("ENV")
	if env == "" {
		env = os.Getenv("GO_ENV")
	}
	if env == "" {
		// Default to production if GIN_MODE is release
		if os.Getenv("GIN_MODE") == "release" {
			env = "production"
		} else {
			env = "development"
		}
	}
	if err := logger.Init(env); err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer logger.GetLogger().Sync()

	// Log startup
	logger.Info("Starting Monera Digital API server",
		"port", cfg.Port,
		"environment", env)

	// Initialize encryption key for activation codes
	if cfg.EncryptionKey != "" {
		normalizedKey, err := services.DecodeEncryptionKey(cfg.EncryptionKey)
		if err != nil {
			logger.Warn("Invalid encryption key for activation codes, using default",
				"error", err.Error())
		} else {
			utils.SetActivationCodeKey([]byte(normalizedKey))
			logger.Info("Activation code encryption key initialized")
		}
	} else {
		logger.Warn("ENCRYPTION_KEY not set, activation codes will use basic encoding")
	}

	// Initialize database
	database, err := db.InitDB(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("Failed to initialize database",
			"error", err.Error())
	}
	defer database.Close()
	logger.Info("Database connected successfully")

	// Initialize Redis cache
	var redisCache *cache.RedisCache
	redisAddr := strings.TrimPrefix(cfg.RedisURL, "redis://")
	if redisAddr != "" {
		redisCache, err = cache.NewRedisCache(redisAddr, "", 0)
		if err != nil {
			logger.Warn("Failed to connect to Redis, idempotency will be disabled",
				"error", err.Error())
		} else {
			logger.Info("Redis connected successfully")
		}
	}

	// Background context for long-lived workers (registry refresh,
	// pool replenisher). Cancelled on process exit via signal handler
	// (not yet wired — best-effort cleanup on SIGINT).
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	// Initialize container
	cont := container.NewContainer(database, cfg.JWTSecret,
		container.WithEncryption(cfg.EncryptionKey),
		container.WithRedisCache(redisCache),
		container.WithSafeheronPool(bgCtx),
		container.WithCosignerCallback())

	// Verify container
	if err := cont.Verify(); err != nil {
		logger.Fatal("Container verification failed",
			"error", err.Error())
	}

	// Debug: Check email service status
	logger.Info("[EmailService] Status check",
		"enabled", cont.EmailService.IsEnabled(),
		"RESEND_API_KEY", os.Getenv("RESEND_API_KEY"),
		"SENDER_EMAIL", os.Getenv("SENDER_EMAIL"))

	// Initialize Gin router
	r := gin.Default()

	// SEC-1: explicit X-Forwarded-For trust list. Empty list (default) trusts
	// no proxies — c.ClientIP() returns RemoteAddr, so the Safeheron webhook
	// IP whitelist cannot be bypassed by spoofed XFF headers. Configure
	// TRUSTED_PROXIES=10.0.0.0/8 (or similar LB CIDRs) when running behind a
	// reverse proxy in production.
	if err := r.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		logger.Fatal("Failed to configure trusted proxies",
			"error", err.Error(),
			"trustedProxies", cfg.TrustedProxies)
	}

	// Add CORS middleware
	r.Use(middleware.CORS())

	// Setup routes
	routes.SetupRoutes(r, cont)

	// Start interest scheduler
	interestScheduler := scheduler.NewInterestScheduler(cont.Repository.Wealth, cont.Repository.AccountV2, cont.Repository.Journal, cont.Repository.DailyInterest)
	go interestScheduler.Start()
	logger.Info("Interest scheduler started")

	// Serve static files in production (MUST be after API routes)
	distPath := "./dist"
	if _, err := os.Stat(distPath); err == nil {
		r.Static("/assets", filepath.Join(distPath, "assets"))
		r.StaticFile("/favicon.ico", filepath.Join(distPath, "favicon.ico"))
		r.StaticFile("/robots.txt", filepath.Join(distPath, "robots.txt"))
		r.StaticFile("/placeholder.svg", filepath.Join(distPath, "placeholder.svg"))

		// SPA fallback - only for non-API routes
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api") {
				c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
				return
			}
			c.File(filepath.Join(distPath, "index.html"))
		})
	} else {
		r.NoRoute(func(c *gin.Context) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})
		})
	}

	// Graceful shutdown: SIGINT/SIGTERM cancels bg ctx (worker/replenisher),
	// drains in-flight HTTP requests, then closes the container (DB, token
	// blacklist). v1.6: Safeheron PEMs live at fixed secrets/ paths and are
	// not managed by the process, so there is nothing to clean up on disk.
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErrCh := make(chan error, 1)
	go func() {
		logger.Info("Server starting on port " + cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
			return
		}
		serverErrCh <- nil
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrCh:
		if err != nil {
			logger.Fatal("Server failed to start", "error", err.Error())
		}
	case sig := <-sigCh:
		logger.Info("Shutdown signal received", "signal", sig.String())
		bgCancel() // stops registry refresh, deposit worker, replenisher

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("HTTP shutdown error", "error", err.Error())
		}

		if err := cont.Close(); err != nil {
			logger.Warn("Container close error", "error", err.Error())
		}
		logger.Info("Server stopped cleanly")
	}
}
