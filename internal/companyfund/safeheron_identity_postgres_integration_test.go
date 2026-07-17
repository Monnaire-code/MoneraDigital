package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/shopspring/decimal"
)

const safeheronIdentityPostgresGate = "RUN_COMPANY_FUND_SAFEHERON_IDENTITY_INTEGRATION"

// TestSafeheronIdentityPostgresIntegration is intentionally opt-in. It uses
// the caller's existing DATABASE_URL and creates a unique schema per scenario;
// it never creates or requires a separately named test database. The gate is
// checked before DATABASE_URL is read or a PostgreSQL driver is opened.
func TestSafeheronIdentityPostgresIntegration(t *testing.T) {
	if os.Getenv(safeheronIdentityPostgresGate) != "1" {
		t.Skip("set RUN_COMPANY_FUND_SAFEHERON_IDENTITY_INTEGRATION=1 to run PostgreSQL Safeheron identity integration coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when Safeheron identity integration tests are enabled")
	}

	t.Run("same pair concurrent replay persists one row", func(t *testing.T) {
		db := newSafeheronIdentityPostgresFixture(t, databaseURL)
		input := safeheronIdentityIntegrationInput("same-pair", "occ-same", "0xsame")
		results, errs := concurrentSafeheronIdentityUpserts(db, input, input)
		requireSafeheronIdentitySuccessCount(t, results, errs, 2)
		if insertedCount(results) != 1 {
			t.Fatalf("inserted results = %#v; want exactly one initial insert", results)
		}
		rows := readSafeheronIdentityRows(t, db)
		if len(rows) != 1 || rows[0].MovementKey != input.MovementKey || rows[0].OccurrenceKey != input.ProviderOccurrenceKey {
			t.Fatalf("persisted rows = %#v; want one authoritative identity pair", rows)
		}
	})

	t.Run("different v2 keys sharing one occurrence allow only one side", func(t *testing.T) {
		db := newSafeheronIdentityPostgresFixture(t, databaseURL)
		first := safeheronIdentityIntegrationInput("v2-left", "occ-shared", "0xshared")
		second := safeheronIdentityIntegrationInput("v2-right", "occ-shared", "0xshared")
		results, errs := concurrentSafeheronIdentityUpserts(db, first, second)
		if successCount(errs) != 1 || errorCount(errs) != 1 || insertedCount(results) != 1 {
			t.Fatalf("results = %#v, errors = %#v; want one insert and one invariant failure", results, errs)
		}
		if !strings.Contains(firstError(errs).Error(), "occurrence alias points to another v2 movement") {
			t.Fatalf("invariant error = %v", firstError(errs))
		}
		rows := readSafeheronIdentityRows(t, db)
		if len(rows) != 1 || rows[0].OccurrenceKey != first.ProviderOccurrenceKey {
			t.Fatalf("persisted rows = %#v; shared occurrence must identify one row", rows)
		}
	})

	t.Run("split movement and occurrence matches fail closed", func(t *testing.T) {
		db := newSafeheronIdentityPostgresFixture(t, databaseURL)
		input := safeheronIdentityIntegrationInput("split-target", "occ-target", "0xsplit")
		insertSafeheronIdentityFixtureRow(t, db, input.MovementKey, SafeheronMovementIdentityAlgorithmVersion, "safeheron-occurrence-v1:other", "0xsplit")
		insertSafeheronIdentityFixtureRow(t, db, "v1:legacy-other", MovementIdentityAlgorithmVersion, input.ProviderOccurrenceKey, "0xsplit")

		result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
		if err == nil || !strings.Contains(err.Error(), "identity pair resolves to 2 rows") || result.Inserted {
			t.Fatalf("split-pair upsert = %#v, %v; want fail-closed invariant", result, err)
		}
		if rows := readSafeheronIdentityRows(t, db); len(rows) != 2 {
			t.Fatalf("split invariant changed persisted rows: %#v", rows)
		}
	})

	t.Run("legacy occurrence replay preserves v1 movement key", func(t *testing.T) {
		db := newSafeheronIdentityPostgresFixture(t, databaseURL)
		input := safeheronIdentityIntegrationInput("new-v2", "legacy-replay", "0xlegacy")
		const legacyMovementKey = "v1:legacy-movement-key"
		insertSafeheronIdentityFixtureRow(t, db, legacyMovementKey, MovementIdentityAlgorithmVersion, input.ProviderOccurrenceKey, "0xlegacy")

		result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
		if err != nil || result.Inserted {
			t.Fatalf("legacy replay = %#v, %v; want update of existing alias", result, err)
		}
		rows := readSafeheronIdentityRows(t, db)
		if len(rows) != 1 || rows[0].MovementKey != legacyMovementKey || rows[0].IdentityVersion != MovementIdentityAlgorithmVersion {
			t.Fatalf("legacy replay rewrote persisted identity: %#v", rows)
		}
	})

	t.Run("same tx hash retains multiple movements", func(t *testing.T) {
		db := newSafeheronIdentityPostgresFixture(t, databaseURL)
		first := safeheronIdentityIntegrationInput("batch-0", "batch-occ-0", "0xbatch")
		second := safeheronIdentityIntegrationInput("batch-1", "batch-occ-1", "0xbatch")
		second.MovementIndex = 1
		for _, input := range []TransactionUpsertInput{first, second} {
			result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
			if err != nil || !result.Inserted {
				t.Fatalf("batch movement upsert = %#v, %v", result, err)
			}
		}
		rows := readSafeheronIdentityRows(t, db)
		if len(rows) != 2 || rows[0].TxHash != "0xbatch" || rows[1].TxHash != "0xbatch" || rows[0].MovementKey == rows[1].MovementKey {
			t.Fatalf("same TxHash movements were collapsed: %#v", rows)
		}
	})
}

type safeheronIdentityPostgresRow struct {
	MovementKey     string
	IdentityVersion string
	OccurrenceKey   string
	TxHash          string
}

func newSafeheronIdentityPostgresFixture(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	t.Cleanup(func() { _ = adminDB.Close() })

	schema := fmt.Sprintf("safeheron_identity_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema %s: %v", schema, err)
		}
	})

	fixtureConfig := adminConfig.Copy()
	if fixtureConfig.RuntimeParams == nil {
		fixtureConfig.RuntimeParams = make(map[string]string)
	}
	fixtureConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*fixtureConfig)
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), safeheronIdentityPostgresFixtureDDL); err != nil {
		t.Fatalf("create Safeheron identity fixture: %v", err)
	}
	return db
}

func concurrentSafeheronIdentityUpserts(db *sql.DB, inputs ...TransactionUpsertInput) ([]TransactionUpsertResult, []error) {
	results := make([]TransactionUpsertResult, len(inputs))
	errs := make([]error, len(inputs))
	start := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(len(inputs))
	done.Add(len(inputs))
	for index := range inputs {
		go func(index int) {
			defer done.Done()
			ready.Done()
			<-start
			results[index], errs[index] = NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), inputs[index])
		}(index)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return results, errs
}

func safeheronIdentityIntegrationInput(movementSuffix, occurrenceSuffix, txHash string) TransactionUpsertInput {
	accountID := int64(100)
	asset := AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "ETHEREUM_USDT"}
	return TransactionUpsertInput{
		MovementKey:                        "safeheron-v2:" + movementSuffix,
		Channel:                            ChannelSafeheron,
		IdentityAlgorithmVersion:           SafeheronMovementIdentityAlgorithmVersion,
		ProviderOccurrenceKey:              "safeheron-occurrence-v1:" + occurrenceSuffix,
		ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion,
		ProviderAccountKey:                 "safeheron-account",
		ProviderTransactionID:              "safeheron-transaction",
		MovementKind:                       MovementKindPrincipal,
		TransferMode:                       TransferModeBatch,
		Direction:                          DirectionOutflow,
		FromCompanyFundAccountID:           &accountID,
		Currency:                           asset.Currency,
		Asset:                              asset,
		Amount:                             decimal.RequireFromString("1.25"),
		FirstSeenSource:                    TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Asset:    &asset,
			TxHash:   &txHash,
			Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook},
		},
	}
}

func insertSafeheronIdentityFixtureRow(t *testing.T, db *sql.DB, movementKey, identityVersion, occurrenceKey, txHash string) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
INSERT INTO company_fund_transactions (
 channel, provider_account_key, provider_transaction_id, movement_index, movement_key,
 identity_algorithm_version, movement_kind, transfer_mode, transaction_direction,
 from_company_fund_account_id, currency, chain_code, provider_asset_key, amount,
 tx_hash, provider_fact_source, status_rank, first_seen_source, last_seen_source,
 provider_occurrence_key, provider_occurrence_algorithm_version
) VALUES (
 'SAFEHERON', 'safeheron-account', 'safeheron-transaction', 0, $1,
 $2, 'PRINCIPAL', 'BATCH', 'OUTFLOW',
 100, 'USDT', 'ETHEREUM', 'ETHEREUM_USDT', 1.25,
 $3, 'WEBHOOK', 0, 'WEBHOOK', 'WEBHOOK', $4, 'safeheron-occurrence-v1'
)`, movementKey, identityVersion, txHash, occurrenceKey)
	if err != nil {
		t.Fatalf("insert Safeheron identity fixture row: %v", err)
	}
}

func readSafeheronIdentityRows(t *testing.T, db *sql.DB) []safeheronIdentityPostgresRow {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Errorf("rollback read-only assertion transaction: %v", err)
		}
	}()
	result, err := tx.QueryContext(context.Background(), `
SELECT movement_key, identity_algorithm_version, provider_occurrence_key, COALESCE(tx_hash, '')
FROM company_fund_transactions
ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	rows := make([]safeheronIdentityPostgresRow, 0)
	for result.Next() {
		var row safeheronIdentityPostgresRow
		if err := result.Scan(&row.MovementKey, &row.IdentityVersion, &row.OccurrenceKey, &row.TxHash); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if err := result.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func requireSafeheronIdentitySuccessCount(t *testing.T, results []TransactionUpsertResult, errs []error, want int) {
	t.Helper()
	if successCount(errs) != want {
		t.Fatalf("results = %#v, errors = %#v; want %d successes", results, errs, want)
	}
}

func successCount(errs []error) int {
	count := 0
	for _, err := range errs {
		if err == nil {
			count++
		}
	}
	return count
}

func errorCount(errs []error) int { return len(errs) - successCount(errs) }

func insertedCount(results []TransactionUpsertResult) int {
	count := 0
	for _, result := range results {
		if result.Inserted {
			count++
		}
	}
	return count
}

func firstError(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

const safeheronIdentityPostgresFixtureDDL = `
CREATE TABLE company_fund_transactions (
 id BIGSERIAL PRIMARY KEY,
 channel VARCHAR(16) NOT NULL,
 provider_account_key VARCHAR(128),
 provider_transaction_id VARCHAR(256),
 provider_event_id VARCHAR(256),
 provider_movement_id VARCHAR(256),
 movement_index INTEGER NOT NULL DEFAULT 0,
 movement_key VARCHAR(256) NOT NULL UNIQUE,
 identity_algorithm_version VARCHAR(64) NOT NULL,
 provider_transaction_fact_id BIGINT,
 parent_transaction_id BIGINT,
 reversal_of_transaction_id BIGINT,
 conversion_group_key VARCHAR(256),
 conversion_leg VARCHAR(8),
 conversion_group_status VARCHAR(16),
 movement_kind VARCHAR(16) NOT NULL,
 transfer_mode VARCHAR(16) NOT NULL,
 transaction_direction VARCHAR(24) NOT NULL,
 from_company_fund_account_id BIGINT,
 to_company_fund_account_id BIGINT,
 from_address_or_account VARCHAR(512),
 to_address_or_account VARCHAR(512),
 payer_name VARCHAR(256),
 payee_name VARCHAR(256),
 from_company_entity_snapshot VARCHAR(256),
 from_fund_account_name_snapshot VARCHAR(256),
 from_sub_account_name_snapshot VARCHAR(256),
 from_account_type_snapshot VARCHAR(64),
 to_company_entity_snapshot VARCHAR(256),
 to_fund_account_name_snapshot VARCHAR(256),
 to_sub_account_name_snapshot VARCHAR(256),
 to_account_type_snapshot VARCHAR(64),
 currency VARCHAR(64) NOT NULL,
 chain_code VARCHAR(64),
 provider_asset_key VARCHAR(256),
 asset_contract VARCHAR(256),
 amount NUMERIC(65,18) NOT NULL,
 provider_reported_fee_amount NUMERIC(65,18),
 provider_reported_fee_currency VARCHAR(64),
 fee_details JSONB NOT NULL DEFAULT '{}'::jsonb,
 tx_hash VARCHAR(256),
 provider_status VARCHAR(64),
 provider_status_version BIGINT,
 provider_updated_at TIMESTAMPTZ,
 provider_fact_source VARCHAR(24) NOT NULL,
 status_rank SMALLINT NOT NULL DEFAULT 0,
 occurred_at TIMESTAMPTZ,
 completed_at TIMESTAMPTZ,
 first_seen_source VARCHAR(16) NOT NULL,
 last_seen_source VARCHAR(16) NOT NULL,
 latest_provider_event_id BIGINT,
 raw_snapshot_digest VARCHAR(64),
 block_height BIGINT,
 block_hash VARCHAR(256),
 is_dust BOOLEAN NOT NULL DEFAULT false,
 dust_policy_id BIGINT,
 dust_threshold NUMERIC(65,18),
 is_source_phishing BOOLEAN,
 is_destination_phishing BOOLEAN,
 is_unrecognized_asset BOOLEAN NOT NULL DEFAULT false,
 aml_lock BOOLEAN,
 aml_screening_state VARCHAR(32) NOT NULL DEFAULT 'NOT_SCREENED',
 aml_risk_level VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN',
 risk_flags JSONB NOT NULL DEFAULT '[]'::jsonb,
 auto_excluded_from_summary BOOLEAN NOT NULL DEFAULT false,
 provider_occurrence_key VARCHAR(256),
 provider_occurrence_algorithm_version VARCHAR(64),
 last_synced_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX safeheron_identity_occurrence_unique
 ON company_fund_transactions (provider_occurrence_key)
 WHERE channel = 'SAFEHERON' AND provider_occurrence_key IS NOT NULL;
CREATE INDEX safeheron_identity_tx_hash_non_unique
 ON company_fund_transactions (channel, tx_hash)
 WHERE tx_hash IS NOT NULL;
`
