// internal/migration/migrations/012_add_user_status.go
package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AddUserStatus migration
type AddUserStatus struct{}

func (m *AddUserStatus) Version() string {
	return "012"
}

func (m *AddUserStatus) Description() string {
	return "Add status field to users table for account disable functionality"
}

func (m *AddUserStatus) Up(db *sql.DB) error {
	// 1. Create user_status enum type if not exists
	// PostgreSQL enum needs to be created before adding the column
	createEnumQuery := `
	DO $$
	BEGIN
		IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'user_status') THEN
			CREATE TYPE user_status AS ENUM ('ACTIVE', 'DISABLED');
		END IF;
	END
	$$;
	`
	_, err := db.Exec(createEnumQuery)
	if err != nil {
		return fmt.Errorf("failed to create user_status enum: %w", err)
	}

	// 2. Add status column to users table
	addColumnQuery := `
	ALTER TABLE users 
	ADD COLUMN IF NOT EXISTS status user_status DEFAULT 'ACTIVE' NOT NULL;
	`
	_, err = db.Exec(addColumnQuery)
	if err != nil {
		return fmt.Errorf("failed to add status column: %w", err)
	}

	// 3. Create index on status for faster queries
	createIndexQuery := `CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);`
	_, err = db.Exec(createIndexQuery)
	if err != nil {
		return fmt.Errorf("failed to create status index: %w", err)
	}

	return nil
}

func (m *AddUserStatus) Down(db *sql.DB) error {
	// 1. Drop index
	dropIndexQuery := `DROP INDEX IF EXISTS idx_users_status;`
	_, err := db.Exec(dropIndexQuery)
	if err != nil {
		return fmt.Errorf("failed to drop index: %w", err)
	}

	// 2. Drop column
	dropColumnQuery := `ALTER TABLE users DROP COLUMN IF EXISTS status;`
	_, err = db.Exec(dropColumnQuery)
	if err != nil {
		return fmt.Errorf("failed to drop status column: %w", err)
	}

	// 3. Drop enum type
	dropEnumQuery := `DROP TYPE IF EXISTS user_status;`
	_, err = db.Exec(dropEnumQuery)
	if err != nil {
		return fmt.Errorf("failed to drop enum type: %w", err)
	}

	return nil
}

// Ensure AddUserStatus implements Migration interface
var _ migration.Migration = (*AddUserStatus)(nil)
