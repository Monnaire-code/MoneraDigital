package routes

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"monera-digital/internal/container"
	"monera-digital/internal/docs"
	"monera-digital/internal/handlers"
	"monera-digital/internal/middleware"
)

// SetupRoutes configures all API routes with middleware
func SetupRoutes(router *gin.Engine, cont *container.Container) {
	// Add global middleware
	router.Use(middleware.RecoveryHandler())
	router.Use(middleware.ErrorHandler())
	router.Use(middleware.RateLimitMiddleware(cont.RateLimiter))

	// Initialize Swagger documentation
	docs.NewSwagger()

	// Swagger documentation endpoint
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Create handler
	h := handlers.NewHandler(
		cont.AuthService,
		cont.LendingService,
		cont.AddressService,
		cont.WithdrawalService,
		cont.DepositService,
		cont.WalletService,
		cont.WealthService,
		cont.IdempotencyService,
	)

	// Create 2FA handler
	twofaHandler := handlers.NewTwoFAHandler(cont.TwoFAService)

	// Root health check endpoint (backup)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API routes
	api := router.Group("/api")

	// ==================== PUBLIC ROUTES (No Auth Required) ====================
	public := api.Group("")
	{
		// Health check
		public.GET("/health", func(c *gin.Context) {
			c.JSON(200, gin.H{"status": "ok"})
		})

		// Authentication routes
		auth := public.Group("/auth")
		{
			auth.POST("/register", h.Register)
			auth.POST("/login", h.Login)
			auth.POST("/refresh", h.RefreshToken)
			auth.POST("/logout", h.Logout)

			// 2FA verification login - public endpoint because no JWT exists yet
			auth.POST("/2fa/verify-login", h.Verify2FALogin)
			// Skip 2FA setup - public endpoint
			auth.POST("/2fa/skip", h.Skip2FALogin)
		}

		// Webhook routes (public)
		webhooks := public.Group("/webhooks")
		{
			webhooks.POST("/core/deposit", h.HandleDepositWebhook)
		}
	}

	// ==================== PROTECTED ROUTES (JWT Auth Required) ====================
	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(cont.JWTSecret, cont.Repository.User))
	{
		// Auth routes
		protectedAuth := protected.Group("/auth")
		{
			protectedAuth.GET("/me", h.GetMe)

			// 2FA endpoints
			twofa := protectedAuth.Group("/2fa")
			{
				twofa.POST("/setup", twofaHandler.Setup2FA)
				twofa.POST("/enable", twofaHandler.Enable2FA)
				twofa.POST("/verify", twofaHandler.Verify2FA)
				twofa.POST("/disable", twofaHandler.Disable2FA)
				twofa.GET("/status", twofaHandler.Get2FAStatus)
			}
		}

		// Assets routes
		assets := protected.Group("/assets")
		{
			assets.GET("", h.GetAssets)
			assets.POST("/refresh-prices", h.RefreshPrices)
		}

		// Lending routes
		lending := protected.Group("/lending")
		{
			lending.POST("/apply", h.ApplyForLending)
			lending.GET("/positions", h.GetUserPositions)
		}

		// Wallet routes
		wallet := protected.Group("/wallet")
		{
			wallet.GET("/info", h.GetWalletInfo)
			wallet.POST("/create", h.CreateWallet)
			wallet.POST("/addresses", h.AddWalletAddress)
			wallet.POST("/address/incomeHistory", h.GetAddressIncomeHistory)
			wallet.POST("/address/get", h.GetWalletAddress)
		}

		// Deposit routes
		deposits := protected.Group("/deposits")
		{
			deposits.GET("", h.GetDeposits)
		}

		// Address routes
		addresses := protected.Group("/addresses")
		{
			addresses.GET("", h.GetAddresses)
			addresses.POST("", h.AddAddress)
			addresses.POST("/:id/verify", h.VerifyAddress)
			addresses.POST("/:id/primary", h.SetPrimaryAddress)
			addresses.POST("/:id/deactivate", h.DeactivateAddress)
		}

		// Withdrawal routes
		withdrawals := protected.Group("/withdrawals")
		{
			withdrawals.GET("", h.GetWithdrawals)
			withdrawals.POST("", h.CreateWithdrawal)
			withdrawals.GET("/fees", h.GetWithdrawalFees)
			withdrawals.GET("/:id", h.GetWithdrawalByID)
		}

		// Wealth routes
		wealth := protected.Group("/wealth")
		{
			wealth.GET("/products", h.GetProducts)
			wealth.POST("/subscribe", h.Subscribe)
			wealth.GET("/orders", h.GetOrders)
			wealth.POST("/redeem", h.Redeem)
		}
	}
}
