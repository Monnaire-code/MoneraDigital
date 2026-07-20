package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// AllowOtherCompanyFundAccounts permits manually maintained company accounts
// without widening provider-owned transaction sources or automation tables.
type AllowOtherCompanyFundAccounts struct{}

func (*AllowOtherCompanyFundAccounts) Version() string { return "059" }

func (*AllowOtherCompanyFundAccounts) Description() string {
	return "Allow manually maintained OTHER company-fund accounts"
}

func (*AllowOtherCompanyFundAccounts) RequiredPreexistingVersion() string { return "058" }

func (*AllowOtherCompanyFundAccounts) RequiredExpectedCeiling() string { return "059" }

func (*AllowOtherCompanyFundAccounts) Up(*sql.DB) error {
	return fmt.Errorf("059 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*AllowOtherCompanyFundAccounts) UpTx(tx *sql.Tx) error {
	return runMigration059(tx)
}

func runMigration059(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration059TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 059 timeouts: %w", err)
	}
	var violations int64
	if err := tx.QueryRowContext(ctx, migration059PreflightSQL).Scan(&violations); err != nil {
		return fmt.Errorf("preflight migration 059 OTHER account identity: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("preflight rejected OTHER account identity migration: violations=%d", violations)
	}
	if _, err := tx.ExecContext(ctx, migration059ReplaceAccountConstraintsSQL); err != nil {
		return fmt.Errorf("expand OTHER account constraints: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration059ValidateAccountConstraintsSQL); err != nil {
		return fmt.Errorf("validate OTHER account constraints: %w", err)
	}
	return nil
}

func (*AllowOtherCompanyFundAccounts) Down(*sql.DB) error {
	return fmt.Errorf("059 is forward-only; OTHER account support must be changed by a new migration")
}

var _ migration.Migration = (*AllowOtherCompanyFundAccounts)(nil)
var _ migration.ControlledMigration = (*AllowOtherCompanyFundAccounts)(nil)

const migration059TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration059PreflightSQL = `
SELECT
  CASE WHEN to_regclass('public.company_fund_accounts') IS NULL THEN 1 ELSE 0 END
  + CASE WHEN EXISTS (
      SELECT 1 FROM pg_constraint
      WHERE conrelid = 'public.company_fund_accounts'::regclass
        AND conname = 'company_fund_accounts_channel_check'
    ) THEN 0 ELSE 1 END
  + CASE WHEN EXISTS (
      SELECT 1 FROM pg_constraint
      WHERE conrelid = 'public.company_fund_accounts'::regclass
        AND conname = 'company_fund_accounts_check'
    ) THEN 0 ELSE 1 END
  + (SELECT count(*) FROM (
      SELECT provider_account_key
      FROM public.company_fund_accounts
      WHERE channel = 'OTHER' AND provider_account_key IS NOT NULL
      GROUP BY provider_account_key
      HAVING count(*) > 1
    ) duplicate_other_identity)
  + CASE WHEN EXISTS (
      SELECT 1 FROM pg_indexes
      WHERE schemaname = 'public'
        AND indexname = 'idx_company_fund_accounts_other_identity'
    ) THEN 1 ELSE 0 END`

const migration059ReplaceAccountConstraintsSQL = `
ALTER TABLE public.company_fund_accounts
  DROP CONSTRAINT company_fund_accounts_channel_check,
  ADD CONSTRAINT company_fund_accounts_channel_check
    CHECK (channel IN ('SAFEHERON', 'AIRWALLEX', 'OTHER')) NOT VALID,
  DROP CONSTRAINT company_fund_accounts_check,
	  ADD CONSTRAINT company_fund_accounts_check
    CHECK (
      (channel = 'SAFEHERON' AND normalized_address IS NOT NULL AND network_family IS NOT NULL)
      OR (channel = 'AIRWALLEX' AND provider_account_key IS NOT NULL)
      OR (
        channel = 'OTHER'
        AND provider_account_key IS NOT NULL
        AND btrim(provider_account_key) <> ''
        AND wallet_address IS NULL
        AND normalized_address IS NULL
        AND network_family IS NULL
      )
	    ) NOT VALID;

CREATE UNIQUE INDEX idx_company_fund_accounts_other_identity
  ON public.company_fund_accounts (channel, provider_account_key)
  WHERE channel = 'OTHER' AND provider_account_key IS NOT NULL`

const migration059ValidateAccountConstraintsSQL = `
ALTER TABLE public.company_fund_accounts
  VALIDATE CONSTRAINT company_fund_accounts_channel_check;
ALTER TABLE public.company_fund_accounts
  VALIDATE CONSTRAINT company_fund_accounts_check`
