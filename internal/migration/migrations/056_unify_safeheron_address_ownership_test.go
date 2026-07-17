package migrations

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/migration"
)

func TestUnifySafeheronAddressOwnershipContract(t *testing.T) {
	var controlled migration.ControlledMigration = &UnifySafeheronAddressOwnership{}
	if controlled.Version() != "056" {
		t.Fatalf("version = %q, want 056", controlled.Version())
	}
	if controlled.RequiredPreexistingVersion() != "055" {
		t.Fatalf("predecessor = %q, want 055", controlled.RequiredPreexistingVersion())
	}
	if controlled.RequiredExpectedCeiling() != "056" {
		t.Fatalf("ceiling = %q, want 056", controlled.RequiredExpectedCeiling())
	}
	if err := controlled.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up error = %v, want controlled migration rejection", err)
	}
	if err := controlled.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down error = %v, want forward-only rejection", err)
	}
}

func TestRunMigration056AppliesPreflightBeforeSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration056TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(migration056LockSourcesSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration056PreflightSQL)).WillReturnRows(
		sqlmock.NewRows([]string{
			"pool_duplicate_count",
			"company_duplicate_count",
			"cross_domain_conflict_count",
			"invalid_identity_count",
			"unexpected_ownership_table_count",
		}).AddRow(0, 0, 0, 0, 0),
	)
	mock.ExpectExec(regexp.QuoteMeta(migration056SchemaSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration056(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunMigration056RejectsCanonicalConflictsWithoutDDL(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		counts [5]int64
	}{
		{name: "customer pool duplicate", counts: [5]int64{1, 0, 0, 0, 0}},
		{name: "company duplicate", counts: [5]int64{0, 1, 0, 0, 0}},
		{name: "cross domain conflict", counts: [5]int64{0, 0, 1, 0, 0}},
		{name: "invalid identity", counts: [5]int64{0, 0, 0, 1, 0}},
		{name: "unexpected ownership table", counts: [5]int64{0, 0, 0, 0, 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta(migration056TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(regexp.QuoteMeta(migration056LockSourcesSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(regexp.QuoteMeta(migration056PreflightSQL)).WillReturnRows(
				sqlmock.NewRows([]string{
					"pool_duplicate_count",
					"company_duplicate_count",
					"cross_domain_conflict_count",
					"invalid_identity_count",
					"unexpected_ownership_table_count",
				}).AddRow(
					testCase.counts[0], testCase.counts[1], testCase.counts[2],
					testCase.counts[3], testCase.counts[4],
				),
			)
			mock.ExpectRollback()

			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			err = runMigration056(tx)
			if err == nil || !strings.Contains(err.Error(), "preflight rejected") {
				t.Fatalf("runMigration056 error = %v, want preflight rejection", err)
			}
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				t.Fatal(rollbackErr)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration056SchemaContainsRequiredDatabaseGuards(t *testing.T) {
	for _, fragment := range []string{
		"monitoring_started_at TIMESTAMPTZ",
		"first_enabled_at TIMESTAMPTZ",
		"CREATE FUNCTION public.safeheron_canonical_network_family",
		"CREATE FUNCTION public.safeheron_canonical_address",
		"CREATE TABLE public.safeheron_address_ownerships",
		"UNIQUE (network_family, normalized_address)",
		"owner_kind IN ('CUSTOMER_POOL', 'COMPANY_ACCOUNT')",
		"REFERENCES public.address_pool(id) ON DELETE RESTRICT",
		"REFERENCES public.company_fund_accounts(id) ON DELETE RESTRICT",
		"CREATE TRIGGER trg_address_pool_claim_ownership",
		"CREATE TRIGGER trg_company_fund_accounts_claim_ownership",
		"CREATE TRIGGER trg_address_pool_identity_immutable",
		"CREATE TRIGGER trg_company_fund_accounts_identity_immutable",
		"CREATE TRIGGER trg_company_fund_accounts_first_enabled",
		"CREATE TRIGGER trg_safeheron_authoritative_ownership_guard",
		"Safeheron authoritative ownership rows cannot be deleted directly",
		"Safeheron ownership must exactly match its authoritative source",
		"FROM public.admin_operation_logs",
		"request_data->'payload'->>'afterEnabled'='true'",
	} {
		if !strings.Contains(migration056SchemaSQL, fragment) {
			t.Errorf("migration056SchemaSQL missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"safeheron_release_source_ownership", "release_ownership", "pg_trigger_depth() > 1"} {
		if strings.Contains(migration056SchemaSQL, forbidden) {
			t.Errorf("migration056SchemaSQL must not contain ownership release bypass %q", forbidden)
		}
	}
}
