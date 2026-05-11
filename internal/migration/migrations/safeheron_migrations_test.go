package migrations

import (
	"fmt"
	"os"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

// --- Metadata Tests ---

func TestSafeheronMigrations_Versions(t *testing.T) {
	tests := []struct {
		name    string
		m       migration.Migration
		version string
	}{
		{"CreateChainsTable", &CreateChainsTable{}, "015"},
		{"CreateCoinsTable", &CreateCoinsTable{}, "016"},
		{"CreateCoinChainsTable", &CreateCoinChainsTable{}, "017"},
		{"CreateAddressPoolTable", &CreateAddressPoolTable{}, "018"},
		{"CreateSafeheronWebhookEventsTable", &CreateSafeheronWebhookEventsTable{}, "019"},
		{"ExtendDepositsForSafeheron", &ExtendDepositsForSafeheron{}, "020"},
		{"SeedSafeheronPhase1", &SeedSafeheronPhase1{}, "021"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Version(); got != tt.version {
				t.Errorf("Version() = %q, want %q", got, tt.version)
			}
		})
	}
}

func TestSafeheronMigrations_Descriptions(t *testing.T) {
	migs := []migration.Migration{
		&CreateChainsTable{},
		&CreateCoinsTable{},
		&CreateCoinChainsTable{},
		&CreateAddressPoolTable{},
		&CreateSafeheronWebhookEventsTable{},
		&ExtendDepositsForSafeheron{},
		&SeedSafeheronPhase1{},
	}

	for _, m := range migs {
		t.Run(m.Version(), func(t *testing.T) {
			if m.Description() == "" {
				t.Error("Description should not be empty")
			}
		})
	}
}

func TestSafeheronMigrations_VersionOrder(t *testing.T) {
	migs := []migration.Migration{
		&CreateChainsTable{},
		&CreateCoinsTable{},
		&CreateCoinChainsTable{},
		&CreateAddressPoolTable{},
		&CreateSafeheronWebhookEventsTable{},
		&ExtendDepositsForSafeheron{},
		&SeedSafeheronPhase1{},
	}

	for i := 1; i < len(migs); i++ {
		prev := migs[i-1].Version()
		curr := migs[i].Version()
		if curr <= prev {
			t.Errorf("Migration %s (version %s) should be after %s (version %s)",
				migs[i].Description(), curr, migs[i-1].Description(), prev)
		}
	}
}

// --- Up() Tests with sqlmock ---

func TestCreateChainsTable_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS chains").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateChainsTable{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateChainsTable_Up_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS chains").WillReturnError(fmt.Errorf("db error"))

	m := &CreateChainsTable{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "failed to create chains table: db error" {
		t.Errorf("unexpected error message: %s", err)
	}
}

func TestCreateCoinsTable_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coins").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateCoinsTable{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateCoinsTable_Up_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coins").WillReturnError(fmt.Errorf("db error"))

	m := &CreateCoinsTable{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "failed to create coins table: db error" {
		t.Errorf("unexpected error message: %s", err)
	}
}

func TestCreateCoinChainsTable_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coin_chains").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateCoinChainsTable{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateCoinChainsTable_Up_CreateTableError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coin_chains").WillReturnError(fmt.Errorf("db error"))

	m := &CreateCoinChainsTable{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "failed to create coin_chains table: db error" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCreateCoinChainsTable_Up_UniqueConstraintError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coin_chains").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnError(fmt.Errorf("constraint error"))

	m := &CreateCoinChainsTable{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "failed to add unique constraint on coin_chains: constraint error" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCreateCoinChainsTable_Up_IndexError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS coin_chains").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnError(fmt.Errorf("index error"))

	m := &CreateCoinChainsTable{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "failed to create coin_chains indexes: index error" {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestCreateAddressPoolTable_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS address_pool").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateAddressPoolTable{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateAddressPoolTable_Up_Errors(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantError string
	}{
		{"CreateTableError", 0, "failed to create address_pool table"},
		{"UniqueConstraintError", 1, "failed to add unique constraint on address_pool"},
		{"IndexError", 2, "failed to create address_pool indexes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for i := 0; i < tt.failAt; i++ {
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec(".*").WillReturnError(fmt.Errorf("db error"))

			m := &CreateAddressPoolTable{}
			err = m.Up(db)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantError) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestCreateSafeheronWebhookEventsTable_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS safeheron_webhook_events").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE INDEX IF NOT EXISTS").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateSafeheronWebhookEventsTable{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateSafeheronWebhookEventsTable_Up_Errors(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantError string
	}{
		{"CreateTableError", 0, "failed to create safeheron_webhook_events table"},
		{"IndexError", 1, "failed to create safeheron_webhook_events indexes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for i := 0; i < tt.failAt; i++ {
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec(".*").WillReturnError(fmt.Errorf("db error"))

			m := &CreateSafeheronWebhookEventsTable{}
			err = m.Up(db)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantError) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

func TestExtendDepositsForSafeheron_Up(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("ALTER TABLE deposits").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX IF NOT EXISTS idx_deposits_safeheron_tx_key").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE deposits SET status").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DO .* BEGIN").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE UNIQUE INDEX IF NOT EXISTS idx_account_user_currency").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &ExtendDepositsForSafeheron{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestExtendDepositsForSafeheron_Up_Errors(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantError string
	}{
		{"AddColumnsError", 0, "failed to add safeheron columns to deposits"},
		{"AddFKsError", 1, "failed to add foreign keys to deposits"},
		{"PartialUniqueIdxError", 2, "failed to create partial unique index"},
		{"NormalizeStatusError", 3, "failed to normalize existing deposits status"},
		{"CheckConstraintError", 4, "failed to add status check constraint"},
		{"AccountUniqueIdxError", 5, "failed to create unique index on account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for i := 0; i < tt.failAt; i++ {
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec(".*").WillReturnError(fmt.Errorf("db error"))

			m := &ExtendDepositsForSafeheron{}
			err = m.Up(db)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantError) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

// --- Down() Tests ---

func TestCreateChainsTable_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP TABLE IF EXISTS chains").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateChainsTable{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestCreateCoinsTable_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP TABLE IF EXISTS coins").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateCoinsTable{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
}

func TestCreateCoinChainsTable_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP TABLE IF EXISTS coin_chains").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateCoinChainsTable{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
}

func TestCreateAddressPoolTable_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP TABLE IF EXISTS address_pool").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateAddressPoolTable{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
}

func TestCreateSafeheronWebhookEventsTable_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP TABLE IF EXISTS safeheron_webhook_events").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &CreateSafeheronWebhookEventsTable{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
}

func TestExtendDepositsForSafeheron_Down(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DROP INDEX IF EXISTS idx_account_user_currency").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE deposits DROP CONSTRAINT IF EXISTS ck_deposits_status").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DROP INDEX IF EXISTS idx_deposits_safeheron_tx_key").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_coin_chain_id_fkey").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE deposits DROP CONSTRAINT IF EXISTS deposits_chain_code_fkey").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("ALTER TABLE deposits").WillReturnResult(sqlmock.NewResult(0, 0))

	m := &ExtendDepositsForSafeheron{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestExtendDepositsForSafeheron_Down_BlockedInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := &ExtendDepositsForSafeheron{}
	err = m.Down(db)
	if err == nil {
		t.Fatal("expected error in production, got nil")
	}
	if !contains(err.Error(), "BLOCKED") {
		t.Errorf("error should contain BLOCKED, got: %s", err)
	}
}

// --- Seed Migration 021 Tests ---

func TestSeedSafeheronPhase1_Up_TestnetDefault(t *testing.T) {
	t.Setenv("APP_ENV", "")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("INSERT INTO coin_chains.*ETH\\(SEPOLIA\\)_ETHEREUM_SEPOLIA").WillReturnResult(sqlmock.NewResult(0, 3))

	m := &SeedSafeheronPhase1{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_Production(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("INSERT INTO coin_chains.*USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET").WillReturnResult(sqlmock.NewResult(0, 8))

	m := &SeedSafeheronPhase1{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_LocalEnv(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("INSERT INTO coin_chains.*TRX\\(SHASTA\\)_TRON_TESTNET").WillReturnResult(sqlmock.NewResult(0, 3))

	m := &SeedSafeheronPhase1{}
	if err := m.Up(db); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_UnknownEnv(t *testing.T) {
	t.Setenv("APP_ENV", "staging")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))

	m := &SeedSafeheronPhase1{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error for unknown APP_ENV")
	}
	if !contains(err.Error(), "unknown APP_ENV") {
		t.Errorf("error should contain 'unknown APP_ENV', got: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_SeedChainsError(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnError(fmt.Errorf("db error"))

	m := &SeedSafeheronPhase1{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "failed to seed chains") {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_SeedCoinsError(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnError(fmt.Errorf("db error"))

	m := &SeedSafeheronPhase1{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "failed to seed coins") {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_CoinChainsError(t *testing.T) {
	t.Setenv("APP_ENV", "local")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("INSERT INTO coin_chains").WillReturnError(fmt.Errorf("db error"))

	m := &SeedSafeheronPhase1{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "failed to seed testnet coin_chains") {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestSeedSafeheronPhase1_Up_ProductionCoinChainsError(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("INSERT INTO coin_chains").WillReturnError(fmt.Errorf("db error"))

	m := &SeedSafeheronPhase1{}
	err = m.Up(db)
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "failed to seed production coin_chains") {
		t.Errorf("unexpected error: %s", err)
	}
}

func TestSeedSafeheronPhase1_Down(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("DELETE FROM coin_chains WHERE safeheron_coin_key IN").WillReturnResult(sqlmock.NewResult(0, 11))
	mock.ExpectExec("DELETE FROM coins WHERE symbol IN").WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("DELETE FROM chains WHERE code IN").WillReturnResult(sqlmock.NewResult(0, 3))

	m := &SeedSafeheronPhase1{}
	if err := m.Down(db); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %s", err)
	}
}

func TestSeedSafeheronPhase1_Down_Errors(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantError string
	}{
		{"DeleteCoinChainsError", 0, "failed to delete phase1 coin_chains"},
		{"DeleteCoinsError", 1, "failed to delete phase1 coins"},
		{"DeleteChainsError", 2, "failed to delete phase1 chains"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for i := 0; i < tt.failAt; i++ {
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec(".*").WillReturnError(fmt.Errorf("db error"))

			m := &SeedSafeheronPhase1{}
			err = m.Down(db)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantError) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

// --- Down Error Tests for simple tables ---

func TestSimpleMigrations_Down_Errors(t *testing.T) {
	tests := []struct {
		name string
		m    migration.Migration
	}{
		{"ChainsDown", &CreateChainsTable{}},
		{"CoinsDown", &CreateCoinsTable{}},
		{"CoinChainsDown", &CreateCoinChainsTable{}},
		{"AddressPoolDown", &CreateAddressPoolTable{}},
		{"WebhookEventsDown", &CreateSafeheronWebhookEventsTable{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			mock.ExpectExec("DROP TABLE IF EXISTS").WillReturnError(fmt.Errorf("db error"))

			err = tt.m.Down(db)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestExtendDepositsForSafeheron_Down_Errors(t *testing.T) {
	tests := []struct {
		name      string
		failAt    int
		wantError string
	}{
		{"DropAccountIdxError", 0, "failed to drop account unique index"},
		{"DropCheckConstraintError", 1, "failed to drop deposits status constraint"},
		{"DropTxKeyIdxError", 2, "failed to drop deposits safeheron_tx_key index"},
		{"DropCoinChainFKError", 3, "failed to drop deposits_coin_chain_id_fkey"},
		{"DropChainCodeFKError", 4, "failed to drop deposits_chain_code_fkey"},
		{"DropColumnsError", 5, "failed to drop safeheron columns from deposits"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("APP_ENV", "local")

			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			for i := 0; i < tt.failAt; i++ {
				mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
			}
			mock.ExpectExec(".*").WillReturnError(fmt.Errorf("db error"))

			m := &ExtendDepositsForSafeheron{}
			err = m.Down(db)
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantError) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantError)
			}
		})
	}
}

// --- Seed Data Verification ---

func TestSeedSafeheronPhase1_ProductionCoinKeys(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))

	expectedKeys := []string{
		"ETH", "USDT_ERC20", "USDC_ERC20", "BNB_BSC",
		"USDT_BEP20", "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET",
		"TRX", "USDT_TRC20",
	}
	for _, key := range expectedKeys {
		mock.ExpectExec(".*" + escapeRegex(key) + ".*").WillReturnResult(sqlmock.NewResult(0, 8))
	}

	m := &SeedSafeheronPhase1{}
	_ = m.Up(db)
}

func TestSeedSafeheronPhase1_TestnetCoinKeys(t *testing.T) {
	t.Setenv("APP_ENV", "test")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectExec("INSERT INTO chains").WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO coins").WillReturnResult(sqlmock.NewResult(0, 5))

	expectedKeys := []string{
		"ETH\\(SEPOLIA\\)_ETHEREUM_SEPOLIA",
		"USDCOIN_ERC20_ETHEREUM_SEPOLIA",
		"TRX\\(SHASTA\\)_TRON_TESTNET",
	}
	for _, key := range expectedKeys {
		mock.ExpectExec(".*" + key + ".*").WillReturnResult(sqlmock.NewResult(0, 3))
	}

	m := &SeedSafeheronPhase1{}
	_ = m.Up(db)
}

// --- Helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func escapeRegex(s string) string {
	result := ""
	for _, c := range s {
		switch c {
		case '(', ')', '[', ']', '{', '}', '.', '*', '+', '?', '^', '$', '|', '\\':
			result += "\\" + string(c)
		default:
			result += string(c)
		}
	}
	return result
}

// Ensure os import is used in test context
var _ = os.Getenv
