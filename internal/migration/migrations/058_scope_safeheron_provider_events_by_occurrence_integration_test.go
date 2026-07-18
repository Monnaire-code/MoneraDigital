package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const migration058PostgresIntegrationGate = "RUN_MIGRATION_058_POSTGRES_INTEGRATION"

func TestMigration058PostgresScopesSafeheronProviderEventsByOccurrence(t *testing.T) {
	if os.Getenv(migration058PostgresIntegrationGate) != "1" {
		t.Skip("set RUN_MIGRATION_058_POSTGRES_INTEGRATION=1 to run isolated PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 058 PostgreSQL integration is enabled")
	}

	db, schema := newMigration058PostgresFixture(t, databaseURL)
	qualify := func(statement string) string {
		statement = strings.ReplaceAll(statement, "public.", schema+".")
		return strings.ReplaceAll(statement, "schemaname='public'", "schemaname='"+schema+"'")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var violations int64
	if err := tx.QueryRow(qualify(migration058PreflightSQL)).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("migration 058 preflight violations = %d", violations)
	}
	if _, err := tx.Exec(qualify(migration058SchemaSQL)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	insert := `INSERT INTO ` + schema + `.company_fund_provider_events
  (safeheron_webhook_event_id,authorized_safeheron_occurrence_key) VALUES ($1,$2)`
	if _, err := db.Exec(insert, 101, "safeheron-occurrence-v1:"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, 101, "safeheron-occurrence-v1:"+strings.Repeat("b", 64)); err != nil {
		t.Fatalf("different occurrence in same webhook must be accepted: %v", err)
	}
	if _, err := db.Exec(insert, 101, "safeheron-occurrence-v1:"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("duplicate routed webhook occurrence was accepted")
	}
	if _, err := db.Exec(insert, 202, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, 202, nil); err == nil {
		t.Fatal("duplicate legacy webhook provider event was accepted")
	}
}

func newMigration058PostgresFixture(t *testing.T, databaseURL string) (*sql.DB, string) {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("migration_058_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	if _, err := db.Exec(`CREATE TABLE ` + schema + `.company_fund_provider_events (
  id BIGSERIAL PRIMARY KEY,
  safeheron_webhook_event_id INTEGER,
  authorized_safeheron_occurrence_key VARCHAR(256)
);
CREATE UNIQUE INDEX idx_company_fund_provider_events_safeheron_webhook
  ON ` + schema + `.company_fund_provider_events (safeheron_webhook_event_id)
  WHERE safeheron_webhook_event_id IS NOT NULL;`); err != nil {
		t.Fatal(err)
	}
	return db, schema
}
