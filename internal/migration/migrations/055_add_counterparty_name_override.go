package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type AddCounterpartyNameOverride struct{}

func (*AddCounterpartyNameOverride) Version() string { return "055" }

func (*AddCounterpartyNameOverride) Description() string {
	return "Add finance-owned company-fund counterparty name override"
}

func (*AddCounterpartyNameOverride) RequiredPreexistingVersion() string { return "054" }

func (*AddCounterpartyNameOverride) RequiredExpectedCeiling() string { return "055" }

func (*AddCounterpartyNameOverride) Up(*sql.DB) error {
	return fmt.Errorf("055 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*AddCounterpartyNameOverride) UpTx(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration055TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 055 timeouts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration055AddColumnSQL); err != nil {
		return fmt.Errorf("add counterparty name override: %w", err)
	}
	return nil
}

func (*AddCounterpartyNameOverride) Down(*sql.DB) error {
	return fmt.Errorf("055 is forward-only; counterparty name override must be changed by a new migration")
}

var _ migration.Migration = (*AddCounterpartyNameOverride)(nil)
var _ migration.ControlledMigration = (*AddCounterpartyNameOverride)(nil)

const migration055TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration055AddColumnSQL = `ALTER TABLE public.company_fund_transactions
  ADD COLUMN counterparty_name_override VARCHAR(256)`
