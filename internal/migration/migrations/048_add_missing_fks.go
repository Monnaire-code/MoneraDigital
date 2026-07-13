// internal/migration/migrations/048_add_missing_fks.go
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AddMissingForeignKeys adds foreign keys to three columns that the
// original migrations forgot to constrain:
//
//   - withdrawal_verification.withdrawal_order_id  -> withdrawal_order(id)
//   - withdrawal_freeze_log.order_id               -> withdrawal_order(id), if a
//     legacy schema actually has that column
//   - address_pool.assigned_user_id                 -> users(id)
//
// ON DELETE semantics:
//   - withdrawal_verification: NO ACTION (default). Verification records
//     are tightly coupled to their order; deleting an order with
//     verifications should fail loudly. The application should not be
//     hard-deleting withdrawal_order rows anyway.
//   - withdrawal_freeze_log: SET NULL. Freeze logs are audit records
//     and must survive order deletion; nulling the FK preserves the
//     log row.
//   - address_pool.assigned_user_id: SET NULL. The pool is
//     re-assignable; if a user is hard-deleted, the address returns
//     to the available pool (status handling is app-side).
//
// Idempotency: each ADD CONSTRAINT is guarded by a pg_constraint
// precheck so a re-run is a no-op.
type AddMissingForeignKeys struct{}

func (m *AddMissingForeignKeys) Version() string { return "048" }

func (m *AddMissingForeignKeys) Description() string {
	return "Add missing foreign keys on withdrawal_verification.withdrawal_order_id, optional legacy withdrawal_freeze_log.order_id, and address_pool.assigned_user_id (H-2)"
}

func (m *AddMissingForeignKeys) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.Tx) error
	}{
		{"precheck_orphans", precheck048Orphans},
		{"add_fk_withdrawal_verification", addFKWithdrawalVerification},
		{"add_fk_withdrawal_freeze_log", addFKWithdrawalFreezeLog},
		{"add_fk_address_pool", addFKAddressPool},
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

func (m *AddMissingForeignKeys) Down(db *sql.DB) error {
	stmts := []string{
		`ALTER TABLE address_pool DROP CONSTRAINT IF EXISTS fk_address_pool_user;`,
		`ALTER TABLE withdrawal_freeze_log DROP CONSTRAINT IF EXISTS fk_withdrawal_freeze_log_order;`,
		`ALTER TABLE withdrawal_verification DROP CONSTRAINT IF EXISTS fk_withdrawal_verification_order;`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("048 Down: %s: %w", s, err)
		}
	}
	return nil
}

var _ migration.Migration = (*AddMissingForeignKeys)(nil)

func precheck048Orphans(tx *sql.Tx) error {
	// Verify existence of all three parent tables before referencing
	// them; the migration chain that creates them may not have been
	// run on a partial database.
	stmt := `
DO $$
DECLARE
    parent_missing TEXT;
    bad_count INTEGER;
BEGIN
    -- Guard the references themselves: if a parent table is missing
    -- entirely, the ALTER TABLE below would fail with a confusing
    -- "relation does not exist" error. Surface a clear message instead.
    SELECT string_agg(t, ', ' ORDER BY t) INTO parent_missing
    FROM (VALUES
        ('withdrawal_order'),
        ('users')
    ) AS needed(t)
    WHERE NOT EXISTS (SELECT 1 FROM pg_tables WHERE tablename = needed.t);

    IF parent_missing IS NOT NULL THEN
        RAISE EXCEPTION 'H-2: required parent tables missing: %', parent_missing;
    END IF;

    -- withdrawal_verification.withdrawal_order_id orphans
    SELECT COUNT(*) INTO bad_count
    FROM withdrawal_verification wv
    WHERE wv.withdrawal_order_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM withdrawal_order wo WHERE wo.id = wv.withdrawal_order_id);
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-2: cannot add FK — % withdrawal_verification rows have no matching withdrawal_order', bad_count;
    END IF;

    -- Some legacy schemas have no withdrawal_freeze_log.order_id. Do not
    -- fabricate a relationship that the audit table never recorded.
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'withdrawal_freeze_log'
          AND column_name = 'order_id'
    ) THEN
        SELECT COUNT(*) INTO bad_count
        FROM withdrawal_freeze_log wfl
        WHERE wfl.order_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM withdrawal_order wo WHERE wo.id = wfl.order_id);
        IF bad_count > 0 THEN
            RAISE EXCEPTION 'H-2: cannot add FK — % withdrawal_freeze_log rows have no matching withdrawal_order', bad_count;
        END IF;
    END IF;

    -- address_pool.assigned_user_id orphans
    SELECT COUNT(*) INTO bad_count
    FROM address_pool ap
    WHERE ap.assigned_user_id IS NOT NULL
      AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = ap.assigned_user_id);
    IF bad_count > 0 THEN
        RAISE EXCEPTION 'H-2: cannot add FK — % address_pool rows have no matching users', bad_count;
    END IF;
END $$;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("precheck orphans: %w", err)
	}
	return nil
}

func addFKWithdrawalVerification(tx *sql.Tx) error {
	stmt := `
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_withdrawal_verification_order'
    ) THEN
        ALTER TABLE withdrawal_verification
            ADD CONSTRAINT fk_withdrawal_verification_order
            FOREIGN KEY (withdrawal_order_id) REFERENCES withdrawal_order(id);
    END IF;
END $$;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add FK withdrawal_verification: %w", err)
	}
	return nil
}

func addFKWithdrawalFreezeLog(tx *sql.Tx) error {
	stmt := `
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'withdrawal_freeze_log'
          AND column_name = 'order_id'
    ) AND NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_withdrawal_freeze_log_order'
    ) THEN
        ALTER TABLE withdrawal_freeze_log
            ADD CONSTRAINT fk_withdrawal_freeze_log_order
            FOREIGN KEY (order_id) REFERENCES withdrawal_order(id)
            ON DELETE SET NULL;
    END IF;
END $$;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add FK withdrawal_freeze_log: %w", err)
	}
	return nil
}

func addFKAddressPool(tx *sql.Tx) error {
	stmt := `
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'fk_address_pool_user'
    ) THEN
        ALTER TABLE address_pool
            ADD CONSTRAINT fk_address_pool_user
            FOREIGN KEY (assigned_user_id) REFERENCES users(id)
            ON DELETE SET NULL;
    END IF;
END $$;`
	if _, err := tx.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("add FK address_pool: %w", err)
	}
	return nil
}
