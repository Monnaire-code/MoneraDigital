package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/migration"

	"github.com/shopspring/decimal"
)

type ExpandCompanyFundOccurrenceAndManualValuation struct{}

func (*ExpandCompanyFundOccurrenceAndManualValuation) Version() string { return "052" }

func (*ExpandCompanyFundOccurrenceAndManualValuation) Description() string {
	return "Expand company-fund Safeheron occurrence aliases and MANUAL valuation integrity"
}

func (*ExpandCompanyFundOccurrenceAndManualValuation) RequiredPreexistingVersion() string {
	return "051"
}

func (*ExpandCompanyFundOccurrenceAndManualValuation) RequiredExpectedCeiling() string {
	return "052"
}

func (*ExpandCompanyFundOccurrenceAndManualValuation) Up(*sql.DB) error {
	return fmt.Errorf("052 is controlled; run it through Migrator.MigrateWithExpectedCeiling so DDL and provenance commit atomically")
}

func (*ExpandCompanyFundOccurrenceAndManualValuation) UpTx(tx *sql.Tx) error {
	return runMigration052(tx)
}

func (*ExpandCompanyFundOccurrenceAndManualValuation) Down(*sql.DB) error {
	return fmt.Errorf("052 is forward-only; occurrence aliases and MANUAL history integrity must be repaired with a new migration")
}

var _ migration.Migration = (*ExpandCompanyFundOccurrenceAndManualValuation)(nil)
var _ migration.ControlledMigration = (*ExpandCompanyFundOccurrenceAndManualValuation)(nil)

func runMigration052(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration052TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration timeouts: %w", err)
	}
	missing, err := migration052Count(tx, migration052PreflightQuery)
	if err != nil {
		return fmt.Errorf("preflight Safeheron v1 occurrence tuples: %w", err)
	}
	if missing != 0 {
		return fmt.Errorf("preflight found %d incomplete Safeheron v1 occurrence tuples", missing)
	}
	if _, err := tx.ExecContext(ctx, migration052AddOccurrenceColumnsSQL); err != nil {
		return fmt.Errorf("add nullable occurrence columns: %w", err)
	}
	if _, err := tx.ExecContext(ctx, migration052SchemaDDL); err != nil {
		return fmt.Errorf("install occurrence index and MANUAL valuation integrity: %w", err)
	}
	if err := migration052Backfill(tx); err != nil {
		return err
	}
	if err := migration052ValidateAliases(tx); err != nil {
		return err
	}
	return nil
}

func migration052Count(tx *sql.Tx, query string) (int64, error) {
	var count int64
	if err := tx.QueryRowContext(context.Background(), query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func migration052Backfill(tx *sql.Tx) error {
	rows, err := tx.QueryContext(context.Background(), migration052BackfillQuery)
	if err != nil {
		return fmt.Errorf("query Safeheron v1 rows for occurrence backfill: %w", err)
	}
	backfillRows, err := readMigration052BackfillRows(rows)
	if err != nil {
		return err
	}
	for _, row := range backfillRows {
		if err := updateMigration052Occurrence(tx, row); err != nil {
			return err
		}
	}
	return nil
}

type migration052RowSet interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func readMigration052BackfillRows(rows migration052RowSet) ([]migration052BackfillRow, error) {
	backfillRows := make([]migration052BackfillRow, 0)
	for rows.Next() {
		row, err := scanMigration052BackfillRow(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		backfillRows = append(backfillRows, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate Safeheron v1 occurrence rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close Safeheron v1 occurrence rows: %w", err)
	}
	return backfillRows, nil
}

type migration052BackfillRow struct {
	id                    int64
	providerTransactionID string
	movementKind          string
	rawCoinKey            string
	from                  string
	to                    string
	amount                string
	transferMode          string
	movementIndex         int
	networkFamily         string
}

func scanMigration052BackfillRow(rows migration052RowSet) (migration052BackfillRow, error) {
	var row migration052BackfillRow
	if err := rows.Scan(&row.id, &row.providerTransactionID, &row.movementKind, &row.rawCoinKey, &row.from, &row.to, &row.amount, &row.transferMode, &row.movementIndex, &row.networkFamily); err != nil {
		return migration052BackfillRow{}, fmt.Errorf("scan Safeheron v1 occurrence row: %w", err)
	}
	return row, nil
}

func updateMigration052Occurrence(tx *sql.Tx, row migration052BackfillRow) error {
	amount, err := decimal.NewFromString(row.amount)
	if err != nil {
		return fmt.Errorf("parse Safeheron v1 row %d amount: %w", row.id, err)
	}
	from, err := companyfund.NormalizeSafeheronOccurrenceAddress(row.networkFamily, row.from)
	if err != nil {
		return fmt.Errorf("normalize Safeheron v1 row %d source: %w", row.id, err)
	}
	to, err := companyfund.NormalizeSafeheronOccurrenceAddress(row.networkFamily, row.to)
	if err != nil {
		return fmt.Errorf("normalize Safeheron v1 row %d destination: %w", row.id, err)
	}
	occurrence, err := companyfund.BuildSafeheronOccurrence(companyfund.SafeheronOccurrenceInput{
		ProviderTransactionKey: row.providerTransactionID,
		MovementKind:           companyfund.MovementKind(row.movementKind),
		RawCoinKey:             row.rawCoinKey,
		NormalizedSource:       from,
		NormalizedDestination:  to,
		Amount:                 amount,
		TransferMode:           companyfund.TransferMode(row.transferMode),
		MovementIndex:          row.movementIndex,
	})
	if err != nil {
		return fmt.Errorf("canonicalize Safeheron v1 row %d: %w", row.id, err)
	}
	result, err := tx.ExecContext(context.Background(), migration052BackfillUpdate, occurrence.Key, occurrence.AlgorithmVersion, row.id)
	if err != nil {
		return fmt.Errorf("backfill Safeheron v1 row %d: %w", row.id, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Safeheron v1 row %d backfill: %w", row.id, err)
	}
	if updated != 1 {
		return fmt.Errorf("backfill Safeheron v1 row %d updated %d rows, want 1", row.id, updated)
	}
	return nil
}

func migration052ValidateAliases(tx *sql.Tx) error {
	missing, err := migration052Count(tx, migration052MissingAliasQuery)
	if err != nil {
		return fmt.Errorf("validate missing Safeheron occurrence aliases: %w", err)
	}
	if missing != 0 {
		return fmt.Errorf("backfill left %d missing Safeheron occurrence aliases", missing)
	}
	duplicates, err := migration052Count(tx, migration052DuplicateAliasQuery)
	if err != nil {
		return fmt.Errorf("validate duplicate Safeheron occurrence aliases: %w", err)
	}
	if duplicates != 0 {
		return fmt.Errorf("backfill found %d duplicate Safeheron occurrence aliases", duplicates)
	}
	return nil
}

const migration052TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration052PreflightQuery = `
SELECT COUNT(*)
FROM public.company_fund_transactions AS movement
LEFT JOIN public.company_fund_accounts AS from_account ON from_account.id = movement.from_company_fund_account_id
LEFT JOIN public.company_fund_accounts AS to_account ON to_account.id = movement.to_company_fund_account_id
WHERE movement.channel = 'SAFEHERON'
  AND movement.identity_algorithm_version = 'v1'
  AND (
    movement.provider_transaction_id IS NULL OR btrim(movement.provider_transaction_id) = ''
    OR movement.movement_kind IS NULL OR btrim(movement.movement_kind) = ''
    OR movement.provider_asset_key IS NULL OR btrim(movement.provider_asset_key) = ''
    OR movement.from_address_or_account IS NULL OR btrim(movement.from_address_or_account) = ''
    OR movement.to_address_or_account IS NULL OR btrim(movement.to_address_or_account) = ''
    OR movement.amount IS NULL OR movement.amount < 0
    OR movement.transfer_mode IS NULL OR btrim(movement.transfer_mode) = ''
    OR movement.movement_index IS NULL OR movement.movement_index < 0
    OR COALESCE(from_account.network_family, to_account.network_family) IS NULL
    OR (from_account.network_family IS NOT NULL AND to_account.network_family IS NOT NULL
        AND from_account.network_family IS DISTINCT FROM to_account.network_family)
  )`

const migration052AddOccurrenceColumnsSQL = `
ALTER TABLE public.company_fund_transactions
  ADD COLUMN provider_occurrence_key VARCHAR(256),
  ADD COLUMN provider_occurrence_algorithm_version VARCHAR(64);`

const migration052BackfillQuery = `
SELECT movement.id, movement.provider_transaction_id, movement.movement_kind, movement.provider_asset_key,
       movement.from_address_or_account, movement.to_address_or_account, movement.amount::text,
       movement.transfer_mode, movement.movement_index,
       COALESCE(from_account.network_family, to_account.network_family) AS network_family
FROM public.company_fund_transactions AS movement
LEFT JOIN public.company_fund_accounts AS from_account ON from_account.id = movement.from_company_fund_account_id
LEFT JOIN public.company_fund_accounts AS to_account ON to_account.id = movement.to_company_fund_account_id
WHERE movement.channel = 'SAFEHERON'
  AND movement.identity_algorithm_version = 'v1'
ORDER BY movement.id
FOR UPDATE OF movement`

const migration052BackfillUpdate = `
UPDATE public.company_fund_transactions
SET provider_occurrence_key = $1,
    provider_occurrence_algorithm_version = $2
WHERE id = $3`

const migration052MissingAliasQuery = `
SELECT COUNT(*)
FROM public.company_fund_transactions
WHERE channel = 'SAFEHERON'
  AND identity_algorithm_version = 'v1'
  AND (provider_occurrence_key IS NULL
       OR provider_occurrence_algorithm_version IS DISTINCT FROM 'safeheron-occurrence-v1')`

const migration052DuplicateAliasQuery = `
SELECT COUNT(*)
FROM (
  SELECT provider_occurrence_key
  FROM public.company_fund_transactions
  WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL
  GROUP BY provider_occurrence_key
  HAVING COUNT(*) > 1
) AS duplicate_occurrences`

var migration052ProtectedProjectionColumns = []string{
	"provider_reported_usd_value", "calculated_usd_value", "usd_value", "usd_unit_price",
	"usd_valuation_status", "usd_valuation_reason_code", "usd_valuation_basis", "usd_valuation_time",
	"usd_valuation_price_at", "usd_valuation_source", "usd_valuation_method", "usd_valuation_granularity",
	"usd_provider_value_scope", "usd_derivation_method", "usd_rate_snapshot_id", "current_valuation_history_id",
	"usd_valued_at", "usd_valuation_policy_version", "usd_valuation_version",
}

const migration052SchemaDDL = `
CREATE UNIQUE INDEX idx_company_fund_transactions_safeheron_occurrence
  ON public.company_fund_transactions (provider_occurrence_key)
  WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL;

ALTER TABLE public.company_fund_transactions
  DROP CONSTRAINT IF EXISTS company_fund_transactions_usd_valuation_source_check;
ALTER TABLE public.company_fund_transactions
  ADD CONSTRAINT company_fund_transactions_usd_valuation_source_check
  CHECK (usd_valuation_source IS NULL OR usd_valuation_source IN ('SAFEHERON', 'AIRWALLEX', 'COINGECKO', 'USD_PAR', 'MANUAL'));

ALTER TABLE public.company_fund_transaction_valuation_history
  ADD COLUMN manual_reason TEXT,
  ADD COLUMN manual_updated_by VARCHAR(256),
  ADD COLUMN manual_updated_at TIMESTAMPTZ;
ALTER TABLE public.company_fund_transaction_valuation_history
  ADD CONSTRAINT company_fund_valuation_history_source_check
  CHECK (usd_valuation_source IS NULL OR usd_valuation_source IN ('SAFEHERON', 'AIRWALLEX', 'COINGECKO', 'USD_PAR', 'MANUAL'));
ALTER TABLE public.company_fund_transaction_valuation_history
  ADD CONSTRAINT company_fund_valuation_history_manual_metadata_check
  CHECK (
    usd_valuation_source IS DISTINCT FROM 'MANUAL'
    OR (
      usd_valuation_method = 'MANUAL_TOTAL'
      AND usd_valuation_status = 'FINAL'
      AND usd_valuation_reason_code = 'MANUAL_OVERRIDE'
      AND valuation_policy_version = 'MANUAL_V1'
      AND transition_trigger = 'MANUAL_ADMIN'
      AND manual_reason IS NOT NULL AND btrim(manual_reason) <> ''
      AND manual_updated_by IS NOT NULL AND btrim(manual_updated_by) <> ''
      AND manual_updated_at IS NOT NULL
      AND manual_updated_at = applied_at
      AND usd_value IS NOT NULL
      AND usd_valuation_basis IS NULL
      AND usd_valuation_time IS NULL
      AND usd_valuation_price_at IS NULL
      AND usd_valuation_granularity IS NULL
      AND usd_rate_snapshot_id IS NULL
      AND usd_derivation_method IS NULL
    )
  );

CREATE OR REPLACE FUNCTION public.company_fund_enforce_manual_valuation_projection()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
  current_history public.company_fund_transaction_valuation_history%ROWTYPE;
  previous_max_version BIGINT;
BEGIN
  IF ROW(
    OLD.provider_reported_usd_value, OLD.calculated_usd_value, OLD.usd_value, OLD.usd_unit_price,
    OLD.usd_valuation_status, OLD.usd_valuation_reason_code, OLD.usd_valuation_basis, OLD.usd_valuation_time,
    OLD.usd_valuation_price_at, OLD.usd_valuation_source, OLD.usd_valuation_method, OLD.usd_valuation_granularity,
    OLD.usd_provider_value_scope, OLD.usd_derivation_method, OLD.usd_rate_snapshot_id, OLD.current_valuation_history_id,
    OLD.usd_valued_at, OLD.usd_valuation_policy_version, OLD.usd_valuation_version
  ) IS NOT DISTINCT FROM ROW(
    NEW.provider_reported_usd_value, NEW.calculated_usd_value, NEW.usd_value, NEW.usd_unit_price,
    NEW.usd_valuation_status, NEW.usd_valuation_reason_code, NEW.usd_valuation_basis, NEW.usd_valuation_time,
    NEW.usd_valuation_price_at, NEW.usd_valuation_source, NEW.usd_valuation_method, NEW.usd_valuation_granularity,
    NEW.usd_provider_value_scope, NEW.usd_derivation_method, NEW.usd_rate_snapshot_id, NEW.current_valuation_history_id,
    NEW.usd_valued_at, NEW.usd_valuation_policy_version, NEW.usd_valuation_version
  ) THEN
    RETURN NEW;
  END IF;

  IF OLD.usd_valuation_source IS DISTINCT FROM 'MANUAL'
     AND NEW.usd_valuation_source IS DISTINCT FROM 'MANUAL' THEN
    RETURN NEW;
  END IF;
  IF OLD.usd_valuation_source = 'MANUAL'
     AND NEW.usd_valuation_source IS DISTINCT FROM 'MANUAL' THEN
    RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: MANUAL valuation cannot be replaced'
      USING ERRCODE = 'P7501';
  END IF;
  IF NEW.usd_valuation_source IS DISTINCT FROM 'MANUAL'
     OR NEW.current_valuation_history_id IS NULL THEN
    RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: MANUAL projection requires current history'
      USING ERRCODE = 'P7501';
  END IF;
  IF NEW.provider_reported_usd_value IS DISTINCT FROM OLD.provider_reported_usd_value
     OR NEW.calculated_usd_value IS DISTINCT FROM OLD.calculated_usd_value
     OR NEW.usd_provider_value_scope IS DISTINCT FROM OLD.usd_provider_value_scope
     OR NEW.provider_transaction_fact_id IS DISTINCT FROM OLD.provider_transaction_fact_id THEN
    RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: MANUAL transition must preserve Provider evidence'
      USING ERRCODE = 'P7501';
  END IF;

  SELECT * INTO current_history
  FROM public.company_fund_transaction_valuation_history
  WHERE id = NEW.current_valuation_history_id AND transaction_id = NEW.id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: current history ownership mismatch'
      USING ERRCODE = 'P7501';
  END IF;

  IF current_history.usd_valuation_source IS DISTINCT FROM 'MANUAL'
     OR current_history.usd_valuation_method IS DISTINCT FROM 'MANUAL_TOTAL'
     OR current_history.usd_valuation_status IS DISTINCT FROM 'FINAL'
     OR current_history.usd_valuation_reason_code IS DISTINCT FROM 'MANUAL_OVERRIDE'
     OR current_history.valuation_policy_version IS DISTINCT FROM 'MANUAL_V1'
     OR current_history.transition_trigger IS DISTINCT FROM 'MANUAL_ADMIN'
     OR current_history.manual_reason IS NULL OR btrim(current_history.manual_reason) = ''
     OR current_history.manual_updated_by IS NULL OR btrim(current_history.manual_updated_by) = ''
     OR current_history.manual_updated_at IS NULL
     OR current_history.manual_updated_at IS DISTINCT FROM current_history.applied_at
     OR NEW.provider_reported_usd_value IS DISTINCT FROM current_history.provider_reported_usd_value
     OR NEW.calculated_usd_value IS DISTINCT FROM current_history.calculated_usd_value
     OR NEW.usd_value IS DISTINCT FROM current_history.usd_value
     OR NEW.usd_unit_price IS DISTINCT FROM current_history.usd_unit_price
     OR NEW.usd_valuation_status IS DISTINCT FROM current_history.usd_valuation_status
     OR NEW.usd_valuation_reason_code IS DISTINCT FROM current_history.usd_valuation_reason_code
     OR NEW.usd_valuation_basis IS DISTINCT FROM current_history.usd_valuation_basis
     OR NEW.usd_valuation_time IS DISTINCT FROM current_history.usd_valuation_time
     OR NEW.usd_valuation_price_at IS DISTINCT FROM current_history.usd_valuation_price_at
     OR NEW.usd_valuation_source IS DISTINCT FROM current_history.usd_valuation_source
     OR NEW.usd_valuation_method IS DISTINCT FROM current_history.usd_valuation_method
     OR NEW.usd_valuation_granularity IS DISTINCT FROM current_history.usd_valuation_granularity
     OR NEW.usd_provider_value_scope IS DISTINCT FROM current_history.usd_provider_value_scope
     OR NEW.usd_derivation_method IS DISTINCT FROM current_history.usd_derivation_method
     OR NEW.usd_rate_snapshot_id IS DISTINCT FROM current_history.usd_rate_snapshot_id
     OR NEW.current_valuation_history_id IS DISTINCT FROM current_history.id
     OR NEW.usd_valued_at IS DISTINCT FROM current_history.applied_at
     OR NEW.usd_valuation_policy_version IS DISTINCT FROM current_history.valuation_policy_version
     OR NEW.usd_valuation_version IS DISTINCT FROM current_history.valuation_version
     OR NEW.provider_transaction_fact_id IS DISTINCT FROM current_history.provider_transaction_fact_id THEN
    RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: projection and MANUAL history mismatch'
      USING ERRCODE = 'P7501';
  END IF;

  IF OLD.usd_valuation_source = 'MANUAL' THEN
    IF current_history.supersedes_history_id IS DISTINCT FROM OLD.current_valuation_history_id
       OR current_history.valuation_version IS DISTINCT FROM OLD.usd_valuation_version + 1 THEN
      RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: invalid MANUAL supersedes/version transition'
        USING ERRCODE = 'P7501';
    END IF;
  ELSE
    SELECT COALESCE(MAX(valuation_version), 0) INTO previous_max_version
    FROM public.company_fund_transaction_valuation_history
    WHERE transaction_id = NEW.id AND id <> current_history.id;
    IF current_history.valuation_version IS DISTINCT FROM previous_max_version + 1
       OR current_history.supersedes_history_id IS DISTINCT FROM OLD.current_valuation_history_id THEN
      RAISE EXCEPTION 'COMPANY_FUND_MANUAL_PROJECTION_INTEGRITY: invalid initial MANUAL version transition'
        USING ERRCODE = 'P7501';
    END IF;
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER company_fund_enforce_manual_valuation_projection
BEFORE UPDATE OF
  provider_reported_usd_value, calculated_usd_value, usd_value, usd_unit_price,
  usd_valuation_status, usd_valuation_reason_code, usd_valuation_basis, usd_valuation_time,
  usd_valuation_price_at, usd_valuation_source, usd_valuation_method, usd_valuation_granularity,
  usd_provider_value_scope, usd_derivation_method, usd_rate_snapshot_id, current_valuation_history_id,
  usd_valued_at, usd_valuation_policy_version, usd_valuation_version
ON public.company_fund_transactions
FOR EACH ROW EXECUTE FUNCTION public.company_fund_enforce_manual_valuation_projection();`
