package migrations

import (
	"database/sql"
	"fmt"
	"os"

	"monera-digital/internal/migration"
)

type CreateApprovalAndSweepTables struct{}

func (m *CreateApprovalAndSweepTables) Version() string { return "023" }
func (m *CreateApprovalAndSweepTables) Description() string {
	return "Create approval_records and sweep_transactions tables"
}

func (m *CreateApprovalAndSweepTables) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{"CreateApprovalRecords", (&CreateApprovalRecordsTable{}).Up},
		{"CreateSweepTransactions", (&CreateSweepTransactionsTable{}).Up},
	}
	for _, s := range steps {
		if err := s.fn(db); err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
	}
	return nil
}

func (m *CreateApprovalAndSweepTables) Down(db *sql.DB) error {
	if os.Getenv("APP_ENV") == "production" {
		return fmt.Errorf("BLOCKED: rollback of approval tables in production would destroy data; use a manual migration instead")
	}
	steps := []struct {
		name string
		fn   func(*sql.DB) error
	}{
		{"DropSweepTransactions", (&CreateSweepTransactionsTable{}).Down},
		{"DropApprovalRecords", (&CreateApprovalRecordsTable{}).Down},
	}
	for _, s := range steps {
		if err := s.fn(db); err != nil {
			return fmt.Errorf("step %s: %w", s.name, err)
		}
	}
	return nil
}

var _ migration.Migration = (*CreateApprovalAndSweepTables)(nil)

// ---------------------------------------------------------------------------
// Step 1: approval_records
// ---------------------------------------------------------------------------

type CreateApprovalRecordsTable struct{}

func (s *CreateApprovalRecordsTable) Up(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS approval_records (
			id                       BIGSERIAL    PRIMARY KEY,
			approval_id              VARCHAR(128) NOT NULL UNIQUE,
			callback_type            VARCHAR(32)  NOT NULL,
			tx_type                  VARCHAR(32),
			action                   VARCHAR(16)  NOT NULL,
			reason                   TEXT,
			tx_key                   VARCHAR(128),
			chain_symbol             VARCHAR(32)  DEFAULT 'UNKNOWN',
			coin_key                 VARCHAR(64),
			tx_amount                VARCHAR(64),
			source_account_key       VARCHAR(128),
			destination_account_key  VARCHAR(128),
			destination_account_type VARCHAR(32),
			destination_address      VARCHAR(256),
			customer_ref_id          VARCHAR(128),
			raw_request              JSONB        NOT NULL,
			created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_approval_records_tx_key
			ON approval_records(tx_key);
		CREATE INDEX IF NOT EXISTS idx_approval_records_created_at
			ON approval_records(created_at);
		CREATE INDEX IF NOT EXISTS idx_approval_records_action
			ON approval_records(action);
	`)
	if err != nil {
		return fmt.Errorf("create approval_records: %w", err)
	}
	return nil
}

func (s *CreateApprovalRecordsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS approval_records CASCADE`)
	if err != nil {
		return fmt.Errorf("drop approval_records: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Step 2: sweep_transactions
// ---------------------------------------------------------------------------

type CreateSweepTransactionsTable struct{}

func (s *CreateSweepTransactionsTable) Up(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sweep_transactions (
			id                       BIGSERIAL    PRIMARY KEY,
			tx_key                   VARCHAR(128) NOT NULL UNIQUE,
			tx_hash                  VARCHAR(256),
			customer_ref_id          VARCHAR(128),
			tx_type                  VARCHAR(32)  NOT NULL,
			chain_symbol             VARCHAR(32)  NOT NULL DEFAULT 'UNKNOWN',
			coin_key                 VARCHAR(64)  NOT NULL,
			fee_coin_key             VARCHAR(64),
			tx_amount                VARCHAR(64)  NOT NULL,
			estimate_fee             VARCHAR(64),
			source_account_key       VARCHAR(128),
			source_address           VARCHAR(256),
			destination_account_key  VARCHAR(128),
			destination_address      VARCHAR(256),
			tx_status                VARCHAR(32)  NOT NULL DEFAULT 'PENDING',
			tx_sub_status            VARCHAR(64),
			approval_id              VARCHAR(128),
			approval_action          VARCHAR(16),
			created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
			completed_at             TIMESTAMPTZ
		);

		CREATE INDEX IF NOT EXISTS idx_sweep_tx_type
			ON sweep_transactions(tx_type);
		CREATE INDEX IF NOT EXISTS idx_sweep_tx_status
			ON sweep_transactions(tx_status);
		CREATE INDEX IF NOT EXISTS idx_sweep_created_at
			ON sweep_transactions(created_at);
		CREATE INDEX IF NOT EXISTS idx_sweep_coin_key
			ON sweep_transactions(coin_key);
		CREATE INDEX IF NOT EXISTS idx_sweep_chain
			ON sweep_transactions(chain_symbol);
	`)
	if err != nil {
		return fmt.Errorf("create sweep_transactions: %w", err)
	}
	return nil
}

func (s *CreateSweepTransactionsTable) Down(db *sql.DB) error {
	_, err := db.Exec(`DROP TABLE IF EXISTS sweep_transactions CASCADE`)
	if err != nil {
		return fmt.Errorf("drop sweep_transactions: %w", err)
	}
	return nil
}
