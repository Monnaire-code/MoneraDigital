package container

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/fundrouting"
	"monera-digital/internal/handlers"
	"monera-digital/internal/safeheron"
)

const (
	companyFundAirwallexWebhookPath = "/api/webhooks/airwallex"
	companyFundSafeheronWebhookPath = "/api/webhooks/safeheron"

	defaultCompanyFundPayloadRetention                = 48 * time.Hour
	defaultCompanyFundEventLeaseDuration              = time.Minute
	defaultCompanyFundEventLeaseRenewInterval         = 20 * time.Second
	defaultCompanyFundEventRetryInitialDelay          = 30 * time.Second
	defaultCompanyFundEventRetryMaxDelay              = time.Hour
	defaultCompanyFundReconciliationLeaseDuration     = 5 * time.Minute
	defaultCompanyFundReconciliationRetryInitialDelay = time.Minute
	defaultCompanyFundReconciliationRetryMaxDelay     = time.Hour
	defaultCompanyFundReconciliationFinalizeTimeout   = 10 * time.Second
	defaultCompanyFundSafeheronHistoryPageSize        = 100
	defaultCompanyFundSafeheronHistoryMaxPages        = 100
	defaultCompanyFundSafeheronCollectorInterval      = time.Minute
	defaultCompanyFundSafeheronCollectorBatchSize     = 100
	defaultCompanyFundAirwallexFinancialPageSize      = 100
	defaultCompanyFundAirwallexFinancialMaxPages      = 100
	defaultCompanyFundCurrentRateRefreshInterval      = 5 * time.Minute
	defaultCompanyFundCurrentRateCacheTTL             = 10 * time.Minute
	defaultCompanyFundCurrentRateCacheMaxAge          = 60 * time.Minute
	defaultCompanyFundCurrentValuationSweepInterval   = time.Minute
	defaultCompanyFundCurrentValuationSweepBatch      = 100
	maxCompanyFundPayloadKeyVersionBytes              = 64
	maxCompanyFundSafeheronCollectorBatchSize         = 500
	maxCompanyFundCurrentValuationSweepBatch          = 1000
)

// WithCompanyFund stages the provider-neutral storage, encrypted payload
// ownership, immutable account registry, and finance boundary. NewContainer
// calls finalizeCompanyFundRuntime only after every option has run, so this
// option remains safe in either order relative to WithSafeheronPool.
func WithCompanyFund(ctx context.Context) ContainerOption {
	return withCompanyFund(ctx, companyFundRuntimeConfigFromViper())
}

type companyFundRuntimeConfig struct {
	Enabled                bool
	StartBackgroundWorkers bool
	AccountRefreshInterval time.Duration
	PayloadKey             string
	PayloadKeyVersion      string
	PayloadRetention       time.Duration
	PayloadLegalHold       bool
	AdminKey               string

	EventPollInterval             time.Duration
	EventMaxIdleInterval          time.Duration
	EventDrainLimit               int
	EventLeaseOwner               string
	EventLeaseDuration            time.Duration
	EventRenewInterval            time.Duration
	EventRetryInitial             time.Duration
	EventRetryMax                 time.Duration
	ReconciliationPoll            time.Duration
	ReconciliationZone            string
	ReconciliationTime            string
	ReconciliationCatchUp         int
	LateStatusOverlapDays         int
	LateStatusOverlapConfigured   bool
	ReconciliationLeaseOwner      string
	ReconciliationLeaseDuration   time.Duration
	ReconciliationRetryInitial    time.Duration
	ReconciliationRetryMax        time.Duration
	ReconciliationFinalizeTimeout time.Duration

	SafeheronHistoryPageSize            int
	SafeheronHistoryMaxPages            int
	SafeheronCollectorInterval          time.Duration
	SafeheronCollectorBatchSize         int
	SafeheronCoinCatalogRefreshInterval time.Duration

	AirwallexBaseURL           string
	AirwallexClientID          string
	AirwallexAPIKey            string
	AirwallexAPIVersion        string
	AirwallexLoginAs           string
	AirwallexWebhookVersion    string
	AirwallexWebhookSecret     string
	AirwallexWebhookMaxAge     time.Duration
	AirwallexWebhookLookback   time.Duration
	AirwallexRuntimeConfigJSON string
	AirwallexFinancialPageSize int
	AirwallexFinancialMaxPages int

	CoinGeckoBaseURL               string
	CoinGeckoDemoAPIKey            string
	CurrentRateDefaultMappingsJSON string
	CurrentRateRefreshInterval     time.Duration
	CurrentRateCacheTTL            time.Duration
	CurrentRateCacheMaxAge         time.Duration
	CurrentValuationSweepInterval  time.Duration
	CurrentValuationSweepBatch     int
	CurrentValuationPolicyVersion  string
}

func companyFundRuntimeConfigFromViper() companyFundRuntimeConfig {
	startBackgroundWorkers := true
	if viper.IsSet("COMPANY_FUND_START_BACKGROUND_WORKERS") {
		startBackgroundWorkers = viper.GetBool("COMPANY_FUND_START_BACKGROUND_WORKERS")
	}
	lateStatusOverlapConfigured := viper.IsSet("COMPANY_FUND_LATE_STATUS_OVERLAP_DAYS")
	lateStatusOverlapDays := 0
	if lateStatusOverlapConfigured {
		lateStatusOverlapDays = viper.GetInt("COMPANY_FUND_LATE_STATUS_OVERLAP_DAYS")
	}
	return companyFundRuntimeConfig{
		Enabled:                             viper.GetBool("COMPANY_FUND_ENABLED"),
		StartBackgroundWorkers:              startBackgroundWorkers,
		AccountRefreshInterval:              viper.GetDuration("COMPANY_FUND_ACCOUNT_REFRESH_INTERVAL"),
		PayloadKey:                          viper.GetString("COMPANY_FUND_PAYLOAD_KEY"),
		PayloadKeyVersion:                   viper.GetString("COMPANY_FUND_PAYLOAD_KEY_VERSION"),
		PayloadRetention:                    viper.GetDuration("COMPANY_FUND_PAYLOAD_RETENTION"),
		PayloadLegalHold:                    viper.GetBool("COMPANY_FUND_PAYLOAD_LEGAL_HOLD"),
		AdminKey:                            viper.GetString("COMPANY_FUND_ADMIN_KEY"),
		EventPollInterval:                   viper.GetDuration("COMPANY_FUND_EVENT_POLL_INTERVAL"),
		EventMaxIdleInterval:                viper.GetDuration("COMPANY_FUND_EVENT_MAX_IDLE_INTERVAL"),
		EventDrainLimit:                     viper.GetInt("COMPANY_FUND_EVENT_DRAIN_LIMIT"),
		EventLeaseOwner:                     viper.GetString("COMPANY_FUND_EVENT_LEASE_OWNER"),
		EventLeaseDuration:                  viper.GetDuration("COMPANY_FUND_EVENT_LEASE_DURATION"),
		EventRenewInterval:                  viper.GetDuration("COMPANY_FUND_EVENT_LEASE_RENEW_INTERVAL"),
		EventRetryInitial:                   viper.GetDuration("COMPANY_FUND_EVENT_RETRY_INITIAL_DELAY"),
		EventRetryMax:                       viper.GetDuration("COMPANY_FUND_EVENT_RETRY_MAX_DELAY"),
		ReconciliationPoll:                  viper.GetDuration("COMPANY_FUND_RECONCILIATION_POLL_INTERVAL"),
		ReconciliationZone:                  viper.GetString("COMPANY_FUND_RECONCILIATION_TIME_ZONE"),
		ReconciliationTime:                  viper.GetString("COMPANY_FUND_RECONCILIATION_DAILY_TIME"),
		ReconciliationCatchUp:               viper.GetInt("COMPANY_FUND_RECONCILIATION_CATCH_UP_DAYS"),
		LateStatusOverlapDays:               lateStatusOverlapDays,
		LateStatusOverlapConfigured:         lateStatusOverlapConfigured,
		ReconciliationLeaseOwner:            viper.GetString("COMPANY_FUND_RECONCILIATION_LEASE_OWNER"),
		ReconciliationLeaseDuration:         viper.GetDuration("COMPANY_FUND_RECONCILIATION_LEASE_DURATION"),
		ReconciliationRetryInitial:          viper.GetDuration("COMPANY_FUND_RECONCILIATION_RETRY_INITIAL_DELAY"),
		ReconciliationRetryMax:              viper.GetDuration("COMPANY_FUND_RECONCILIATION_RETRY_MAX_DELAY"),
		ReconciliationFinalizeTimeout:       viper.GetDuration("COMPANY_FUND_RECONCILIATION_FINALIZE_TIMEOUT"),
		SafeheronHistoryPageSize:            viper.GetInt("COMPANY_FUND_SAFEHERON_HISTORY_PAGE_SIZE"),
		SafeheronHistoryMaxPages:            viper.GetInt("COMPANY_FUND_SAFEHERON_HISTORY_MAX_PAGES"),
		SafeheronCollectorInterval:          viper.GetDuration("COMPANY_FUND_SAFEHERON_COLLECTOR_INTERVAL"),
		SafeheronCollectorBatchSize:         viper.GetInt("COMPANY_FUND_SAFEHERON_COLLECTOR_BATCH_SIZE"),
		SafeheronCoinCatalogRefreshInterval: viper.GetDuration("COMPANY_FUND_SAFEHERON_COIN_CATALOG_REFRESH_INTERVAL"),
		AirwallexBaseURL:                    viper.GetString("AIRWALLEX_BASE_URL"),
		AirwallexClientID:                   viper.GetString("AIRWALLEX_CLIENT_ID"),
		AirwallexAPIKey:                     viper.GetString("AIRWALLEX_API_KEY"),
		AirwallexAPIVersion:                 viper.GetString("AIRWALLEX_API_VERSION"),
		AirwallexLoginAs:                    viper.GetString("AIRWALLEX_LOGIN_AS"),
		AirwallexWebhookVersion:             viper.GetString("AIRWALLEX_WEBHOOK_VERSION"),
		AirwallexWebhookSecret:              viper.GetString("AIRWALLEX_WEBHOOK_SECRET"),
		AirwallexWebhookMaxAge:              viper.GetDuration("AIRWALLEX_WEBHOOK_TIMESTAMP_TOLERANCE"),
		AirwallexWebhookLookback:            viper.GetDuration("COMPANY_FUND_AIRWALLEX_WEBHOOK_LOOKBACK"),
		AirwallexRuntimeConfigJSON:          viper.GetString("AIRWALLEX_FINANCIAL_TRANSACTIONS_RUNTIME_CONFIG"),
		AirwallexFinancialPageSize:          viper.GetInt("AIRWALLEX_FINANCIAL_TRANSACTIONS_PAGE_SIZE"),
		AirwallexFinancialMaxPages:          viper.GetInt("AIRWALLEX_FINANCIAL_TRANSACTIONS_MAX_PAGES"),
		CoinGeckoBaseURL:                    viper.GetString("COINGECKO_BASE_URL"),
		CoinGeckoDemoAPIKey:                 viper.GetString("COINGECKO_DEMO_API_KEY"),
		CurrentRateDefaultMappingsJSON:      viper.GetString("COMPANY_FUND_USD_RATE_DEFAULT_MAPPINGS"),
		CurrentRateRefreshInterval:          viper.GetDuration("COMPANY_FUND_USD_RATE_REFRESH_INTERVAL"),
		CurrentRateCacheTTL:                 viper.GetDuration("COMPANY_FUND_USD_RATE_CACHE_TTL"),
		CurrentRateCacheMaxAge:              viper.GetDuration("COMPANY_FUND_USD_RATE_CACHE_MAX_AGE"),
		CurrentValuationSweepInterval:       viper.GetDuration("COMPANY_FUND_USD_VALUATION_SWEEP_INTERVAL"),
		CurrentValuationSweepBatch:          viper.GetInt("COMPANY_FUND_USD_VALUATION_SWEEP_BATCH"),
		CurrentValuationPolicyVersion:       viper.GetString("COMPANY_FUND_USD_VALUATION_POLICY_VERSION"),
	}
}

func withCompanyFund(ctx context.Context, config companyFundRuntimeConfig) ContainerOption {
	return func(c *Container) {
		if c == nil || c.DB == nil {
			log.Printf("company-fund runtime disabled: database is unavailable")
			return
		}
		if !config.Enabled {
			log.Printf("company-fund runtime disabled: COMPANY_FUND_ENABLED is false")
			return
		}
		if ctx == nil {
			ctx = context.Background()
		}

		payloadKey, err := decodeCompanyFundPayloadKey(config.PayloadKey)
		if err != nil {
			log.Printf("company-fund runtime disabled: payload encryption key is invalid or unavailable")
			return
		}
		payloadKeyVersion, err := normalizeCompanyFundPayloadKeyVersion(config.PayloadKeyVersion)
		if err != nil {
			log.Printf("company-fund runtime disabled: payload encryption key version is invalid or unavailable")
			return
		}
		retention, err := companyFundPositiveDuration(config.PayloadRetention, defaultCompanyFundPayloadRetention)
		if err != nil {
			log.Printf("company-fund runtime disabled: payload retention is invalid")
			return
		}
		config.PayloadRetention = retention
		payloadCipher, err := companyfund.NewAES256GCMPayloadCipher(map[string][]byte{payloadKeyVersion: payloadKey})
		if err != nil {
			log.Printf("company-fund runtime disabled: payload cipher could not be initialized")
			return
		}

		repository := companyfund.NewDBRepository(c.DB)
		switch c.SafeheronRoutingMode {
		case fundrouting.ModeCaptureOnly:
			repository.SetSafeheronProviderClaimMode(companyfund.SafeheronProviderClaimDisabled)
		case fundrouting.ModeRoutingAuthoritative:
			repository.SetSafeheronProviderClaimMode(companyfund.SafeheronProviderClaimRoutingScoped)
		}
		payloadService, err := companyfund.NewOwnedProviderPayloadService(repository, payloadCipher, time.Now)
		if err != nil {
			log.Printf("company-fund runtime disabled: owned payload service could not be initialized")
			return
		}
		registry := companyfund.NewCompanyFundAccountRegistry(
			companyfund.NewPostgresAccountRegistryLoader(c.DB),
			config.AccountRefreshInterval,
		)
		if err := registry.Load(ctx); err != nil {
			log.Printf("company-fund runtime disabled: initial account registry load failed")
			return
		}
		financeHandler, financeHandlerErr := newCompanyFundFinanceHandler(repository, config.AdminKey)
		if financeHandlerErr != nil {
			log.Printf("company-fund finance management routes disabled: admin key is unavailable or invalid")
		}

		c.CompanyFundRepository = repository
		c.CompanyFundAccountRegistry = registry
		wireDepositCompanyFundRouting(c)
		c.CompanyFundOwnedPayloadService = payloadService
		c.CompanyFundFinanceHandler = financeHandler
		c.companyFundRuntimeConfig = config
		c.companyFundRuntimeContext = ctx
		c.companyFundRuntimePending = true

		log.Printf("company-fund core enabled: account_refresh_interval=%s finance_management=%t",
			registry.RefreshInterval(), financeHandler != nil)
	}
}

func wireDepositCompanyFundRouting(c *Container) {
	if c == nil || c.DepositPipeline == nil || c.CompanyFundAccountRegistry == nil {
		return
	}
	c.DepositPipeline.SetCompanyFundDestinationMatcher(c.CompanyFundAccountRegistry)
}

// finalizeCompanyFundRuntime runs only after NewContainer has applied every
// option. In particular, it sees an already-composed Safeheron client/webhook
// regardless of option order, while a missing webhook never prevents the
// independent Safeheron REST history compensation path from operating.
func finalizeCompanyFundRuntime(c *Container) {
	if c == nil || !c.companyFundRuntimePending || c.companyFundRuntimeFinalized {
		return
	}
	c.companyFundRuntimeFinalized = true
	if c.CompanyFundRepository == nil || c.CompanyFundAccountRegistry == nil || c.CompanyFundOwnedPayloadService == nil {
		return
	}
	config := c.companyFundRuntimeConfig

	cache, refresher, valuator := newCompanyFundCurrentValuationRuntime(c, config)
	if cache != nil {
		c.CompanyFundCurrentRateCache = cache
	}
	if refresher != nil {
		c.CompanyFundCoinGeckoRateRefresher = refresher
	}
	if valuator != nil {
		c.CompanyFundCurrentValuator = valuator
	}

	safeNormalizer, safeHistoryClient := newCompanyFundSafeheronNormalizer(c, config.SafeheronCoinCatalogRefreshInterval)
	airBundle, airwallexConfig := newCompanyFundAirwallexRuntimeBundle(c.CompanyFundAccountRegistry, config)

	normalizers := make(map[companyfund.Channel]companyfund.ProviderEventNormalizer, 2)
	if safeNormalizer != nil {
		normalizers[companyfund.ChannelSafeheron] = safeNormalizer
	}
	if airBundle != nil && airBundle.Enabled && airBundle.ProviderEvents != nil {
		normalizers[companyfund.ChannelAirwallex] = airBundle.ProviderEvents
	}
	if len(normalizers) == 0 {
		startCompanyFundCoreLoops(c, config, nil, refresher, valuator)
		log.Printf("company-fund workers disabled: no provider normalizer is configured")
		return
	}

	payloadReader, err := companyfund.NewProviderEventSourceBytesReader(
		companyfund.NewPostgresSafeheronWebhookPayloadReader(c.DB),
		c.CompanyFundOwnedPayloadService,
	)
	if err != nil {
		log.Printf("company-fund workers disabled: provider event source reader could not be initialized")
		startCompanyFundCoreLoops(c, config, nil, refresher, valuator)
		return
	}
	worker, err := companyfund.NewProviderEventWorker(
		c.CompanyFundRepository,
		payloadReader,
		normalizers,
		companyFundProviderEventWorkerConfig(config, valuator),
	)
	if err != nil {
		log.Printf("company-fund workers disabled: provider event worker configuration is invalid")
		startCompanyFundCoreLoops(c, config, nil, refresher, valuator)
		return
	}

	var syncAdapter *companyfund.CompanyFundReconciliationSyncRunAdapter
	if safeHistoryClient != nil || (airBundle != nil && airBundle.Enabled) {
		syncAdapter, err = companyfund.NewCompanyFundReconciliationSyncRunAdapter(
			c.CompanyFundRepository,
			companyfund.CompanyFundReconciliationSyncRunAdapterConfig{
				LeaseOwner:    companyFundLeaseOwner("company-fund-reconcile", config.ReconciliationLeaseOwner),
				LeaseDuration: companyFundDurationOrDefault(config.ReconciliationLeaseDuration, defaultCompanyFundReconciliationLeaseDuration),
			},
		)
		if err != nil {
			log.Printf("company-fund REST reconciliation disabled: sync-run lease configuration is invalid")
			syncAdapter = nil
		}
	}

	var safeReconciler *companyfund.SafeheronTransactionHistoryReconciler
	if safeHistoryClient != nil && syncAdapter != nil {
		var historyIngester companyfund.SafeheronHistoryOwnedProviderEventIngestor = c.CompanyFundOwnedPayloadService
		if c.SafeheronRoutingMode == fundrouting.ModeCaptureOnly || c.SafeheronRoutingMode == fundrouting.ModeRoutingAuthoritative {
			routingHistoryIngester, historyErr := fundrouting.NewHistoryInboxIngester(c.DB)
			if historyErr != nil {
				log.Printf("company-fund Safeheron routing history inbox disabled: %v", historyErr)
			} else {
				historyIngester = routingHistoryIngester
			}
		}
		safeReconciler, err = companyfund.NewSafeheronTransactionHistoryReconciler(
			safeHistoryClient,
			historyIngester,
			syncAdapter,
			companyfund.SafeheronTransactionHistoryReconcilerConfig{
				PageSize:          int32(companyFundPositiveIntOrDefault(config.SafeheronHistoryPageSize, defaultCompanyFundSafeheronHistoryPageSize)),
				MaxPages:          companyFundPositiveIntOrDefault(config.SafeheronHistoryMaxPages, defaultCompanyFundSafeheronHistoryMaxPages),
				PayloadKeyVersion: c.companyFundPayloadKeyVersion(),
				PayloadRetention:  config.PayloadRetention,
			},
		)
		if err != nil {
			log.Printf("company-fund Safeheron REST reconciliation disabled: configuration is invalid")
			safeReconciler = nil
		}
	}

	var airwallexClient *companyfund.AirwallexClient
	var airwallexReconciler *companyfund.AirwallexFinancialTransactionsReconciler
	if airBundle != nil && airBundle.Enabled && syncAdapter != nil {
		airwallexClient, err = newCompanyFundAirwallexClient(config)
		if err != nil {
			log.Printf("company-fund Airwallex REST client disabled: incomplete or invalid configuration")
			airwallexClient = nil
		}
		if airwallexClient != nil {
			airwallexReconciler, err = companyfund.NewAirwallexFinancialTransactionsReconciler(
				airwallexClient,
				c.CompanyFundOwnedPayloadService,
				syncAdapter,
				companyfund.AirwallexFinancialTransactionsReconcilerConfig{
					APIVersion:        airwallexConfig.APIVersion,
					SchemaVersion:     airwallexConfig.SchemaVersion,
					EventVersion:      airwallexConfig.EventVersion,
					PageSize:          companyFundPositiveIntOrDefault(config.AirwallexFinancialPageSize, defaultCompanyFundAirwallexFinancialPageSize),
					MaxPages:          companyFundPositiveIntOrDefault(config.AirwallexFinancialMaxPages, defaultCompanyFundAirwallexFinancialMaxPages),
					PayloadKeyVersion: c.companyFundPayloadKeyVersion(),
					PayloadRetention:  config.PayloadRetention,
				},
			)
			if err != nil {
				log.Printf("company-fund Airwallex REST reconciliation disabled: configuration is invalid")
				airwallexReconciler = nil
				airwallexClient = nil
			}
		}
	}

	runtimeDependencies := companyfund.CompanyFundRuntimeDependencies{
		ProviderEventWorker: worker,
	}
	if safeReconciler != nil || airwallexReconciler != nil {
		runtimeDependencies.AccountSnapshots = c.CompanyFundAccountRegistry
		runtimeDependencies.SyncRunFinalizer = syncAdapter
	}
	if safeReconciler != nil {
		runtimeDependencies.SafeheronReconciler = safeReconciler
	}
	if airwallexReconciler != nil {
		runtimeDependencies.AirwallexReconciler = airwallexReconciler
	}
	runtime, err := companyfund.NewCompanyFundRuntime(runtimeDependencies, companyfund.CompanyFundRuntimeConfig{
		EventPollInterval:          config.EventPollInterval,
		EventMaxIdleInterval:       config.EventMaxIdleInterval,
		EventDrainLimit:            config.EventDrainLimit,
		ReconciliationPollInterval: config.ReconciliationPoll,
		ReconciliationSchedule: companyfund.ReconciliationDailyScheduleConfig{
			TimeZone:    config.ReconciliationZone,
			DailyTime:   config.ReconciliationTime,
			CatchUpDays: config.ReconciliationCatchUp,
		},
		LateStatusOverlapDays:       config.LateStatusOverlapDays,
		LateStatusOverlapConfigured: config.LateStatusOverlapConfigured,
		ReconciliationRetryPolicy: companyfund.CompanyFundReconciliationRetryPolicy{
			InitialDelay: companyFundDurationOrDefault(config.ReconciliationRetryInitial, defaultCompanyFundReconciliationRetryInitialDelay),
			MaxDelay:     companyFundDurationOrDefault(config.ReconciliationRetryMax, defaultCompanyFundReconciliationRetryMaxDelay),
		},
		FinalizeTimeout:          companyFundDurationOrDefault(config.ReconciliationFinalizeTimeout, defaultCompanyFundReconciliationFinalizeTimeout),
		AirwallexWebhookLookback: config.AirwallexWebhookLookback,
		Now:                      time.Now,
	})
	if err != nil {
		log.Printf("company-fund workers disabled: runtime configuration is invalid")
		startCompanyFundCoreLoops(c, config, nil, refresher, valuator)
		return
	}

	c.CompanyFundProviderEventWorker = worker
	c.CompanyFundRuntime = runtime
	c.CompanyFundSafeheronNormalizer = safeNormalizer
	c.CompanyFundSafeheronReconciler = safeReconciler
	c.CompanyFundAirwallexRuntimeBundle = nil
	c.CompanyFundAirwallexClient = nil
	c.CompanyFundAirwallexReconciler = nil
	if airwallexReconciler != nil {
		c.CompanyFundAirwallexRuntimeBundle = airBundle
		c.CompanyFundAirwallexClient = airwallexClient
		c.CompanyFundAirwallexReconciler = airwallexReconciler
		if config.AirwallexWebhookVersion != "" && config.AirwallexWebhookVersion != airwallexConfig.EventVersion {
			log.Printf("company-fund Airwallex webhook disabled: webhook event version does not match strict runtime mapping")
		} else {
			airwallexWebhookHandler, webhookErr := newCompanyFundAirwallexWebhookHandler(
				config,
				c.CompanyFundOwnedPayloadService,
				c.companyFundPayloadKeyVersion(),
				composeCompanyFundWakeFuncs(
					runtime.ProviderEventWakeFunc(),
					runtime.AirwallexWebhookWakeFunc(),
				),
				runtime.AirwallexWebhookEligibilityFunc(),
			)
			if webhookErr != nil {
				log.Printf("company-fund Airwallex webhook disabled: incomplete or invalid configuration")
			} else if airwallexWebhookHandler != nil {
				c.CompanyFundAirwallexWebhookHandler = airwallexWebhookHandler
				if c.RateLimiter != nil {
					c.RateLimiter.SkipPath(companyFundAirwallexWebhookPath)
				}
			}
		}
	}

	// A Safeheron bridge is useful only when a live worker has a Safeheron
	// normalizer and a fail-closed candidate evaluator. The same eligibility
	// service is shared with the raw-event compensator so the two delivery paths
	// make identical company-wallet decisions.
	eligibility := newCompanyFundSafeheronWebhookEligibility(c, safeNormalizer)
	wireCompanyFundSafeheronBridge(c, eligibility)
	collector := newCompanyFundSafeheronCollector(c, safeNormalizer, eligibility)
	c.CompanyFundSafeheronCollector = collector
	startCompanyFundCoreLoops(c, config, runtime, refresher, valuator)

	log.Printf("company-fund runtime assembled: worker=%t safeheron_history=%t airwallex_reconciliation=%t airwallex_webhook=%t valuation=%t",
		worker != nil, safeReconciler != nil, airwallexReconciler != nil, c.CompanyFundAirwallexWebhookHandler != nil, valuator != nil)
}

func newCompanyFundSafeheronNormalizer(c *Container, catalogRefreshInterval time.Duration) (*companyfund.SafeheronProviderEventNormalizer, safeheron.TransactionHistoryClient) {
	if c == nil || c.SafeheronClient == nil || c.CompanyFundAccountRegistry == nil {
		return nil, nil
	}
	historyClient, ok := c.SafeheronClient.(safeheron.TransactionHistoryClient)
	if !ok {
		log.Printf("company-fund Safeheron history disabled: transaction history client is unavailable")
		return nil, nil
	}
	var err error
	var catalog *companyfund.SafeheronCoinCatalog
	if coinClient, available := c.SafeheronClient.(safeheron.CoinClient); available {
		catalog, err = companyfund.NewSafeheronCoinCatalog(coinClient, companyfund.SafeheronCoinCatalogConfig{RefreshInterval: catalogRefreshInterval})
		if err != nil {
			log.Printf("company-fund Safeheron coin catalog disabled: configuration is invalid")
			catalog = nil
		} else {
			refreshContext := c.companyFundRuntimeContext
			if refreshContext == nil {
				refreshContext = context.Background()
			}
			if refreshErr := catalog.Refresh(refreshContext); refreshErr != nil {
				log.Printf("company-fund Safeheron coin catalog cold start failed; policyless fallback remains enabled: %v", refreshErr)
			}
			c.CompanyFundSafeheronCoinCatalog = catalog
		}
	}
	var mapping *companyfund.RegistrySafeheronTransactionMappingResolver
	if catalog != nil {
		mapping, err = companyfund.NewRegistrySafeheronTransactionMappingResolver(c.CompanyFundAccountRegistry, catalog)
	} else {
		mapping, err = companyfund.NewRegistrySafeheronTransactionMappingResolver(c.CompanyFundAccountRegistry)
	}
	if err != nil {
		log.Printf("company-fund Safeheron normalizer disabled: account mapping is unavailable")
		return nil, nil
	}
	historyContext, err := companyfund.NewRegistrySafeheronHistoryAccountContextResolver(c.CompanyFundAccountRegistry)
	if err != nil {
		log.Printf("company-fund Safeheron normalizer disabled: history account context is unavailable")
		return nil, nil
	}
	normalizer, err := companyfund.NewSafeheronProviderEventNormalizer(companyfund.SafeheronProviderEventNormalizerConfig{
		MappingResolver:        mapping,
		RegistrySnapshots:      c.CompanyFundAccountRegistry,
		HistoryAccountResolver: historyContext,
	})
	if err != nil {
		log.Printf("company-fund Safeheron normalizer disabled: configuration is invalid")
		return nil, nil
	}
	return normalizer, historyClient
}

func newCompanyFundAirwallexRuntimeBundle(registry *companyfund.AccountRegistry, config companyFundRuntimeConfig) (*companyfund.AirwallexFinancialTransactionsRuntimeBundle, companyfund.AirwallexFinancialTransactionsRuntimeConfig) {
	runtimeConfig, err := companyfund.ParseAirwallexFinancialTransactionsRuntimeConfigJSON([]byte(config.AirwallexRuntimeConfigJSON))
	if err != nil {
		log.Printf("company-fund Airwallex runtime disabled: strict mapping configuration is invalid")
		return nil, companyfund.AirwallexFinancialTransactionsRuntimeConfig{}
	}
	if !runtimeConfig.Enabled {
		return nil, runtimeConfig
	}
	if registry == nil {
		log.Printf("company-fund Airwallex runtime disabled: configured account scope is not eligible")
		return nil, runtimeConfig
	}
	if _, eligible := companyfund.ResolveAirwallexSingleAccountScope(registry.Snapshot(), config.AirwallexLoginAs); !eligible {
		// The official Financial Transactions response has no account ownership
		// proof. Never enable its worker, REST reconciler, or webhook route
		// unless one configured account exactly matches x-login-as.
		log.Printf("company-fund Airwallex runtime disabled: configured account scope is not eligible")
		return nil, runtimeConfig
	}
	bundle, err := companyfund.NewAirwallexFinancialTransactionsScopedRuntimeBundle(runtimeConfig, registry, config.AirwallexLoginAs)
	if err != nil {
		log.Printf("company-fund Airwallex runtime disabled: strict mapping configuration is invalid")
		return nil, companyfund.AirwallexFinancialTransactionsRuntimeConfig{}
	}
	return bundle, runtimeConfig
}

func newCompanyFundCurrentValuationRuntime(c *Container, config companyFundRuntimeConfig) (*companyfund.CurrentRateCache, *companyfund.CoinGeckoCurrentRateRefresher, *companyfund.CompanyFundCurrentValuator) {
	if c == nil || c.CompanyFundRepository == nil || c.CompanyFundAccountRegistry == nil {
		return nil, nil, nil
	}
	defaultMappings, err := companyfund.ParseCoinGeckoDefaultRateMappingsJSON([]byte(config.CurrentRateDefaultMappingsJSON))
	if err != nil {
		log.Printf("company-fund USD valuation disabled: default rate mappings are invalid")
		return nil, nil, nil
	}
	cache, err := companyfund.NewCurrentRateCache(companyfund.CurrentRateCacheConfig{
		TTL:         companyFundDurationOrDefault(config.CurrentRateCacheTTL, defaultCompanyFundCurrentRateCacheTTL),
		MaxQuoteAge: companyFundDurationOrDefault(config.CurrentRateCacheMaxAge, defaultCompanyFundCurrentRateCacheMaxAge),
		Clock:       time.Now,
	})
	if err != nil {
		log.Printf("company-fund USD valuation disabled: current-rate cache configuration is invalid")
		return nil, nil, nil
	}
	client, err := companyfund.NewCoinGeckoClient(companyfund.CoinGeckoClientConfig{
		BaseURL:    config.CoinGeckoBaseURL,
		DemoAPIKey: config.CoinGeckoDemoAPIKey,
		Clock:      time.Now,
	})
	if err != nil {
		log.Printf("company-fund USD valuation disabled: CoinGecko client configuration is invalid")
		return nil, nil, nil
	}
	refresher, err := companyfund.NewCoinGeckoCurrentRateRefresher(client, c.CompanyFundAccountRegistry, cache, companyfund.CoinGeckoCurrentRateRefresherConfig{
		RefreshInterval: companyFundDurationOrDefault(config.CurrentRateRefreshInterval, defaultCompanyFundCurrentRateRefreshInterval),
		Clock:           time.Now,
		SnapshotStore:   c.CompanyFundRepository,
		PolicyVersion:   config.CurrentValuationPolicyVersion,
		DefaultMappings: defaultMappings,
	})
	if err != nil {
		log.Printf("company-fund USD valuation disabled: CoinGecko refresher configuration is invalid")
		return nil, nil, nil
	}
	valuator, err := companyfund.NewCompanyFundCurrentValuator(c.CompanyFundRepository, c.CompanyFundAccountRegistry, cache, companyfund.CompanyFundCurrentValuatorConfig{
		PolicyVersion:   config.CurrentValuationPolicyVersion,
		DefaultMappings: defaultMappings,
	})
	if err != nil {
		log.Printf("company-fund USD valuation disabled: valuator configuration is invalid")
		return nil, nil, nil
	}
	return cache, refresher, valuator
}

func companyFundProviderEventWorkerConfig(config companyFundRuntimeConfig, valuator companyfund.CompanyFundTransactionValuator) companyfund.ProviderEventWorkerConfig {
	leaseDuration := companyFundDurationOrDefault(config.EventLeaseDuration, defaultCompanyFundEventLeaseDuration)
	renewInterval := companyFundDurationOrDefault(config.EventRenewInterval, defaultCompanyFundEventLeaseRenewInterval)
	return companyfund.ProviderEventWorkerConfig{
		Owner:         companyFundLeaseOwner("company-fund-events", config.EventLeaseOwner),
		LeaseDuration: leaseDuration,
		RenewInterval: renewInterval,
		RetryPolicy: companyfund.ProviderEventRetryPolicy{
			InitialDelay: companyFundDurationOrDefault(config.EventRetryInitial, defaultCompanyFundEventRetryInitialDelay),
			MaxDelay:     companyFundDurationOrDefault(config.EventRetryMax, defaultCompanyFundEventRetryMaxDelay),
		},
		Now:                 time.Now,
		TransactionValuator: valuator,
	}
}

func companyFundLeaseOwner(prefix, configured string) string {
	if value := strings.TrimSpace(configured); value != "" {
		return value
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown-host"
	}
	return fmt.Sprintf("%s-%s-%d", prefix, host, os.Getpid())
}

func companyFundDurationOrDefault(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

func companyFundPositiveDuration(value, fallback time.Duration) (time.Duration, error) {
	value = companyFundDurationOrDefault(value, fallback)
	if value <= 0 || value.Microseconds() <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return value, nil
}

func companyFundPositiveIntOrDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func (c *Container) companyFundPayloadKeyVersion() string {
	if c == nil {
		return ""
	}
	return c.companyFundRuntimeConfig.PayloadKeyVersion
}

func decodeCompanyFundPayloadKey(value string) ([]byte, error) {
	if value == "" || value != strings.TrimSpace(value) {
		return nil, fmt.Errorf("company-fund payload key must be a non-blank exact value")
	}
	if len(value) == 64 {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return nil, fmt.Errorf("company-fund payload key hex form is invalid")
		}
		return decoded, nil
	}
	if len([]byte(value)) != 32 {
		return nil, fmt.Errorf("company-fund payload key must be 32 bytes or 64 hex characters")
	}
	return []byte(value), nil
}

func normalizeCompanyFundPayloadKeyVersion(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxCompanyFundPayloadKeyVersionBytes {
		return "", fmt.Errorf("company-fund payload key version must be a non-blank bounded exact value")
	}
	return value, nil
}

func newCompanyFundAirwallexClient(config companyFundRuntimeConfig) (*companyfund.AirwallexClient, error) {
	configured := config.AirwallexClientID != "" || config.AirwallexAPIKey != "" || config.AirwallexAPIVersion != "" || config.AirwallexLoginAs != ""
	if !configured {
		return nil, nil
	}
	if strings.TrimSpace(config.AirwallexClientID) == "" || strings.TrimSpace(config.AirwallexAPIKey) == "" || strings.TrimSpace(config.AirwallexAPIVersion) == "" ||
		config.AirwallexLoginAs == "" || config.AirwallexLoginAs != strings.TrimSpace(config.AirwallexLoginAs) {
		return nil, fmt.Errorf("Airwallex client credentials, API version, and exact login scope are required together")
	}
	return companyfund.NewAirwallexClient(companyfund.AirwallexClientConfig{
		BaseURL:    config.AirwallexBaseURL,
		ClientID:   config.AirwallexClientID,
		APIKey:     config.AirwallexAPIKey,
		APIVersion: config.AirwallexAPIVersion,
		LoginAs:    config.AirwallexLoginAs,
	})
}

func newCompanyFundAirwallexWebhookHandler(config companyFundRuntimeConfig, ingestor *companyfund.OwnedProviderPayloadService, keyVersion string, wake func(), eligible func() bool) (*handlers.CompanyFundAirwallexWebhookHandler, error) {
	configured := config.AirwallexWebhookSecret != "" || config.AirwallexWebhookVersion != ""
	if !configured {
		return nil, nil
	}
	if strings.TrimSpace(config.AirwallexWebhookSecret) == "" || config.AirwallexWebhookVersion == "" || config.AirwallexWebhookVersion != strings.TrimSpace(config.AirwallexWebhookVersion) {
		return nil, fmt.Errorf("Airwallex webhook secret and version are required together")
	}
	verifier, err := companyfund.NewAirwallexWebhookVerifier(companyfund.AirwallexWebhookVerifierConfig{
		Secret: config.AirwallexWebhookSecret,
		MaxAge: config.AirwallexWebhookMaxAge,
	})
	if err != nil {
		return nil, err
	}
	return handlers.NewCompanyFundAirwallexWebhookHandler(handlers.CompanyFundAirwallexWebhookHandlerConfig{
		Verifier:             verifier,
		Ingestor:             ingestor,
		Wake:                 wake,
		Eligible:             eligible,
		ProviderEventVersion: config.AirwallexWebhookVersion,
		KeyVersion:           keyVersion,
		Retention:            config.PayloadRetention,
		LegalHold:            config.PayloadLegalHold,
	})
}

func newCompanyFundFinanceHandler(store companyfund.CompanyFundFinanceStore, adminKey string) (*handlers.CompanyFundFinanceHandler, error) {
	if adminKey == "" {
		return nil, nil
	}
	return handlers.NewCompanyFundFinanceHandler(handlers.CompanyFundFinanceHandlerConfig{
		Store:    store,
		AdminKey: adminKey,
	})
}

// wireCompanyFundSafeheronBridge is called by both options and again after
// finalization. The rate-limit exemption is independent of the bridge, while
// bridge attachment requires a ready Safeheron normalizer so raw webhook rows
// can never create an unconsumable company-fund inbox entry.
func newCompanyFundSafeheronWebhookEligibility(c *Container, normalizer *companyfund.SafeheronProviderEventNormalizer) companyfund.SafeheronWebhookEligibility {
	if c == nil || normalizer == nil || c.CompanyFundAccountRegistry == nil || c.CompanyFundRepository == nil {
		return nil
	}
	var evaluator *companyfund.RegistrySafeheronWebhookCandidateEvaluator
	var err error
	if c.CompanyFundSafeheronCoinCatalog != nil {
		evaluator, err = companyfund.NewRegistrySafeheronWebhookCandidateEvaluator(c.CompanyFundAccountRegistry, c.CompanyFundSafeheronCoinCatalog)
	} else {
		evaluator, err = companyfund.NewRegistrySafeheronWebhookCandidateEvaluator(c.CompanyFundAccountRegistry)
	}
	if err != nil {
		log.Printf("company-fund Safeheron eligibility disabled: account evaluator is unavailable")
		return nil
	}
	eligibility, err := companyfund.NewSafeheronWebhookEligibilityService(evaluator, c.CompanyFundRepository)
	if err != nil {
		log.Printf("company-fund Safeheron eligibility disabled: exclusion store is unavailable")
		return nil
	}
	return eligibility
}

func wireCompanyFundSafeheronBridge(c *Container, eligibility ...companyfund.SafeheronWebhookEligibility) {
	if c == nil || c.SafeheronWebhookHandler == nil {
		return
	}
	if c.RateLimiter != nil {
		c.RateLimiter.SkipPath(companyFundSafeheronWebhookPath)
	}
	if c.SafeheronRoutingMode == fundrouting.ModeCaptureOnly || c.SafeheronRoutingMode == fundrouting.ModeRoutingAuthoritative {
		return
	}
	if len(eligibility) != 1 || eligibility[0] == nil || c.CompanyFundSafeheronNormalizer == nil || c.CompanyFundRepository == nil || c.DepositEventRepo == nil {
		return
	}
	sourceLookup, ok := c.DepositEventRepo.(handlers.SafeheronEventSourceLookup)
	if !ok {
		return
	}
	c.SafeheronWebhookHandler.SetCompanyFundBridge(sourceLookup, c.CompanyFundRepository)
	c.SafeheronWebhookHandler.SetCompanyFundEligibility(eligibility[0])
	if c.CompanyFundRuntime != nil {
		c.SafeheronWebhookHandler.SetCompanyFundProviderEventWake(c.CompanyFundRuntime.ProviderEventWakeFunc())
	}
}

// composeCompanyFundWakeFuncs merges process-local wake callbacks. Nil entries
// are skipped; the returned func is nil only when every input is nil.
func composeCompanyFundWakeFuncs(wakes ...func()) func() {
	var active []func()
	for _, wake := range wakes {
		if wake != nil {
			active = append(active, wake)
		}
	}
	if len(active) == 0 {
		return nil
	}
	if len(active) == 1 {
		return active[0]
	}
	return func() {
		for _, wake := range active {
			wake()
		}
	}
}
