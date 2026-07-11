package migrations

import (
	"strings"
	"testing"

	"monera-digital/internal/migration"
)

func TestCreateCompanyFundLedger_Metadata(t *testing.T) {
	var _ migration.Migration = (*CreateCompanyFundLedger)(nil)

	m := &CreateCompanyFundLedger{}
	if got := m.Version(); got != "050" {
		t.Fatalf("Version() = %q, want %q", got, "050")
	}
	if m.Description() == "" {
		t.Fatal("Description() must not be empty")
	}
}

func TestCreateCompanyFundLedger_UpSchemaContracts(t *testing.T) {
	ddl := strings.Join(companyFundLedgerUpStatements(), "\n")

	for _, table := range []string{
		"company_fund_accounts",
		"company_fund_account_asset_policies",
		"finance_categories",
		"company_fund_provider_events",
		"company_fund_safeheron_raw_event_exclusions",
		"company_fund_provider_transaction_facts",
		"company_fund_transactions",
		"company_fund_sync_runs",
		"company_fund_rate_snapshots",
		"company_fund_rate_budget_periods",
		"company_fund_rate_requests",
		"company_fund_transaction_valuation_history",
		"company_fund_valuation_jobs",
	} {
		if !strings.Contains(ddl, "CREATE TABLE IF NOT EXISTS "+table) {
			t.Errorf("migration DDL does not create %s", table)
		}
	}

	for _, contract := range []string{
		"NUMERIC(65, 18)",
		"movement_key                            VARCHAR(256) NOT NULL UNIQUE",
		"transfer_mode                           VARCHAR(16)",
		"movement_kind                           VARCHAR(16)",
		"parent_transaction_id                   BIGINT REFERENCES company_fund_transactions(id)",
		"reversal_of_transaction_id              BIGINT REFERENCES company_fund_transactions(id)",
		"conversion_group_key                    VARCHAR(256)",
		"conversion_group_status                 VARCHAR(16)",
		"idx_company_fund_transactions_parent_links",
		"transaction_direction                   VARCHAR(24)",
		"provider_updated_at                     TIMESTAMPTZ",
		"provider_fact_source                    VARCHAR(24) NOT NULL",
		"provider_fact_source IN ('WEBHOOK', 'PRODUCT_DETAIL', 'RECONCILIATION')",
		"(latest_provider_event_id IS NULL AND raw_snapshot_digest IS NULL)",
		"(latest_provider_event_id IS NOT NULL AND raw_snapshot_digest IS NOT NULL)",
		"FOREIGN KEY (id, current_valuation_history_id)",
		"REFERENCES company_fund_transaction_valuation_history(transaction_id, id)",
		"DEFERRABLE INITIALLY DEFERRED",
		"UNIQUE (transaction_id, valuation_version)",
		"UNIQUE (transaction_id, id)",
		"UNIQUE (transaction_id, id, dependency_fingerprint)",
		"company_fund_transaction_valuation_history_immutable",
		"company_fund_reject_rate_snapshot_cycle",
		"UNIQUE (provider, billing_anchor, period_key)",
		"UNIQUE (provider, logical_request_key, attempt_no)",
		"ON company_fund_rate_requests (provider, logical_request_key)",
		"idx_company_fund_rate_requests_active_key",
		"CHECK (payload_digest IS NULL OR payload_digest ~ '^[0-9a-f]{64}$')",
		"safeheron_webhook_event_id     INTEGER REFERENCES safeheron_webhook_events(id)",
		"CREATE TABLE IF NOT EXISTS company_fund_safeheron_raw_event_exclusions",
		"source_payload_digest     VARCHAR(64) NOT NULL",
		"exclusion_reason          VARCHAR(64) NOT NULL",
		"configuration_fingerprint VARCHAR(64)",
		"exclusion_reason IN ('UNMAPPED_ASSET', 'NO_CONFIGURED_ADDRESS')",
		"configuration_fingerprint IS NOT NULL",
		"'NO_CONFIGURED_ADDRESS'",
		"settings-dependent reasons bind to a stable configuration fingerprint",
		"provider_event_version          VARCHAR(64)",
		"event_state IN ('PENDING', 'LEASED', 'PROCESSED', 'FAILED', 'IGNORED', 'DEAD_LETTER')",
		"event_state = 'FAILED' AND next_attempt_at IS NOT NULL",
		"event_state IN ('PROCESSED', 'IGNORED', 'DEAD_LETTER') AND processed_at IS NOT NULL",
		"channel = 'SAFEHERON'",
		"octet_length(owned_payload_ciphertext) <= 1048576",
		"idx_company_fund_provider_events_claim",
		"next_attempt_at    TIMESTAMPTZ",
		"status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL",
		"status NOT IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NULL",
		"idx_company_fund_sync_runs_claim",
		"company_fund_assert_finance_category_assignment",
		"company_fund_validate_transaction_finance_categories",
		"company_fund_revalidate_finance_category_hierarchy",
		"CREATE CONSTRAINT TRIGGER company_fund_transactions_finance_category_hierarchy",
		"CREATE CONSTRAINT TRIGGER finance_categories_company_fund_hierarchy_guard",
		"level2_parent_id IS DISTINCT FROM p_level1_id",
		"dust_threshold                          NUMERIC(65, 18)",
		"Explicit CoinGecko asset mapping: coin ID, or fiat:<CODE>",
		"fee_details                             JSONB NOT NULL DEFAULT '{}'::jsonb",
		"CHECK (jsonb_typeof(fee_details) = 'object')",
		"CHECK (block_height IS NULL OR block_height >= 0)",
		"CHECK (dust_threshold IS NULL OR dust_threshold >= 0)",
		"CHECK (NOT is_dust OR (dust_policy_id IS NOT NULL AND dust_threshold IS NOT NULL))",
		"auto_excluded_from_summary              BOOLEAN NOT NULL DEFAULT false",
		"summary_inclusion_override              BOOLEAN",
		"is_source_phishing                      BOOLEAN",
		"is_destination_phishing                 BOOLEAN",
		"is_unrecognized_asset                   BOOLEAN NOT NULL DEFAULT false",
		"aml_lock                                BOOLEAN",
		"aml_screening_state                     VARCHAR(32) NOT NULL DEFAULT 'NOT_SCREENED'",
		"aml_risk_level                          VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN'",
		"risk_flags                              JSONB NOT NULL DEFAULT '[]'::jsonb",
		"jsonb_typeof(risk_flags) = 'array'",
		"idx_company_fund_transactions_aml_filter",
		"idx_company_fund_transactions_risk_flags",
	} {
		if !strings.Contains(ddl, contract) {
			t.Errorf("migration DDL is missing contract %q", contract)
		}
	}
}

func TestCreateCompanyFundLedger_AccountsHasSingleWalletAddressAndProviderIdentityIndexes(t *testing.T) {
	accountsDDL := companyFundAccountsDDL(t)

	if count := strings.Count(accountsDDL, "wallet_address       VARCHAR(256)"); count != 1 {
		t.Fatalf("company_fund_accounts wallet_address declaration count = %d, want 1", count)
	}
	for _, contract := range []string{
		"normalized_address   VARCHAR(256)",
		"network_family       VARCHAR(64)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_accounts_safeheron_identity",
		"ON company_fund_accounts (channel, network_family, normalized_address)",
		"WHERE channel = 'SAFEHERON' AND normalized_address IS NOT NULL",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_accounts_airwallex_identity",
		"ON company_fund_accounts (channel, provider_account_key)",
		"WHERE channel = 'AIRWALLEX' AND provider_account_key IS NOT NULL",
	} {
		if !strings.Contains(accountsDDL, contract) {
			t.Errorf("company_fund_accounts DDL is missing identity contract %q", contract)
		}
	}
}

func TestCreateCompanyFundLedger_TransactionsHasSingleUSDValuationTime(t *testing.T) {
	transactionsDDL := companyFundTransactionsDDL(t)
	if count := strings.Count(transactionsDDL, "usd_valuation_time                      TIMESTAMPTZ"); count != 1 {
		t.Fatalf("company_fund_transactions usd_valuation_time declaration count = %d, want 1", count)
	}
}

func TestCreateCompanyFundLedger_ProviderRiskResultsRemainNullable(t *testing.T) {
	ddl := strings.Join(companyFundLedgerUpStatements(), "\n")

	for _, column := range []string{
		"is_source_phishing",
		"is_destination_phishing",
		"aml_lock",
	} {
		definition := ""
		for _, line := range strings.Split(ddl, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, column+" ") {
				definition = trimmed
				break
			}
		}
		if definition == "" {
			t.Errorf("migration DDL is missing %s", column)
			continue
		}
		if strings.Contains(definition, "NOT NULL") {
			t.Errorf("%s must remain nullable so an unknown provider result is not stored as false", column)
		}
		if strings.Contains(definition, "DEFAULT") {
			t.Errorf("%s must not default to false because false and unknown are distinct", column)
		}
	}
}

func TestCreateCompanyFundLedger_OwnedProviderPayloadPurgeContracts(t *testing.T) {
	eventDDL := companyFundProviderEventsDDL(t)

	for _, contract := range []string{
		"owned_payload_purged_at        TIMESTAMPTZ",
		"source_kind = 'OWNED_ENCRYPTED_PAYLOAD'",
		"owned_payload_digest IS NOT NULL",
		"owned_payload_key_version IS NOT NULL",
		"btrim(owned_payload_key_version) <> ''",
		"owned_payload_retention_until IS NOT NULL",
		"source_payload_digest = owned_payload_digest",
		"owned_payload_ciphertext IS NOT NULL",
		"owned_payload_purged_at IS NULL",
		"owned_payload_ciphertext IS NULL",
		"owned_payload_purged_at IS NOT NULL",
		"owned_payload_purged_at >= owned_payload_retention_until",
		"source_kind = 'EXISTING_SAFEHERON_WEBHOOK_REF'",
		"owned_payload_purged_at IS NULL",
		"idx_company_fund_provider_events_owned_payload_purge",
		"owned_payload_legal_hold = false",
		"idx_company_fund_provider_events_claim",
		"WHERE event_state IN ('PENDING', 'LEASED', 'FAILED')\n\t\tAND owned_payload_purged_at IS NULL",
	} {
		if !strings.Contains(eventDDL, contract) {
			t.Errorf("provider event DDL is missing owned-payload purge contract %q", contract)
		}
	}
}

func TestCreateCompanyFundLedger_RateAndValuationIntegrityContracts(t *testing.T) {
	ddl := strings.Join(companyFundLedgerUpStatements(), "\n")

	for _, contract := range []string{
		"rate                            NUMERIC(65, 18) NOT NULL CHECK (rate > 0)",
		"UNIQUE (id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)",
		"FOREIGN KEY (supersedes_snapshot_id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)",
		"REFERENCES company_fund_rate_snapshots (id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)",
		"company_fund_reject_rate_snapshot_mutation",
		"company_fund_rate_snapshots_append_only",
		"WITH RECURSIVE dependencies(id) AS",
		"UNION\n\t\t\tSELECT edge.id",
		"JOIN company_fund_rate_snapshots AS snapshot ON snapshot.id = dependencies.id",
		"CROSS JOIN LATERAL (VALUES",
		"(snapshot.supersedes_snapshot_id)",
		"(snapshot.numerator_snapshot_id)",
		"(snapshot.denominator_snapshot_id)",
		"OLD.is_eligible_leaf = true",
		"NEW.is_eligible_leaf = false",
		"to_jsonb(NEW) - ARRAY['is_eligible_leaf', 'updated_at']",
		"UNIQUE (id, provider)",
		"FOREIGN KEY (budget_period_id, provider)",
		"REFERENCES company_fund_rate_budget_periods (id, provider)",
		"FOREIGN KEY (originating_rate_request_id, provider)",
		"REFERENCES company_fund_rate_requests (id, provider)",
		"WHERE conrelid = 'company_fund_rate_snapshots'::regclass\n\t\t\tAND conname = 'fk_company_fund_rate_snapshots_originating_request'",
		"WHERE conrelid = 'company_fund_transactions'::regclass\n\t\t\tAND conname = 'fk_company_fund_transactions_usd_rate_snapshot'",
		"WHERE conrelid = 'company_fund_transactions'::regclass\n\t\t\tAND conname = 'fk_company_fund_transactions_current_valuation_history'",
		"request_state IN ('PENDING', 'LEASED', 'RETRY_WAIT', 'DISPATCHED')",
		"request_state <> 'RETRY_WAIT' OR not_before IS NOT NULL",
		"request_state <> 'DISPATCHED' OR dispatched_at IS NOT NULL",
		"request_state IN ('SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED') AND completed_at IS NOT NULL",
		"request_state NOT IN ('SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED') AND completed_at IS NULL",
		"FOREIGN KEY (transaction_id, supersedes_history_id)",
		"FOREIGN KEY (transaction_id, source_valuation_history_id)",
		"FOREIGN KEY (transaction_id, expected_current_history_id, expected_current_dependency_fingerprint)",
		"REFERENCES company_fund_transaction_valuation_history (transaction_id, id)",
		"REFERENCES company_fund_transaction_valuation_history (transaction_id, id, dependency_fingerprint)",
		"expected_current_state IN ('NONE', 'HISTORY')",
		"expected_current_history_id IS NULL AND expected_current_dependency_fingerprint IS NULL",
		"expected_current_state = 'HISTORY'",
		"job_state = 'RETRY_WAIT' AND next_attempt_at IS NOT NULL",
		"job_state IN ('SUCCEEDED', 'SUPERSEDED', 'FAILED') AND completed_at IS NOT NULL",
	} {
		if !strings.Contains(ddl, contract) {
			t.Errorf("migration DDL is missing rate/valuation integrity contract %q", contract)
		}
	}

	for _, forbidden := range []string{
		"UNIQUE (logical_request_key, attempt_no)",
		"ON company_fund_rate_requests (logical_request_key)",
		"rate                            NUMERIC(65, 18) NOT NULL CHECK (rate >= 0)",
		"supersedes_snapshot_id          BIGINT REFERENCES company_fund_rate_snapshots(id)",
		"budget_period_id                BIGINT NOT NULL REFERENCES company_fund_rate_budget_periods(id)",
		"FOREIGN KEY (originating_rate_request_id)\n\t\t\tREFERENCES company_fund_rate_requests(id)",
		"supersedes_history_id           BIGINT REFERENCES company_fund_transaction_valuation_history(id)",
		"source_valuation_history_id     BIGINT REFERENCES company_fund_transaction_valuation_history(id)",
		"UNION ALL\n\t\t\tSELECT edge.id",
	} {
		if strings.Contains(ddl, forbidden) {
			t.Errorf("migration DDL must not retain scalar/cross-series contract %q", forbidden)
		}
	}
	if count := strings.Count(ddl, "UNIQUE (id, provider)"); count < 2 {
		t.Errorf("rate budget periods and requests must each expose UNIQUE (id, provider), found %d", count)
	}
}

func TestCreateCompanyFundLedger_ProviderTransactionFactOwnershipContracts(t *testing.T) {
	factDDL := companyFundProviderTransactionFactsDDL(t)

	for _, contract := range []string{
		"provider_account_key            VARCHAR(128) NOT NULL",
		"source_provider_event_id        BIGINT NOT NULL REFERENCES company_fund_provider_events(id) ON DELETE RESTRICT",
		`CHECK (
		(allocation_state = 'PROVEN_DERIVABLE'
			AND derivation_contract_version IS NOT NULL
			AND btrim(derivation_contract_version) <> '')
		OR (allocation_state <> 'PROVEN_DERIVABLE'
			AND derivation_contract_version IS NULL)
	)`,
	} {
		if !strings.Contains(factDDL, contract) {
			t.Errorf("provider transaction fact DDL is missing contract %q", contract)
		}
	}
}

func TestCreateCompanyFundLedger_DownReversesFeatureObjects(t *testing.T) {
	down := strings.Join(companyFundLedgerDownStatements(), "\n")
	previous := -1
	for _, statement := range []string{
		"DROP TABLE IF EXISTS company_fund_valuation_jobs",
		"DROP TABLE IF EXISTS company_fund_transaction_valuation_history",
		"DROP TABLE IF EXISTS company_fund_rate_requests",
		"DROP TABLE IF EXISTS company_fund_rate_budget_periods",
		"DROP TABLE IF EXISTS company_fund_rate_snapshots",
		"DROP TABLE IF EXISTS company_fund_sync_runs",
		"DROP TABLE IF EXISTS company_fund_transactions",
		"DROP TABLE IF EXISTS company_fund_provider_transaction_facts",
		"DROP TABLE IF EXISTS company_fund_provider_events",
		"DROP TABLE IF EXISTS company_fund_safeheron_raw_event_exclusions",
		"DROP TABLE IF EXISTS finance_categories",
		"DROP TABLE IF EXISTS company_fund_account_asset_policies",
		"DROP TABLE IF EXISTS company_fund_accounts",
		"ALTER TABLE safeheron_webhook_events DROP COLUMN IF EXISTS payload_digest",
	} {
		position := strings.Index(down, statement)
		if position < 0 {
			t.Errorf("rollback is missing %q", statement)
			continue
		}
		if position <= previous {
			t.Errorf("rollback statement %q is not in reverse dependency order", statement)
		}
		previous = position
	}

	for _, statement := range []string{
		"DROP TRIGGER IF EXISTS company_fund_transactions_finance_category_hierarchy",
		"DROP TRIGGER IF EXISTS finance_categories_company_fund_hierarchy_guard",
		"DROP FUNCTION IF EXISTS company_fund_revalidate_finance_category_hierarchy()",
		"DROP FUNCTION IF EXISTS company_fund_validate_transaction_finance_categories()",
		"DROP FUNCTION IF EXISTS company_fund_assert_finance_category_assignment(BIGINT, BIGINT)",
	} {
		if !strings.Contains(down, statement) {
			t.Errorf("rollback is missing finance hierarchy cleanup %q", statement)
		}
	}
}

func TestCreateCompanyFundLedger_DownBlocksProductionBeforeDatabaseUse(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	err := (&CreateCompanyFundLedger{}).Down(nil)
	if err == nil {
		t.Fatal("Down(nil) in production must be blocked before database access")
	}
	if !strings.Contains(err.Error(), "BLOCKED") {
		t.Fatalf("Down(nil) production error = %q, want BLOCKED safeguard", err)
	}
}

func companyFundProviderTransactionFactsDDL(t *testing.T) string {
	t.Helper()
	for _, statement := range companyFundLedgerUpStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS company_fund_provider_transaction_facts") {
			return statement
		}
	}
	t.Fatal("migration DDL does not create company_fund_provider_transaction_facts")
	return ""
}

func companyFundAccountsDDL(t *testing.T) string {
	t.Helper()
	for _, statement := range companyFundLedgerUpStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS company_fund_accounts") {
			return statement
		}
	}
	t.Fatal("migration DDL does not create company_fund_accounts")
	return ""
}

func companyFundTransactionsDDL(t *testing.T) string {
	t.Helper()
	for _, statement := range companyFundLedgerUpStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS company_fund_transactions") {
			return statement
		}
	}
	t.Fatal("migration DDL does not create company_fund_transactions")
	return ""
}

func companyFundProviderEventsDDL(t *testing.T) string {
	t.Helper()
	for _, statement := range companyFundLedgerUpStatements() {
		if strings.Contains(statement, "CREATE TABLE IF NOT EXISTS company_fund_provider_events") {
			return statement
		}
	}
	t.Fatal("migration DDL does not create company_fund_provider_events")
	return ""
}
