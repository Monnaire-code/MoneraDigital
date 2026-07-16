package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMigration053PostgresContract is intentionally opt-in. The gate is
// checked before DATABASE_URL is read or a driver is opened. It uses a unique
// schema inside a transaction that is always rolled back.
func TestMigration053PostgresContract(t *testing.T) {
	if os.Getenv("RUN_COMPANY_FUND_MIGRATION_053_INTEGRATION") != "1" {
		t.Skip("set RUN_COMPANY_FUND_MIGRATION_053_INTEGRATION=1 to run PostgreSQL migration 053 contract tests")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 053 integration tests are enabled")
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

	schema := fmt.Sprintf("migration_053_%d", time.Now().UnixNano())
	if _, err := tx.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`SET LOCAL search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(migration053IntegrationSchemaA); err != nil {
		t.Fatal(err)
	}
	validKey := "safeheron-occurrence-v1:" + strings.Repeat("a", 64)
	if _, err := tx.Exec(`INSERT INTO company_fund_transactions (channel, provider_occurrence_key, provider_occurrence_algorithm_version) VALUES ('SAFEHERON', $1, 'safeheron-occurrence-v1')`, validKey); err != nil {
		t.Fatal(err)
	}
	if err := runMigration053InTestSchema(tx, schema); err != nil {
		t.Fatal(err)
	}

	expectMigration053SQLState(t, tx, "null_alias", "23514", `INSERT INTO company_fund_transactions (channel) VALUES ('SAFEHERON')`)
	expectMigration053SQLState(t, tx, "wrong_version", "23514", `INSERT INTO company_fund_transactions (channel, provider_occurrence_key, provider_occurrence_algorithm_version) VALUES ('SAFEHERON', $1, 'wrong')`, "safeheron-occurrence-v1:"+strings.Repeat("b", 64))
	if _, err := tx.Exec(`INSERT INTO company_fund_transactions (channel, provider_occurrence_key, provider_occurrence_algorithm_version) VALUES ('SAFEHERON', $1, 'safeheron-occurrence-v1')`, "safeheron-occurrence-v1:"+strings.Repeat("c", 64)); err != nil {
		t.Fatalf("complete Safeheron insert failed: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO company_fund_transactions (channel) VALUES ('AIRWALLEX')`); err != nil {
		t.Fatalf("Airwallex insert was constrained: %v", err)
	}
}

func runMigration053InTestSchema(tx *sql.Tx, schema string) error {
	qualify := func(statement string) string { return qualifyCompanyFundIntegrationSQL(statement, schema) }
	if _, err := tx.Exec(qualify(migration053TimeoutsSQL)); err != nil {
		return err
	}
	var preflight migration053Preflight
	if err := tx.QueryRow(qualify(migration053PreflightSQL)).Scan(&preflight.missing, &preflight.wrongVersion, &preflight.duplicate, &preflight.invariant); err != nil {
		return err
	}
	if preflight.unsafe() {
		return fmt.Errorf("unsafe test fixture: %#v", preflight)
	}
	if _, err := tx.Exec(qualify(migration053AddConstraintSQL)); err != nil {
		return err
	}
	_, err := tx.Exec(qualify(migration053ValidateConstraintSQL))
	return err
}

func expectMigration053SQLState(t *testing.T, tx *sql.Tx, savepoint, code, statement string, args ...any) {
	t.Helper()
	if _, err := tx.Exec(`SAVEPOINT ` + savepoint); err != nil {
		t.Fatal(err)
	}
	_, err := tx.Exec(statement, args...)
	var pgError *pgconn.PgError
	if err == nil || (!errors.As(err, &pgError) || pgError.Code != code) {
		t.Fatalf("statement error = %v, want SQLSTATE %s", err, code)
	}
	if _, rollbackErr := tx.Exec(`ROLLBACK TO SAVEPOINT ` + savepoint); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
}

const migration053IntegrationSchemaA = `
CREATE TABLE company_fund_transactions (
  id BIGSERIAL PRIMARY KEY,
  channel VARCHAR(32) NOT NULL,
  provider_occurrence_key VARCHAR(256),
  provider_occurrence_algorithm_version VARCHAR(64)
);
CREATE UNIQUE INDEX idx_company_fund_transactions_safeheron_occurrence
  ON company_fund_transactions (provider_occurrence_key)
  WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL;`
