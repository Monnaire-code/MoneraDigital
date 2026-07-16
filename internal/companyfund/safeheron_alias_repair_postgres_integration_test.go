package companyfund

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const safeheronAliasRepairPostgresGate = "RUN_COMPANY_FUND_SAFEHERON_ALIAS_REPAIR_POSTGRES_INTEGRATION"

// TestSafeheronAliasRepairPostgresIntegration is isolated and opt-in. The
// execution gate is intentionally checked before DATABASE_URL is read or a
// PostgreSQL driver is opened.
func TestSafeheronAliasRepairPostgresIntegration(t *testing.T) {
	if os.Getenv(safeheronAliasRepairPostgresGate) != "1" {
		t.Skip("set RUN_COMPANY_FUND_SAFEHERON_ALIAS_REPAIR_POSTGRES_INTEGRATION=1 to run the isolated alias repair contract")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when the alias repair integration contract is enabled")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("safeheron_alias_repair_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`) })
	fixtureConfig := config.Copy()
	fixtureConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*fixtureConfig)
	t.Cleanup(func() { _ = db.Close() })

	for _, statement := range []string{
		`CREATE TABLE company_fund_transactions (
			id BIGSERIAL PRIMARY KEY, movement_key TEXT NOT NULL UNIQUE, channel TEXT NOT NULL,
			identity_algorithm_version TEXT NOT NULL, provider_occurrence_key TEXT UNIQUE,
			provider_occurrence_algorithm_version TEXT, provider_account_key TEXT,
			provider_transaction_id TEXT, provider_event_id TEXT, provider_movement_id TEXT,
			provider_transaction_fact_id BIGINT, latest_provider_event_id BIGINT, raw_snapshot_digest TEXT,
			amount NUMERIC NOT NULL, currency TEXT NOT NULL, chain_code TEXT, provider_asset_key TEXT,
			asset_contract TEXT, is_unrecognized_asset BOOLEAN NOT NULL DEFAULT FALSE, provider_status TEXT,
			provider_status_version BIGINT, provider_fact_source TEXT NOT NULL, status_rank INTEGER NOT NULL,
			last_seen_source TEXT NOT NULL, tx_hash TEXT, occurred_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
			provider_updated_at TIMESTAMPTZ, movement_kind TEXT NOT NULL, from_address_or_account TEXT,
			to_address_or_account TEXT, transfer_mode TEXT NOT NULL, movement_index INTEGER NOT NULL
		)`,
		`CREATE TABLE company_fund_provider_transaction_facts (
			id BIGSERIAL PRIMARY KEY, channel TEXT NOT NULL, provider_account_key TEXT NOT NULL,
			provider_transaction_id TEXT NOT NULL, provider_extras JSONB NOT NULL
		)`,
		`CREATE TABLE company_fund_provider_events (event_state TEXT NOT NULL, lease_expires_at TIMESTAMPTZ)`,
		`CREATE TABLE company_fund_sync_runs (status TEXT NOT NULL, lease_expires_at TIMESTAMPTZ)`,
		`CREATE TABLE company_fund_accounts (id BIGINT PRIMARY KEY, channel TEXT NOT NULL, provider_account_key TEXT, normalized_address TEXT, network_family TEXT, is_enabled BOOLEAN NOT NULL)`,
		`CREATE TABLE company_fund_account_asset_policies (id BIGSERIAL PRIMARY KEY, company_fund_account_id BIGINT NOT NULL, currency TEXT, provider_asset_key TEXT, is_enabled BOOLEAN NOT NULL)`,
		`INSERT INTO company_fund_accounts (id, channel, provider_account_key, normalized_address, network_family, is_enabled) VALUES (1, 'SAFEHERON', 'account-a', '0xabc', 'EVM', true)`,
		`INSERT INTO company_fund_transactions (
			movement_key, channel, identity_algorithm_version, provider_account_key, provider_transaction_id,
			amount, currency, provider_asset_key, provider_fact_source, status_rank, last_seen_source,
			movement_kind, from_address_or_account, to_address_or_account, transfer_mode, movement_index
		) VALUES ('v1:legacy', 'SAFEHERON', 'v1', 'account-a', 'safeheron-tx', 1, 'ETHEREUM_USDT',
			'ETHEREUM_USDT', 'WEBHOOK', 1, 'WEBHOOK', 'PRINCIPAL', '0xfrom', '0xto', 'SINGLE', 0)`,
		`INSERT INTO company_fund_provider_transaction_facts (channel, provider_account_key, provider_transaction_id, provider_extras)
		 VALUES ('SAFEHERON', 'account-a', 'safeheron-tx', '{"coinKey":"ETHEREUM_USDT"}')`,
	} {
		if _, err := db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	evidence := validSafeheronAliasRepairRequest().Evidence
	scanner := newDBSafeheronAliasRepairScanner(t, db)
	dryRun, err := scanner.ScanSafeheronAliasNull(context.Background(), evidence, 0, 10)
	if err != nil || dryRun.AliasNull != 1 || dryRun.Repairable != 1 || dryRun.Applied != 0 {
		t.Fatalf("dry-run = %#v, %v", dryRun, err)
	}
	applied, err := scanner.ScanAndApplySafeheronAliasNull(context.Background(), evidence, 0, 10)
	if err != nil || applied.Applied != 1 {
		t.Fatalf("apply = %#v, %v", applied, err)
	}
	var alias, version string
	if err := db.QueryRowContext(context.Background(), `SELECT provider_occurrence_key, provider_occurrence_algorithm_version FROM company_fund_transactions`).Scan(&alias, &version); err != nil {
		t.Fatal(err)
	}
	if alias == "" || version != SafeheronOccurrenceAlgorithmVersion {
		t.Fatalf("persisted alias = %q / %q", alias, version)
	}
}
