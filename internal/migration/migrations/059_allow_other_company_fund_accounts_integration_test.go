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

const migration059PostgresIntegrationGate = "RUN_MIGRATION_059_POSTGRES_INTEGRATION"

func TestMigration059PostgresAllowsOtherAccountWithoutProviderAutomationFields(t *testing.T) {
	if os.Getenv(migration059PostgresIntegrationGate) != "1" {
		t.Skip("set RUN_MIGRATION_059_POSTGRES_INTEGRATION=1 to run isolated PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 059 PostgreSQL integration is enabled")
	}
	db, schema := newMigration059PostgresFixture(t, databaseURL)
	qualify := func(statement string) string {
		return strings.ReplaceAll(statement, "public.company_fund_accounts", schema+".company_fund_accounts")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(qualify(migration059ReplaceAccountConstraintsSQL)); err != nil {
		t.Fatalf("apply migration 059 account constraints: %v", err)
	}
	if _, err := tx.Exec(qualify(migration059ValidateAccountConstraintsSQL)); err != nil {
		t.Fatalf("validate migration 059 account constraints: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	insert := `INSERT INTO ` + schema + `.company_fund_accounts
  (channel, provider_account_key, wallet_address, normalized_address, network_family)
  VALUES ($1, $2, $3, $4, $5)`
	if _, err := db.Exec(insert, "OTHER", "other:bank:finance:1234", nil, nil, nil); err != nil {
		t.Fatalf("valid OTHER account rejected: %v", err)
	}
	if _, err := db.Exec(insert, "OTHER", "other:bank:finance:1234", nil, nil, nil); err == nil {
		t.Fatal("duplicate OTHER provider account key was accepted")
	}
	for _, testCase := range []struct {
		name                          string
		key, wallet, address, network any
	}{
		{"NULL provider key", nil, nil, nil, nil},
		{"blank provider key", " ", nil, nil, nil},
		{"wallet address", "other:bank:finance:1235", "masked", nil, nil},
		{"normalized address", "other:bank:finance:1236", nil, "normalized", nil},
		{"network", "other:bank:finance:1237", nil, nil, "EVM"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := db.Exec(insert, "OTHER", testCase.key, testCase.wallet, testCase.address, testCase.network); err == nil {
				t.Fatal("invalid OTHER shape was accepted")
			}
		})
	}
	if _, err := db.Exec(insert, "SAFEHERON", nil, nil, "0xabc", "EVM"); err != nil {
		t.Fatalf("existing Safeheron shape regressed: %v", err)
	}
	if _, err := db.Exec(insert, "AIRWALLEX", "awx-main", nil, nil, nil); err != nil {
		t.Fatalf("existing Airwallex shape regressed: %v", err)
	}
}

func newMigration059PostgresFixture(t *testing.T, databaseURL string) (*sql.DB, string) {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("migration_059_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	fixtureSQL := `
CREATE TABLE ` + schema + `.company_fund_accounts (
  id BIGSERIAL PRIMARY KEY,
  channel VARCHAR(16) NOT NULL CONSTRAINT company_fund_accounts_channel_check
    CHECK (channel IN ('SAFEHERON', 'AIRWALLEX')),
  provider_account_key VARCHAR(128),
  wallet_address VARCHAR(256),
  normalized_address VARCHAR(256),
  network_family VARCHAR(64),
  CONSTRAINT company_fund_accounts_check CHECK (
    (channel = 'SAFEHERON' AND normalized_address IS NOT NULL AND network_family IS NOT NULL)
    OR (channel = 'AIRWALLEX' AND provider_account_key IS NOT NULL)
  )
);`
	if _, err := db.Exec(fixtureSQL); err != nil {
		t.Fatal(err)
	}
	return db, schema
}
