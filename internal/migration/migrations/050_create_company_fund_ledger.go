package migrations

import (
	"database/sql"
	"fmt"
	"os"

	"monera-digital/internal/migration"
)

// CreateCompanyFundLedger creates the isolated company-fund ingestion ledger.
// Its complete schema is added in this migration rather than coupling finance
// reporting to customer deposit processing tables.
type CreateCompanyFundLedger struct{}

func (m *CreateCompanyFundLedger) Version() string { return "050" }

func (m *CreateCompanyFundLedger) Description() string {
	return "Create isolated company-fund transaction ingestion, valuation, and audit ledger"
}

func (m *CreateCompanyFundLedger) Up(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin company-fund ledger migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for i, statement := range companyFundLedgerUpStatements() {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("company-fund ledger up statement %d: %w", i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit company-fund ledger migration: %w", err)
	}
	committed = true
	return nil
}

func (m *CreateCompanyFundLedger) Down(db *sql.DB) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("BLOCKED: rollback of migration 050 in production would destroy company-fund audit data; use a manual migration instead")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin company-fund ledger rollback: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for i, statement := range companyFundLedgerDownStatements() {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("company-fund ledger down statement %d: %w", i+1, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit company-fund ledger rollback: %w", err)
	}
	committed = true
	return nil
}

var _ migration.Migration = (*CreateCompanyFundLedger)(nil)

// companyFundLedgerUpStatements deliberately keeps all feature DDL inside one
// transaction. PostgreSQL transactional DDL means a failed migration leaves no
// partially-created ledger schema behind. Statements are idempotent so a prior
// DDL commit without the migrator tracking record can be retried safely.
func companyFundLedgerUpStatements() []string {
	return []string{
		`
ALTER TABLE safeheron_webhook_events
		ADD COLUMN IF NOT EXISTS payload_digest VARCHAR(64);

DO $$
BEGIN
	-- event_id remains compatibility-tolerant during rollout because the live
	-- pre-050 handler still emits its legacy txKey:status identity. Phase 6
	-- switches new rows to the SHA-256 identity; payload_digest is validated now.
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conname = 'chk_safeheron_webhook_events_payload_digest_sha256'
	) THEN
		ALTER TABLE safeheron_webhook_events
			ADD CONSTRAINT chk_safeheron_webhook_events_payload_digest_sha256
			CHECK (payload_digest IS NULL OR payload_digest ~ '^[0-9a-f]{64}$');
	END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_safeheron_webhook_events_payload_digest
	ON safeheron_webhook_events (payload_digest)
	WHERE payload_digest IS NOT NULL;
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_accounts (
	id                   BIGSERIAL PRIMARY KEY,
	channel              VARCHAR(16) NOT NULL CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
	provider_account_key VARCHAR(128),
	wallet_address       VARCHAR(256),
	normalized_address   VARCHAR(256),
	network_family       VARCHAR(64),
	company_entity       VARCHAR(256),
	fund_account_name    VARCHAR(256),
	sub_account_name     VARCHAR(256),
	account_type         VARCHAR(64),
	account_name         VARCHAR(256) NOT NULL,
	account_role         VARCHAR(64),
	remark               TEXT,
	credential_ref       VARCHAR(256),
	is_enabled           BOOLEAN NOT NULL DEFAULT true,
	created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
	CHECK (
		(channel = 'SAFEHERON' AND normalized_address IS NOT NULL AND network_family IS NOT NULL)
		OR (channel = 'AIRWALLEX' AND provider_account_key IS NOT NULL)
	)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_accounts_safeheron_identity
	ON company_fund_accounts (channel, network_family, normalized_address)
	WHERE channel = 'SAFEHERON' AND normalized_address IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_accounts_airwallex_identity
	ON company_fund_accounts (channel, provider_account_key)
	WHERE channel = 'AIRWALLEX' AND provider_account_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_accounts_enabled_channel
	ON company_fund_accounts (channel, is_enabled);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_account_asset_policies (
	id                                  BIGSERIAL PRIMARY KEY,
	company_fund_account_id             BIGINT NOT NULL REFERENCES company_fund_accounts(id) ON DELETE RESTRICT,
	currency                            VARCHAR(64) NOT NULL,
	chain_code                          VARCHAR(64),
	provider_asset_key                  VARCHAR(256),
	asset_contract                      VARCHAR(256),
	dust_detection_enabled              BOOLEAN NOT NULL DEFAULT false,
	dust_threshold                      NUMERIC(65, 18),
	auto_exclude_dust_from_summary      BOOLEAN NOT NULL DEFAULT true,
	valuation_provider_asset_id         VARCHAR(256),
	valuation_provider_platform_id      VARCHAR(256),
	valuation_contract_address          VARCHAR(256),
	is_enabled                          BOOLEAN NOT NULL DEFAULT true,
	created_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
	CHECK (
		(dust_detection_enabled AND dust_threshold IS NOT NULL AND dust_threshold >= 0)
		OR (NOT dust_detection_enabled AND dust_threshold IS NULL)
	)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_account_asset_policies_identity
	ON company_fund_account_asset_policies
	(company_fund_account_id, currency, COALESCE(chain_code, ''));

CREATE INDEX IF NOT EXISTS idx_company_fund_account_asset_policies_enabled
	ON company_fund_account_asset_policies (company_fund_account_id, is_enabled);
`,
		`
COMMENT ON COLUMN company_fund_account_asset_policies.valuation_provider_asset_id IS
	'Explicit CoinGecko asset mapping: coin ID, or fiat:<CODE> (for example fiat:JPY) only when currency equals CODE; no symbol inference.';
`,
		`
CREATE TABLE IF NOT EXISTS finance_categories (
	id           BIGSERIAL PRIMARY KEY,
	level        SMALLINT NOT NULL CHECK (level IN (1, 2)),
	parent_id    BIGINT,
	parent_level SMALLINT NOT NULL DEFAULT 1 CHECK (parent_level = 1),
	code         VARCHAR(128) NOT NULL UNIQUE,
	name         VARCHAR(256) NOT NULL,
	is_enabled   BOOLEAN NOT NULL DEFAULT true,
	display_order INT NOT NULL DEFAULT 0,
	created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (id, level),
	FOREIGN KEY (parent_id, parent_level)
		REFERENCES finance_categories(id, level)
		DEFERRABLE INITIALLY DEFERRED,
	CHECK (
		(level = 1 AND parent_id IS NULL)
		OR (level = 2 AND parent_id IS NOT NULL)
	)
);

CREATE INDEX IF NOT EXISTS idx_finance_categories_parent_display
	ON finance_categories (parent_id, display_order, name);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_provider_events (
	id                             BIGSERIAL PRIMARY KEY,
	channel                        VARCHAR(16) NOT NULL CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
	provider_event_id              VARCHAR(256) NOT NULL,
	event_type                     VARCHAR(128) NOT NULL,
	provider_event_version          VARCHAR(64),
	provider_org_key               VARCHAR(128),
	provider_account_key           VARCHAR(128),
	source_kind                    VARCHAR(48) NOT NULL,
	safeheron_webhook_event_id     INTEGER REFERENCES safeheron_webhook_events(id) ON DELETE RESTRICT,
	source_payload_digest          VARCHAR(64) NOT NULL CHECK (source_payload_digest ~ '^[0-9a-f]{64}$'),
	owned_payload_ciphertext       BYTEA,
	owned_payload_digest           VARCHAR(64) CHECK (owned_payload_digest IS NULL OR owned_payload_digest ~ '^[0-9a-f]{64}$'),
	owned_payload_key_version      VARCHAR(64),
	owned_payload_retention_until  TIMESTAMPTZ,
	owned_payload_purged_at        TIMESTAMPTZ,
	owned_payload_legal_hold       BOOLEAN NOT NULL DEFAULT false,
	event_state                    VARCHAR(16) NOT NULL DEFAULT 'PENDING'
		CHECK (event_state IN ('PENDING', 'LEASED', 'PROCESSED', 'FAILED', 'IGNORED', 'DEAD_LETTER')),
	attempt_count                  INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at                TIMESTAMPTZ,
	lease_owner                    VARCHAR(128),
	lease_expires_at               TIMESTAMPTZ,
	last_error                     TEXT,
	received_at                    TIMESTAMPTZ NOT NULL DEFAULT now(),
	processed_at                   TIMESTAMPTZ,
	created_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (channel, provider_event_id),
	CHECK (
		(source_kind = 'OWNED_ENCRYPTED_PAYLOAD'
			AND safeheron_webhook_event_id IS NULL
			AND owned_payload_digest IS NOT NULL
			AND owned_payload_key_version IS NOT NULL
			AND btrim(owned_payload_key_version) <> ''
			AND owned_payload_retention_until IS NOT NULL
			AND source_payload_digest = owned_payload_digest
			AND (
				(owned_payload_ciphertext IS NOT NULL
					AND octet_length(owned_payload_ciphertext) > 0
					AND octet_length(owned_payload_ciphertext) <= 1048576
					AND owned_payload_purged_at IS NULL)
				OR (owned_payload_ciphertext IS NULL
					AND owned_payload_purged_at IS NOT NULL
					AND owned_payload_purged_at >= owned_payload_retention_until)
			))
		OR
		(source_kind = 'EXISTING_SAFEHERON_WEBHOOK_REF'
			AND channel = 'SAFEHERON'
			AND safeheron_webhook_event_id IS NOT NULL
			AND owned_payload_ciphertext IS NULL
			AND owned_payload_digest IS NULL
			AND owned_payload_key_version IS NULL
			AND owned_payload_retention_until IS NULL
			AND owned_payload_purged_at IS NULL
			AND owned_payload_legal_hold = false)
	),
	CHECK (
		(event_state = 'FAILED' AND next_attempt_at IS NOT NULL)
		OR (event_state <> 'FAILED' AND next_attempt_at IS NULL)
	),
	CHECK (
		(event_state IN ('PROCESSED', 'IGNORED', 'DEAD_LETTER') AND processed_at IS NOT NULL)
		OR (event_state NOT IN ('PROCESSED', 'IGNORED', 'DEAD_LETTER') AND processed_at IS NULL)
	),
	CHECK (
		(event_state = 'LEASED' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
		OR (event_state <> 'LEASED' AND lease_owner IS NULL AND lease_expires_at IS NULL)
	)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_provider_events_safeheron_webhook
	ON company_fund_provider_events (safeheron_webhook_event_id)
	WHERE safeheron_webhook_event_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_provider_events_claim
	ON company_fund_provider_events (event_state, next_attempt_at, lease_expires_at, received_at)
	WHERE event_state IN ('PENDING', 'LEASED', 'FAILED')
		AND owned_payload_purged_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_provider_events_owned_payload_purge
	ON company_fund_provider_events (owned_payload_retention_until, id)
	WHERE source_kind = 'OWNED_ENCRYPTED_PAYLOAD'
		AND owned_payload_ciphertext IS NOT NULL
		AND owned_payload_legal_hold = false
		AND owned_payload_retention_until IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_provider_events_account_time
	ON company_fund_provider_events (channel, provider_account_key, received_at DESC);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_safeheron_raw_event_exclusions (
	safeheron_webhook_event_id INTEGER PRIMARY KEY
		REFERENCES safeheron_webhook_events(id) ON DELETE RESTRICT,
	source_payload_digest     VARCHAR(64) NOT NULL
		CHECK (source_payload_digest ~ '^[0-9a-f]{64}$'),
	exclusion_reason          VARCHAR(64) NOT NULL
		CHECK (exclusion_reason IN (
			'NON_TRANSACTION_STATUS',
			'INVALID_PAYLOAD',
			'EVENT_TYPE_MISMATCH',
			'UNMAPPED_ASSET',
			'NO_CONFIGURED_ADDRESS'
		)),
	configuration_fingerprint VARCHAR(64)
		CHECK (configuration_fingerprint IS NULL OR configuration_fingerprint ~ '^[0-9a-f]{64}$'),
	CHECK (
		(exclusion_reason IN ('UNMAPPED_ASSET', 'NO_CONFIGURED_ADDRESS')
			AND configuration_fingerprint IS NOT NULL)
		OR (exclusion_reason IN ('NON_TRANSACTION_STATUS', 'INVALID_PAYLOAD', 'EVENT_TYPE_MISMATCH')
			AND configuration_fingerprint IS NULL)
	),
	created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE company_fund_safeheron_raw_event_exclusions IS
	'Negative eligibility markers for verified Safeheron raw events; immutable reasons are permanent while settings-dependent reasons bind to a stable configuration fingerprint; never modifies deposit process_status.';
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_provider_transaction_facts (
	id                              BIGSERIAL PRIMARY KEY,
	channel                         VARCHAR(16) NOT NULL CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
	provider_account_key            VARCHAR(128) NOT NULL,
	provider_transaction_id         VARCHAR(256),
	provider_group_id               VARCHAR(256),
	fact_identity_key               VARCHAR(256) NOT NULL,
	fact_version                    INTEGER NOT NULL DEFAULT 1 CHECK (fact_version > 0),
	source_provider_event_id        BIGINT NOT NULL REFERENCES company_fund_provider_events(id) ON DELETE RESTRICT,
	source_payload_digest           VARCHAR(64) NOT NULL CHECK (source_payload_digest ~ '^[0-9a-f]{64}$'),
	provider_occurred_at            TIMESTAMPTZ,
	provider_amount                 NUMERIC(65, 18),
	provider_currency               VARCHAR(64),
	provider_reported_usd_value     NUMERIC(65, 18),
	conversion_from_currency        VARCHAR(64),
	conversion_to_currency          VARCHAR(64),
	conversion_rate                 NUMERIC(65, 18),
	conversion_buy_amount           NUMERIC(65, 18),
	conversion_sell_amount          NUMERIC(65, 18),
	value_scope                     VARCHAR(32) NOT NULL
		CHECK (value_scope IN ('TRANSACTION_TOTAL', 'DIRECT_ITEM', 'CONVERSION_GROUP')),
	allocation_state                VARCHAR(32) NOT NULL
		CHECK (allocation_state IN ('NOT_APPLICABLE', 'UNPROVEN', 'PROVEN_DERIVABLE')),
	derivation_contract_version     VARCHAR(64),
	provider_extras                 JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (channel, fact_identity_key, fact_version),
	CHECK (
		(allocation_state = 'PROVEN_DERIVABLE'
			AND derivation_contract_version IS NOT NULL
			AND btrim(derivation_contract_version) <> '')
		OR (allocation_state <> 'PROVEN_DERIVABLE'
			AND derivation_contract_version IS NULL)
	)
);

CREATE INDEX IF NOT EXISTS idx_company_fund_provider_facts_transaction
	ON company_fund_provider_transaction_facts (channel, provider_transaction_id, provider_account_key);

CREATE INDEX IF NOT EXISTS idx_company_fund_provider_facts_source_event
	ON company_fund_provider_transaction_facts (source_provider_event_id);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_transactions (
	id                                      BIGSERIAL PRIMARY KEY,
	channel                                 VARCHAR(16) NOT NULL CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
	provider_account_key                    VARCHAR(128),
	provider_transaction_id                 VARCHAR(256),
	provider_event_id                       VARCHAR(256),
	provider_movement_id                    VARCHAR(256),
	movement_index                          INTEGER NOT NULL DEFAULT 0 CHECK (movement_index >= 0),
	movement_key                            VARCHAR(256) NOT NULL UNIQUE,
	identity_algorithm_version              VARCHAR(64) NOT NULL,
	provider_transaction_fact_id            BIGINT REFERENCES company_fund_provider_transaction_facts(id) ON DELETE RESTRICT,
	parent_transaction_id                   BIGINT REFERENCES company_fund_transactions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
	reversal_of_transaction_id              BIGINT REFERENCES company_fund_transactions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
	conversion_pair_transaction_id          BIGINT REFERENCES company_fund_transactions(id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
	conversion_group_key                    VARCHAR(256),
	conversion_leg                          VARCHAR(8) CHECK (conversion_leg IS NULL OR conversion_leg IN ('BUY', 'SELL')),
	conversion_group_status                 VARCHAR(16) CHECK (conversion_group_status IS NULL OR conversion_group_status IN ('COMPLETE', 'INCOMPLETE')),
	conversion_reason_code                  VARCHAR(64),
	movement_kind                           VARCHAR(16) NOT NULL CHECK (movement_kind IN ('PRINCIPAL', 'FEE', 'REVERSAL', 'ADJUSTMENT', 'CONVERSION')),
	transfer_mode                           VARCHAR(16) NOT NULL DEFAULT 'SINGLE' CHECK (transfer_mode IN ('SINGLE', 'BATCH')),
	transaction_direction                   VARCHAR(24) NOT NULL CHECK (transaction_direction IN ('INFLOW', 'OUTFLOW', 'INTERNAL_TRANSFER')),
	from_company_fund_account_id            BIGINT REFERENCES company_fund_accounts(id) ON DELETE RESTRICT,
	to_company_fund_account_id              BIGINT REFERENCES company_fund_accounts(id) ON DELETE RESTRICT,
	from_address_or_account                 VARCHAR(512),
	to_address_or_account                   VARCHAR(512),
	payer_name                              VARCHAR(256),
	payee_name                              VARCHAR(256),
	from_company_entity_snapshot            VARCHAR(256),
	from_fund_account_name_snapshot         VARCHAR(256),
	from_sub_account_name_snapshot          VARCHAR(256),
	from_account_type_snapshot              VARCHAR(64),
	to_company_entity_snapshot              VARCHAR(256),
	to_fund_account_name_snapshot           VARCHAR(256),
	to_sub_account_name_snapshot            VARCHAR(256),
	to_account_type_snapshot                VARCHAR(64),
	currency                                VARCHAR(64) NOT NULL,
	chain_code                              VARCHAR(64),
	coin_chain_id                           VARCHAR(128),
	provider_asset_key                      VARCHAR(256),
	asset_contract                          VARCHAR(256),
	amount                                  NUMERIC(65, 18) NOT NULL CHECK (amount >= 0),
	provider_reported_fee_amount            NUMERIC(65, 18) CHECK (provider_reported_fee_amount IS NULL OR provider_reported_fee_amount >= 0),
	provider_reported_fee_currency          VARCHAR(64),
	fee_details                             JSONB NOT NULL DEFAULT '{}'::jsonb,
	tx_hash                                 VARCHAR(256),
	provider_transaction_type               VARCHAR(64),
	provider_status                         VARCHAR(64),
	provider_sub_status                     VARCHAR(64),
	provider_status_version                 BIGINT CHECK (provider_status_version IS NULL OR provider_status_version >= 0),
	provider_updated_at                     TIMESTAMPTZ,
	provider_fact_source                    VARCHAR(24) NOT NULL CHECK (provider_fact_source IN ('WEBHOOK', 'PRODUCT_DETAIL', 'RECONCILIATION')),
	status_rank                             SMALLINT NOT NULL DEFAULT 0 CHECK (status_rank >= 0),
	occurred_at                             TIMESTAMPTZ,
	completed_at                            TIMESTAMPTZ,
	provider_created_at                     TIMESTAMPTZ,
	block_height                            BIGINT,
	block_hash                              VARCHAR(256),
	first_seen_source                       VARCHAR(16) NOT NULL CHECK (first_seen_source IN ('WEBHOOK', 'RECONCILIATION')),
	last_seen_source                        VARCHAR(16) NOT NULL CHECK (last_seen_source IN ('WEBHOOK', 'RECONCILIATION')),
	latest_provider_event_id                BIGINT REFERENCES company_fund_provider_events(id) ON DELETE RESTRICT,
	raw_snapshot_digest                     VARCHAR(64) CHECK (raw_snapshot_digest IS NULL OR raw_snapshot_digest ~ '^[0-9a-f]{64}$'),
	provider_extras                         JSONB NOT NULL DEFAULT '{}'::jsonb,
	provider_reported_usd_value             NUMERIC(65, 18) CHECK (provider_reported_usd_value IS NULL OR provider_reported_usd_value >= 0),
	calculated_usd_value                    NUMERIC(65, 18) CHECK (calculated_usd_value IS NULL OR calculated_usd_value >= 0),
	usd_value                               NUMERIC(65, 18) CHECK (usd_value IS NULL OR usd_value >= 0),
	usd_unit_price                          NUMERIC(65, 18) CHECK (usd_unit_price IS NULL OR usd_unit_price >= 0),
	usd_valuation_status                    VARCHAR(16) CHECK (usd_valuation_status IS NULL OR usd_valuation_status IN ('PROVISIONAL', 'FINAL', 'UNPRICED', 'STALE')),
	usd_valuation_reason_code               VARCHAR(64),
	usd_valuation_basis                     VARCHAR(24) CHECK (usd_valuation_basis IS NULL OR usd_valuation_basis IN ('TRANSACTION_TIME', 'INGESTION_TIME')),
	usd_valuation_time                      TIMESTAMPTZ,
	usd_valuation_price_at                  TIMESTAMPTZ,
	usd_valuation_source                    VARCHAR(32) CHECK (usd_valuation_source IS NULL OR usd_valuation_source IN ('SAFEHERON', 'AIRWALLEX', 'COINGECKO', 'USD_PAR')),
	usd_valuation_method                    VARCHAR(64),
	usd_valuation_granularity               VARCHAR(16),
	usd_provider_value_scope                VARCHAR(24) CHECK (usd_provider_value_scope IS NULL OR usd_provider_value_scope IN ('TRANSACTION_TOTAL', 'DIRECT_ITEM', 'CONVERSION_GROUP')),
	usd_derivation_method                   VARCHAR(32) CHECK (usd_derivation_method IS NULL OR usd_derivation_method IN ('DIRECT_ITEM', 'DERIVED_FROM_PARENT', 'MARKET_PRICE')),
	usd_rate_snapshot_id                    BIGINT,
	current_valuation_history_id            BIGINT,
	usd_valued_at                           TIMESTAMPTZ,
	usd_valuation_policy_version            VARCHAR(64),
	usd_valuation_version                   BIGINT CHECK (usd_valuation_version IS NULL OR usd_valuation_version >= 0),
	finance_category_level1_id              BIGINT REFERENCES finance_categories(id) ON DELETE RESTRICT,
	finance_category_level2_id              BIGINT REFERENCES finance_categories(id) ON DELETE RESTRICT,
	is_operating_income_expense              BOOLEAN,
	applicant                               VARCHAR(256),
	business_description                    TEXT,
	classification_status                   VARCHAR(32) NOT NULL DEFAULT 'UNCLASSIFIED',
	classification_updated_by               VARCHAR(256),
	classification_updated_at               TIMESTAMPTZ,
	risk_status                             VARCHAR(32) NOT NULL DEFAULT 'UNREVIEWED',
	risk_reason_code                        VARCHAR(64),
	risk_override_reason                    TEXT,
	risk_override_by                        VARCHAR(256),
	risk_override_at                        TIMESTAMPTZ,
	is_dust                                 BOOLEAN NOT NULL DEFAULT false,
	auto_excluded_from_summary              BOOLEAN NOT NULL DEFAULT false,
	summary_inclusion_override              BOOLEAN,
	dust_policy_id                          BIGINT REFERENCES company_fund_account_asset_policies(id) ON DELETE RESTRICT,
	dust_threshold                          NUMERIC(65, 18),
	is_source_phishing                      BOOLEAN,
	is_destination_phishing                 BOOLEAN,
	is_unrecognized_asset                   BOOLEAN NOT NULL DEFAULT false,
	aml_lock                                BOOLEAN,
	aml_screening_state                     VARCHAR(32) NOT NULL DEFAULT 'NOT_SCREENED'
		CHECK (aml_screening_state IN ('NOT_SCREENED', 'PENDING', 'SCREENED', 'REVIEW_REQUIRED', 'CLEARED', 'BLOCKED', 'ERROR')),
	aml_risk_level                          VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN'
		CHECK (aml_risk_level IN ('UNKNOWN', 'LOW', 'MEDIUM', 'HIGH', 'CRITICAL')),
	risk_flags                              JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(risk_flags) = 'array'),
	first_seen_at                           TIMESTAMPTZ NOT NULL DEFAULT now(),
	last_synced_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),
	created_at                              TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                              TIMESTAMPTZ NOT NULL DEFAULT now(),
	CHECK (from_company_fund_account_id IS NOT NULL OR to_company_fund_account_id IS NOT NULL),
	CHECK (
		(latest_provider_event_id IS NULL AND raw_snapshot_digest IS NULL)
		OR (latest_provider_event_id IS NOT NULL AND raw_snapshot_digest IS NOT NULL)
	),
	CHECK (finance_category_level2_id IS NULL OR finance_category_level1_id IS NOT NULL),
	CHECK (jsonb_typeof(fee_details) = 'object'),
	CHECK (block_height IS NULL OR block_height >= 0),
	CHECK (dust_threshold IS NULL OR dust_threshold >= 0),
	CHECK (NOT is_dust OR (dust_policy_id IS NOT NULL AND dust_threshold IS NOT NULL)),
	CHECK (parent_transaction_id IS NULL OR parent_transaction_id <> id),
	CHECK (reversal_of_transaction_id IS NULL OR reversal_of_transaction_id <> id),
	CHECK (conversion_pair_transaction_id IS NULL OR conversion_pair_transaction_id <> id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_transactions_provider_movement_identity
	ON company_fund_transactions (channel, provider_account_key, provider_transaction_id, provider_movement_id)
	WHERE provider_transaction_id IS NOT NULL AND provider_movement_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_provider_fact
	ON company_fund_transactions (provider_transaction_fact_id);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_from_account_time
	ON company_fund_transactions (from_company_fund_account_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_to_account_time
	ON company_fund_transactions (to_company_fund_account_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_tx_hash
	ON company_fund_transactions (channel, tx_hash)
	WHERE tx_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_report_filter
	ON company_fund_transactions (channel, occurred_at DESC, transaction_direction, movement_kind);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_category_filter
	ON company_fund_transactions (finance_category_level1_id, finance_category_level2_id, is_operating_income_expense);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_parent_links
	ON company_fund_transactions (parent_transaction_id, reversal_of_transaction_id, conversion_pair_transaction_id);

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_current_valuation
	ON company_fund_transactions (current_valuation_history_id, usd_rate_snapshot_id)
	WHERE current_valuation_history_id IS NOT NULL OR usd_rate_snapshot_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_aml_filter
	ON company_fund_transactions (aml_lock, aml_screening_state, aml_risk_level, occurred_at DESC)
	WHERE aml_lock OR is_source_phishing OR is_destination_phishing OR is_unrecognized_asset;

CREATE INDEX IF NOT EXISTS idx_company_fund_transactions_risk_flags
	ON company_fund_transactions USING GIN (risk_flags);
`,
		`
CREATE OR REPLACE FUNCTION company_fund_assert_finance_category_assignment(
	p_level1_id BIGINT,
	p_level2_id BIGINT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
	level1_level SMALLINT;
	level2_level SMALLINT;
	level2_parent_id BIGINT;
BEGIN
	IF p_level1_id IS NULL AND p_level2_id IS NULL THEN
		RETURN;
	END IF;

	IF p_level1_id IS NULL THEN
		RAISE EXCEPTION 'finance category level 2 requires a level 1 category';
	END IF;

	SELECT level INTO level1_level
	FROM finance_categories
	WHERE id = p_level1_id;
	IF NOT FOUND OR level1_level <> 1 THEN
		RAISE EXCEPTION 'finance_category_level1_id must reference a level 1 finance category';
	END IF;

	IF p_level2_id IS NULL THEN
		RETURN;
	END IF;

	SELECT level, parent_id INTO level2_level, level2_parent_id
	FROM finance_categories
	WHERE id = p_level2_id;
	IF NOT FOUND OR level2_level <> 2 OR level2_parent_id IS DISTINCT FROM p_level1_id THEN
		RAISE EXCEPTION 'finance_category_level2_id must reference a level 2 child of finance_category_level1_id';
	END IF;
END;
$$;

CREATE OR REPLACE FUNCTION company_fund_validate_transaction_finance_categories()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	PERFORM company_fund_assert_finance_category_assignment(
		NEW.finance_category_level1_id,
		NEW.finance_category_level2_id
	);
	RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION company_fund_revalidate_finance_category_hierarchy()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM company_fund_transactions AS transaction
		LEFT JOIN finance_categories AS level1
			ON level1.id = transaction.finance_category_level1_id
		LEFT JOIN finance_categories AS level2
			ON level2.id = transaction.finance_category_level2_id
		WHERE (transaction.finance_category_level1_id IS NULL AND transaction.finance_category_level2_id IS NOT NULL)
			OR (transaction.finance_category_level1_id IS NOT NULL AND (level1.id IS NULL OR level1.level <> 1))
			OR (transaction.finance_category_level2_id IS NOT NULL AND (
				level2.id IS NULL
				OR level2.level <> 2
				OR level2.parent_id IS DISTINCT FROM transaction.finance_category_level1_id
			))
	) THEN
		RAISE EXCEPTION 'finance category hierarchy update would invalidate company-fund transaction classifications';
	END IF;
	RETURN NEW;
END;
$$;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = 'company_fund_transactions'::regclass
			AND tgname = 'company_fund_transactions_finance_category_hierarchy'
			AND NOT tgisinternal
	) THEN
		EXECUTE 'CREATE CONSTRAINT TRIGGER company_fund_transactions_finance_category_hierarchy
			AFTER INSERT OR UPDATE ON company_fund_transactions
			DEFERRABLE INITIALLY DEFERRED
			FOR EACH ROW EXECUTE FUNCTION company_fund_validate_transaction_finance_categories()';
	END IF;

	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = 'finance_categories'::regclass
			AND tgname = 'finance_categories_company_fund_hierarchy_guard'
			AND NOT tgisinternal
	) THEN
		EXECUTE 'CREATE CONSTRAINT TRIGGER finance_categories_company_fund_hierarchy_guard
			AFTER UPDATE OF level, parent_id ON finance_categories
			DEFERRABLE INITIALLY DEFERRED
			FOR EACH ROW EXECUTE FUNCTION company_fund_revalidate_finance_category_hierarchy()';
	END IF;
END
$$;
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_sync_runs (
	id                 BIGSERIAL PRIMARY KEY,
	channel            VARCHAR(16) NOT NULL CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
	sync_kind          VARCHAR(32) NOT NULL,
	window_key         VARCHAR(128) NOT NULL,
	window_start       TIMESTAMPTZ NOT NULL,
	window_end         TIMESTAMPTZ NOT NULL,
	status             VARCHAR(16) NOT NULL DEFAULT 'PENDING'
		CHECK (status IN ('PENDING', 'LEASED', 'SUCCEEDED', 'FAILED', 'PARTIAL', 'SKIPPED')),
	checkpoint         JSONB NOT NULL DEFAULT '{}'::jsonb,
	candidates_seen    INTEGER NOT NULL DEFAULT 0 CHECK (candidates_seen >= 0),
	events_created     INTEGER NOT NULL DEFAULT 0 CHECK (events_created >= 0),
	transactions_upserted INTEGER NOT NULL DEFAULT 0 CHECK (transactions_upserted >= 0),
	transactions_skipped  INTEGER NOT NULL DEFAULT 0 CHECK (transactions_skipped >= 0),
	attempt_count      INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at    TIMESTAMPTZ,
	lease_owner        VARCHAR(128),
	lease_expires_at   TIMESTAMPTZ,
	started_at         TIMESTAMPTZ,
	completed_at       TIMESTAMPTZ,
	last_error         TEXT,
	created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (channel, sync_kind, window_key),
	CHECK (window_end > window_start),
	CHECK (
		(status IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NOT NULL)
		OR (status NOT IN ('FAILED', 'PARTIAL') AND next_attempt_at IS NULL)
	),
	CHECK (
		(status = 'LEASED' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
		OR (status <> 'LEASED' AND lease_owner IS NULL AND lease_expires_at IS NULL)
	)
);

CREATE INDEX IF NOT EXISTS idx_company_fund_sync_runs_claim
	ON company_fund_sync_runs (status, next_attempt_at, lease_expires_at, window_start)
	WHERE status IN ('PENDING', 'LEASED', 'FAILED', 'PARTIAL');

CREATE INDEX IF NOT EXISTS idx_company_fund_sync_runs_window
	ON company_fund_sync_runs (channel, sync_kind, window_start DESC, window_end DESC);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_rate_snapshots (
	id                              BIGSERIAL PRIMARY KEY,
	provider                        VARCHAR(64) NOT NULL,
	asset_identity_key              VARCHAR(512) NOT NULL,
	provider_asset_id               VARCHAR(256),
	provider_platform_id            VARCHAR(256),
	asset_contract                  VARCHAR(256),
	base_currency                   VARCHAR(64) NOT NULL,
	quote_currency                  VARCHAR(64) NOT NULL DEFAULT 'USD',
	rate                            NUMERIC(65, 18) NOT NULL CHECK (rate > 0),
	method                          VARCHAR(64) NOT NULL,
	granularity                     VARCHAR(16) NOT NULL,
	bucket_start                    TIMESTAMPTZ NOT NULL,
	effective_at                    TIMESTAMPTZ,
	available_at                    TIMESTAMPTZ NOT NULL,
	fetched_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	cutoff_at                       TIMESTAMPTZ,
	snapshot_group_id               VARCHAR(256),
	policy_version                  VARCHAR(64) NOT NULL,
	provider_revision               VARCHAR(128),
	internal_revision               INTEGER NOT NULL DEFAULT 1 CHECK (internal_revision > 0),
	supersedes_snapshot_id          BIGINT,
	numerator_snapshot_id           BIGINT REFERENCES company_fund_rate_snapshots(id) ON DELETE RESTRICT,
	denominator_snapshot_id         BIGINT REFERENCES company_fund_rate_snapshots(id) ON DELETE RESTRICT,
	source_provider_fact_id         BIGINT REFERENCES company_fund_provider_transaction_facts(id) ON DELETE RESTRICT,
	source_payload_digest           VARCHAR(64) NOT NULL CHECK (source_payload_digest ~ '^[0-9a-f]{64}$'),
	is_eligible_leaf                BOOLEAN NOT NULL DEFAULT true,
	is_final                        BOOLEAN NOT NULL DEFAULT false,
	created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version, internal_revision),
	UNIQUE (provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version, source_payload_digest),
	UNIQUE (id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version),
	FOREIGN KEY (supersedes_snapshot_id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)
		REFERENCES company_fund_rate_snapshots (id, provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)
		ON DELETE RESTRICT,
	CHECK (supersedes_snapshot_id IS NULL OR supersedes_snapshot_id <> id),
	CHECK (numerator_snapshot_id IS NULL OR numerator_snapshot_id <> id),
	CHECK (denominator_snapshot_id IS NULL OR denominator_snapshot_id <> id),
	CHECK (method <> 'COINGECKO_BTC_CROSS' OR (numerator_snapshot_id IS NOT NULL AND denominator_snapshot_id IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_eligible_leaf
	ON company_fund_rate_snapshots
	(provider, asset_identity_key, quote_currency, method, granularity, bucket_start, policy_version)
	WHERE is_eligible_leaf;

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_lookup
	ON company_fund_rate_snapshots (provider, asset_identity_key, quote_currency, bucket_start DESC, available_at DESC);

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_supersedes
	ON company_fund_rate_snapshots (supersedes_snapshot_id)
	WHERE supersedes_snapshot_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_numerator
	ON company_fund_rate_snapshots (numerator_snapshot_id)
	WHERE numerator_snapshot_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_denominator
	ON company_fund_rate_snapshots (denominator_snapshot_id)
	WHERE denominator_snapshot_id IS NOT NULL;
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_rate_budget_periods (
	id                         BIGSERIAL PRIMARY KEY,
	provider                   VARCHAR(64) NOT NULL,
	billing_anchor             DATE NOT NULL,
	period_key                 VARCHAR(64) NOT NULL,
	period_start               TIMESTAMPTZ NOT NULL,
	period_end                 TIMESTAMPTZ NOT NULL,
	call_limit                 INTEGER NOT NULL CHECK (call_limit >= 0),
	reserved_calls             INTEGER NOT NULL DEFAULT 0 CHECK (reserved_calls >= 0),
	used_calls                 INTEGER NOT NULL DEFAULT 0 CHECK (used_calls >= 0),
	plan_name                  VARCHAR(128),
	license_reference          VARCHAR(256),
	config_version             VARCHAR(64) NOT NULL,
	config_frozen_at           TIMESTAMPTZ,
	first_reserved_at          TIMESTAMPTZ,
	limit_change_audit         JSONB NOT NULL DEFAULT '[]'::jsonb,
	created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (provider, billing_anchor, period_key),
	UNIQUE (id, provider),
	CHECK (period_end > period_start),
	CHECK (used_calls <= reserved_calls AND reserved_calls <= call_limit)
);

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_budget_periods_active
	ON company_fund_rate_budget_periods (provider, billing_anchor, period_start DESC, period_end DESC);
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_rate_requests (
	id                              BIGSERIAL PRIMARY KEY,
	budget_period_id                BIGINT NOT NULL,
	provider                        VARCHAR(64) NOT NULL,
	logical_request_key             VARCHAR(512) NOT NULL,
	request_kind                    VARCHAR(32) NOT NULL CHECK (request_kind IN ('CURRENT', 'HISTORICAL', 'RETRY', 'CONTRACT_CHECK')),
	normalized_bucket_start         TIMESTAMPTZ,
	attempt_no                      INTEGER NOT NULL CHECK (attempt_no > 0),
	request_state                   VARCHAR(16) NOT NULL DEFAULT 'PENDING'
		CHECK (request_state IN ('PENDING', 'LEASED', 'RETRY_WAIT', 'DISPATCHED', 'SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED')),
	not_before                      TIMESTAMPTZ,
	lease_owner                     VARCHAR(128),
	lease_expires_at                TIMESTAMPTZ,
	reserved_at                     TIMESTAMPTZ NOT NULL DEFAULT now(),
	dispatched_at                   TIMESTAMPTZ,
	charged_at                      TIMESTAMPTZ,
	completed_at                    TIMESTAMPTZ,
	response_snapshot_group_id      VARCHAR(256),
	error_code                      VARCHAR(64),
	error_detail                    TEXT,
	created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (provider, logical_request_key, attempt_no),
	UNIQUE (id, provider),
	FOREIGN KEY (budget_period_id, provider)
		REFERENCES company_fund_rate_budget_periods (id, provider) ON DELETE RESTRICT,
	CHECK (
		(request_state = 'LEASED' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
		OR (request_state <> 'LEASED' AND lease_owner IS NULL AND lease_expires_at IS NULL)
	),
	CHECK (request_state <> 'RETRY_WAIT' OR not_before IS NOT NULL),
	CHECK (request_state <> 'DISPATCHED' OR dispatched_at IS NOT NULL),
	CHECK (
		(request_state IN ('SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED') AND completed_at IS NOT NULL)
		OR (request_state NOT IN ('SUCCEEDED', 'FAILED', 'UNKNOWN', 'CANCELLED') AND completed_at IS NULL)
	)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_company_fund_rate_requests_active_key
	ON company_fund_rate_requests (provider, logical_request_key)
	WHERE request_state IN ('PENDING', 'LEASED', 'RETRY_WAIT', 'DISPATCHED');

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_requests_claim
	ON company_fund_rate_requests (request_state, not_before, lease_expires_at, normalized_bucket_start)
	WHERE request_state IN ('PENDING', 'LEASED', 'RETRY_WAIT');

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_requests_budget
	ON company_fund_rate_requests (budget_period_id, reserved_at DESC);
`,
		`
ALTER TABLE company_fund_rate_snapshots
	ADD COLUMN IF NOT EXISTS originating_rate_request_id BIGINT;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid = 'company_fund_rate_snapshots'::regclass
			AND conname = 'fk_company_fund_rate_snapshots_originating_request'
	) THEN
		ALTER TABLE company_fund_rate_snapshots
			ADD CONSTRAINT fk_company_fund_rate_snapshots_originating_request
			FOREIGN KEY (originating_rate_request_id, provider)
			REFERENCES company_fund_rate_requests (id, provider) ON DELETE RESTRICT;
	END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_company_fund_rate_snapshots_originating_request
	ON company_fund_rate_snapshots (originating_rate_request_id)
	WHERE originating_rate_request_id IS NOT NULL;

CREATE OR REPLACE FUNCTION company_fund_reject_rate_snapshot_cycle()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	IF EXISTS (
		WITH RECURSIVE dependencies(id) AS (
			SELECT edge.id
			FROM (VALUES
				(NEW.supersedes_snapshot_id),
				(NEW.numerator_snapshot_id),
				(NEW.denominator_snapshot_id)
			) AS edge(id)
			WHERE edge.id IS NOT NULL
			UNION
			SELECT edge.id
			FROM dependencies
			JOIN company_fund_rate_snapshots AS snapshot ON snapshot.id = dependencies.id
			CROSS JOIN LATERAL (VALUES
				(snapshot.supersedes_snapshot_id),
				(snapshot.numerator_snapshot_id),
				(snapshot.denominator_snapshot_id)
			) AS edge(id)
			WHERE edge.id IS NOT NULL
		)
		SELECT 1 FROM dependencies WHERE id = NEW.id
	) THEN
		RAISE EXCEPTION 'company_fund_rate_snapshots dependency graph must be acyclic';
	END IF;
	RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION company_fund_reject_rate_snapshot_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	IF TG_OP = 'UPDATE' THEN
		IF OLD.is_eligible_leaf = true
			AND NEW.is_eligible_leaf = false
			AND (to_jsonb(NEW) - ARRAY['is_eligible_leaf', 'updated_at'])
				= (to_jsonb(OLD) - ARRAY['is_eligible_leaf', 'updated_at']) THEN
			RETURN NEW;
		END IF;
	END IF;
	RAISE EXCEPTION 'company_fund_rate_snapshots are append-only except eligible leaf retirement';
END;
$$;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = 'company_fund_rate_snapshots'::regclass
			AND tgname = 'company_fund_rate_snapshots_prevent_cycle'
			AND NOT tgisinternal
	) THEN
		EXECUTE 'CREATE TRIGGER company_fund_rate_snapshots_prevent_cycle
			BEFORE INSERT OR UPDATE OF supersedes_snapshot_id, numerator_snapshot_id, denominator_snapshot_id
			ON company_fund_rate_snapshots
			FOR EACH ROW EXECUTE FUNCTION company_fund_reject_rate_snapshot_cycle()';
	END IF;

	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = 'company_fund_rate_snapshots'::regclass
			AND tgname = 'company_fund_rate_snapshots_append_only'
			AND NOT tgisinternal
	) THEN
		EXECUTE 'CREATE TRIGGER company_fund_rate_snapshots_append_only
			BEFORE UPDATE OR DELETE ON company_fund_rate_snapshots
			FOR EACH ROW EXECUTE FUNCTION company_fund_reject_rate_snapshot_mutation()';
	END IF;
END
$$;
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_transaction_valuation_history (
	id                              BIGSERIAL PRIMARY KEY,
	transaction_id                  BIGINT NOT NULL REFERENCES company_fund_transactions(id) ON DELETE RESTRICT,
	valuation_version               BIGINT NOT NULL CHECK (valuation_version > 0),
	usd_value                       NUMERIC(65, 18) CHECK (usd_value IS NULL OR usd_value >= 0),
	provider_reported_usd_value     NUMERIC(65, 18) CHECK (provider_reported_usd_value IS NULL OR provider_reported_usd_value >= 0),
	calculated_usd_value            NUMERIC(65, 18) CHECK (calculated_usd_value IS NULL OR calculated_usd_value >= 0),
	usd_unit_price                  NUMERIC(65, 18) CHECK (usd_unit_price IS NULL OR usd_unit_price >= 0),
	usd_valuation_status            VARCHAR(16) CHECK (usd_valuation_status IS NULL OR usd_valuation_status IN ('PROVISIONAL', 'FINAL', 'UNPRICED', 'STALE')),
	usd_valuation_reason_code       VARCHAR(64),
	usd_valuation_basis             VARCHAR(24) CHECK (usd_valuation_basis IS NULL OR usd_valuation_basis IN ('TRANSACTION_TIME', 'INGESTION_TIME')),
	usd_valuation_time              TIMESTAMPTZ,
	usd_valuation_price_at          TIMESTAMPTZ,
	usd_valuation_source            VARCHAR(32),
	usd_valuation_method            VARCHAR(64),
	usd_valuation_granularity       VARCHAR(16),
	usd_provider_value_scope        VARCHAR(24),
	usd_derivation_method           VARCHAR(32),
	usd_rate_snapshot_id            BIGINT REFERENCES company_fund_rate_snapshots(id) ON DELETE RESTRICT,
	provider_transaction_fact_id    BIGINT REFERENCES company_fund_provider_transaction_facts(id) ON DELETE RESTRICT,
	dependency_fingerprint          VARCHAR(64) NOT NULL CHECK (dependency_fingerprint ~ '^[0-9a-f]{64}$'),
	valuation_policy_version        VARCHAR(64) NOT NULL,
	transition_trigger              VARCHAR(64) NOT NULL,
	supersedes_history_id           BIGINT,
	applied_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (transaction_id, valuation_version),
	UNIQUE (transaction_id, id),
	UNIQUE (transaction_id, id, dependency_fingerprint),
	FOREIGN KEY (transaction_id, supersedes_history_id)
		REFERENCES company_fund_transaction_valuation_history (transaction_id, id) ON DELETE RESTRICT,
	CHECK (supersedes_history_id IS NULL OR supersedes_history_id <> id)
);

CREATE INDEX IF NOT EXISTS idx_company_fund_valuation_history_transaction_applied
	ON company_fund_transaction_valuation_history (transaction_id, applied_at DESC, valuation_version DESC);

CREATE INDEX IF NOT EXISTS idx_company_fund_valuation_history_rate_snapshot
	ON company_fund_transaction_valuation_history (usd_rate_snapshot_id)
	WHERE usd_rate_snapshot_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_company_fund_valuation_history_provider_fact
	ON company_fund_transaction_valuation_history (provider_transaction_fact_id)
	WHERE provider_transaction_fact_id IS NOT NULL;

CREATE OR REPLACE FUNCTION company_fund_reject_valuation_history_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
	RAISE EXCEPTION 'company_fund_transaction_valuation_history is append-only';
END;
$$;

DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_trigger
		WHERE tgrelid = 'company_fund_transaction_valuation_history'::regclass
			AND tgname = 'company_fund_transaction_valuation_history_immutable'
			AND NOT tgisinternal
	) THEN
		EXECUTE 'CREATE TRIGGER company_fund_transaction_valuation_history_immutable
			BEFORE UPDATE OR DELETE ON company_fund_transaction_valuation_history
			FOR EACH ROW EXECUTE FUNCTION company_fund_reject_valuation_history_mutation()';
	END IF;
END
$$;
`,
		`
CREATE TABLE IF NOT EXISTS company_fund_valuation_jobs (
	id                              BIGSERIAL PRIMARY KEY,
	transaction_id                  BIGINT NOT NULL REFERENCES company_fund_transactions(id) ON DELETE RESTRICT,
	source_valuation_history_id     BIGINT,
	trigger_kind                    VARCHAR(64) NOT NULL,
	trigger_id                      VARCHAR(256),
	target_dependency_fingerprint   VARCHAR(64) NOT NULL CHECK (target_dependency_fingerprint ~ '^[0-9a-f]{64}$'),
	policy_version                  VARCHAR(64) NOT NULL,
	expected_current_state          VARCHAR(16) NOT NULL
		CHECK (expected_current_state IN ('NONE', 'HISTORY')),
	expected_current_history_id     BIGINT,
	expected_current_dependency_fingerprint VARCHAR(64),
	job_state                       VARCHAR(16) NOT NULL DEFAULT 'PENDING'
		CHECK (job_state IN ('PENDING', 'LEASED', 'RETRY_WAIT', 'SUCCEEDED', 'SUPERSEDED', 'FAILED')),
	attempt_count                   INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
	next_attempt_at                 TIMESTAMPTZ,
	lease_owner                     VARCHAR(128),
	lease_expires_at                TIMESTAMPTZ,
	last_error                      TEXT,
	completed_at                    TIMESTAMPTZ,
	created_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at                      TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (transaction_id, target_dependency_fingerprint, policy_version),
	FOREIGN KEY (transaction_id, source_valuation_history_id)
		REFERENCES company_fund_transaction_valuation_history (transaction_id, id) ON DELETE RESTRICT,
	FOREIGN KEY (transaction_id, expected_current_history_id, expected_current_dependency_fingerprint)
		REFERENCES company_fund_transaction_valuation_history (transaction_id, id, dependency_fingerprint) ON DELETE RESTRICT,
	CHECK (
		(expected_current_history_id IS NULL AND expected_current_dependency_fingerprint IS NULL)
		OR (expected_current_history_id IS NOT NULL AND expected_current_dependency_fingerprint IS NOT NULL)
	),
	CHECK (
		(expected_current_state = 'NONE'
			AND expected_current_history_id IS NULL
			AND expected_current_dependency_fingerprint IS NULL)
		OR (expected_current_state = 'HISTORY'
			AND expected_current_history_id IS NOT NULL
			AND expected_current_dependency_fingerprint ~ '^[0-9a-f]{64}$')
	),
	CHECK (
		(job_state = 'LEASED' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL)
		OR (job_state <> 'LEASED' AND lease_owner IS NULL AND lease_expires_at IS NULL)
	),
	CHECK (
		(job_state = 'RETRY_WAIT' AND next_attempt_at IS NOT NULL)
		OR (job_state <> 'RETRY_WAIT' AND next_attempt_at IS NULL)
	),
	CHECK (
		(job_state IN ('SUCCEEDED', 'SUPERSEDED', 'FAILED') AND completed_at IS NOT NULL)
		OR (job_state NOT IN ('SUCCEEDED', 'SUPERSEDED', 'FAILED') AND completed_at IS NULL)
	)
);

CREATE INDEX IF NOT EXISTS idx_company_fund_valuation_jobs_claim
	ON company_fund_valuation_jobs (job_state, next_attempt_at, lease_expires_at, created_at)
	WHERE job_state IN ('PENDING', 'LEASED', 'RETRY_WAIT');

CREATE INDEX IF NOT EXISTS idx_company_fund_valuation_jobs_history
	ON company_fund_valuation_jobs (source_valuation_history_id)
	WHERE source_valuation_history_id IS NOT NULL;
`,
		`
DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid = 'company_fund_transactions'::regclass
			AND conname = 'fk_company_fund_transactions_usd_rate_snapshot'
	) THEN
		ALTER TABLE company_fund_transactions
			ADD CONSTRAINT fk_company_fund_transactions_usd_rate_snapshot
			FOREIGN KEY (usd_rate_snapshot_id)
			REFERENCES company_fund_rate_snapshots(id) ON DELETE RESTRICT;
	END IF;

	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conrelid = 'company_fund_transactions'::regclass
			AND conname = 'fk_company_fund_transactions_current_valuation_history'
	) THEN
		-- (transaction id, current history id) must name one history row for that transaction.
		ALTER TABLE company_fund_transactions
			ADD CONSTRAINT fk_company_fund_transactions_current_valuation_history
			FOREIGN KEY (id, current_valuation_history_id)
			REFERENCES company_fund_transaction_valuation_history(transaction_id, id)
			DEFERRABLE INITIALLY DEFERRED;
	END IF;
END
$$;
`,
	}
}

func companyFundLedgerDownStatements() []string {
	return []string{
		`
ALTER TABLE IF EXISTS company_fund_transactions
	DROP CONSTRAINT IF EXISTS fk_company_fund_transactions_current_valuation_history;

ALTER TABLE IF EXISTS company_fund_transactions
	DROP CONSTRAINT IF EXISTS fk_company_fund_transactions_usd_rate_snapshot;

ALTER TABLE IF EXISTS company_fund_rate_snapshots
	DROP CONSTRAINT IF EXISTS fk_company_fund_rate_snapshots_originating_request;

ALTER TABLE IF EXISTS company_fund_rate_snapshots
	DROP COLUMN IF EXISTS originating_rate_request_id;
`,
		`
DROP TRIGGER IF EXISTS company_fund_transactions_finance_category_hierarchy
	ON company_fund_transactions;

DROP TRIGGER IF EXISTS finance_categories_company_fund_hierarchy_guard
	ON finance_categories;

DROP FUNCTION IF EXISTS company_fund_revalidate_finance_category_hierarchy();
DROP FUNCTION IF EXISTS company_fund_validate_transaction_finance_categories();
DROP FUNCTION IF EXISTS company_fund_assert_finance_category_assignment(BIGINT, BIGINT);
`,
		`
DROP TABLE IF EXISTS company_fund_valuation_jobs;
`,
		`
DROP TRIGGER IF EXISTS company_fund_transaction_valuation_history_immutable
	ON company_fund_transaction_valuation_history;

DROP FUNCTION IF EXISTS company_fund_reject_valuation_history_mutation();

DROP TABLE IF EXISTS company_fund_transaction_valuation_history;
`,
		`
DROP TABLE IF EXISTS company_fund_rate_requests;
`,
		`
DROP TABLE IF EXISTS company_fund_rate_budget_periods;
`,
		`
DROP TRIGGER IF EXISTS company_fund_rate_snapshots_prevent_cycle
	ON company_fund_rate_snapshots;

DROP TRIGGER IF EXISTS company_fund_rate_snapshots_append_only
	ON company_fund_rate_snapshots;

DROP FUNCTION IF EXISTS company_fund_reject_rate_snapshot_cycle();
DROP FUNCTION IF EXISTS company_fund_reject_rate_snapshot_mutation();

DROP TABLE IF EXISTS company_fund_rate_snapshots;
`,
		`
DROP TABLE IF EXISTS company_fund_sync_runs;
`,
		`
DROP TABLE IF EXISTS company_fund_transactions;
`,
		`
DROP TABLE IF EXISTS company_fund_provider_transaction_facts;
`,
		`
DROP TABLE IF EXISTS company_fund_provider_events;
`,
		`
DROP TABLE IF EXISTS company_fund_safeheron_raw_event_exclusions;
`,
		`
DROP TABLE IF EXISTS finance_categories;
`,
		`
DROP TABLE IF EXISTS company_fund_account_asset_policies;
`,
		`
DROP TABLE IF EXISTS company_fund_accounts;
`,
		`
ALTER TABLE IF EXISTS safeheron_webhook_events
	DROP CONSTRAINT IF EXISTS chk_safeheron_webhook_events_payload_digest_sha256;
`,
		`DROP INDEX IF EXISTS idx_safeheron_webhook_events_payload_digest;`,
		`ALTER TABLE safeheron_webhook_events DROP COLUMN IF EXISTS payload_digest;`,
	}
}
