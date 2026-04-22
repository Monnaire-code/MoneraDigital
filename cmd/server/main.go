package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"monera-digital/internal/cache"
	"monera-digital/internal/config"
	"monera-digital/internal/container"
	"monera-digital/internal/db"
	"monera-digital/internal/logger"
	"monera-digital/internal/middleware"
	"monera-digital/internal/routes"
	"monera-digital/internal/scheduler"
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

	// Initialize container
	cont := container.NewContainer(database, cfg.JWTSecret,
		container.WithEncryption(cfg.EncryptionKey),
		container.WithRedisCache(redisCache))

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

	// Start server
	logger.Info("Server starting on port " + cfg.Port)
	if err := r.Run(":" + cfg.Port); err != nil {
		logger.Fatal("Server failed to start",
			"error", err.Error())
	}
}
