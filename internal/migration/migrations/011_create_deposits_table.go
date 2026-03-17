// internal/migration/migrations/011_create_deposits_table.go
package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// CreateDepositsTable migration
type CreateDepositsTable struct{}

func (m *CreateDepositsTable) Version() string {
	return "011"
}

func (m *CreateDepositsTable) Description() string {
	return "Create deposits table"
}

func (m *CreateDepositsTable) Up(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS deposits (
		id SERIAL PRIMARY KEY,
		user_id INTEGER NOT NULL,
		tx_hash VARCHAR(255) NOT NULL UNIQUE,
		amount VARCHAR(65) NOT NULL,
		asset VARCHAR(50) NOT NULL,
		chain VARCHAR(50) NOT NULL,
		status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
		from_address VARCHAR(255),
		to_address VARCHAR(255),
		created_at TIMESTAMP NOT NULL DEFAULT NOW(),
		confirmed_at TIMESTAMP,
		updated_at TIMESTAMP NOT NULL DEFAULT NOW()
	);
	`

	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create deposits table: %w", err)
	}

	indexQueries := []string{
		`CREATE INDEX IF NOT EXISTS idx_deposits_user_id ON deposits(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_deposits_status ON deposits(status)`,
		`CREATE INDEX IF NOT EXISTS idx_deposits_created_at ON deposits(created_at DESC)`,
	}

	for _, idxQuery := range indexQueries {
		_, err = db.Exec(idxQuery)
		if err != nil {
			return fmt.Errorf("failed to create deposit index: %w", err)
		}
	}

	return nil
}

func (m *CreateDepositsTable) Down(db *sql.DB) error {
	query := `DROP TABLE IF EXISTS deposits CASCADE`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to drop deposits table: %w", err)
	}
	return nil
}

var _ migration.Migration = (*CreateDepositsTable)(nil)
