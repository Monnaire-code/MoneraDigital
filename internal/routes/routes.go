package routes

import (
	"monera-digital/internal/container"
	"monera-digital/internal/docs"
	"monera-digital/internal/handlers"
	"monera-digital/internal/middleware"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
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
		cont.ActivationService,
	)
	// Wire Safeheron pool/registry only when both are present — typed nil
	// pointers would otherwise satisfy the interface and bypass the
	// 503-fallback guard inside the handler.
	if cont.PoolManager != nil && cont.WalletRegistry != nil {
		h.SetSafeheronDeps(cont.PoolManager, cont.WalletRegistry)
	}

	// Create 2FA handler
	twofaHandler := handlers.NewTwoFAHandler(cont.TwoFAService)

	// Create activation handler
	activationHandler := handlers.NewActivationHandler(cont.ActivationService)

	// Create contact handler
	contactHandler := handlers.NewContactHandler(cont.ContactService)

	// Create fund handler
	fundHandler := handlers.NewFundHandler(cont.FundService)

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

			// Activation routes - public endpoints
			auth.POST("/send-activation", activationHandler.SendActivation)
			auth.POST("/verify-activation", activationHandler.VerifyActivation)
		}

		// Webhook routes (public)
		webhooks := public.Group("/webhooks")
		{
			webhooks.POST("/core/deposit", h.HandleDepositWebhook)
			if cont.SafeheronWebhookHandler != nil {
				webhooks.POST("/safeheron", cont.SafeheronWebhookHandler.Receive)
			}
			if cont.CompanyFundAirwallexWebhookHandler != nil {
				webhooks.POST("/airwallex", cont.CompanyFundAirwallexWebhookHandler.Receive)
			}
		}

		// Public fund stats (homepage AUM widget, no auth)
		public.GET("/fund/stats", fundHandler.GetStats)
	}

	// Company-fund management routes deliberately sit outside the ordinary
	// customer JWT group. They are only registered after WithCompanyFund has
	// constructed a dedicated constant-time admin-key boundary; an unset key
	// leaves no routable management endpoint at all.
	if cont.CompanyFundFinanceHandler != nil {
		companyFund := api.Group("/company-fund")
		companyFund.Use(cont.CompanyFundFinanceHandler.RequireAdminKey())
		finance := companyFund.Group("/finance")
		{
			finance.GET("/dashboard", cont.CompanyFundFinanceHandler.GetDashboard)
			finance.GET("/transactions", cont.CompanyFundFinanceHandler.ListTransactions)
			finance.PUT("/transactions/:transactionID/classification", cont.CompanyFundFinanceHandler.UpdateClassification)
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

		// Contact info routes
		protected.POST("/contact-info", contactHandler.SubmitContactInfo)

		// Assets routes
		assets := protected.Group("/assets")
		{
			assets.GET("", h.GetAssets)
			assets.GET("/prices", h.GetPrices)
			assets.POST("/refresh-prices", h.RefreshPrices)
		}

		// Lending routes
		lending := protected.Group("/lending")
		{
			lending.POST("/apply", h.ApplyForLending)
			lending.GET("/positions", h.GetUserPositions)
		}

		// Wallet routes — Safeheron Phase 1
		wallet := protected.Group("/wallet")
		{
			wallet.GET("/deposit-address", h.GetDepositAddress)
			wallet.GET("/deposit-coins", h.GetDepositCoins)
			wallet.GET("/supported-chains", h.GetSupportedChains)

			// Legacy Core-API wallet endpoints — replaced by deposit-address.
			// Kept routed so the frontend gets a clear 410 + migration message
			// instead of a generic 404 during the rollout window.
			wallet.GET("/info", h.GetWalletInfo)
			wallet.POST("/create", handlers.DeprecatedWalletEndpoint)
			wallet.POST("/addresses", handlers.DeprecatedWalletEndpoint)
			wallet.POST("/address/incomeHistory", handlers.DeprecatedWalletEndpoint)
			wallet.POST("/address/get", handlers.DeprecatedWalletEndpoint)
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
			wealth.GET("/interest-history", h.GetInterestHistory)
		}
	}
}
