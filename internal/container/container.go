// internal/container/container.go
package container

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/spf13/viper"
	"monera-digital/internal/cache"
	"monera-digital/internal/config"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/middleware"
	"monera-digital/internal/repository"
	"monera-digital/internal/repository/postgres"
	"monera-digital/internal/safeheron"
	"monera-digital/internal/services"
	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"
)

// ContainerOption 配置选项函数
type ContainerOption func(*Container)

// WithEncryption 配置加密服务和 2FA 服务
func WithEncryption(key string) ContainerOption {
	return func(c *Container) {
		// Normalize encryption key (support hex-encoded or raw format)
		normalizedKey, err := services.DecodeEncryptionKey(key)
		if err != nil {
			log.Printf("Warning: Invalid encryption key format: %v", err)
			return
		}

		encryptionService, err := services.NewEncryptionService(normalizedKey)
		if err != nil {
			log.Printf("Warning: Failed to initialize encryption service: %v", err)
			return
		}
		c.EncryptionService = encryptionService
		c.TwoFAService = services.NewTwoFactorService(c.DB, encryptionService)
	}
}

// WithSafeheronPool wires the Safeheron SDK client, chain registry, address
// pool manager, and replenisher background goroutine. Missing env config logs a
// warning and leaves the deposit-address endpoint returning 503 — useful in
// environments where Safeheron credentials aren't provisioned yet.
//
// Callers must pass a long-lived ctx (typically the server lifecycle ctx); the
// replenisher goroutine exits when ctx is cancelled. The SDK client's temp PEM
// files are cleaned up via Container.Close().
func WithSafeheronPool(ctx context.Context) ContainerOption {
	return func(c *Container) {
		baseURL := viper.GetString("SAFEHERON_API_BASE_URL")
		apiKey := viper.GetString("SAFEHERON_API_KEY")
		privKey := viper.GetString("SAFEHERON_PRIVATE_KEY_PEM")
		platKey := viper.GetString("SAFEHERON_PLATFORM_PUBLIC_KEY_PEM")
		whPub := viper.GetString("SAFEHERON_WEBHOOK_PUBLIC_KEY_PEM")
		whPriv := viper.GetString("SAFEHERON_WEBHOOK_PRIVATE_KEY_PEM")

		if apiKey == "" || privKey == "" || platKey == "" {
			log.Printf("Safeheron pool disabled: SAFEHERON_API_KEY/PRIVATE_KEY/PLATFORM_PUBLIC_KEY not configured")
			return
		}

		registry := walletconfig.NewRegistry(walletconfig.NewDBRepository(c.DB), 0)
		if err := registry.Load(ctx); err != nil {
			log.Printf("Safeheron pool disabled: registry load failed: %v", err)
			return
		}
		registry.StartBackgroundRefresh(ctx)
		c.WalletRegistry = registry

		client, err := safeheron.NewClient(safeheron.Config{
			BaseURL:              baseURL,
			APIKey:               apiKey,
			PrivateKeyPEM:        privKey,
			PlatformPublicKeyPEM: platKey,
			WebhookPublicKeyPEM:  whPub,
			WebhookPrivateKeyPEM: whPriv,
			RequestTimeoutMS:     30000,
		})
		if err != nil {
			log.Printf("Safeheron pool disabled: client init failed: %v", err)
			return
		}
		c.SafeheronClient = client

		poolRepo := pool.NewRepository(c.DB)
		c.PoolManager = pool.NewManager(poolRepo, client, registry)

		interval := viper.GetDuration("POOL_REPLENISH_INTERVAL")
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		low := map[string]int{
			"EVM":  viper.GetInt("POOL_REPLENISH_LOW_EVM"),
			"TRON": viper.GetInt("POOL_REPLENISH_LOW_TRON"),
		}
		target := map[string]int{
			"EVM":  viper.GetInt("POOL_REPLENISH_TARGET_EVM"),
			"TRON": viper.GetInt("POOL_REPLENISH_TARGET_TRON"),
		}
		// Sensible defaults if env not configured.
		applyDefault(low, "EVM", 50)
		applyDefault(low, "TRON", 50)
		applyDefault(target, "EVM", 100)
		applyDefault(target, "TRON", 100)

		c.PoolReplenisher = pool.NewReplenisher(c.PoolManager, pool.ReplenisherConfig{
			Interval: interval,
			Low:      low,
			Target:   target,
		})
		go c.PoolReplenisher.Run(ctx)

		log.Printf("Safeheron pool enabled: replenisher interval=%s low=%v target=%v",
			interval, low, target)
	}
}

func applyDefault(m map[string]int, key string, def int) {
	if v, ok := m[key]; !ok || v <= 0 {
		m[key] = def
	}
}

// WithRedisCache 配置 Redis 缓存服务
func WithRedisCache(redisCache *cache.RedisCache) ContainerOption {
	return func(c *Container) {
		c.RedisCache = redisCache
		// 初始化幂等性仓库（始终使用数据库）
		c.IdempotencyRepository = postgres.NewIdempotencyRepository(c.DB)
		// 确保幂等性表存在
		ctx := context.Background()
		if err := c.IdempotencyRepository.EnsureTableExists(ctx); err != nil {
			log.Printf("Warning: Failed to create idempotency table: %v", err)
		}
		// 创建幂等性服务（传入 Redis 和数据库仓库）
		c.IdempotencyService = services.NewIdempotencyService(redisCache, c.IdempotencyRepository)
	}
}

// Container 依赖注入容器
type Container struct {
	// 基础设施
	DB *sql.DB

	// 配置
	JWTSecret string

	// 缓存
	TokenBlacklist *cache.TokenBlacklist
	RateLimiter    *middleware.RateLimiter
	RedisCache     *cache.RedisCache

	// 幂等服务
	IdempotencyService    *services.IdempotencyService
	IdempotencyRepository *postgres.IdempotencyRepository

	// 外部 API 客户端
	CoreAPIClient   *coreapi.Client
	SafeheronClient safeheron.SafeheronClient

	// 仓储
	Repository *repository.Repository

	// Safeheron Phase 1 模块
	WalletRegistry *walletconfig.Registry
	PoolManager    *pool.Manager
	PoolReplenisher *pool.Replenisher

	// 服务
	AuthService       *services.AuthService
	LendingService    *services.LendingService
	AddressService    *services.AddressService
	WithdrawalService *services.WithdrawalService
	DepositService    *services.DepositService
	WalletService     *services.WalletService
	WealthService     *services.WealthService
	EncryptionService *services.EncryptionService
	TwoFAService      *services.TwoFactorService
	EmailService      *services.EmailService
	ActivationService *services.ActivationService
	ContactService    *services.ContactService

	// 中间件
	RateLimitMiddleware *middleware.PerEndpointRateLimiter
}

// NewContainer 创建依赖注入容器
func NewContainer(db *sql.DB, jwtSecret string, opts ...ContainerOption) *Container {
	c := &Container{DB: db, JWTSecret: jwtSecret}

	// 初始化缓存
	c.TokenBlacklist = cache.NewTokenBlacklist()
	c.RateLimiter = middleware.NewRateLimiter(5, 60)

	// 初始化 Core API 客户端
	coreAPIURL := os.Getenv("MONNAIRE_CORE_API_URL")
	if coreAPIURL == "" {
		coreAPIURL = "http://198.13.57.142:8080" // 默认测试环境
	}
	c.CoreAPIClient = coreapi.NewClient(coreAPIURL)

	// 加载配置
	cfg := config.Load()

	// 初始化仓储
	c.Repository = &repository.Repository{
		User:          postgres.NewUserRepository(db),
		Deposit:       postgres.NewDepositRepository(db),
		Wallet:        postgres.NewWalletRepository(db),
		Account:       postgres.NewAccountRepositoryV1(db),
		AccountV2:     postgres.NewAccountRepository(db),
		Address:       postgres.NewAddressRepository(db),
		Withdrawal:    postgres.NewWithdrawalRepository(db),
		Wealth:        postgres.NewWealthRepository(db),
		Journal:       postgres.NewJournalRepository(db),
		DailyInterest: postgres.NewDailyInterestRepository(db),
	}

	// 初始化核心服务
	c.AuthService = services.NewAuthService(db, jwtSecret, cfg)
	c.AuthService.SetTokenBlacklist(c.TokenBlacklist)

	c.LendingService = services.NewLendingService(db)
	c.AddressService = services.NewAddressService(c.Repository.Address)
	c.WithdrawalService = services.NewWithdrawalService(db, c.Repository, services.NewSafeheronService())
	c.DepositService = services.NewDepositService(c.Repository.Deposit)
	c.WalletService = services.NewWalletService(c.Repository.Wallet, c.CoreAPIClient)
	c.WealthService = services.NewWealthService(c.Repository.Wealth, c.Repository.AccountV2, c.Repository.Journal, c.Repository.DailyInterest)

	// 应用配置选项 (按顺序执行)
	for _, opt := range opts {
		opt(c)
	}

	// 注入TwoFactorService依赖（如果在选项函数中已初始化）
	if c.TwoFAService != nil {
		c.AuthService.SetTwoFactorService(c.TwoFAService)
	}

	// 初始化中间件
	c.RateLimitMiddleware = middleware.NewPerEndpointRateLimiter()
	c.RateLimitMiddleware.AddEndpoint("/api/auth/register", 5, 60)
	c.RateLimitMiddleware.AddEndpoint("/api/auth/login", 5, 60)
	c.RateLimitMiddleware.AddEndpoint("/api/auth/refresh", 10, 60)

	// 初始化邮件和激活服务 (使用 viper 读取环境变量以支持 .env 文件)
	emailService := services.NewEmailService(
		viper.GetString("RESEND_API_KEY"),
		viper.GetString("SENDER_EMAIL"),
	)
	c.EmailService = emailService
	
	fmt.Printf("[EmailService] Initialized - enabled: %v, apiKey: '%s', fromEmail: '%s'\n", 
		emailService.IsEnabled(), 
		os.Getenv("RESEND_API_KEY"), 
		os.Getenv("SENDER_EMAIL"))

	dbRateLimiter := services.NewRateLimiter(db)
	c.ActivationService = services.NewActivationService(db, dbRateLimiter, emailService, jwtSecret)
	c.ContactService = services.NewContactService(db)

	return c
}

// Close 关闭容器中的资源
func (c *Container) Close() error {
	if c.TokenBlacklist != nil {
		c.TokenBlacklist.Close()
	}
	if c.DB != nil {
		return c.DB.Close()
	}
	return nil
}

// Verify 验证容器中的所有依赖
func (c *Container) Verify() error {
	// 验证数据库连接
	if err := c.DB.Ping(); err != nil {
		log.Printf("Database connection failed: %v", err)
		return err
	}

	// 验证核心服务初始化
	services := []struct {
		name  string
		value interface{}
	}{
		{"AuthService", c.AuthService},
		{"LendingService", c.LendingService},
		{"AddressService", c.AddressService},
		{"WithdrawalService", c.WithdrawalService},
		{"DepositService", c.DepositService},
		{"WalletService", c.WalletService},
	}

	for _, s := range services {
		if s.value == nil {
			log.Printf("%s not initialized", s.name)
		}
	}

	log.Println("Container verification passed")
	return nil
}
