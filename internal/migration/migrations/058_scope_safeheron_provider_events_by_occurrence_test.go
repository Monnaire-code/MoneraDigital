package migrations

import (
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

func TestScopeSafeheronProviderEventsByOccurrenceContract(t *testing.T) {
	var controlled migration.ControlledMigration = &ScopeSafeheronProviderEventsByOccurrence{}
	if controlled.Version() != "058" || controlled.RequiredPreexistingVersion() != "057" || controlled.RequiredExpectedCeiling() != "058" {
		t.Fatalf("controlled migration contract = version %s predecessor %s ceiling %s",
			controlled.Version(), controlled.RequiredPreexistingVersion(), controlled.RequiredExpectedCeiling())
	}
	if err := controlled.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up() error = %v", err)
	}
}

func TestRunMigration058ReplacesWebhookUniquenessWithLegacyAndOccurrenceScopes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration058TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration058PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(migration058SchemaSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration058(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration058SchemaKeepsLegacyUniqueAndAllowsDistinctRoutedOccurrences(t *testing.T) {
	for _, fragment := range []string{
		"DROP INDEX public.idx_company_fund_provider_events_safeheron_webhook",
		"idx_company_fund_provider_events_safeheron_webhook_legacy",
		"authorized_safeheron_occurrence_key IS NULL",
		"idx_company_fund_provider_events_safeheron_occurrence",
		"(safeheron_webhook_event_id, authorized_safeheron_occurrence_key)",
		"authorized_safeheron_occurrence_key IS NOT NULL",
	} {
		if !strings.Contains(migration058SchemaSQL, fragment) {
			t.Errorf("migration058SchemaSQL missing %q", fragment)
		}
	}
}

func TestRunMigration058RejectsUnexpectedOrDuplicateProviderEventIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration058TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration058PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"violations"}).AddRow(1))
	mock.ExpectRollback()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = runMigration058(tx)
	if err == nil || !strings.Contains(err.Error(), "violations=1") {
		t.Fatalf("runMigration058 error = %v", err)
	}
	_ = tx.Rollback()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
