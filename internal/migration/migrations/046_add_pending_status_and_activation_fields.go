// internal/migration/migrations/046_add_pending_status_and_activation_fields.go
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AddPendingStatusAndActivationFields is the Go equivalent of the legacy
// 00046_add_pending_status_and_activation_fields.sql that lived next to
// this directory. It is idempotent (every DDL uses IF NOT EXISTS or a
// pg_enum precheck) so re-running it on a database where 00046 was
// applied is a safe no-op.
//
// What it does:
//  1. Add 'PENDING' to the user_status enum (guarded by pg_enum precheck
//     because PostgreSQL enum values can only be added in a tx that has
//     not yet touched the same enum).
//  2. Add activation-related columns to the users table.
//  3. Create the rate_limits table used by the in-process rate limiter.
//  4. Create supporting indexes.
type AddPendingStatusAndActivationFields struct{}

func (m *AddPendingStatusAndActivationFields) Version() string {
	return "046"
}

func (m *AddPendingStatusAndActivationFields) Description() string {
	return "Add PENDING status to user_status enum, activation columns to users, and the rate_limits table (C-2: Go replacement for legacy 00046.sql)"
}

func (m *AddPendingStatusAndActivationFields) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(sqlExecutor) error
	}{
		{"AddPendingEnumValue", addPendingEnumValue046},
		{"AddActivationColumnsToUsers", addActivationColumnsToUsers},
		{"CreateRateLimitsTable", createRateLimitsTable},
		{"CreateRateLimitsAndActivationIndexes", createRateLimitsAndActivationIndexes},
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, s := range steps {
		if err := s.fn(tx); err != nil {
			return fmt.Errorf("step %s failed: %w", s.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (m *AddPendingStatusAndActivationFields) Down(db *sql.DB) error {
	// Down is best-effort: drop indexes and table only. The enum value
	// 'PENDING' and the users.* columns are intentionally NOT removed
	// because PostgreSQL does not support removing enum values, and
	// removing columns can cascade-drop data we may want to inspect
	// post-rollback. A human-initiated cleanup migration is the safe path.
	stmts := []string{
		`DROP INDEX IF EXISTS idx_users_activation_expires;`,
		`DROP INDEX IF EXISTS idx_users_activation_code;`,
		`DROP INDEX IF EXISTS idx_rate_limits_key;`,
		`DROP TABLE IF EXISTS rate_limits;`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("down: %s: %w", s, err)
		}
	}
	return nil
}

var _ migration.Migration = (*AddPendingStatusAndActivationFields)(nil)

func addPendingEnumValue046(db sqlExecutor) error {
	// Guard: only ALTER TYPE if PENDING is not already an enum label.
	// ADD VALUE inside a transaction is allowed in PG 12+ as long as the
	// enum hasn't been used in the same transaction. We touch the enum
	// only in this step, so the constraint holds.
	const stmt = `
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_enum WHERE enumlabel = 'PENDING') THEN
        ALTER TYPE user_status ADD VALUE 'PENDING';
    END IF;
END
$$;
`
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add PENDING to user_status: %w", err)
	}
	return nil
}

func addActivationColumnsToUsers(db sqlExecutor) error {
	const stmt = `
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS activation_code        VARCHAR(255),
  ADD COLUMN IF NOT EXISTS activation_attempts    INT          DEFAULT 0,
  ADD COLUMN IF NOT EXISTS activation_expires_at TIMESTAMP,
  ADD COLUMN IF NOT EXISTS activated_at           TIMESTAMP;
`
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add activation columns to users: %w", err)
	}
	return nil
}

func createRateLimitsTable(db sqlExecutor) error {
	const stmt = `
CREATE TABLE IF NOT EXISTS rate_limits (
    id              SERIAL PRIMARY KEY,
    key_type        VARCHAR(50)  NOT NULL,
    key_value       VARCHAR(255) NOT NULL,
    action          VARCHAR(50)  NOT NULL,
    attempt_count   INT          DEFAULT 1,
    window_start    TIMESTAMP    NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMP    DEFAULT NOW(),
    updated_at      TIMESTAMP    DEFAULT NOW(),
    UNIQUE(key_type, key_value, action)
);
`
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("create rate_limits: %w", err)
	}
	return nil
}

func createRateLimitsAndActivationIndexes(db sqlExecutor) error {
	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_rate_limits_key
            ON rate_limits(key_type, key_value);`,
		`CREATE INDEX IF NOT EXISTS idx_users_activation_code
            ON users(activation_code) WHERE activation_code IS NOT NULL;`,
		`CREATE INDEX IF NOT EXISTS idx_users_activation_expires
            ON users(activation_expires_at) WHERE activation_expires_at IS NOT NULL;`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			return fmt.Errorf("create index: %w (stmt=%s)", err, s)
		}
	}
	return nil
}
