package migrations

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

func TestMigration054MetadataIsControlledAndForwardOnly(t *testing.T) {
	var _ migration.Migration = (*AllowManualCompanyFundTransactions)(nil)
	var _ migration.ControlledMigration = (*AllowManualCompanyFundTransactions)(nil)
	value := &AllowManualCompanyFundTransactions{}
	if value.Version() != "054" || value.Description() == "" || value.RequiredPreexistingVersion() != "053" || value.RequiredExpectedCeiling() != "054" {
		t.Fatalf("unexpected migration 054 metadata")
	}
	if err := value.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up() = %v", err)
	}
	if err := value.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down() = %v", err)
	}
}

func TestMigration054ExecutesAndValidatesInsideTheRunnerTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	expectMigration054Success(mock)
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := (&AllowManualCompanyFundTransactions{}).UpTx(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration054ReturnsEveryDDLFailureForRollback(t *testing.T) {
	for _, testCase := range []struct {
		name, want string
		setup      func(sqlmock.Sqlmock)
	}{
		{name: "timeouts", want: "timeouts", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration054TimeoutsSQL)).WillReturnError(errors.New("timeout"))
		}},
		{name: "replace", want: "expand", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration054TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration054ReplaceConstraintsSQL)).WillReturnError(errors.New("replace"))
		}},
		{name: "validate", want: "validate", setup: func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration054TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration054ReplaceConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration054ValidateConstraintsSQL)).WillReturnError(errors.New("validate"))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectBegin()
			testCase.setup(mock)
			mock.ExpectRollback()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := (&AllowManualCompanyFundTransactions{}).UpTx(tx); err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("UpTx() = %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration054ExpandsOnlyTransactionSourceConstraints(t *testing.T) {
	source, err := os.ReadFile("054_allow_manual_company_fund_transactions.go")
	if err != nil {
		t.Fatalf("read migration 054: %v", err)
	}
	text := string(source)
	for _, required := range []string{
		`Version() string { return "054" }`,
		`RequiredPreexistingVersion() string { return "053" }`,
		`RequiredExpectedCeiling() string { return "054" }`,
		"company_fund_transactions_channel_check",
		"company_fund_transactions_provider_fact_source_check",
		"company_fund_transactions_first_seen_source_check",
		"company_fund_transactions_last_seen_source_check",
		"'SAFEHERON', 'AIRWALLEX', 'MANUAL'",
		"'WEBHOOK', 'PRODUCT_DETAIL', 'RECONCILIATION', 'MANUAL'",
		"'WEBHOOK', 'RECONCILIATION', 'MANUAL'",
		"VALIDATE CONSTRAINT",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("migration 054 missing %q", required)
		}
	}
	for _, forbidden := range []string{"company_fund_accounts_channel_check", "company_fund_provider_events_channel_check", "UPDATE public.company_fund_transactions"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("migration 054 must not change provider-owned data or account/provider constraints: %q", forbidden)
		}
	}
}

func expectMigration054Success(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta(migration054TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration054ReplaceConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration054ValidateConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
}
