package container

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/handlers"
	"monera-digital/internal/middleware"
	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

type companyFundEventWriterStub struct{}

type companyFundRegistryLoaderStub struct{}

func (companyFundRegistryLoaderStub) LoadCompanyFundAccounts(context.Context) ([]companyfund.CompanyFundAccount, []companyfund.AccountAssetPolicy, error) {
	return []companyfund.CompanyFundAccount{{
		ID: 1, Channel: companyfund.AccountChannelSafeheron, ProviderAccountKey: "safeheron-account",
		NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true,
	}}, nil, nil
}

func (companyFundEventWriterStub) InsertProviderEvent(context.Context, companyfund.ProviderEventInput) (companyfund.ProviderEventInsertResult, error) {
	return companyfund.ProviderEventInsertResult{ID: 1, Inserted: true}, nil
}

type companyFundHistoryClientStub struct{}

func TestCompanyFundCurrentRateDefaultsMatchDemoRefreshBudget(t *testing.T) {
	require.Equal(t, 5*time.Minute, defaultCompanyFundCurrentRateRefreshInterval)
	require.Equal(t, 10*time.Minute, defaultCompanyFundCurrentRateCacheTTL)
	require.Equal(t, 60*time.Minute, defaultCompanyFundCurrentRateCacheMaxAge)
}

func TestWireDepositCompanyFundRoutingAcceptsEitherOptionOrder(t *testing.T) {
	registry := companyfund.NewAccountRegistry(companyFundRegistryLoaderStub{}, time.Hour)
	require.NoError(t, registry.Load(t.Context()))
	pipeline := deposit.NewService(nil, nil, nil)

	require.NotPanics(t, func() {
		wireDepositCompanyFundRouting(nil)
		wireDepositCompanyFundRouting(&Container{DepositPipeline: pipeline})
		wireDepositCompanyFundRouting(&Container{CompanyFundAccountRegistry: registry})
		wireDepositCompanyFundRouting(&Container{DepositPipeline: pipeline, CompanyFundAccountRegistry: registry})
	})
}

func TestNewCompanyFundCurrentValuationRuntime_ParsesDefaultMappingsBeforeWiring(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	registry := companyfund.NewAccountRegistry(companyFundRegistryLoaderStub{}, time.Hour)
	require.NoError(t, registry.Load(t.Context()))
	repository := companyfund.NewDBRepository(db)
	cont := &Container{DB: db, CompanyFundRepository: repository, CompanyFundAccountRegistry: registry}

	cache, refresher, valuator := newCompanyFundCurrentValuationRuntime(cont, companyFundRuntimeConfig{
		CurrentRateDefaultMappingsJSON: `{"crypto":{"BTC":"bitcoin","USDT":"tether"},"fiat":["USD","SGD"]}`,
	})
	require.NotNil(t, cache)
	require.NotNil(t, refresher)
	require.NotNil(t, valuator)

	cache, refresher, valuator = newCompanyFundCurrentValuationRuntime(cont, companyFundRuntimeConfig{
		CurrentRateDefaultMappingsJSON: `{"crypto":{"USD":"unsafe"},"fiat":["USD"]}`,
	})
	require.Nil(t, cache)
	require.Nil(t, refresher)
	require.Nil(t, valuator)
}

func (companyFundHistoryClientStub) CreateAssetWallet(context.Context, string, []string) (*safeheron.Wallet, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) AddCoin(context.Context, string, []string) (*safeheron.Wallet, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) ListAccountCoin(context.Context, string) ([]safeheron.AccountCoin, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) GetAccountByAddress(context.Context, string) (*safeheron.Account, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) KytReport(context.Context, string) (*safeheron.KytReportResponse, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) CreateTransaction(context.Context, safeheron.CreateTransactionRequest) (*safeheron.CreateTransactionResponse, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) GetTransaction(context.Context, string) (*safeheron.TransactionDetail, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) WebhookConvert([]byte) (*safeheron.WebhookEvent, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) Close() error { return nil }

func (companyFundHistoryClientStub) ListTransactions(context.Context, safeheron.TransactionHistoryRequest) ([]safeheron.TransactionSnapshot, error) {
	return nil, nil
}

func (companyFundHistoryClientStub) LookupTransaction(context.Context, safeheron.TransactionLookup) (*safeheron.TransactionSnapshot, error) {
	return nil, nil
}

func TestWithCompanyFund_ComposesIndependentCoreAndOptionalAdapters(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	expectCompanyFundRegistryLoadWithAirwallexScope(mock, "awx-usd")
	cont := &Container{DB: db, RateLimiter: middleware.NewRateLimiter(5, time.Minute)}
	defer cont.RateLimiter.Stop()
	config := companyFundRuntimeConfig{
		Enabled:                    true,
		StartBackgroundWorkers:     false,
		AccountRefreshInterval:     time.Hour,
		PayloadKey:                 strings.Repeat("a", 64),
		PayloadKeyVersion:          "payload-v1",
		PayloadRetention:           48 * time.Hour,
		AirwallexClientID:          "client-id",
		AirwallexAPIKey:            "api-key",
		AirwallexAPIVersion:        "2025-04-29",
		AirwallexLoginAs:           "awx-usd",
		AirwallexWebhookVersion:    "event-v1",
		AirwallexWebhookSecret:     "webhook-secret",
		AirwallexWebhookMaxAge:     time.Minute,
		AirwallexRuntimeConfigJSON: testCompanyFundAirwallexRuntimeJSON(),
		AdminKey:                   "company-fund-admin-key",
	}
	withCompanyFund(context.Background(), config)(cont)
	finalizeCompanyFundRuntime(cont)
	finalizeCompanyFundRuntime(cont)
	defer cont.CompanyFundAccountRegistry.Stop()

	require.NotNil(t, cont.CompanyFundRepository)
	require.NotNil(t, cont.CompanyFundAccountRegistry)
	require.NotNil(t, cont.CompanyFundOwnedPayloadService)
	require.NotNil(t, cont.CompanyFundAirwallexClient)
	require.NotNil(t, cont.CompanyFundAirwallexWebhookHandler)
	require.NotNil(t, cont.CompanyFundFinanceHandler)
	require.NotNil(t, cont.CompanyFundRuntime)
	require.Equal(t, "2025-04-29", cont.CompanyFundAirwallexClient.PinnedAPIVersion())
	require.True(t, cont.RateLimiter.IsPathWhitelisted(companyFundAirwallexWebhookPath))
	require.False(t, cont.RateLimiter.IsPathWhitelisted("/api/webhooks"))
	require.False(t, cont.RateLimiter.IsPathWhitelisted(companyFundSafeheronWebhookPath), "Safeheron is only skipped once its handler exists")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithCompanyFund_MissingIndependentPayloadKeyDisablesWithoutDatabaseRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	cont := &Container{DB: db, RateLimiter: middleware.NewRateLimiter(5, time.Minute)}
	defer cont.RateLimiter.Stop()

	withCompanyFund(context.Background(), companyFundRuntimeConfig{
		Enabled:                true,
		StartBackgroundWorkers: false,
		PayloadKeyVersion:      "payload-v1",
	})(cont)

	require.Nil(t, cont.CompanyFundRepository)
	require.Nil(t, cont.CompanyFundAccountRegistry)
	require.Nil(t, cont.CompanyFundOwnedPayloadService)
	require.False(t, cont.RateLimiter.IsPathWhitelisted(companyFundAirwallexWebhookPath))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWithCompanyFund_LeavesManagementRoutesUnavailableWithoutAdminKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectCompanyFundRegistryLoad(mock)
	cont := &Container{DB: db, RateLimiter: middleware.NewRateLimiter(5, time.Minute)}
	defer cont.RateLimiter.Stop()

	withCompanyFund(context.Background(), companyFundRuntimeConfig{
		Enabled:                true,
		StartBackgroundWorkers: false,
		PayloadKey:             strings.Repeat("b", 64),
		PayloadKeyVersion:      "payload-v1",
	})(cont)
	finalizeCompanyFundRuntime(cont)
	defer cont.CompanyFundAccountRegistry.Stop()

	require.NotNil(t, cont.CompanyFundRepository)
	require.Nil(t, cont.CompanyFundFinanceHandler, "routes must not be registered without the dedicated management key")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinalizeCompanyFundRuntime_InvalidStrictAirwallexMappingDoesNotExposeIngress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectCompanyFundRegistryLoad(mock)
	cont := &Container{DB: db, RateLimiter: middleware.NewRateLimiter(5, time.Minute)}
	defer cont.RateLimiter.Stop()

	withCompanyFund(context.Background(), companyFundRuntimeConfig{
		Enabled:                    true,
		StartBackgroundWorkers:     false,
		PayloadKey:                 strings.Repeat("d", 64),
		PayloadKeyVersion:          "payload-v1",
		AirwallexClientID:          "client-id",
		AirwallexAPIKey:            "api-key",
		AirwallexAPIVersion:        "2025-04-29",
		AirwallexWebhookVersion:    "event-v1",
		AirwallexWebhookSecret:     "webhook-secret",
		AirwallexRuntimeConfigJSON: `{"enabled":true,"unknown":true}`,
	})(cont)
	finalizeCompanyFundRuntime(cont)
	defer cont.CompanyFundAccountRegistry.Stop()

	require.Nil(t, cont.CompanyFundAirwallexRuntimeBundle)
	require.Nil(t, cont.CompanyFundAirwallexClient)
	require.Nil(t, cont.CompanyFundAirwallexReconciler)
	require.Nil(t, cont.CompanyFundAirwallexWebhookHandler)
	require.Nil(t, cont.CompanyFundRuntime)
	require.False(t, cont.RateLimiter.IsPathWhitelisted(companyFundAirwallexWebhookPath))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinalizeCompanyFundRuntime_AirwallexScopeMismatchDoesNotExposeIngress(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectCompanyFundRegistryLoadWithAirwallexScope(mock, "awx-usd")
	cont := &Container{DB: db, RateLimiter: middleware.NewRateLimiter(5, time.Minute)}
	defer cont.RateLimiter.Stop()

	withCompanyFund(context.Background(), companyFundRuntimeConfig{
		Enabled:                    true,
		StartBackgroundWorkers:     false,
		PayloadKey:                 strings.Repeat("d", 64),
		PayloadKeyVersion:          "payload-v1",
		AirwallexClientID:          "client-id",
		AirwallexAPIKey:            "api-key",
		AirwallexAPIVersion:        "2025-04-29",
		AirwallexLoginAs:           "awx-other",
		AirwallexWebhookVersion:    "event-v1",
		AirwallexWebhookSecret:     "webhook-secret",
		AirwallexRuntimeConfigJSON: testCompanyFundAirwallexRuntimeJSON(),
	})(cont)
	finalizeCompanyFundRuntime(cont)
	defer cont.CompanyFundAccountRegistry.Stop()

	require.Nil(t, cont.CompanyFundAirwallexRuntimeBundle)
	require.Nil(t, cont.CompanyFundAirwallexClient)
	require.Nil(t, cont.CompanyFundAirwallexReconciler)
	require.Nil(t, cont.CompanyFundAirwallexWebhookHandler)
	require.Nil(t, cont.CompanyFundRuntime)
	require.False(t, cont.RateLimiter.IsPathWhitelisted(companyFundAirwallexWebhookPath))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestNewContainer_FinalizesSafeheronRuntimeAfterAllOptionsRegardlessOrder(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		companyFirst bool
	}{
		{name: "company fund before safeheron", companyFirst: true},
		{name: "safeheron before company fund", companyFirst: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			expectCompanyFundRegistryLoad(mock)

			companyOption := withCompanyFund(context.Background(), companyFundRuntimeConfig{
				Enabled:                true,
				StartBackgroundWorkers: false,
				PayloadKey:             strings.Repeat("e", 64),
				PayloadKeyVersion:      "payload-v1",
			})
			safeheronOption := ContainerOption(func(c *Container) {
				c.SafeheronClient = companyFundHistoryClientStub{}
			})
			options := []ContainerOption{companyOption, safeheronOption}
			if !testCase.companyFirst {
				options[0], options[1] = options[1], options[0]
			}

			cont := NewContainer(db, "test-jwt", options...)
			defer cont.Close()

			require.NotNil(t, cont.CompanyFundSafeheronNormalizer)
			require.NotNil(t, cont.CompanyFundSafeheronReconciler)
			require.NotNil(t, cont.CompanyFundProviderEventWorker)
			require.NotNil(t, cont.CompanyFundRuntime)
			require.Nil(t, cont.CompanyFundSafeheronCollector, "no webhook source means no raw-event bridge compensator")
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestNewCompanyFundAirwallexWebhookHandler_RejectsInvalidRetentionInsteadOfExtendingIt(t *testing.T) {
	payloadKey, err := decodeCompanyFundPayloadKey(strings.Repeat("c", 64))
	require.NoError(t, err)
	cipher, err := companyfund.NewAES256GCMPayloadCipher(map[string][]byte{"payload-v1": payloadKey})
	require.NoError(t, err)
	repository := &companyFundEventWriterStub{}
	payloadService, err := companyfund.NewOwnedProviderPayloadService(repository, cipher, time.Now)
	require.NoError(t, err)

	_, err = newCompanyFundAirwallexWebhookHandler(companyFundRuntimeConfig{
		AirwallexWebhookVersion: "2026-05-29",
		AirwallexWebhookSecret:  "webhook-secret",
		PayloadRetention:        -time.Hour,
	}, payloadService, "payload-v1", nil, nil)
	require.Error(t, err)
}

func TestDecodeCompanyFundPayloadKey_UsesOnlyDedicatedValidKeyMaterial(t *testing.T) {
	decoded, err := decodeCompanyFundPayloadKey(strings.Repeat("ab", 32))
	require.NoError(t, err)
	require.Len(t, decoded, 32)

	raw := strings.Repeat("x", 32)
	decoded, err = decodeCompanyFundPayloadKey(raw)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), decoded)

	for _, invalid := range []string{"", "short", " " + strings.Repeat("a", 64), strings.Repeat("z", 64)} {
		if _, err := decodeCompanyFundPayloadKey(invalid); err == nil {
			t.Fatalf("decodeCompanyFundPayloadKey(%q) error = nil, want rejection", invalid)
		}
	}
}

func TestWireCompanyFundSafeheronBridge_ExemptsOnlyTheExistingWebhookPath(t *testing.T) {
	limiter := middleware.NewRateLimiter(5, time.Minute)
	defer limiter.Stop()
	cont := &Container{
		RateLimiter:             limiter,
		SafeheronWebhookHandler: handlers.NewSafeheronWebhookHandler(nil, nil, nil),
	}

	wireCompanyFundSafeheronBridge(cont)

	require.True(t, limiter.IsPathWhitelisted(companyFundSafeheronWebhookPath))
	require.False(t, limiter.IsPathWhitelisted("/api/webhooks"))
	require.False(t, limiter.IsPathWhitelisted("/api/webhooks/safeheron/replay"))
}

func expectCompanyFundRegistryLoad(mock sqlmock.Sqlmock) {
	expectCompanyFundRegistryLoadWithAirwallexScope(mock, "")
}

func expectCompanyFundRegistryLoadWithAirwallexScope(mock sqlmock.Sqlmock, loginAs string) {
	mock.ExpectBegin()
	accountRows := sqlmock.NewRows([]string{
		"id", "channel", "provider_account_key", "wallet_address", "normalized_address", "network_family",
		"company_entity", "fund_account_name", "sub_account_name", "account_type", "account_name", "account_role", "is_enabled",
		"monitoring_started_at", "first_enabled_at",
	})
	if loginAs != "" {
		enabledAt := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
		accountRows.AddRow(1, "AIRWALLEX", loginAs, "", "", "", "Monera Ltd", "Treasury", "USD", "BANK", "USD", "", true, enabledAt, enabledAt)
	}
	mock.ExpectQuery(regexp.QuoteMeta("FROM company_fund_accounts\nWHERE is_enabled = true")).WillReturnRows(accountRows)
	mock.ExpectQuery(regexp.QuoteMeta("FROM company_fund_account_asset_policies\nWHERE is_enabled = true")).WillReturnRows(sqlmock.NewRows([]string{
		"id", "company_fund_account_id", "currency", "chain_code", "provider_asset_key", "asset_contract",
		"dust_detection_enabled", "dust_threshold", "auto_exclude_dust_from_summary", "valuation_provider_asset_id",
		"valuation_provider_platform_id", "valuation_contract_address", "is_enabled",
	}))
	mock.ExpectCommit()
}

func testCompanyFundAirwallexRuntimeJSON() string {
	return `{
  "enabled": true,
  "api_version": "2025-04-29",
  "schema_version": "schema-v1",
  "event_version": "event-v1",
  "mapping_version": "mapping-v1",
  "fact_version": 1,
  "rules": [{
    "evidence_reference": "sandbox-fixture-1",
    "provider_account_key": "awx-usd",
    "currency": "usd",
    "status": "settled",
    "classification": {
      "transaction_type": "fixture_credit",
      "source_type": "fixture_source",
      "action": "APPLY",
      "movement_kind": "PRINCIPAL",
      "direction": "INFLOW",
      "transfer_mode": "SINGLE",
      "amount_field": "AMOUNT",
      "expected_sign": "POSITIVE",
      "occurred_at_field": "CREATED_AT"
    }
  }]
}`
}
