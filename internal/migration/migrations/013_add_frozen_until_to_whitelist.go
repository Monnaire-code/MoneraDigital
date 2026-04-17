package migrations

import (
	"database/sql"
	"fmt"
	"time"

	"monera-digital/internal/migration"
)

type AddFrozenUntilToWhitelist struct{}

func (m *AddFrozenUntilToWhitelist) Version() string {
	return "013"
}

func (m *AddFrozenUntilToWhitelist) Description() string {
	return "Add frozen_until column to withdrawal_address_whitelist for 4-hour freeze period"
}

func (m *AddFrozenUntilToWhitelist) Up(db *sql.DB) error {
	query := `
	ALTER TABLE withdrawal_address_whitelist 
	ADD COLUMN IF NOT EXISTS frozen_until TIMESTAMP;
	
	CREATE INDEX IF NOT EXISTS idx_whitelist_frozen_until 
	ON withdrawal_address_whitelist(frozen_until) 
	WHERE frozen_until IS NOT NULL;
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to add frozen_until column: %w", err)
	}

	fmt.Printf("[Migration %s] Added frozen_until column to withdrawal_address_whitelist at %s\n",
		m.Version(), time.Now().Format("2006-01-02 15:04:05"))
	return nil
}

func (m *AddFrozenUntilToWhitelist) Down(db *sql.DB) error {
	query := `
	ALTER TABLE withdrawal_address_whitelist 
	DROP COLUMN IF EXISTS frozen_until;
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to drop frozen_until column: %w", err)
	}
	return nil
}

var _ migration.Migration = (*AddFrozenUntilToWhitelist)(nil)
