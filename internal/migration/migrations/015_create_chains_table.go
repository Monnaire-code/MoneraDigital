package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateChainsTable struct{}

func (m *CreateChainsTable) Version() string {
	return "015"
}

func (m *CreateChainsTable) Description() string {
	return "Create chains table for blockchain network dictionary"
}

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

var _ migration.Migration = (*CreateChainsTable)(nil)
