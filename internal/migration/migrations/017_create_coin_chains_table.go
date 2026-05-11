package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateCoinChainsTable struct{}

func (m *CreateCoinChainsTable) Version() string {
	return "017"
}

func (m *CreateCoinChainsTable) Description() string {
	return "Create coin_chains table for coin-chain relationship with Safeheron coinKey mapping"
}

func (m *CreateCoinChainsTable) Up(db *sql.DB) error {
	createTable := `
	CREATE TABLE IF NOT EXISTS coin_chains (
		id                      SERIAL       PRIMARY KEY,
		chain_code              VARCHAR(32)  NOT NULL REFERENCES chains(code),
		coin_id                 INT          NOT NULL REFERENCES coins(id),
		is_native               BOOLEAN      NOT NULL DEFAULT false,
		token_contract          VARCHAR(128),
		decimals                INT          NOT NULL,
		safeheron_coin_key      VARCHAR(64)  NOT NULL UNIQUE,
		min_deposit_amount      VARCHAR(64)  NOT NULL,
		deposit_enabled         BOOLEAN      NOT NULL DEFAULT true,
		withdraw_enabled        BOOLEAN      NOT NULL DEFAULT false,
		required_confirmations  INT          NOT NULL DEFAULT 0,
		display_order           INT          NOT NULL DEFAULT 0,
		created_at              TIMESTAMP    NOT NULL DEFAULT NOW(),
		updated_at              TIMESTAMP    NOT NULL DEFAULT NOW()
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

var _ migration.Migration = (*CreateCoinChainsTable)(nil)
