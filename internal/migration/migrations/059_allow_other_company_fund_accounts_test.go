package migrations

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

func TestMigration059MetadataIsControlledAndForwardOnly(t *testing.T) {
	var controlled migration.ControlledMigration = &AllowOtherCompanyFundAccounts{}
	if controlled.Version() != "059" || controlled.RequiredPreexistingVersion() != "058" || controlled.RequiredExpectedCeiling() != "059" {
		t.Fatalf("migration 059 contract = %s/%s/%s", controlled.Version(), controlled.RequiredPreexistingVersion(), controlled.RequiredExpectedCeiling())
	}
	if err := controlled.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up() error = %v", err)
	}
	if err := controlled.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down() error = %v", err)
	}
}

func TestMigration059ReplacesAndValidatesOnlyAccountChannelConstraints(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration059TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration059PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(migration059ReplaceAccountConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration059ValidateAccountConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration059(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration059RejectsUnsafeOtherAccountIdentityPreflight(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration059TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration059PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(1))
	mock.ExpectRollback()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration059(tx); err == nil || !strings.Contains(err.Error(), "violations=1") {
		t.Fatalf("runMigration059 error = %v, want preflight rejection", err)
	}
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration059ReturnsDDLFailuresForRollback(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(sqlmock.Sqlmock)
		want  string
	}{
		{"timeouts", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration059TimeoutsSQL)).WillReturnError(errors.New("timeout"))
		}, "timeouts"},
		{"replace", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration059TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration059PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(0))
			mock.ExpectExec(regexp.QuoteMeta(migration059ReplaceAccountConstraintsSQL)).WillReturnError(errors.New("replace"))
		}, "expand"},
		{"validate", func(mock sqlmock.Sqlmock) {
			mock.ExpectExec(regexp.QuoteMeta(migration059TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration059PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(0))
			mock.ExpectExec(regexp.QuoteMeta(migration059ReplaceAccountConstraintsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration059ValidateAccountConstraintsSQL)).WillReturnError(errors.New("validate"))
		}, "validate"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mock.ExpectBegin()
			testCase.setup(mock)
			mock.ExpectRollback()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			err = (&AllowOtherCompanyFundAccounts{}).UpTx(tx)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("UpTx() error = %v", err)
			}
			_ = tx.Rollback()
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration059AllowsOnlyOtherAccountShapeWithoutChangingTransactionSources(t *testing.T) {
	for _, fragment := range []string{
		"company_fund_accounts_channel_check",
		"company_fund_accounts_check",
		"'SAFEHERON', 'AIRWALLEX', 'OTHER'",
		"channel = 'OTHER'",
		"provider_account_key IS NOT NULL",
		"btrim(provider_account_key) <> ''",
		"wallet_address IS NULL",
		"normalized_address IS NULL",
		"network_family IS NULL",
		"idx_company_fund_accounts_other_identity",
		"WHERE channel = 'OTHER' AND provider_account_key IS NOT NULL",
		"VALIDATE CONSTRAINT",
	} {
		if !strings.Contains(migration059ReplaceAccountConstraintsSQL+"\n"+migration059ValidateAccountConstraintsSQL, fragment) {
			t.Errorf("migration 059 missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"company_fund_transactions_channel_check",
		"company_fund_provider_events_channel_check",
		"company_fund_provider_transaction_facts_channel_check",
		"company_fund_sync_runs_channel_check",
		"UPDATE public.company_fund_accounts",
	} {
		if strings.Contains(migration059ReplaceAccountConstraintsSQL+"\n"+migration059ValidateAccountConstraintsSQL, forbidden) {
			t.Errorf("migration 059 must not widen provider or transaction source constraints: %q", forbidden)
		}
	}
}
