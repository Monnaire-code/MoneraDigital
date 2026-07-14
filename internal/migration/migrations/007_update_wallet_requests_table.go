package migrations

import (
	"database/sql"
	"fmt"
	"monera-digital/internal/migration"
)

// UpdateWalletRequestsTable migration
type UpdateWalletRequestsTable struct{}

func (m *UpdateWalletRequestsTable) Version() string {
	return "007"
}

func (m *UpdateWalletRequestsTable) Description() string {
	return "Update wallet requests table with product code and currency"
}

func (m *UpdateWalletRequestsTable) Up(db *sql.DB) error {
	if err := ensureWalletCreationRequestsTable(db); err != nil {
		return err
	}

	// Add product_code and currency columns if they don't exist
	_, err := db.Exec(`
		ALTER TABLE wallet_creation_requests 
		ADD COLUMN IF NOT EXISTS product_code VARCHAR(50) DEFAULT '',
		ADD COLUMN IF NOT EXISTS currency VARCHAR(20) DEFAULT '';
	`)
	if err != nil {
		return fmt.Errorf("failed to add columns: %w", err)
	}

	// Create unique index for user_id, product_code, and currency
	// We first need to handle potential duplicates if there's existing data
	// For simplicity in this migration, we'll assume clean data or manual cleanup required if duplicates exist
	// In production, we might want to do a cleanup step first

	_, err = db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_wallet_requests_user_product_currency 
		ON wallet_creation_requests (user_id, product_code, currency)
		WHERE status = 'SUCCESS';
	`)
	if err != nil {
		return fmt.Errorf("failed to create unique index: %w", err)
	}

	return nil
}

// ensureWalletCreationRequestsTable repairs the historical bootstrap gap in
// migration 007. Earlier deployments created this table through the retired
// Drizzle path, but a fresh Go-migrated database has no such prerequisite.
func ensureWalletCreationRequestsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS wallet_creation_requests (
			id SERIAL PRIMARY KEY,
			request_id VARCHAR(36) NOT NULL UNIQUE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			product_code VARCHAR(50) DEFAULT '',
			currency VARCHAR(20) DEFAULT '',
			status VARCHAR(20) NOT NULL DEFAULT 'CREATING'
				CHECK (status IN ('CREATING', 'SUCCESS', 'FAILED')),
			wallet_id VARCHAR(100),
			address VARCHAR(255),
			addresses TEXT,
			error_message TEXT,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create wallet creation requests table: %w", err)
	}

	_, err = db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_wallet_creation_requests_user_id
		ON wallet_creation_requests(user_id)
	`)
	if err != nil {
		return fmt.Errorf("failed to create wallet creation requests user index: %w", err)
	}

	return nil
}

func (m *UpdateWalletRequestsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`
		DROP INDEX IF EXISTS idx_wallet_requests_user_product_currency;
		ALTER TABLE wallet_creation_requests 
		DROP COLUMN IF EXISTS product_code,
		DROP COLUMN IF EXISTS currency;
	`)
	return err
}

// Ensure UpdateWalletRequestsTable implements Migration interface
var _ migration.Migration = (*UpdateWalletRequestsTable)(nil)
