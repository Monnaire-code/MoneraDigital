package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateAddressPoolTable struct{}

func (m *CreateAddressPoolTable) Version() string {
	return "018"
}

func (m *CreateAddressPoolTable) Description() string {
	return "Create address_pool table for pre-generated deposit addresses (EVM + TRON)"
}

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

var _ migration.Migration = (*CreateAddressPoolTable)(nil)
