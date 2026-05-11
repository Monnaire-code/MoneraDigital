package migrations

import (
	"database/sql"
	"fmt"
	"os"

	"monera-digital/internal/migration"
)

type ExtendDepositsForSafeheron struct{}

func (m *ExtendDepositsForSafeheron) Version() string {
	return "020"
}

func (m *ExtendDepositsForSafeheron) Description() string {
	return "Extend deposits table with Safeheron fields, add account(user_id, currency) unique index"
}

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
		ADD COLUMN IF NOT EXISTS failed_reason         TEXT;
	`
	_, err := db.Exec(addColumns)
	if err != nil {
		return fmt.Errorf("failed to add safeheron columns to deposits: %w", err)
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

	normalizeStatus := `
	UPDATE deposits SET status = 'PENDING'
	WHERE status NOT IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
	                      'CREDITED', 'FAILED', 'MANUAL_REVIEW');
	`
	_, err = db.Exec(normalizeStatus)
	if err != nil {
		return fmt.Errorf("failed to normalize existing deposits status values: %w", err)
	}

	checkConstraint := `
	DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'ck_deposits_status'
		) THEN
			ALTER TABLE deposits ADD CONSTRAINT ck_deposits_status
				CHECK (status IN ('PENDING', 'CHAIN_VERIFYING', 'CHAIN_VERIFIED',
				                  'CREDITED', 'FAILED', 'MANUAL_REVIEW'));
		END IF;
	END $$;
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

	return nil
}

func (m *ExtendDepositsForSafeheron) Down(db *sql.DB) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("BLOCKED: rollback of migration 020 in production would destroy deposit data; use a manual migration instead")
	}

	dropAccountIdx := `DROP INDEX IF EXISTS idx_account_user_currency;`
	_, err := db.Exec(dropAccountIdx)
	if err != nil {
		return fmt.Errorf("failed to drop account unique index: %w", err)
	}

	dropConstraint := `
	ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status;
	`
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

var _ migration.Migration = (*ExtendDepositsForSafeheron)(nil)
