package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AddManualTransactionVoidColumns stores Effective Book void metadata for
// Manual Transaction correction. Application void rules live in MoneraDigitalMgt.
type AddManualTransactionVoidColumns struct{}

func (*AddManualTransactionVoidColumns) Version() string { return "060" }

func (*AddManualTransactionVoidColumns) Description() string {
	return "Add nullable void metadata columns for Voided Manual Transactions"
}

func (*AddManualTransactionVoidColumns) RequiredPreexistingVersion() string { return "059" }

func (*AddManualTransactionVoidColumns) RequiredExpectedCeiling() string { return "060" }

func (*AddManualTransactionVoidColumns) Up(*sql.DB) error {
	return fmt.Errorf("060 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*AddManualTransactionVoidColumns) UpTx(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration060TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 060 timeouts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration060AddColumnsSQL); err != nil {
		return fmt.Errorf("add manual transaction void columns: %w", err)
	}
	return nil
}

func (*AddManualTransactionVoidColumns) Down(*sql.DB) error {
	return fmt.Errorf("060 is forward-only; void metadata columns must be changed by a new migration")
}

var _ migration.Migration = (*AddManualTransactionVoidColumns)(nil)
var _ migration.ControlledMigration = (*AddManualTransactionVoidColumns)(nil)

const migration060TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

// Effective Book predicate for consumers: voided_at IS NULL.
// voided_by has no FK (loose coupling with management-app actors).
const migration060AddColumnsSQL = `ALTER TABLE public.company_fund_transactions
  ADD COLUMN voided_at TIMESTAMPTZ,
  ADD COLUMN voided_by BIGINT,
  ADD COLUMN void_reason TEXT`
