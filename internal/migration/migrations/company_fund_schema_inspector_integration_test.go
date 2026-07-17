package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestLiveCompanyFundSchemaInspectorPostgresContract(t *testing.T) {
	if os.Getenv("RUN_COMPANY_FUND_SCHEMA_INSPECTOR_INTEGRATION") != "1" {
		t.Skip("set RUN_COMPANY_FUND_SCHEMA_INSPECTOR_INTEGRATION=1 to run the isolated PostgreSQL catalog contract")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when the schema inspector integration test is enabled")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schema := fmt.Sprintf("company_fund_inspector_%d", time.Now().UnixNano())
	if _, err := tx.Exec(`CREATE SCHEMA ` + schema + `; SET LOCAL search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(companyFundInspectorIntegrationBaseSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(qualifyCompanyFundIntegrationSQL(migration052SchemaDDL, schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(qualifyCompanyFundIntegrationSQL(migration053AddConstraintSQL+`;`+migration053ValidateConstraintSQL, schema) + `; INSERT INTO "` + schema + `".migrations(version, name) VALUES ('052', 'A'), ('053', 'B')`); err != nil {
		t.Fatal(err)
	}
	report, err := InspectLiveCompanyFundSchema(context.Background(), schemaBoundInspectorCatalog{tx: tx, schema: schema})
	if err != nil || report.State != CompanyFundSchemaStateB || !report.Migration052Recorded || !report.Migration053Recorded || report.Fingerprint == nil {
		t.Fatalf("live report = %#v, %v", report, err)
	}
	publicReport, err := InspectLiveCompanyFundSchema(context.Background(), tx)
	if err != nil {
		t.Fatalf("inspect canonical public schema with malicious search_path: %v", err)
	}
	if publicReport.Snapshot.OccurrenceSchema == schema {
		t.Fatalf("production inspector followed malicious search_path %q", schema)
	}
}

type schemaBoundInspectorCatalog struct {
	tx     *sql.Tx
	schema string
}

func (catalog schemaBoundInspectorCatalog) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return catalog.tx.QueryContext(ctx, catalog.query(query), args...)
}

func (catalog schemaBoundInspectorCatalog) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return catalog.tx.QueryRowContext(ctx, catalog.query(query), args...)
}

func (catalog schemaBoundInspectorCatalog) query(query string) string {
	quoted := `"` + catalog.schema + `"`
	quotedPrefix := `"` + catalog.schema + `".`
	query = strings.ReplaceAll(query, "public.migrations", quoted+".migrations")
	query = strings.ReplaceAll(query, "= 'public'", "= '"+catalog.schema+"'")
	query = strings.Replace(query, "SELECT table_schema, table_name", "SELECT 'public' AS table_schema, table_name", 1)
	query = strings.Replace(query, "SELECT namespace.nspname, table_class.relname", "SELECT 'public' AS schema_name, table_class.relname", 1)
	query = strings.Replace(query, "SELECT namespace.nspname, proc.proname", "SELECT 'public' AS schema_name, proc.proname", 1)
	query = strings.Replace(query, "       function_namespace.nspname, function_proc.proname", "       'public' AS function_schema, function_proc.proname", 1)
	query = strings.Replace(query, "pg_get_indexdef(idx.indexrelid)", "replace(replace(pg_get_indexdef(idx.indexrelid), '"+quotedPrefix+"', 'public.'), '"+catalog.schema+".', 'public.')", 1)
	query = strings.Replace(query, "proc.oid, proc.prosrc", "proc.oid, replace(replace(proc.prosrc, '"+quotedPrefix+"', 'public.'), '"+catalog.schema+".', 'public.')", 1)
	return query
}

const companyFundInspectorIntegrationBaseSchema = `
CREATE TABLE migrations (version VARCHAR(50) PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE company_fund_transactions (
  id BIGINT PRIMARY KEY,
  channel VARCHAR(32) NOT NULL,
  provider_occurrence_key VARCHAR(256),
  provider_occurrence_algorithm_version VARCHAR(64),
  provider_reported_usd_value NUMERIC, calculated_usd_value NUMERIC, usd_value NUMERIC, usd_unit_price NUMERIC,
  usd_valuation_status TEXT, usd_valuation_reason_code TEXT, usd_valuation_basis TEXT, usd_valuation_time TIMESTAMPTZ,
  usd_valuation_price_at TIMESTAMPTZ, usd_valuation_source VARCHAR(32), usd_valuation_method TEXT, usd_valuation_granularity TEXT,
  usd_provider_value_scope TEXT, usd_derivation_method TEXT, usd_rate_snapshot_id BIGINT, current_valuation_history_id BIGINT,
  usd_valued_at TIMESTAMPTZ, usd_valuation_policy_version TEXT, usd_valuation_version BIGINT, provider_transaction_fact_id BIGINT
);
CREATE TABLE company_fund_transaction_valuation_history (
  id BIGINT PRIMARY KEY, transaction_id BIGINT NOT NULL, provider_reported_usd_value NUMERIC, calculated_usd_value NUMERIC,
  usd_value NUMERIC, usd_unit_price NUMERIC, usd_valuation_status TEXT, usd_valuation_reason_code TEXT,
  usd_valuation_basis TEXT, usd_valuation_time TIMESTAMPTZ, usd_valuation_price_at TIMESTAMPTZ,
  usd_valuation_source VARCHAR(32), usd_valuation_method TEXT, usd_valuation_granularity TEXT,
  usd_provider_value_scope TEXT, usd_derivation_method TEXT, usd_rate_snapshot_id BIGINT,
  applied_at TIMESTAMPTZ, valuation_policy_version TEXT, valuation_version BIGINT,
  provider_transaction_fact_id BIGINT, transition_trigger TEXT, supersedes_history_id BIGINT
);`
