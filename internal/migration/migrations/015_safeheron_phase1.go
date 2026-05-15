package migrations

import (
	"database/sql"
	"fmt"
	"os"

	"monera-digital/internal/migration"
)

// SafeheronPhase1 is the single migration that creates all Safeheron Phase 1
// infrastructure: chains/coins/coin_chains/address_pool/webhook_events tables,
// deposits extension, and seed data.
type SafeheronPhase1 struct{}

func (m *SafeheronPhase1) Version() string {
	return "015"
}

func (m *SafeheronPhase1) Description() string {
	return "Safeheron Phase 1: tables, deposits extension, and seed data"
}

func (m *SafeheronPhase1) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{"AddPendingUserStatus", (&AddPendingUserStatus{}).Up},
		{"CreateChainsTable", (&CreateChainsTable{}).Up},
		{"CreateCoinsTable", (&CreateCoinsTable{}).Up},
		{"CreateCoinChainsTable", (&CreateCoinChainsTable{}).Up},
		{"CreateAddressPoolTable", (&CreateAddressPoolTable{}).Up},
		{"CreateSafeheronWebhookEventsTable", (&CreateSafeheronWebhookEventsTable{}).Up},
		{"ExtendDepositsForSafeheron", (&ExtendDepositsForSafeheron{}).Up},
		{"SeedSafeheronPhase1", (&SeedSafeheronPhase1Data{}).Up},
		{"AddAccountBalanceConstraints", (&AddAccountBalanceConstraints{}).Up},
	}
	for _, s := range steps {
		if err := s.fn(db); err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
	}
	return nil
}

func (m *SafeheronPhase1) Down(db *sql.DB) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("BLOCKED: rollback of Safeheron Phase 1 in production would destroy data; use a manual migration instead")
	}

	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{"AddAccountBalanceConstraints", (&AddAccountBalanceConstraints{}).Down},
		{"SeedSafeheronPhase1", (&SeedSafeheronPhase1Data{}).Down},
		{"ExtendDepositsForSafeheron", (&ExtendDepositsForSafeheron{}).Down},
		{"CreateSafeheronWebhookEventsTable", (&CreateSafeheronWebhookEventsTable{}).Down},
		{"CreateAddressPoolTable", (&CreateAddressPoolTable{}).Down},
		{"CreateCoinChainsTable", (&CreateCoinChainsTable{}).Down},
		{"CreateCoinsTable", (&CreateCoinsTable{}).Down},
		{"CreateChainsTable", (&CreateChainsTable{}).Down},
		{"AddPendingUserStatus", (&AddPendingUserStatus{}).Down},
	}
	for _, s := range steps {
		if err := s.fn(db); err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
	}
	return nil
}

var _ migration.Migration = (*SafeheronPhase1)(nil)

// ---------------------------------------------------------------------------
// Step 0: ensure PENDING exists in user_status enum
// ---------------------------------------------------------------------------

type AddPendingUserStatus struct{}

func (m *AddPendingUserStatus) Up(db *sql.DB) error {
	_, err := db.Exec(`ALTER TYPE user_status ADD VALUE IF NOT EXISTS 'PENDING';`)
	if err != nil {
		return fmt.Errorf("failed to add PENDING to user_status enum: %w", err)
	}
	return nil
}

func (m *AddPendingUserStatus) Down(_ *sql.DB) error {
	return nil // PostgreSQL does not support removing enum values
}

// ---------------------------------------------------------------------------
// Step 1: chains
// ---------------------------------------------------------------------------

type CreateChainsTable struct{}

func (m *CreateChainsTable) Up(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS chains (
		code            VARCHAR(32)  PRIMARY KEY,
		name            VARCHAR(64)  NOT NULL,
		description     TEXT,
		network_family  VARCHAR(16)  NOT NULL,
		chain_id        VARCHAR(32),
		native_symbol   VARCHAR(16)  NOT NULL,
		explorer_url    VARCHAR(255),
		icon_url        VARCHAR(255),
		short_name      VARCHAR(16),
		enabled         BOOLEAN      NOT NULL DEFAULT true,
		display_order   INT          NOT NULL DEFAULT 0,
		created_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
		updated_at      TIMESTAMP    NOT NULL DEFAULT NOW()
	);
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create chains table: %w", err)
	}
	return nil
}

func (m *CreateChainsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS chains;`)
	if err != nil {
		return fmt.Errorf("failed to drop chains table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 2: coins
// ---------------------------------------------------------------------------

type CreateCoinsTable struct{}

func (m *CreateCoinsTable) Up(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS coins (
		id              SERIAL       PRIMARY KEY,
		symbol          VARCHAR(32)  NOT NULL UNIQUE,
		name            VARCHAR(64)  NOT NULL,
		description     TEXT,
		icon_url        VARCHAR(255),
		is_stable       BOOLEAN      NOT NULL DEFAULT false,
		enabled         BOOLEAN      NOT NULL DEFAULT true,
		display_order   INT          NOT NULL DEFAULT 0,
		created_at      TIMESTAMP    NOT NULL DEFAULT NOW(),
		updated_at      TIMESTAMP    NOT NULL DEFAULT NOW()
	);
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create coins table: %w", err)
	}
	return nil
}

func (m *CreateCoinsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS coins;`)
	if err != nil {
		return fmt.Errorf("failed to drop coins table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 3: coin_chains
// ---------------------------------------------------------------------------

type CreateCoinChainsTable struct{}

func (m *CreateCoinChainsTable) Up(db *sql.DB) error {
	createTable := `
	CREATE TABLE IF NOT EXISTS coin_chains (
		id                        SERIAL       PRIMARY KEY,
		chain_code                VARCHAR(32)  NOT NULL REFERENCES chains(code),
		coin_id                   INT          NOT NULL REFERENCES coins(id),
		symbol                    VARCHAR(32)  NOT NULL DEFAULT '',
		is_native                 BOOLEAN      NOT NULL DEFAULT false,
		token_contract            VARCHAR(128),
		decimals                  INT          NOT NULL,
		safeheron_coin_key        VARCHAR(64)  NOT NULL UNIQUE,
		min_deposit_amount        VARCHAR(64)  NOT NULL,
		token_standard            VARCHAR(16),
		estimated_arrival_minutes INT,
		deposit_enabled           BOOLEAN      NOT NULL DEFAULT true,
		withdraw_enabled          BOOLEAN      NOT NULL DEFAULT false,
		required_confirmations    INT          NOT NULL DEFAULT 0,
		display_order             INT          NOT NULL DEFAULT 0,
		created_at                TIMESTAMP    NOT NULL DEFAULT NOW(),
		updated_at                TIMESTAMP    NOT NULL DEFAULT NOW()
	);
	`
	_, err := db.Exec(createTable)
	if err != nil {
		return fmt.Errorf("failed to create coin_chains table: %w", err)
	}

	addUnique := `
	DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'coin_chains_chain_code_coin_id_key'
		) THEN
			ALTER TABLE coin_chains ADD CONSTRAINT coin_chains_chain_code_coin_id_key UNIQUE (chain_code, coin_id);
		END IF;
	END $$;
	`
	_, err = db.Exec(addUnique)
	if err != nil {
		return fmt.Errorf("failed to add unique constraint on coin_chains: %w", err)
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_coin_chains_chain_enabled ON coin_chains(chain_code, deposit_enabled);
	CREATE INDEX IF NOT EXISTS idx_coin_chains_safeheron_key ON coin_chains(safeheron_coin_key);
	CREATE INDEX IF NOT EXISTS idx_coin_chains_coin ON coin_chains(coin_id);
	`
	_, err = db.Exec(indexes)
	if err != nil {
		return fmt.Errorf("failed to create coin_chains indexes: %w", err)
	}

	return nil
}

func (m *CreateCoinChainsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS coin_chains;`)
	if err != nil {
		return fmt.Errorf("failed to drop coin_chains table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 4: address_pool
// ---------------------------------------------------------------------------

type CreateAddressPoolTable struct{}

func (m *CreateAddressPoolTable) Up(db *sql.DB) error {
	createTable := `
	CREATE TABLE IF NOT EXISTS address_pool (
		id                      SERIAL       PRIMARY KEY,
		network_family          VARCHAR(16)  NOT NULL,
		address                 VARCHAR(128) NOT NULL,
		safeheron_account_key   VARCHAR(64)  NOT NULL,
		customer_ref_id         VARCHAR(64)  NOT NULL UNIQUE,
		address_group_key       VARCHAR(64),
		derive_path             VARCHAR(64),
		account_tag             VARCHAR(32),
		hidden_on_ui            BOOLEAN      NOT NULL DEFAULT true,
		auto_fuel               BOOLEAN      NOT NULL DEFAULT false,
		status                  VARCHAR(16)  NOT NULL DEFAULT 'AVAILABLE',
		assigned_user_id        INT,
		assigned_at             TIMESTAMP,
		created_at              TIMESTAMP    NOT NULL DEFAULT NOW(),
		updated_at              TIMESTAMP    NOT NULL DEFAULT NOW()
	);
	`
	_, err := db.Exec(createTable)
	if err != nil {
		return fmt.Errorf("failed to create address_pool table: %w", err)
	}

	addUnique := `
	DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'address_pool_network_family_address_key'
		) THEN
			ALTER TABLE address_pool ADD CONSTRAINT address_pool_network_family_address_key UNIQUE (network_family, address);
		END IF;
	END $$;
	`
	_, err = db.Exec(addUnique)
	if err != nil {
		return fmt.Errorf("failed to add unique constraint on address_pool: %w", err)
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_pool_status_family ON address_pool(network_family, status);
	CREATE INDEX IF NOT EXISTS idx_pool_user ON address_pool(assigned_user_id);
	`
	_, err = db.Exec(indexes)
	if err != nil {
		return fmt.Errorf("failed to create address_pool indexes: %w", err)
	}

	return nil
}

func (m *CreateAddressPoolTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS address_pool;`)
	if err != nil {
		return fmt.Errorf("failed to drop address_pool table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 5: safeheron_webhook_events
// ---------------------------------------------------------------------------

type CreateSafeheronWebhookEventsTable struct{}

func (m *CreateSafeheronWebhookEventsTable) Up(db *sql.DB) error {
	createTable := `
	CREATE TABLE IF NOT EXISTS safeheron_webhook_events (
		id                SERIAL       PRIMARY KEY,
		event_id          VARCHAR(128) NOT NULL UNIQUE,
		event_type        VARCHAR(64)  NOT NULL,
		safeheron_tx_key  VARCHAR(128),
		customer_ref_id   VARCHAR(128),
		raw_payload       JSONB        NOT NULL,
		process_status    VARCHAR(16)  NOT NULL DEFAULT 'PENDING',
		process_attempts  INT          NOT NULL DEFAULT 0,
		error_message     TEXT,
		received_at       TIMESTAMP    NOT NULL DEFAULT NOW(),
		processed_at      TIMESTAMP
	);
	`
	_, err := db.Exec(createTable)
	if err != nil {
		return fmt.Errorf("failed to create safeheron_webhook_events table: %w", err)
	}

	indexes := `
	CREATE INDEX IF NOT EXISTS idx_webhook_status ON safeheron_webhook_events(process_status);
	CREATE INDEX IF NOT EXISTS idx_webhook_tx_key ON safeheron_webhook_events(safeheron_tx_key);
	CREATE INDEX IF NOT EXISTS idx_webhook_customer_ref ON safeheron_webhook_events(customer_ref_id);
	`
	_, err = db.Exec(indexes)
	if err != nil {
		return fmt.Errorf("failed to create safeheron_webhook_events indexes: %w", err)
	}

	return nil
}

func (m *CreateSafeheronWebhookEventsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS safeheron_webhook_events;`)
	if err != nil {
		return fmt.Errorf("failed to drop safeheron_webhook_events table: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 6: extend deposits
// ---------------------------------------------------------------------------

type ExtendDepositsForSafeheron struct{}

func (m *ExtendDepositsForSafeheron) Up(db *sql.DB) error {
	addColumns := `
	ALTER TABLE deposits
		ADD COLUMN IF NOT EXISTS safeheron_tx_key      VARCHAR(128),
		ADD COLUMN IF NOT EXISTS safeheron_coin_key    VARCHAR(64),
		ADD COLUMN IF NOT EXISTS chain_code            VARCHAR(32),
		ADD COLUMN IF NOT EXISTS coin_chain_id         INT,
		ADD COLUMN IF NOT EXISTS block_height          BIGINT,
		ADD COLUMN IF NOT EXISTS block_hash            VARCHAR(128),
		ADD COLUMN IF NOT EXISTS safeheron_status      VARCHAR(32),
		ADD COLUMN IF NOT EXISTS safeheron_sub_status  VARCHAR(64),
		ADD COLUMN IF NOT EXISTS status_rank           SMALLINT NOT NULL DEFAULT 0,
		ADD COLUMN IF NOT EXISTS credited_at           TIMESTAMP,
		ADD COLUMN IF NOT EXISTS failed_reason         TEXT,
		ADD COLUMN IF NOT EXISTS updated_at            TIMESTAMP NOT NULL DEFAULT NOW();
	`
	_, err := db.Exec(addColumns)
	if err != nil {
		return fmt.Errorf("failed to add safeheron columns to deposits: %w", err)
	}

	// KYT 合规筛查字段（v1.5 spec §4.6 新增）
	amlColumns := `
	ALTER TABLE deposits
		ADD COLUMN IF NOT EXISTS aml_screening_state VARCHAR(16),
		ADD COLUMN IF NOT EXISTS aml_risk_level      VARCHAR(8),
		ADD COLUMN IF NOT EXISTS aml_evaluated_at    TIMESTAMP,
		ADD COLUMN IF NOT EXISTS aml_list            JSONB;
	`
	_, err = db.Exec(amlColumns)
	if err != nil {
		return fmt.Errorf("failed to add AML columns to deposits: %w", err)
	}

	addFKs := `
	DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'deposits_chain_code_fkey'
		) THEN
			ALTER TABLE deposits ADD CONSTRAINT deposits_chain_code_fkey
				FOREIGN KEY (chain_code) REFERENCES chains(code);
		END IF;

		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'deposits_coin_chain_id_fkey'
		) THEN
			ALTER TABLE deposits ADD CONSTRAINT deposits_coin_chain_id_fkey
				FOREIGN KEY (coin_chain_id) REFERENCES coin_chains(id);
		END IF;
	END $$;
	`
	_, err = db.Exec(addFKs)
	if err != nil {
		return fmt.Errorf("failed to add foreign keys to deposits: %w", err)
	}

	partialUniqueIdx := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_deposits_safeheron_tx_key
		ON deposits(safeheron_tx_key)
		WHERE safeheron_tx_key IS NOT NULL;
	`
	_, err = db.Exec(partialUniqueIdx)
	if err != nil {
		return fmt.Errorf("failed to create partial unique index on deposits.safeheron_tx_key: %w", err)
	}

	enumToVarchar := `
	DO $$ BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'deposits' AND column_name = 'status'
			  AND data_type = 'USER-DEFINED'
		) THEN
			ALTER TABLE deposits ALTER COLUMN status TYPE VARCHAR(32) USING status::text;
		END IF;
	END $$;
	`
	_, err = db.Exec(enumToVarchar)
	if err != nil {
		return fmt.Errorf("failed to convert deposits.status from enum to varchar: %w", err)
	}

	normalizeStatus := `
	UPDATE deposits SET status = 'PENDING'
	WHERE status NOT IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
	                      'KYT_PENDING',
	                      'CREDITED', 'FAILED', 'MANUAL_REVIEW');
	`
	_, err = db.Exec(normalizeStatus)
	if err != nil {
		return fmt.Errorf("failed to normalize existing deposits status values: %w", err)
	}

	checkConstraint := `
	ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status;
	ALTER TABLE deposits ADD CONSTRAINT ck_deposits_status
		CHECK (status IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
		                  'KYT_PENDING',
		                  'CREDITED', 'FAILED', 'MANUAL_REVIEW'));
	`
	_, err = db.Exec(checkConstraint)
	if err != nil {
		return fmt.Errorf("failed to add status check constraint on deposits: %w", err)
	}

	accountUniqueIdx := `
	CREATE UNIQUE INDEX IF NOT EXISTS idx_account_user_currency
		ON account(user_id, currency);
	`
	_, err = db.Exec(accountUniqueIdx)
	if err != nil {
		return fmt.Errorf("failed to create unique index on account(user_id, currency): %w", err)
	}

	// 超时扫描索引：仅 KYT_PENDING 行，节省空间
	kytPendingIdx := `
	CREATE INDEX IF NOT EXISTS idx_deposits_kyt_pending
		ON deposits(updated_at)
		WHERE status = 'KYT_PENDING';
	`
	_, err = db.Exec(kytPendingIdx)
	if err != nil {
		return fmt.Errorf("failed to create KYT_PENDING partial index: %w", err)
	}

	return nil
}

func (m *ExtendDepositsForSafeheron) Down(db *sql.DB) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("BLOCKED: rollback of migration 020 in production would destroy deposit data; use a manual migration instead")
	}

	dropKytIdx := `DROP INDEX IF EXISTS idx_deposits_kyt_pending;`
	_, err := db.Exec(dropKytIdx)
	if err != nil {
		return fmt.Errorf("failed to drop KYT_PENDING index: %w", err)
	}

	dropAccountIdx := `DROP INDEX IF EXISTS idx_account_user_currency;`
	_, err = db.Exec(dropAccountIdx)
	if err != nil {
		return fmt.Errorf("failed to drop account unique index: %w", err)
	}

	dropConstraint := `ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status;`
	_, err = db.Exec(dropConstraint)
	if err != nil {
		return fmt.Errorf("failed to drop deposits status constraint: %w", err)
	}

	dropIdx := `DROP INDEX IF EXISTS idx_deposits_safeheron_tx_key;`
	_, err = db.Exec(dropIdx)
	if err != nil {
		return fmt.Errorf("failed to drop deposits safeheron_tx_key index: %w", err)
	}

	_, err = db.Exec(`ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_coin_chain_id_fkey;`)
	if err != nil {
		return fmt.Errorf("failed to drop deposits_coin_chain_id_fkey: %w", err)
	}

	_, err = db.Exec(`ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_code_fkey;`)
	if err != nil {
		return fmt.Errorf("failed to drop deposits_chain_code_fkey: %w", err)
	}

	dropColumns := `
	ALTER TABLE deposits
		DROP COLUMN IF EXISTS aml_list,
		DROP COLUMN IF EXISTS aml_evaluated_at,
		DROP COLUMN IF EXISTS aml_risk_level,
		DROP COLUMN IF EXISTS aml_screening_state,
		DROP COLUMN IF EXISTS failed_reason,
		DROP COLUMN IF EXISTS credited_at,
		DROP COLUMN IF EXISTS status_rank,
		DROP COLUMN IF EXISTS safeheron_sub_status,
		DROP COLUMN IF EXISTS safeheron_status,
		DROP COLUMN IF EXISTS block_hash,
		DROP COLUMN IF EXISTS block_height,
		DROP COLUMN IF EXISTS coin_chain_id,
		DROP COLUMN IF EXISTS chain_code,
		DROP COLUMN IF EXISTS safeheron_coin_key,
		DROP COLUMN IF EXISTS safeheron_tx_key;
	`
	_, err = db.Exec(dropColumns)
	if err != nil {
		return fmt.Errorf("failed to drop safeheron columns from deposits: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Step 7: seed data
// ---------------------------------------------------------------------------

// SeedSafeheronPhase1Data seeds chains, coins, and coin_chains.
// Named with "Data" suffix to avoid collision with the orchestrator.
type SeedSafeheronPhase1Data struct{}

func (m *SeedSafeheronPhase1Data) Up(db *sql.DB) error {
	seedChains := `
	INSERT INTO chains (code, name, network_family, chain_id, native_symbol, explorer_url, short_name, display_order)
	VALUES
		('ETHEREUM', 'Ethereum',        'EVM',  '1',  'ETH', 'https://etherscan.io',  'ETH',  10),
		('BSC',      'BNB Smart Chain', 'EVM',  '56', 'BNB', 'https://bscscan.com',   'BSC',  20),
		('TRON',     'TRON',            'TRON', NULL,  'TRX', 'https://tronscan.org',  'TRON', 30)
	ON CONFLICT (code) DO NOTHING;
	`
	_, err := db.Exec(seedChains)
	if err != nil {
		return fmt.Errorf("failed to seed chains: %w", err)
	}

	seedCoins := `
	INSERT INTO coins (symbol, name, is_stable, display_order)
	VALUES
		('ETH',  'Ether',      false, 10),
		('BNB',  'BNB',        false, 20),
		('TRX',  'TRON',       false, 30),
		('USDT', 'Tether USD', true,  40),
		('USDC', 'USD Coin',   true,  50)
	ON CONFLICT (symbol) DO NOTHING;
	`
	_, err = db.Exec(seedCoins)
	if err != nil {
		return fmt.Errorf("failed to seed coins: %w", err)
	}

	return m.seedProductionCoinChains(db)
}

func (m *SeedSafeheronPhase1Data) seedProductionCoinChains(db *sql.DB) error {
	query := `
	INSERT INTO coin_chains (chain_code, coin_id, symbol, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, token_standard, estimated_arrival_minutes, display_order)
	    SELECT 'ETHEREUM', id, 'ETH',  true,  NULL,                                          18, 'ETH',        '0.0001', 'Native', 2, 10 FROM coins WHERE symbol='ETH'
	UNION ALL
	    SELECT 'ETHEREUM', id, 'USDT', false, '0xdAC17F958D2ee523a2206206994597C13D831ec7', 6,  'USDT_ERC20', '0.01',   'ERC20',  2, 20 FROM coins WHERE symbol='USDT'
	UNION ALL
	    SELECT 'ETHEREUM', id, 'USDC', false, '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48', 6,  'USDC_ERC20', '0.01',   'ERC20',  2, 30 FROM coins WHERE symbol='USDC'
	UNION ALL
	    SELECT 'BSC',      id, 'BNB',  true,  NULL,                                          18, 'BNB_BSC',    '0.0001', 'Native', 1, 40 FROM coins WHERE symbol='BNB'
	UNION ALL
	    SELECT 'BSC',      id, 'USDT', false, '0x55d398326f99059fF775485246999027B3197955',  18, 'USDT_BEP20', '0.01',   'BEP20',  1, 50 FROM coins WHERE symbol='USDT'
	UNION ALL
	    SELECT 'BSC',      id, 'USDC', false, '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d',  18, 'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', '0.01', 'BEP20', 1, 60 FROM coins WHERE symbol='USDC'
	UNION ALL
	    SELECT 'TRON',     id, 'TRX',  true,  NULL,                                          6,  'TRX',        '0.01',   'Native', 1, 70 FROM coins WHERE symbol='TRX'
	UNION ALL
	    SELECT 'TRON',     id, 'USDT', false, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',          6,  'USDT_TRC20', '0.01',   'TRC20',  1, 80 FROM coins WHERE symbol='USDT'
	ON CONFLICT (safeheron_coin_key) DO NOTHING;
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to seed production coin_chains: %w", err)
	}
	return nil
}

func (m *SeedSafeheronPhase1Data) Down(db *sql.DB) error {
	_, err := db.Exec(`
		DELETE FROM coin_chains WHERE safeheron_coin_key IN (
			'ETH', 'USDT_ERC20', 'USDC_ERC20', 'BNB_BSC', 'USDT_BEP20',
			'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', 'TRX', 'USDT_TRC20'
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 coin_chains: %w", err)
	}

	_, err = db.Exec(`
		DELETE FROM coins WHERE symbol IN ('ETH', 'BNB', 'TRX', 'USDT', 'USDC')
		AND NOT EXISTS (SELECT 1 FROM coin_chains WHERE coin_chains.coin_id = coins.id);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 coins: %w", err)
	}

	_, err = db.Exec(`
		DELETE FROM chains WHERE code IN ('ETHEREUM', 'BSC', 'TRON')
		AND NOT EXISTS (SELECT 1 FROM coin_chains WHERE coin_chains.chain_code = chains.code);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 chains: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Step 8: account balance non-negative constraints (D-44)
// ---------------------------------------------------------------------------

type AddAccountBalanceConstraints struct{}

func (m *AddAccountBalanceConstraints) Up(db *sql.DB) error {
	// frozen_balance 允许负值（业务设计），不添加 ck_frozen_non_negative 约束。
	query := `
	DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'ck_balance_non_negative'
		) THEN
			ALTER TABLE account ADD CONSTRAINT ck_balance_non_negative CHECK (balance >= 0);
		END IF;
	END $$;`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to add account balance constraints: %w", err)
	}
	return nil
}

func (m *AddAccountBalanceConstraints) Down(db *sql.DB) error {
	_, err := db.Exec(`
		ALTER TABLE account DROP CONSTRAINT IF EXISTS ck_balance_non_negative;`)
	return err
}
