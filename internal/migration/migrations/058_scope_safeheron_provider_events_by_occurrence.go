package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

type ScopeSafeheronProviderEventsByOccurrence struct{}

func (*ScopeSafeheronProviderEventsByOccurrence) Version() string { return "058" }

func (*ScopeSafeheronProviderEventsByOccurrence) Description() string {
	return "Scope routed Safeheron provider-event idempotency by webhook occurrence"
}

func (*ScopeSafeheronProviderEventsByOccurrence) RequiredPreexistingVersion() string { return "057" }

func (*ScopeSafeheronProviderEventsByOccurrence) RequiredExpectedCeiling() string { return "058" }

func (*ScopeSafeheronProviderEventsByOccurrence) Up(*sql.DB) error {
	return fmt.Errorf("058 is controlled; run it through Migrator.MigrateWithExpectedCeiling")
}

func (*ScopeSafeheronProviderEventsByOccurrence) UpTx(tx *sql.Tx) error {
	return runMigration058(tx)
}

func (*ScopeSafeheronProviderEventsByOccurrence) Down(*sql.DB) error {
	return fmt.Errorf("058 is forward-only; provider-event idempotency must be changed by a new migration")
}

var _ migration.Migration = (*ScopeSafeheronProviderEventsByOccurrence)(nil)
var _ migration.ControlledMigration = (*ScopeSafeheronProviderEventsByOccurrence)(nil)

func runMigration058(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration058TimeoutsSQL); err != nil {
		return fmt.Errorf("configure migration 058 timeouts: %w", err)
	}
	var violations int64
	if err := tx.QueryRowContext(ctx, migration058PreflightSQL).Scan(&violations); err != nil {
		return fmt.Errorf("preflight migration 058 provider-event identity: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("preflight rejected provider-event identity migration: violations=%d", violations)
	}
	if _, err := tx.ExecContext(ctx, migration058SchemaSQL); err != nil {
		return fmt.Errorf("apply migration 058 provider-event identity: %w", err)
	}
	return nil
}

const migration058TimeoutsSQL = `SET LOCAL search_path = pg_catalog, public; SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '30s'; SET LOCAL idle_in_transaction_session_timeout = '30s';`

const migration058PreflightSQL = `
SELECT
  CASE WHEN EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE schemaname='public'
      AND indexname='idx_company_fund_provider_events_safeheron_webhook'
      AND indexdef='CREATE UNIQUE INDEX idx_company_fund_provider_events_safeheron_webhook ON public.company_fund_provider_events USING btree (safeheron_webhook_event_id) WHERE (safeheron_webhook_event_id IS NOT NULL)'
  ) THEN 0 ELSE 1 END
  + (SELECT count(*) FROM (
      SELECT safeheron_webhook_event_id
      FROM public.company_fund_provider_events
      WHERE safeheron_webhook_event_id IS NOT NULL
        AND authorized_safeheron_occurrence_key IS NULL
      GROUP BY safeheron_webhook_event_id HAVING count(*) > 1
    ) duplicate_legacy)
  + (SELECT count(*) FROM (
      SELECT safeheron_webhook_event_id, authorized_safeheron_occurrence_key
      FROM public.company_fund_provider_events
      WHERE safeheron_webhook_event_id IS NOT NULL
        AND authorized_safeheron_occurrence_key IS NOT NULL
      GROUP BY safeheron_webhook_event_id, authorized_safeheron_occurrence_key HAVING count(*) > 1
    ) duplicate_occurrence)`

const migration058SchemaSQL = `
DROP INDEX public.idx_company_fund_provider_events_safeheron_webhook;

CREATE UNIQUE INDEX idx_company_fund_provider_events_safeheron_webhook_legacy
  ON public.company_fund_provider_events (safeheron_webhook_event_id)
  WHERE safeheron_webhook_event_id IS NOT NULL
    AND authorized_safeheron_occurrence_key IS NULL;

CREATE UNIQUE INDEX idx_company_fund_provider_events_safeheron_occurrence
  ON public.company_fund_provider_events (safeheron_webhook_event_id, authorized_safeheron_occurrence_key)
  WHERE safeheron_webhook_event_id IS NOT NULL
    AND authorized_safeheron_occurrence_key IS NOT NULL;
`
