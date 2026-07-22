// internal/container/container.go
package container

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"monera-digital/internal/adaptiveschedule"
	"monera-digital/internal/alert"
	"monera-digital/internal/cache"
	"monera-digital/internal/companyfund"
	"monera-digital/internal/config"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/fundrouting"
	"monera-digital/internal/handlers"
	"monera-digital/internal/middleware"
	"monera-digital/internal/repository"
	"monera-digital/internal/repository/postgres"
	"monera-digital/internal/safeheron"
	"monera-digital/internal/services"
	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/deposit"
	"monera-digital/internal/wallet/pool"

	"github.com/spf13/viper"
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
// replenisher goroutine exits when ctx is cancelled. v1.6: PEM files now live
// at SAFEHERON_*_KEY_PATH locations under secrets/ — operators are responsible
// for placing them with correct permissions before startup.
func WithSafeheronPool(ctx context.Context) ContainerOption {
	return func(c *Container) {
		routingMode, err := fundrouting.ParseMode(viper.GetString("SAFEHERON_TRANSACTION_ROUTING_MODE"))
		if err != nil {
			panic(err)
		}
		c.SafeheronRoutingMode = routingMode
		c.safeheronRuntimeContext = ctx
		baseURL := viper.GetString("SAFEHERON_API_BASE_URL")
		apiKey := viper.GetString("SAFEHERON_API_KEY")
		privPath := viper.GetString("SAFEHERON_PRIVATE_KEY_PATH")
		platPath := viper.GetString("SAFEHERON_PLATFORM_PUBLIC_KEY_PATH")
		whPubPath := viper.GetString("SAFEHERON_WEBHOOK_PUBLIC_KEY_PATH")
		whPrivPath := viper.GetString("SAFEHERON_WEBHOOK_PRIVATE_KEY_PATH")

		if apiKey == "" || privPath == "" || platPath == "" {
			log.Printf("Safeheron pool disabled: SAFEHERON_API_KEY/PRIVATE_KEY_PATH/PLATFORM_PUBLIC_KEY_PATH not configured")
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
			BaseURL:               baseURL,
			APIKey:                apiKey,
			PrivateKeyPath:        privPath,
			PlatformPublicKeyPath: platPath,
			WebhookPublicKeyPath:  whPubPath,
			WebhookPrivateKeyPath: whPrivPath,
			RequestTimeoutMS:      30000,
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
		c.PoolManager.SetOnAllocated(func() {
			_ = c.PoolReplenisher.Notify()
		})
		runContainerBackgroundTask(ctx, "wallet_pool_replenisher", c.PoolReplenisher.Run)

		log.Printf("Safeheron pool enabled: replenisher interval=%s low=%v target=%v",
			interval, low, target)

		// Alert sink (Feishu webhook + email recipients).
		feishuURL := viper.GetString("ALERT_WEBHOOK_URL")
		feishuSecret := viper.GetString("ALERT_WEBHOOK_SIGN_SECRET")
		recipients := splitNonEmpty(viper.GetString("ALERT_EMAIL_RECIPIENTS"))
		c.AlertService = alert.NewAlertService(feishuURL, feishuSecret, recipients, c.EmailService)
		c.PoolManager.SetAlertFunc(func(level, title, message string) {
			c.AlertService.Send(level, title, map[string]string{"message": message})
		})

		// KYT configuration + production startup validation (K-16)
		kytEnabled := true
		if viper.IsSet("KYT_ENABLED") {
			kytEnabled = viper.GetBool("KYT_ENABLED")
		}
		if viper.GetString("APP_ENV") == "production" && !kytEnabled {
			panic("KYT_ENABLED=false is not allowed in production (K-16): " +
				"set KYT_ENABLED=true or unset for production deployment")
		}

		kytOrphanMaxRetry := viper.GetInt("KYT_ORPHAN_ALERT_MAX_RETRY")
		if kytOrphanMaxRetry <= 0 {
			kytOrphanMaxRetry = 100
		}
		kytTimeout := viper.GetDuration("KYT_TIMEOUT")
		if kytTimeout <= 0 {
			kytTimeout = 20 * time.Minute
		}
		kytScanInterval := viper.GetDuration("KYT_SCAN_INTERVAL")
		if kytScanInterval <= 0 {
			kytScanInterval = time.Minute
		}

		// Deposit pipeline: webhook handler (sync) + worker (async).
		depRepo := deposit.NewRepository(c.DB)
		depRepo.SetTransactionClaimsEnabled(false)
		depRepo.SetRoutingProjectionClaimsEnabled(routingMode == fundrouting.ModeRoutingAuthoritative)
		c.DepositEventRepo = depRepo
		c.DepositPipeline = deposit.NewService(depRepo, registry, c.AlertService.Send)
		wireDepositCompanyFundRouting(c)
		c.DepositPipeline.SetKYTDeps(client, kytEnabled, kytOrphanMaxRetry, kytTimeout)
		amlFirstPollDelay := viper.GetDuration("AML_FIRST_POLL_DELAY")
		if amlFirstPollDelay <= 0 || amlFirstPollDelay >= kytTimeout {
			amlFirstPollDelay = 5 * time.Minute
		}
		c.DepositPipeline.SetAMLFirstPollDelay(amlFirstPollDelay)
		webhookAllowedIPs := splitNonEmpty(viper.GetString("SAFEHERON_WEBHOOK_ALLOWED_IPS"))
		// L-1: production must enforce IP allowlist — empty list opens the
		// webhook handler to anyone who can reach the port (the SDK verify is
		// the only remaining defence). Mirrors the K-16 KYT_ENABLED check.
		if viper.GetString("APP_ENV") == "production" && len(webhookAllowedIPs) == 0 {
			panic("SAFEHERON_WEBHOOK_ALLOWED_IPS must be set in production (L-1): " +
				"comma-separated allowlist of Safeheron source IPs is required")
		}
		c.SafeheronWebhookHandler = handlers.NewSafeheronWebhookHandler(client, depRepo, webhookAllowedIPs)
		wireCompanyFundSafeheronBridge(c)

		workerInterval := viper.GetDuration("DEPOSIT_WORKER_INTERVAL")
		if workerInterval <= 0 {
			workerInterval = time.Second
		}
		amlPollInterval := viper.GetDuration("AML_POLL_INTERVAL")
		if amlPollInterval <= 0 {
			amlPollInterval = 60 * time.Second
		}
		c.DepositWorker = deposit.NewWorker(c.DepositPipeline, deposit.WorkerConfig{
			Interval:        workerInterval,
			KYTScanInterval: kytScanInterval,
			AMLPollInterval: amlPollInterval,
			PanicBackoff:    5 * time.Second,
		})
		runContainerBackgroundTask(ctx, "deposit_worker", c.DepositWorker.Run)
		wireSafeheronWebhookWorkerWakes(c)

		log.Printf("Safeheron deposit pipeline enabled: worker interval=%s maxIdle=10m", workerInterval)
		log.Printf("[KYT] enabled=%v scan_interval=%s timeout=%s orphan_max_retry=%d",
			kytEnabled, kytScanInterval, kytTimeout, kytOrphanMaxRetry)
	}
}

func splitNonEmpty(csv string) []string {
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// installProcessMaintenanceWindow aligns all adaptiveschedule loops that do not
// override SharedMaintenance onto one MaxIdle quiet budget. Prefer the company-
// fund event idle knob when set so stage/prod keep a single operator control.
func installProcessMaintenanceWindow() {
	maxIdle := viper.GetDuration("COMPANY_FUND_EVENT_MAX_IDLE_INTERVAL")
	if maxIdle <= 0 {
		maxIdle = adaptiveschedule.DefaultMaxIdle
	}
	adaptiveschedule.SetProcessMaintenance(adaptiveschedule.NewMaintenanceWindow(maxIdle))
	log.Printf("adaptive schedule shared maintenance window: maxIdle=%s", maxIdle)
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
	WalletRegistry              *walletconfig.Registry
	PoolManager                 *pool.Manager
	PoolReplenisher             *pool.Replenisher
	DepositEventRepo            deposit.Repository
	DepositPipeline             *deposit.Service
	DepositWorker               *deposit.Worker
	SafeheronWebhookHandler     *handlers.SafeheronWebhookHandler
	AlertService                *alert.AlertService
	SafeheronRoutingMode        fundrouting.Mode
	FundRoutingRepository       *fundrouting.Repository
	FundRoutingWorker           *fundrouting.Worker
	FundRoutingProjectionWorker *fundrouting.ProjectionWorker
	FundRoutingReconciler       *fundrouting.Reconciler
	FundRoutingAlertNotifier    *fundrouting.AlertNotifier
	FundRoutingAlertEscalator   *fundrouting.AlertEscalator

	// Company-fund runtime. These dependencies intentionally remain independent
	// of the customer wallet pool and its Redis/cache lifecycle. The runtime is
	// finalized after every ContainerOption has been applied, which makes
	// WithSafeheronPool and WithCompanyFund order-independent.
	CompanyFundRepository              *companyfund.DBRepository
	CompanyFundAccountRegistry         *companyfund.AccountRegistry
	CompanyFundOwnedPayloadService     *companyfund.OwnedProviderPayloadService
	CompanyFundProviderEventWorker     *companyfund.ProviderEventWorker
	CompanyFundRuntime                 *companyfund.CompanyFundRuntime
	CompanyFundSafeheronNormalizer     *companyfund.SafeheronProviderEventNormalizer
	CompanyFundSafeheronCoinCatalog    *companyfund.SafeheronCoinCatalog
	CompanyFundSafeheronReconciler     *companyfund.SafeheronTransactionHistoryReconciler
	CompanyFundSafeheronCollector      *companyfund.SafeheronProviderEventCollector
	CompanyFundAirwallexRuntimeBundle  *companyfund.AirwallexFinancialTransactionsRuntimeBundle
	CompanyFundAirwallexClient         *companyfund.AirwallexClient
	CompanyFundAirwallexReconciler     *companyfund.AirwallexFinancialTransactionsReconciler
	CompanyFundAirwallexWebhookHandler *handlers.CompanyFundAirwallexWebhookHandler
	CompanyFundCurrentRateCache        *companyfund.CurrentRateCache
	CompanyFundCoinGeckoRateRefresher  *companyfund.CoinGeckoCurrentRateRefresher
	CompanyFundCurrentValuator         *companyfund.CompanyFundCurrentValuator
	CompanyFundFinanceHandler          *handlers.CompanyFundFinanceHandler

	companyFundRuntimeConfig    companyFundRuntimeConfig
	companyFundRuntimeContext   context.Context
	companyFundRuntimePending   bool
	companyFundRuntimeFinalized bool
	companyFundAuxCancel        context.CancelFunc
	companyFundAuxDone          chan struct{}
	companyFundRateRefreshLoop  *adaptiveschedule.Loop
	companyFundValuationLoop    *adaptiveschedule.Loop
	safeheronRuntimeContext     context.Context

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
	FundService       *services.FundService

	// 中间件
	RateLimitMiddleware *middleware.PerEndpointRateLimiter
}

// NewContainer 创建依赖注入容器
func NewContainer(db *sql.DB, jwtSecret string, opts ...ContainerOption) *Container {
	c := &Container{DB: db, JWTSecret: jwtSecret}

	// Aggregate pure-fallback DB scans onto one process-wide MaxIdle quiet window
	// before any option starts background loops (ADR 0002 / #53).
	installProcessMaintenanceWindow()

	// 初始化缓存
	c.TokenBlacklist = cache.NewTokenBlacklist()
	c.RateLimiter = middleware.NewRateLimiter(5, 60*time.Second)
	// L2: exempt /api/fund/stats from the global limiter. The FundService
	// in-memory cache (L1) already collapses N concurrent homepage
	// fetches into 1 repo roundtrip; putting a 5/min/IP limiter in
	// front of a public read endpoint is what produced the
	// "too many requests" symptom on the homepage.
	c.RateLimiter.SkipPath("/api/fund/stats")

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

	// EmailService must be wired BEFORE the opts loop: WithSafeheronPool reads
	// c.EmailService to build AlertService, and a nil *services.EmailService
	// becomes a typed-nil alertEmailer interface — `emailSvc == nil` would
	// evaluate false and the first alert would panic on nil-receiver method
	// access. Pre-ship code-review Critical.
	c.EmailService = services.NewEmailService(
		viper.GetString("RESEND_API_KEY"),
		viper.GetString("SENDER_EMAIL"),
	)
	// R2-I-3: never log the API key. enabled+fromEmail is enough operational signal.
	log.Printf("[EmailService] Initialized enabled=%v fromEmail=%q",
		c.EmailService.IsEnabled(), os.Getenv("SENDER_EMAIL"))

	// 应用配置选项 (按顺序执行)
	for _, opt := range opts {
		opt(c)
	}
	// Company-fund provider adapters need the completed option graph: the
	// Safeheron option can be before or after WithCompanyFund. Finalization
	// starts no background work until all dependencies have been assembled.
	finalizeCompanyFundRuntime(c)
	finalizeSafeheronRouting(c)

	// 注入TwoFactorService依赖（如果在选项函数中已初始化）
	if c.TwoFAService != nil {
		c.AuthService.SetTwoFactorService(c.TwoFAService)
	}

	// 初始化中间件
	c.RateLimitMiddleware = middleware.NewPerEndpointRateLimiter()
	c.RateLimitMiddleware.AddEndpoint("/api/auth/register", 5, 60)
	c.RateLimitMiddleware.AddEndpoint("/api/auth/login", 5, 60)
	c.RateLimitMiddleware.AddEndpoint("/api/auth/refresh", 10, 60)

	dbRateLimiter := services.NewRateLimiter(db)
	c.ActivationService = services.NewActivationService(db, dbRateLimiter, c.EmailService, jwtSecret)
	c.ContactService = services.NewContactService(db)
	c.FundService = services.NewFundService(postgres.NewFundReportRepository(db))

	return c
}

// Close 关闭容器中的资源
//
// 顺序：company-fund auxiliary loops → company-fund runtime → company-fund
// registry → TokenBlacklist → SafeheronClient → DB。任一资源 Close 失败仅记录
// 首个错误，后续资源仍会尝试关闭。v1.6 起 SafeheronClient.Close 是 no-op（PEM
// 改读 secrets/ 真实文件，不再有进程托管的临时文件），保留调用是为了向后兼容
// 接口契约。
func (c *Container) Close() error {
	var firstErr error

	// Stop auxiliary ingress/valuation loops before halting the durable worker,
	// then retain the registry until every worker/reconciliation call has
	// returned. This order keeps new bridge rows from arriving during shutdown
	// and ensures no loop can observe a closed database.
	stopCompanyFundAuxiliaryLoops(c)
	if c.CompanyFundRuntime != nil {
		c.CompanyFundRuntime.Stop()
	}
	if c.CompanyFundSafeheronCoinCatalog != nil {
		c.CompanyFundSafeheronCoinCatalog.Stop()
	}
	if c.CompanyFundAccountRegistry != nil {
		c.CompanyFundAccountRegistry.Stop()
	}

	if c.TokenBlacklist != nil {
		c.TokenBlacklist.Close()
	}
	if c.SafeheronClient != nil {
		if err := c.SafeheronClient.Close(); err != nil {
			firstErr = fmt.Errorf("safeheron client close: %w", err)
		}
	}
	if c.DB != nil {
		if err := c.DB.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("db close: %w", err)
		}
	}

	return firstErr
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
