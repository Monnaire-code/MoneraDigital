package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type CreateCoinsTable struct{}

func (m *CreateCoinsTable) Version() string {
	return "016"
}

func (m *CreateCoinsTable) Description() string {
	return "Create coins table for cryptocurrency dictionary"
}

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

var _ migration.Migration = (*CreateCoinsTable)(nil)
