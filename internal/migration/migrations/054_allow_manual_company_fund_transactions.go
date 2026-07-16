package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type AllowManualCompanyFundTransactions struct{}

func (*AllowManualCompanyFundTransactions) Version() string { return "054" }

func (*AllowManualCompanyFundTransactions) Description() string {
	return "Allow explicitly sourced manual company-fund transactions"
}

func (*AllowManualCompanyFundTransactions) RequiredPreexistingVersion() string { return "053" }

func (*AllowManualCompanyFundTransactions) RequiredExpectedCeiling() string { return "054" }

func (*AllowManualCompanyFundTransactions) Up(*sql.DB) error {
	return fmt.Errorf("054 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*AllowManualCompanyFundTransactions) UpTx(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration054TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 054 timeouts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration054ReplaceConstraintsSQL); err != nil {
		return fmt.Errorf("expand manual transaction source constraints: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration054ValidateConstraintsSQL); err != nil {
		return fmt.Errorf("validate manual transaction source constraints: %w", err)
	}
	return nil
}

func (*AllowManualCompanyFundTransactions) Down(*sql.DB) error {
	return fmt.Errorf("054 is forward-only; manual transaction source support must be changed by a new migration")
}

var _ migration.Migration = (*AllowManualCompanyFundTransactions)(nil)
var _ migration.ControlledMigration = (*AllowManualCompanyFundTransactions)(nil)

const migration054TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration054ReplaceConstraintsSQL = `
ALTER TABLE public.company_fund_transactions
  DROP CONSTRAINT company_fund_transactions_channel_check,
  ADD CONSTRAINT company_fund_transactions_channel_check
    CHECK (channel IN ('SAFEHERON', 'AIRWALLEX', 'MANUAL')) NOT VALID,
  DROP CONSTRAINT company_fund_transactions_provider_fact_source_check,
  ADD CONSTRAINT company_fund_transactions_provider_fact_source_check
    CHECK (provider_fact_source IN ('WEBHOOK', 'PRODUCT_DETAIL', 'RECONCILIATION', 'MANUAL')) NOT VALID,
  DROP CONSTRAINT company_fund_transactions_first_seen_source_check,
  ADD CONSTRAINT company_fund_transactions_first_seen_source_check
    CHECK (first_seen_source IN ('WEBHOOK', 'RECONCILIATION', 'MANUAL')) NOT VALID,
  DROP CONSTRAINT company_fund_transactions_last_seen_source_check,
  ADD CONSTRAINT company_fund_transactions_last_seen_source_check
    CHECK (last_seen_source IN ('WEBHOOK', 'RECONCILIATION', 'MANUAL')) NOT VALID`

const migration054ValidateConstraintsSQL = `
ALTER TABLE public.company_fund_transactions
  VALIDATE CONSTRAINT company_fund_transactions_channel_check;
ALTER TABLE public.company_fund_transactions
  VALIDATE CONSTRAINT company_fund_transactions_provider_fact_source_check;
ALTER TABLE public.company_fund_transactions
  VALIDATE CONSTRAINT company_fund_transactions_first_seen_source_check;
ALTER TABLE public.company_fund_transactions
  VALIDATE CONSTRAINT company_fund_transactions_last_seen_source_check`
