package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

const migration053ConstraintName = "company_fund_transactions_safeheron_occurrence_required_check"

type EnforceSafeheronOccurrence struct{}

func (*EnforceSafeheronOccurrence) Version() string { return "053" }

func (*EnforceSafeheronOccurrence) Description() string {
	return "Enforce required Safeheron provider occurrence identity"
}

func (*EnforceSafeheronOccurrence) RequiredPreexistingVersion() string { return "052" }

func (*EnforceSafeheronOccurrence) RequiredExpectedCeiling() string { return "053" }

func (*EnforceSafeheronOccurrence) Up(*sql.DB) error {
	return fmt.Errorf("053 is controlled; run it through Migrator.MigrateWithExpectedCeiling so DDL and provenance commit atomically")
}

func (*EnforceSafeheronOccurrence) UpTx(tx *sql.Tx) error {
	return runMigration053(tx)
}

func (*EnforceSafeheronOccurrence) Down(*sql.DB) error {
	return fmt.Errorf("053 is forward-only; required Safeheron occurrence identity must be changed by a new migration")
}

var _ migration.Migration = (*EnforceSafeheronOccurrence)(nil)
var _ migration.ControlledMigration = (*EnforceSafeheronOccurrence)(nil)

type migration053Preflight struct {
	missing, wrongVersion, duplicate, invariant int64
}

func (result migration053Preflight) unsafe() bool {
	return result.missing != 0 || result.wrongVersion != 0 || result.duplicate != 0 || result.invariant != 0
}

func runMigration053(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration053TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration timeouts: %w", err)
	}
	var preflight migration053Preflight
	if err := tx.QueryRowContext(ctx, migration053PreflightSQL).Scan(&preflight.missing, &preflight.wrongVersion, &preflight.duplicate, &preflight.invariant); err != nil {
		return fmt.Errorf("preflight required Safeheron occurrence identity: %w", err)
	}
	if preflight.unsafe() {
		return fmt.Errorf("preflight rejected Safeheron occurrence state: missing=%d wrong_version=%d duplicate=%d invariant=%d", preflight.missing, preflight.wrongVersion, preflight.duplicate, preflight.invariant)
	}
	if _, err := tx.ExecContext(ctx, migration053AddConstraintSQL); err != nil {
		return fmt.Errorf("add required Safeheron occurrence constraint: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration053ValidateConstraintSQL); err != nil {
		return fmt.Errorf("validate required Safeheron occurrence constraint: %w", err)
	}
	return nil
}

const migration053TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration053PreflightSQL = `
SELECT
  COUNT(*) FILTER (
    WHERE provider_occurrence_key IS NULL OR btrim(provider_occurrence_key) = ''
  ) AS missing_count,
  COUNT(*) FILTER (
    WHERE provider_occurrence_algorithm_version IS DISTINCT FROM 'safeheron-occurrence-v1'
  ) AS wrong_version_count,
  (
    SELECT COUNT(*)
    FROM (
      SELECT provider_occurrence_key
      FROM public.company_fund_transactions
      WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL
      GROUP BY provider_occurrence_key
      HAVING COUNT(*) > 1
    ) AS duplicate_occurrences
  ) AS duplicate_count,
  COUNT(*) FILTER (
    WHERE provider_occurrence_key IS NOT NULL
      AND btrim(provider_occurrence_key) <> ''
      AND provider_occurrence_key !~ '^safeheron-occurrence-v1:[0-9a-f]{64}$'
  ) AS invariant_count
FROM public.company_fund_transactions
WHERE channel = 'SAFEHERON'`

const migration053AddConstraintSQL = `
ALTER TABLE public.company_fund_transactions
  ADD CONSTRAINT company_fund_transactions_safeheron_occurrence_required_check
  CHECK (
    channel <> 'SAFEHERON'
    OR (
      provider_occurrence_key IS NOT NULL
      AND btrim(provider_occurrence_key) <> ''
      AND provider_occurrence_algorithm_version = 'safeheron-occurrence-v1'
    )
  ) NOT VALID`

const migration053ValidateConstraintSQL = `
ALTER TABLE public.company_fund_transactions
  VALIDATE CONSTRAINT company_fund_transactions_safeheron_occurrence_required_check`
