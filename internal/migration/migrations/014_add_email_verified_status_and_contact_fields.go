// internal/migration/migrations/014_add_email_verified_status_and_contact_fields.go
package migrations

import (
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AddEmailVerifiedStatusAndContactFields migration
type AddEmailVerifiedStatusAndContactFields struct{}

func (m *AddEmailVerifiedStatusAndContactFields) Version() string {
	return "014"
}

func (m *AddEmailVerifiedStatusAndContactFields) Description() string {
	return "Add EMAIL_VERIFIED and INFO_SUBMITTED status, plus contact fields (phone, telegram, wechat)"
}

func (m *AddEmailVerifiedStatusAndContactFields) Up(db *sql.DB) error {
	// 1. Extend user_status enum with new values
	extendEnumQuery := `
	DO $$
	BEGIN
		-- Check if EMAIL_VERIFIED exists
		IF NOT EXISTS (SELECT 1 FROM pg_enum WHERE enumlabel = 'EMAIL_VERIFIED') THEN
			ALTER TYPE user_status ADD VALUE 'EMAIL_VERIFIED';
		END IF;
		-- Check if INFO_SUBMITTED exists
		IF NOT EXISTS (SELECT 1 FROM pg_enum WHERE enumlabel = 'INFO_SUBMITTED') THEN
			ALTER TYPE user_status ADD VALUE 'INFO_SUBMITTED';
		END IF;
	END
	$$;
	`
	_, err := db.Exec(extendEnumQuery)
	if err != nil {
		return fmt.Errorf("failed to extend user_status enum: %w", err)
	}

	// 2. Add contact fields
	addColumnsQuery := `
	ALTER TABLE users 
	ADD COLUMN IF NOT EXISTS phone VARCHAR(20),
	ADD COLUMN IF NOT EXISTS telegram VARCHAR(100),
	ADD COLUMN IF NOT EXISTS wechat VARCHAR(100),
	ADD COLUMN IF NOT EXISTS contact_submitted_at TIMESTAMP;
	`
	_, err = db.Exec(addColumnsQuery)
	if err != nil {
		return fmt.Errorf("failed to add contact columns: %w", err)
	}

	// 3. Add comments
	commentQuery := `
	COMMENT ON COLUMN users.phone IS 'International phone number, format: +8613812345678';
	COMMENT ON COLUMN users.telegram IS 'Telegram username or ID';
	COMMENT ON COLUMN users.wechat IS 'WeChat ID';
	COMMENT ON COLUMN users.contact_submitted_at IS 'Timestamp when contact info was submitted';
	`
	_, err = db.Exec(commentQuery)
	if err != nil {
		return fmt.Errorf("failed to add column comments: %w", err)
	}

	return nil
}

func (m *AddEmailVerifiedStatusAndContactFields) Down(db *sql.DB) error {
	// 1. Drop columns
	dropColumnsQuery := `
	ALTER TABLE users 
	DROP COLUMN IF EXISTS contact_submitted_at,
	DROP COLUMN IF EXISTS wechat,
	DROP COLUMN IF EXISTS telegram,
	DROP COLUMN IF EXISTS phone;
	`
	_, err := db.Exec(dropColumnsQuery)
	if err != nil {
		return fmt.Errorf("failed to drop contact columns: %w", err)
	}

	// 2. Note: PostgreSQL doesn't support removing enum values easily
	// In production, you would need to recreate the enum type

	return nil
}

// Ensure AddEmailVerifiedStatusAndContactFields implements Migration interface
var _ migration.Migration = (*AddEmailVerifiedStatusAndContactFields)(nil)
