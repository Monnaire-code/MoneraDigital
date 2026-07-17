package deposit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const depositPostgresIntegrationGate = "RUN_DEPOSIT_POSTGRES_INTEGRATION"

// TestDepositPoisonEventPostgresIntegration is opt-in and creates a unique
// schema on the caller-provided PostgreSQL database. The gate is checked before
// DATABASE_URL is read or a driver is opened.
func TestDepositPoisonEventPostgresIntegration(t *testing.T) {
	if os.Getenv(depositPostgresIntegrationGate) != "1" {
		t.Skip("set RUN_DEPOSIT_POSTGRES_INTEGRATION=1 to run isolated deposit PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when deposit PostgreSQL integration tests are enabled")
	}

	db := newDepositPostgresFixture(t, databaseURL)
	repo := NewRepository(db)

	t.Run("production foreign key rejects synthetic user zero", func(t *testing.T) {
		_, err := db.ExecContext(context.Background(), `
			INSERT INTO deposits
				(user_id, tx_hash, amount, asset, chain, status)
			VALUES (0, 'tx-pg-user-zero', 1, 'ETH', 'ETHEREUM', 'MANUAL_REVIEW')`)
		if err == nil {
			t.Fatal("expected deposits.user_id foreign key to reject synthetic user 0")
		}
	})

	t.Run("unsupported coin without an owner never inserts a synthetic user", func(t *testing.T) {
		if got := len(testUnknownCoinKey64); got != 64 {
			t.Fatalf("test fixture CoinKey length = %d, want 64", got)
		}

		eventID := insertDepositIntegrationEvent(t, db, PayloadEnvelope{
			EventType: "TRANSACTION_CREATED",
			EventDetail: PayloadEventDetail{
				TxKey:                "tx-pg-unsupported",
				CoinKey:              testUnknownCoinKey64,
				TxAmount:             "1",
				TransactionStatus:    "COMPLETED",
				TransactionSubStatus: "CONFIRMED",
				TransactionDirection: "INFLOW",
				SourceAddress:        "0xfrom",
				DestinationAddress:   "0xto",
			},
		})

		processed, err := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH", "0.0001", 11), nil).
			ProcessOne(context.Background())
		if err == nil || !processed {
			t.Fatalf("process unsupported event: processed=%v err=%v", processed, err)
		}

		var depositCount int
		if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM deposits WHERE safeheron_tx_key=$1`, "tx-pg-unsupported").Scan(&depositCount); err != nil {
			t.Fatal(err)
		}
		if depositCount != 0 {
			t.Fatalf("unsupported unowned deposit count=%d", depositCount)
		}
		var eventStatus string
		if err := db.QueryRowContext(context.Background(), `SELECT process_status FROM safeheron_webhook_events WHERE id=$1`, eventID).Scan(&eventStatus); err != nil {
			t.Fatal(err)
		}
		if eventStatus != ProcessError {
			t.Fatalf("unsupported raw event status=%q", eventStatus)
		}
	})

	t.Run("aborted FK statement rolls back before conditional error finalization", func(t *testing.T) {
		if _, err := db.ExecContext(context.Background(), `
			INSERT INTO address_pool (address, network_family, assigned_user_id)
			VALUES ('0xowned', 'EVM', 42)`); err != nil {
			t.Fatal(err)
		}
		eventID := insertDepositIntegrationEvent(t, db, PayloadEnvelope{
			EventType: "TRANSACTION_STATUS_CHANGED",
			EventDetail: PayloadEventDetail{
				TxKey:                "tx-pg-bad-fk",
				CoinKey:              "KNOWN_BAD_FK",
				TxAmount:             "1",
				TransactionStatus:    "COMPLETED",
				TransactionSubStatus: "CONFIRMED",
				TransactionDirection: "INFLOW",
				DestinationAddress:   "0xowned",
			},
		})

		processed, err := NewService(repo,
			newTestRegistry("USDC", "MISSING_CHAIN", "KNOWN_BAD_FK", "0.0001", 999), nil).
			ProcessOne(context.Background())
		if !processed || err == nil {
			t.Fatalf("expected row-specific FK failure, processed=%v err=%v", processed, err)
		}
		if errors.Is(err, ErrMarkErrorFailed) || strings.Contains(err.Error(), "25P02") {
			t.Fatalf("error finalization ran in an aborted transaction: %v", err)
		}
		if got := readDepositIntegrationEventStatus(t, db, eventID); got != ProcessError {
			t.Fatalf("failed raw event status = %q, want ERROR", got)
		}
		var count int
		if err := db.QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM deposits WHERE safeheron_tx_key = $1`, "tx-pg-bad-fk").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed deposit transaction left %d rows", count)
		}
	})

	t.Run("conditional error finalization preserves concurrent done", func(t *testing.T) {
		eventID := insertDepositIntegrationEvent(t, db, PayloadEnvelope{
			EventType:   "ACCOUNT_CREATED",
			EventDetail: PayloadEventDetail{TxKey: "tx-pg-concurrent-done"},
		})
		if _, err := db.ExecContext(context.Background(),
			`UPDATE safeheron_webhook_events SET process_status = 'DONE' WHERE id = $1`, eventID); err != nil {
			t.Fatal(err)
		}
		updated, err := repo.MarkEventErrorNoTx(context.Background(), eventID, "stale worker")
		if err != nil {
			t.Fatal(err)
		}
		if updated || readDepositIntegrationEventStatus(t, db, eventID) != ProcessDone {
			t.Fatal("conditional ERROR finalization overwrote concurrent DONE")
		}
	})
}

func newDepositPostgresFixture(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	t.Cleanup(func() { _ = adminDB.Close() })

	schema := fmt.Sprintf("deposit_poison_%d", time.Now().UnixNano())
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
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), depositPostgresFixtureDDL); err != nil {
		t.Fatalf("create deposit fixture: %v", err)
	}
	return db
}

func insertDepositIntegrationEvent(t *testing.T, db *sql.DB, env PayloadEnvelope) int64 {
	t.Helper()
	raw, err := MarshalRawPayload(env)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = db.QueryRowContext(context.Background(), `
		INSERT INTO safeheron_webhook_events
			(event_id, event_type, safeheron_tx_key, raw_payload, process_status)
		VALUES ($1, $2, $3, $4, 'PENDING') RETURNING id`,
		env.EventDetail.TxKey+":"+env.EventType, env.EventType, env.EventDetail.TxKey, raw).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func readDepositIntegrationEventStatus(t *testing.T, db *sql.DB, eventID int64) string {
	t.Helper()
	var status string
	if err := db.QueryRowContext(context.Background(),
		`SELECT process_status FROM safeheron_webhook_events WHERE id = $1`, eventID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

const depositPostgresFixtureDDL = `
CREATE TABLE users (
    id SERIAL PRIMARY KEY,
    email VARCHAR(255) UNIQUE NOT NULL,
    password VARCHAR(255) NOT NULL
);
INSERT INTO users (id, email, password)
VALUES (42, 'deposit-fixture@example.com', 'not-used');
CREATE TABLE chains (code VARCHAR(32) PRIMARY KEY);
CREATE TABLE coin_chains (
    id INT PRIMARY KEY,
    chain_code VARCHAR(32) NOT NULL REFERENCES chains(code)
);
CREATE TABLE deposits (
    id BIGSERIAL PRIMARY KEY,
    user_id INT NOT NULL,
    tx_hash VARCHAR(255) NOT NULL UNIQUE,
    amount NUMERIC NOT NULL,
    asset VARCHAR(50) NOT NULL,
    chain VARCHAR(50) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING',
    from_address VARCHAR(255),
    to_address VARCHAR(255),
    safeheron_tx_key VARCHAR(128),
    safeheron_coin_key VARCHAR(64),
    chain_code VARCHAR(32) REFERENCES chains(code),
    coin_chain_id INT REFERENCES coin_chains(id),
    block_height BIGINT,
    block_hash VARCHAR(128),
    safeheron_status VARCHAR(32),
    safeheron_sub_status VARCHAR(64),
    status_rank SMALLINT NOT NULL DEFAULT 0,
    credited_at TIMESTAMP,
    failed_reason TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
    CONSTRAINT deposits_user_id_users_id_fk
        FOREIGN KEY (user_id) REFERENCES users(id)
);
CREATE UNIQUE INDEX idx_deposits_safeheron_tx_key
    ON deposits(safeheron_tx_key) WHERE safeheron_tx_key IS NOT NULL;
CREATE TABLE safeheron_webhook_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL UNIQUE,
    event_type VARCHAR(64) NOT NULL,
    safeheron_tx_key VARCHAR(128),
    customer_ref_id VARCHAR(128),
    raw_payload JSONB NOT NULL,
    process_status VARCHAR(16) NOT NULL,
    process_attempts INT NOT NULL DEFAULT 0,
    error_message TEXT,
    received_at TIMESTAMP NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMP
);
CREATE TABLE address_pool (
    address VARCHAR(255) NOT NULL,
    network_family VARCHAR(32) NOT NULL,
    assigned_user_id INT REFERENCES users(id) ON DELETE SET NULL
);
`
