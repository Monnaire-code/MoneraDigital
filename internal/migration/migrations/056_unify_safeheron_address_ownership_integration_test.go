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

const migration056PostgresIntegrationGate = "RUN_MIGRATION_056_POSTGRES_INTEGRATION"

func TestMigration056PostgresIntegration(t *testing.T) {
	if os.Getenv(migration056PostgresIntegrationGate) != "1" {
		t.Skip("set RUN_MIGRATION_056_POSTGRES_INTEGRATION=1 to run isolated PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when migration 056 PostgreSQL integration is enabled")
	}

	db, schema := newMigration056PostgresFixture(t, databaseURL)
	qualify := func(statement string) string {
		return strings.ReplaceAll(statement, "public.", schema+".")
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(qualify(migration056LockSourcesSQL)); err != nil {
		t.Fatal(err)
	}
	var preflight migration056Preflight
	if err := tx.QueryRow(qualify(migration056PreflightSQL)).Scan(
		&preflight.poolDuplicates,
		&preflight.companyDuplicates,
		&preflight.crossDomainConflicts,
		&preflight.invalidIdentities,
		&preflight.unexpectedOwnershipTable,
	); err != nil {
		t.Fatal(err)
	}
	if preflight.unsafe() {
		t.Fatalf("unexpected unsafe fixture: %+v", preflight)
	}
	if _, err := tx.Exec(qualify(migration056SchemaSQL)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	t.Run("backfill canonicalizes and claims both domains", func(t *testing.T) {
		var poolNetwork, poolAddress string
		if err := db.QueryRow(`SELECT network_family, address FROM `+schema+`.address_pool WHERE id = 1`).
			Scan(&poolNetwork, &poolAddress); err != nil {
			t.Fatal(err)
		}
		if poolNetwork != "EVM" || poolAddress != "0xabcdef" {
			t.Fatalf("canonical pool identity = %q/%q", poolNetwork, poolAddress)
		}
		var ownershipCount int
		if err := db.QueryRow(`SELECT count(*) FROM ` + schema + `.safeheron_address_ownerships`).Scan(&ownershipCount); err != nil {
			t.Fatal(err)
		}
		if ownershipCount != 4 {
			t.Fatalf("ownership rows = %d, want 4", ownershipCount)
		}
	})

	t.Run("existing monitoring boundaries are deterministic", func(t *testing.T) {
		var enabledMonitoring, enabledFirst time.Time
		if err := db.QueryRow(`SELECT monitoring_started_at, first_enabled_at FROM `+schema+`.company_fund_accounts WHERE id = 10`).
			Scan(&enabledMonitoring, &enabledFirst); err != nil {
			t.Fatal(err)
		}
		if !enabledMonitoring.Equal(enabledFirst) {
			t.Fatalf("enabled monitoring/first mismatch: %s/%s", enabledMonitoring, enabledFirst)
		}
		var disabledMonitoring time.Time
		var disabledFirst sql.NullTime
		if err := db.QueryRow(`SELECT monitoring_started_at, first_enabled_at FROM `+schema+`.company_fund_accounts WHERE id = 11`).
			Scan(&disabledMonitoring, &disabledFirst); err != nil {
			t.Fatal(err)
		}
		if disabledFirst.Valid || disabledMonitoring.IsZero() {
			t.Fatalf("disabled boundaries = %s/%#v", disabledMonitoring, disabledFirst)
		}
		var historicalMonitoring, historicalFirst time.Time
		if err := db.QueryRow(`SELECT monitoring_started_at, first_enabled_at FROM `+schema+`.company_fund_accounts WHERE id = 12`).
			Scan(&historicalMonitoring, &historicalFirst); err != nil {
			t.Fatal(err)
		}
		if !historicalMonitoring.Equal(historicalFirst) || historicalFirst.After(time.Now().Add(-47*time.Hour)) {
			t.Fatalf("historically enabled disabled account was not restored from audit: %s/%s", historicalMonitoring, historicalFirst)
		}
	})

	t.Run("new customer address is canonical and protected", func(t *testing.T) {
		if _, err := db.Exec(`INSERT INTO ` + schema + `.address_pool
			(id, network_family, address) VALUES (2, ' evm ', ' 0xAABB ')`); err != nil {
			t.Fatal(err)
		}
		var address, ownerKind string
		if err := db.QueryRow(`
			SELECT pool.address, ownership.owner_kind
			FROM `+schema+`.address_pool pool
			JOIN `+schema+`.safeheron_address_ownerships ownership
			  ON ownership.address_pool_id = pool.id
			WHERE pool.id = 2`).Scan(&address, &ownerKind); err != nil {
			t.Fatal(err)
		}
		if address != "0xaabb" || ownerKind != "CUSTOMER_POOL" {
			t.Fatalf("new ownership = %q/%q", address, ownerKind)
		}
		if _, err := db.Exec(`UPDATE ` + schema + `.address_pool SET address = '0xccdd' WHERE id = 2`); err == nil {
			t.Fatal("expected source identity update to fail")
		}
	})

	t.Run("authoritative ownership rejects direct mutation and fabricated rows", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE ` + schema + `.safeheron_address_ownerships SET normalized_address='0xfake' WHERE address_pool_id=1`); err == nil {
			t.Fatal("expected direct ownership update rejection")
		}
		if _, err := db.Exec(`DELETE FROM ` + schema + `.safeheron_address_ownerships WHERE address_pool_id=1`); err == nil {
			t.Fatal("expected direct ownership delete rejection")
		}
		if _, err := db.Exec(`INSERT INTO ` + schema + `.safeheron_address_ownerships
			(network_family,normalized_address,owner_kind,address_pool_id)
			VALUES ('EVM','0xfake','CUSTOMER_POOL',1)`); err == nil {
			t.Fatal("expected fabricated ownership insertion rejection")
		}
	})

	t.Run("cross-domain insert fails closed", func(t *testing.T) {
		_, err := db.Exec(`INSERT INTO ` + schema + `.company_fund_accounts
			(channel, normalized_address, network_family, account_name, is_enabled)
			VALUES ('SAFEHERON', '0xAABB', 'EVM', 'conflict', false)`)
		if err == nil {
			t.Fatal("expected cross-domain ownership conflict")
		}
	})

	t.Run("authoritative source deletion is rejected and retains ownership", func(t *testing.T) {
		if _, err := db.Exec(`DELETE FROM ` + schema + `.address_pool WHERE id = 2`); err == nil {
			t.Fatal("authoritative source deletion must be rejected")
		}
		var remaining int
		if err := db.QueryRow(`SELECT count(*) FROM ` + schema + `.safeheron_address_ownerships WHERE address_pool_id=2`).Scan(&remaining); err != nil || remaining != 1 {
			t.Fatalf("retained ownership count=%d err=%v", remaining, err)
		}
	})

	t.Run("first enable is immutable and monitoring backfill is explicit", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE ` + schema + `.company_fund_accounts SET is_enabled = true WHERE id = 11`); err != nil {
			t.Fatal(err)
		}
		var firstEnabled time.Time
		if err := db.QueryRow(`SELECT first_enabled_at FROM ` + schema + `.company_fund_accounts WHERE id = 11`).Scan(&firstEnabled); err != nil {
			t.Fatal(err)
		}
		if firstEnabled.IsZero() {
			t.Fatal("first enable transition did not set first_enabled_at")
		}
		if _, err := db.Exec(`UPDATE ` + schema + `.company_fund_accounts SET first_enabled_at = now() + interval '1 hour' WHERE id = 11`); err == nil {
			t.Fatal("expected first_enabled_at mutation to fail")
		}
		if _, err := db.Exec(`UPDATE ` + schema + `.company_fund_accounts SET monitoring_started_at = now() - interval '1 day' WHERE id = 11`); err == nil {
			t.Fatal("expected ordinary monitoring backfill to fail")
		}
		backfillTx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer backfillTx.Rollback()
		if _, err := backfillTx.Exec(`SET LOCAL monera.company_fund_monitoring_backfill = 'on'`); err != nil {
			t.Fatal(err)
		}
		if _, err := backfillTx.Exec(`UPDATE ` + schema + `.company_fund_accounts SET monitoring_started_at = now() - interval '1 day' WHERE id = 11`); err != nil {
			t.Fatal(err)
		}
		if err := backfillTx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
}

func newMigration056PostgresFixture(t *testing.T, databaseURL string) (*sql.DB, string) {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("migration_056_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	fixtureSQL := `
CREATE TABLE ` + schema + `.address_pool (
  id INTEGER PRIMARY KEY,
  network_family VARCHAR(16) NOT NULL,
  address VARCHAR(128) NOT NULL
);
CREATE TABLE ` + schema + `.company_fund_accounts (
  id BIGSERIAL PRIMARY KEY,
  channel VARCHAR(16) NOT NULL,
  normalized_address VARCHAR(256),
  network_family VARCHAR(64),
  account_name VARCHAR(256) NOT NULL,
  is_enabled BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE ` + schema + `.admin_operation_logs (
  id BIGSERIAL PRIMARY KEY,
  module VARCHAR(64) NOT NULL,
  request_data JSONB,
  created_at TIMESTAMPTZ NOT NULL
);
INSERT INTO ` + schema + `.address_pool (id, network_family, address)
VALUES (1, ' evm ', ' 0xAbCdEf ');
INSERT INTO ` + schema + `.company_fund_accounts
  (id, channel, normalized_address, network_family, account_name, is_enabled, created_at)
VALUES
  (10, 'SAFEHERON', '0x1122AA', 'evm', 'enabled', true, now() - interval '2 days'),
  (11, 'SAFEHERON', '0x3344BB', 'EVM', 'disabled', false, now() - interval '1 day'),
  (12, 'SAFEHERON', '0x5566CC', 'EVM', 'historically-enabled', false, now() - interval '3 days');
INSERT INTO ` + schema + `.admin_operation_logs (module,request_data,created_at)
VALUES ('company_fund',jsonb_build_object(
  'resourceType','account','resourceId','12','payload',jsonb_build_object('afterEnabled',true)
),now()-interval '2 days');`
	if _, err := db.Exec(fixtureSQL); err != nil {
		t.Fatal(err)
	}
	return db, schema
}
