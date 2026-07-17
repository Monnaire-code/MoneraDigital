package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/shopspring/decimal"
	"monera-digital/internal/companyfund"
)

// TestMigration052PostgresDirectSQL is intentionally opt-in. It uses the
// caller's existing DATABASE_URL, but creates all fixtures in an isolated
// schema inside one transaction and rolls the transaction back. The default
// unit-test path never connects to PostgreSQL or executes migration DDL.
func TestMigration052PostgresDirectSQL(t *testing.T) {
	if os.Getenv("RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION") != "1" {
		t.Skip("set RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION=1 to run PostgreSQL migration 052 direct-SQL tests")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 052 integration tests are enabled")
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

	schema := fmt.Sprintf("migration_052_%d", time.Now().UnixNano())
	if _, err := tx.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(migration052IntegrationBaseSchema); err != nil {
		t.Fatalf("create base schema: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO company_fund_accounts (id, network_family) VALUES (10, 'EVM')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
INSERT INTO company_fund_transactions (
  id, channel, identity_algorithm_version, provider_transaction_id, movement_kind, provider_asset_key,
  from_address_or_account, to_address_or_account, amount, transfer_mode, movement_index,
  from_company_fund_account_id, movement_key, usd_value, finance_category_level1_id,
  current_valuation_history_id, provider_status
) VALUES (
  100, 'SAFEHERON', 'v1', 'legacy-safe-tx', 'PRINCIPAL', 'ETHEREUM_USDT',
  '0xAbC', '0xDeF', 3, 'SINGLE', 0, 10, 'v1:legacy-key', 30, 9, 77, 'COMPLETED'
)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigration052InTestSchema(tx, schema); err != nil {
		t.Fatalf("run Migration 052: %v", err)
	}
	var occurrenceKey, occurrenceVersion, movementKey, identityVersion, amount, usdValue string
	var categoryID, currentHistoryID int64
	if err := tx.QueryRow(`
SELECT provider_occurrence_key, provider_occurrence_algorithm_version,
       movement_key, identity_algorithm_version, amount::text, usd_value::text,
       finance_category_level1_id, current_valuation_history_id
FROM company_fund_transactions WHERE id=100`).
		Scan(&occurrenceKey, &occurrenceVersion, &movementKey, &identityVersion, &amount, &usdValue, &categoryID, &currentHistoryID); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(occurrenceKey, "safeheron-occurrence-v1:") || occurrenceVersion != "safeheron-occurrence-v1" ||
		movementKey != "v1:legacy-key" || identityVersion != "v1" || amount != "3.000000000000000000" ||
		usdValue != "30.000000000000000000" || categoryID != 9 || currentHistoryID != 77 {
		t.Fatalf("pre-v2 backfill changed protected legacy facts: %q %q %q %q %q %q %d %d", occurrenceKey, occurrenceVersion, movementKey, identityVersion, amount, usdValue, categoryID, currentHistoryID)
	}
	var legacyCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM company_fund_transactions WHERE id=100`).Scan(&legacyCount); err != nil || legacyCount != 1 {
		t.Fatalf("legacy row count = %d, %v", legacyCount, err)
	}

	for _, row := range []struct {
		id             int
		channel, txKey string
	}{{101, "SAFEHERON", "old-safe-insert"}, {102, "AIRWALLEX", "old-awx-insert"}} {
		if _, err := tx.Exec(`
INSERT INTO company_fund_transactions (
  id, channel, identity_algorithm_version, provider_transaction_id, movement_kind, provider_asset_key,
  from_address_or_account, to_address_or_account, amount, transfer_mode, movement_index, movement_key
) VALUES ($1,$2,'v1',$3,'PRINCIPAL','USD','payer','payee',1,'SINGLE',0,$3)`, row.id, row.channel, row.txKey); err != nil {
			t.Fatalf("old %s alias-null insert: %v", row.channel, err)
		}
	}
	var aliasNullCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM company_fund_transactions WHERE id IN (101,102) AND provider_occurrence_key IS NULL AND provider_occurrence_algorithm_version IS NULL`).Scan(&aliasNullCount); err != nil || aliasNullCount != 2 {
		t.Fatalf("old writer alias-null count = %d, %v", aliasNullCount, err)
	}

	expectMigration052SQLState(t, tx, "manual_null_total", "23514", `
INSERT INTO company_fund_transaction_valuation_history (
 transaction_id, valuation_version, usd_value, usd_valuation_status, usd_valuation_reason_code,
 usd_valuation_source, usd_valuation_method, dependency_fingerprint, valuation_policy_version,
 transition_trigger, applied_at, manual_reason, manual_updated_by, manual_updated_at
) VALUES (1,1,NULL,'FINAL','MANUAL_OVERRIDE','MANUAL','MANUAL_TOTAL',$1,'MANUAL_V1','MANUAL_ADMIN',$2::timestamptz,'reason','actor',$2::timestamptz)`, strings.Repeat("c", 64), "2026-07-15T00:30:00Z")
	if _, err := tx.Exec(`
INSERT INTO company_fund_transactions (
  id, channel, identity_algorithm_version, provider_transaction_id, movement_kind, provider_asset_key,
  from_address_or_account, to_address_or_account, amount, transfer_mode, movement_index,
  provider_reported_usd_value, calculated_usd_value, usd_provider_value_scope,
  provider_transaction_fact_id, provider_status
) VALUES (
  1, 'SAFEHERON', 'v1', 'safe-tx-1', 'PRINCIPAL', 'ETHEREUM_USDT',
  '0xabc', '0xdef', 2, 'SINGLE', 0, 10, 11, 'DIRECT_ITEM', 7, 'PENDING'
)`); err != nil {
		t.Fatalf("insert initial transaction: %v", err)
	}

	initialHistoryID := insertMigration052ManualHistory(t, tx, 1, 1, nil, "12", "6", strings.Repeat("a", 64), "2026-07-15T01:00:00Z")
	if result, err := tx.Exec(migration052ManualProjectionUpdate, "12", "6", initialHistoryID, "2026-07-15T01:00:00Z", 1, int64(1)); err != nil {
		t.Fatalf("legal initial MANUAL transition: %v", err)
	} else {
		requireMigration052RowsAffected(t, result, 1)
	}

	secondHistoryID := insertMigration052ManualHistory(t, tx, 1, 2, &initialHistoryID, "14", "7", strings.Repeat("b", 64), "2026-07-15T02:00:00Z")
	if result, err := tx.Exec(migration052ManualProjectionUpdate, "14", "7", secondHistoryID, "2026-07-15T02:00:00Z", 2, int64(1)); err != nil {
		t.Fatalf("legal MANUAL to MANUAL transition: %v", err)
	} else {
		requireMigration052RowsAffected(t, result, 1)
	}

	invalid := []struct {
		name string
		sql  string
		args []any
	}{
		{name: "replace", sql: `UPDATE company_fund_transactions SET usd_valuation_source='COINGECKO' WHERE id=1`},
		{name: "value", sql: `UPDATE company_fund_transactions SET usd_value=15 WHERE id=1`},
		{name: "pointer", sql: `UPDATE company_fund_transactions SET current_valuation_history_id=$1 WHERE id=1`, args: []any{initialHistoryID}},
		{name: "version", sql: `UPDATE company_fund_transactions SET usd_valuation_version=3 WHERE id=1`},
		{name: "status", sql: `UPDATE company_fund_transactions SET usd_valuation_status='PROVISIONAL' WHERE id=1`},
		{name: "matrix", sql: `UPDATE company_fund_transactions SET usd_valuation_basis='TRANSACTION_TIME' WHERE id=1`},
		{name: "provider evidence", sql: `UPDATE company_fund_transactions SET provider_reported_usd_value=99 WHERE id=1`},
	}
	for index, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			expectMigration052P7501(t, tx, fmt.Sprintf("invalid_%d", index), tc.sql, tc.args...)
		})
	}

	if _, err := tx.Exec(`
INSERT INTO company_fund_transactions (
  id, channel, identity_algorithm_version, provider_transaction_id, movement_kind, provider_asset_key,
  from_address_or_account, to_address_or_account, amount, transfer_mode, movement_index,
  usd_value, usd_valuation_source, provider_status
) VALUES (2, 'AIRWALLEX', 'v1', 'awx-1', 'PRINCIPAL', 'USD', 'payer', 'payee', 1, 'SINGLE', 0, 1, 'COINGECKO', 'PENDING')`); err != nil {
		t.Fatal(err)
	}
	if result, err := tx.Exec(`UPDATE company_fund_transactions SET usd_value=2, usd_valuation_status='PROVISIONAL' WHERE id=2`); err != nil {
		t.Fatalf("non-MANUAL protected update must remain compatible: %v", err)
	} else {
		requireMigration052RowsAffected(t, result, 1)
	}
	if result, err := tx.Exec(`UPDATE company_fund_transactions SET provider_status='COMPLETED', provider_transaction_fact_id=8 WHERE id=1`); err != nil {
		t.Fatalf("unrelated Provider lifecycle update must remain compatible: %v", err)
	} else {
		requireMigration052RowsAffected(t, result, 1)
	}
}

func TestMigration052PostgresHandlesDeferredTriggerEvents(t *testing.T) {
	if os.Getenv("RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION") != "1" {
		t.Skip("set RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION=1 to run PostgreSQL migration 052 deferred-trigger coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 052 integration tests are enabled")
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

	schema := fmt.Sprintf("migration_052_deferred_%d", time.Now().UnixNano())
	if _, err := tx.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(migration052IntegrationBaseSchema + migration052DeferredTriggerFixture); err != nil {
		t.Fatalf("create deferred-trigger fixture: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO company_fund_accounts (id, network_family) VALUES (10, 'EVM')`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`
INSERT INTO company_fund_transactions (
  id, channel, identity_algorithm_version, provider_transaction_id, movement_kind, provider_asset_key,
  from_address_or_account, to_address_or_account, amount, transfer_mode, movement_index,
  from_company_fund_account_id, movement_key
) VALUES (
  100, 'SAFEHERON', 'v1', 'legacy-safe-tx', 'PRINCIPAL', 'ETHEREUM_USDT',
  '0xAbC', '0xDeF', 3, 'SINGLE', 0, 10, 'v1:legacy-key'
)`); err != nil {
		t.Fatal(err)
	}

	if err := runMigration052InTestSchema(tx, schema); err != nil {
		t.Fatalf("migration 052 must complete with deferred trigger events: %v", err)
	}
}

func TestMigration052PostgresDDLAndProvenanceRollbackAndRetry(t *testing.T) {
	if os.Getenv("RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION") != "1" {
		t.Skip("set RUN_COMPANY_FUND_MIGRATION_052_INTEGRATION=1 to run PostgreSQL migration 052 atomic provenance tests")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 052 integration tests are enabled")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	schema := fmt.Sprintf("migration_052_atomic_%d", time.Now().UnixNano())
	quotedSchema := `"` + schema + `"`
	if _, err := db.Exec(`CREATE SCHEMA ` + quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP SCHEMA ` + quotedSchema + ` CASCADE`)
	setup, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(`SET LOCAL search_path TO ` + quotedSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := setup.Exec(migration052IntegrationBaseSchema + `;
CREATE TABLE migrations (
  version VARCHAR(50) PRIMARY KEY,
  name TEXT NOT NULL,
  CONSTRAINT migrations_reject_052 CHECK (version <> '052')
);
INSERT INTO migrations(version, name) VALUES ('051', 'preexisting');`); err != nil {
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}

	attempt, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attempt.Exec(`SET LOCAL search_path TO ` + quotedSchema); err != nil {
		t.Fatal(err)
	}
	if err := runMigration052InTestSchema(attempt, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := attempt.Exec(`INSERT INTO ` + quotedSchema + `.migrations(version, name) VALUES ('052', 'checkpoint A')`); err == nil {
		t.Fatal("provenance rejection did not fail the pinned transaction")
	}
	if err := attempt.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertMigration052AtomicState(t, db, schema, false)

	if _, err := db.Exec(`ALTER TABLE ` + quotedSchema + `.migrations DROP CONSTRAINT migrations_reject_052`); err != nil {
		t.Fatal(err)
	}
	retry, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := retry.Exec(`SET LOCAL search_path TO ` + quotedSchema); err != nil {
		t.Fatal(err)
	}
	if err := runMigration052InTestSchema(retry, schema); err != nil {
		t.Fatal(err)
	}
	if _, err := retry.Exec(`INSERT INTO ` + quotedSchema + `.migrations(version, name) VALUES ('052', 'checkpoint A')`); err != nil {
		t.Fatal(err)
	}
	if err := retry.Commit(); err != nil {
		t.Fatal(err)
	}
	assertMigration052AtomicState(t, db, schema, true)
}

func assertMigration052AtomicState(t *testing.T, db *sql.DB, schema string, committed bool) {
	t.Helper()
	var columns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name='company_fund_transactions' AND column_name IN ('provider_occurrence_key','provider_occurrence_algorithm_version')`, schema).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	var recorded bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM "` + schema + `".migrations WHERE version='052')`).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if (columns == 2) != committed || recorded != committed {
		t.Fatalf("atomic 052 state columns=%d recorded=%t committed=%t", columns, recorded, committed)
	}
}

func runMigration052InTestSchema(tx *sql.Tx, schema string) error {
	qualify := func(statement string) string { return qualifyCompanyFundIntegrationSQL(statement, schema) }
	if _, err := tx.Exec(qualify(migration052TimeoutsSQL)); err != nil {
		return err
	}
	var missing int64
	if err := tx.QueryRow(qualify(migration052PreflightQuery)).Scan(&missing); err != nil || missing != 0 {
		return fmt.Errorf("052 test preflight missing=%d: %w", missing, err)
	}
	if _, err := tx.Exec(qualify(migration052AddOccurrenceColumnsSQL)); err != nil {
		return err
	}
	if _, err := tx.Exec(qualify(migration052SchemaDDL)); err != nil {
		return err
	}
	rows, err := tx.Query(qualify(migration052BackfillQuery))
	if err != nil {
		return err
	}
	backfillRows, err := readMigration052BackfillRows(rows)
	if err != nil {
		return err
	}
	for _, row := range backfillRows {
		amount, parseErr := decimal.NewFromString(row.amount)
		if parseErr != nil {
			return parseErr
		}
		from, normalizeErr := companyfund.NormalizeSafeheronOccurrenceAddress(row.networkFamily, row.from)
		if normalizeErr != nil {
			return normalizeErr
		}
		to, normalizeErr := companyfund.NormalizeSafeheronOccurrenceAddress(row.networkFamily, row.to)
		if normalizeErr != nil {
			return normalizeErr
		}
		occurrence, buildErr := companyfund.BuildSafeheronOccurrence(companyfund.SafeheronOccurrenceInput{
			ProviderTransactionKey: row.providerTransactionID, MovementKind: companyfund.MovementKind(row.movementKind),
			RawCoinKey: row.rawCoinKey, NormalizedSource: from, NormalizedDestination: to, Amount: amount,
			TransferMode: companyfund.TransferMode(row.transferMode), MovementIndex: row.movementIndex,
		})
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(context.Background(), qualify(migration052BackfillUpdate), occurrence.Key, occurrence.AlgorithmVersion, row.id); err != nil {
			return err
		}
	}
	for _, query := range []string{migration052MissingAliasQuery, migration052DuplicateAliasQuery} {
		var count int64
		if err := tx.QueryRow(qualify(query)).Scan(&count); err != nil || count != 0 {
			return fmt.Errorf("052 test validation count=%d: %w", count, err)
		}
	}
	return nil
}

func requireMigration052RowsAffected(t *testing.T, result sql.Result, want int64) {
	t.Helper()
	got, err := result.RowsAffected()
	if err != nil || got != want {
		t.Fatalf("RowsAffected() = %d, %v; want %d", got, err, want)
	}
}

func insertMigration052ManualHistory(t *testing.T, tx *sql.Tx, transactionID, version int64, supersedes *int64, value, unitPrice, fingerprint, appliedAt string) int64 {
	t.Helper()
	var id int64
	err := tx.QueryRow(`
INSERT INTO company_fund_transaction_valuation_history (
  transaction_id, valuation_version, usd_value, provider_reported_usd_value, calculated_usd_value,
  usd_unit_price, usd_valuation_status, usd_valuation_reason_code, usd_valuation_basis,
  usd_valuation_time, usd_valuation_price_at, usd_valuation_source, usd_valuation_method,
  usd_valuation_granularity, usd_provider_value_scope, usd_derivation_method, usd_rate_snapshot_id,
  provider_transaction_fact_id, dependency_fingerprint, valuation_policy_version, transition_trigger,
  supersedes_history_id, applied_at, manual_reason, manual_updated_by, manual_updated_at
) VALUES (
  $1, $2, $3, 10, 11, $4, 'FINAL', 'MANUAL_OVERRIDE', NULL,
  NULL, NULL, 'MANUAL', 'MANUAL_TOTAL', NULL, 'DIRECT_ITEM', NULL, NULL,
  7, $5, 'MANUAL_V1', 'MANUAL_ADMIN', $6, $7::timestamptz,
  'confirmed total value', 'admin-user', $7::timestamptz
) RETURNING id`, transactionID, version, value, unitPrice, fingerprint, supersedes, appliedAt).Scan(&id)
	if err != nil {
		t.Fatalf("insert MANUAL history v%d: %v", version, err)
	}
	return id
}

func expectMigration052P7501(t *testing.T, tx *sql.Tx, savepoint, statement string, args ...any) {
	expectMigration052SQLState(t, tx, savepoint, "P7501", statement, args...)
}

func expectMigration052SQLState(t *testing.T, tx *sql.Tx, savepoint, code, statement string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(`SAVEPOINT ` + savepoint); err != nil {
		t.Fatal(err)
	}
	_, execErr := tx.Exec(statement, args...)
	var pgErr *pgconn.PgError
	if !errors.As(execErr, &pgErr) || pgErr.Code != code {
		t.Fatalf("direct SQL error = %v, want SQLSTATE %s", execErr, code)
	}
	if _, err := tx.Exec(`ROLLBACK TO SAVEPOINT ` + savepoint); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`RELEASE SAVEPOINT ` + savepoint); err != nil {
		t.Fatal(err)
	}
}

const migration052ManualProjectionUpdate = `
UPDATE company_fund_transactions SET
  usd_value=$1, usd_unit_price=$2, usd_valuation_status='FINAL',
  usd_valuation_reason_code='MANUAL_OVERRIDE', usd_valuation_basis=NULL,
  usd_valuation_time=NULL, usd_valuation_price_at=NULL, usd_valuation_source='MANUAL',
  usd_valuation_method='MANUAL_TOTAL', usd_valuation_granularity=NULL,
  usd_derivation_method=NULL, usd_rate_snapshot_id=NULL,
  current_valuation_history_id=$3, usd_valued_at=$4::timestamptz,
  usd_valuation_policy_version='MANUAL_V1', usd_valuation_version=$5
WHERE id=$6`

const migration052DeferredTriggerFixture = `
CREATE FUNCTION company_fund_test_deferred_trigger()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$ BEGIN RETURN NEW; END $$;
CREATE CONSTRAINT TRIGGER company_fund_test_deferred_trigger
AFTER UPDATE ON company_fund_transactions
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION company_fund_test_deferred_trigger();
`

const migration052IntegrationBaseSchema = `
CREATE TABLE company_fund_accounts (
  id BIGINT PRIMARY KEY,
  network_family VARCHAR(64)
);
CREATE TABLE company_fund_transactions (
  id BIGSERIAL PRIMARY KEY,
  channel VARCHAR(32) NOT NULL,
  identity_algorithm_version VARCHAR(64) NOT NULL,
  movement_key VARCHAR(256),
  provider_transaction_id VARCHAR(256),
  movement_kind VARCHAR(16),
  provider_asset_key VARCHAR(256),
  from_address_or_account VARCHAR(512),
  to_address_or_account VARCHAR(512),
  amount NUMERIC(65,18),
  transfer_mode VARCHAR(16),
  movement_index INTEGER,
  from_company_fund_account_id BIGINT,
  to_company_fund_account_id BIGINT,
  provider_reported_usd_value NUMERIC(65,18),
  calculated_usd_value NUMERIC(65,18),
  usd_value NUMERIC(65,18),
  usd_unit_price NUMERIC(65,18),
  usd_valuation_status VARCHAR(16),
  usd_valuation_reason_code VARCHAR(64),
  usd_valuation_basis VARCHAR(24),
  usd_valuation_time TIMESTAMPTZ,
  usd_valuation_price_at TIMESTAMPTZ,
  usd_valuation_source VARCHAR(32),
  usd_valuation_method VARCHAR(64),
  usd_valuation_granularity VARCHAR(16),
  usd_provider_value_scope VARCHAR(24),
  usd_derivation_method VARCHAR(32),
  usd_rate_snapshot_id BIGINT,
  current_valuation_history_id BIGINT,
  usd_valued_at TIMESTAMPTZ,
  usd_valuation_policy_version VARCHAR(64),
  usd_valuation_version BIGINT,
  provider_transaction_fact_id BIGINT,
  provider_status VARCHAR(64),
  finance_category_level1_id BIGINT,
  CONSTRAINT company_fund_transactions_usd_valuation_source_check
    CHECK (usd_valuation_source IS NULL OR usd_valuation_source IN ('SAFEHERON','AIRWALLEX','COINGECKO','USD_PAR'))
);
CREATE TABLE company_fund_transaction_valuation_history (
  id BIGSERIAL PRIMARY KEY,
  transaction_id BIGINT NOT NULL,
  valuation_version BIGINT NOT NULL,
  usd_value NUMERIC(65,18),
  provider_reported_usd_value NUMERIC(65,18),
  calculated_usd_value NUMERIC(65,18),
  usd_unit_price NUMERIC(65,18),
  usd_valuation_status VARCHAR(16),
  usd_valuation_reason_code VARCHAR(64),
  usd_valuation_basis VARCHAR(24),
  usd_valuation_time TIMESTAMPTZ,
  usd_valuation_price_at TIMESTAMPTZ,
  usd_valuation_source VARCHAR(32),
  usd_valuation_method VARCHAR(64),
  usd_valuation_granularity VARCHAR(16),
  usd_provider_value_scope VARCHAR(24),
  usd_derivation_method VARCHAR(32),
  usd_rate_snapshot_id BIGINT,
  provider_transaction_fact_id BIGINT,
  dependency_fingerprint VARCHAR(64) NOT NULL,
  valuation_policy_version VARCHAR(64) NOT NULL,
  transition_trigger VARCHAR(64) NOT NULL,
  supersedes_history_id BIGINT,
  applied_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`
