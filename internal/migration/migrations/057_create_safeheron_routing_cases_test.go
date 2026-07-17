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

func TestCreateSafeheronRoutingCasesContract(t *testing.T) {
	var controlled migration.ControlledMigration = &CreateSafeheronRoutingCases{}
	if controlled.Version() != "057" || controlled.RequiredPreexistingVersion() != "056" || controlled.RequiredExpectedCeiling() != "057" {
		t.Fatalf("controlled migration contract = %s/%s/%s", controlled.Version(), controlled.RequiredPreexistingVersion(), controlled.RequiredExpectedCeiling())
	}
	if err := controlled.Up(nil); err == nil || !strings.Contains(err.Error(), "controlled") {
		t.Fatalf("direct Up error = %v", err)
	}
	if err := controlled.Down(nil); err == nil || !strings.Contains(err.Error(), "forward-only") {
		t.Fatalf("Down error = %v", err)
	}
}

func TestRunMigration057CreatesSchemaOnlyAfterCleanPreflight(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration057TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration057PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"unexpected_relation_count"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta(migration057SchemaSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigration057(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRunMigration057RejectsPartialSchema(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(migration057TimeoutsSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(migration057PreflightSQL)).WillReturnRows(sqlmock.NewRows([]string{"unexpected_relation_count"}).AddRow(1))
	mock.ExpectRollback()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = runMigration057(tx)
	if err == nil || !strings.Contains(err.Error(), "preflight rejected") {
		t.Fatalf("runMigration057 error = %v", err)
	}
	if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		t.Fatal(rollbackErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMigration057SchemaContainsRoutingAndAlertInvariants(t *testing.T) {
	for _, fragment := range []string{
		"ALTER TABLE public.company_fund_provider_events",
		"ADD COLUMN authorized_safeheron_occurrence_key VARCHAR(256)",
		"ADD COLUMN authorizing_routing_action_id BIGINT",
		"company_fund_provider_events_authorizing_action_fk",
		"trg_safeheron_provider_event_authorization",
		"ALTER TABLE public.safeheron_webhook_events",
		"safeheron_webhook_events_authorizing_action_fk",
		"trg_safeheron_customer_event_authorization",
		"provider event routing authorization differs from action occurrence",
		"company_fund_provider_events_authorized_occurrence_check",
		"authorized_safeheron_occurrence_key ~ '^safeheron-occurrence-v1:[0-9a-f]{64}$'",
		"CREATE TABLE public.safeheron_transaction_routing_cases",
		"routing_identity_key VARCHAR(256) NOT NULL UNIQUE",
		"decision IN ('OPEN', 'PARTIAL', 'CUSTOMER', 'COMPANY', 'DUAL', 'NOT_RELEVANT', 'DISMISSED')",
		"CREATE TABLE public.safeheron_transaction_routing_case_sources",
		"UNIQUE (safeheron_webhook_event_id, source_line_slot_key)",
		"CREATE TABLE public.safeheron_transaction_routing_case_commands",
		"UNIQUE (actor_scope, idempotency_key)",
		"CREATE TABLE public.safeheron_transaction_routing_case_actions",
		"UNIQUE (command_id, projection_kind)",
		"CREATE TABLE public.safeheron_transaction_routing_case_results",
		"UNIQUE (case_id, projection_kind)",
		"CREATE TABLE public.safeheron_transaction_routing_alerts",
		"UNIQUE (case_id, alert_type, transition_key)",
		"CREATE TABLE public.safeheron_transaction_routing_alert_deliveries",
		"UNIQUE (alert_id, sink_kind, recipient_fingerprint)",
		"CREATE TABLE public.safeheron_transaction_routing_alert_delivery_attempts",
		"CREATE TABLE public.safeheron_transaction_routing_recovery_runs",
		"occurrence_identity_digest VARCHAR(64) NOT NULL",
		"recovery_report JSONB NOT NULL",
		"status IN ('PENDING', 'DISPATCHING', 'SENT', 'FAILED_DEFINITE', 'AMBIGUOUS')",
		"FOREIGN KEY (pending_command_id)",
		"DEFERRABLE INITIALLY IMMEDIATE",
		"CREATE UNIQUE INDEX idx_safeheron_routing_cases_deposit_result",
		"CREATE UNIQUE INDEX idx_safeheron_routing_cases_company_result",
		"CREATE CONSTRAINT TRIGGER trg_safeheron_routing_result_consistency",
		"CREATE CONSTRAINT TRIGGER trg_safeheron_routing_case_result_consistency",
		"CREATE CONSTRAINT TRIGGER trg_safeheron_pending_command_case",
		"CREATE CONSTRAINT TRIGGER trg_safeheron_command_pending_case",
		"AFTER INSERT OR UPDATE OR DELETE ON public.safeheron_transaction_routing_case_results",
		"routing result action belongs to another case",
		"pending routing command belongs to another case",
	} {
		if !strings.Contains(migration057SchemaSQL, fragment) {
			t.Errorf("migration057SchemaSQL missing %q", fragment)
		}
	}
}
