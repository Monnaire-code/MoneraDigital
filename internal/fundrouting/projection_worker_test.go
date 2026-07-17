package fundrouting

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/companyfund"
)

type projectionEventInserterStub struct {
	inputs []companyfund.ProviderEventInput
}

func (stub *projectionEventInserterStub) InsertProviderEvent(_ context.Context, input companyfund.ProviderEventInput) (companyfund.ProviderEventInsertResult, error) {
	stub.inputs = append(stub.inputs, input)
	return companyfund.ProviderEventInsertResult{}, nil
}

func TestProjectionWorkerClaimsActionWithLeaseAndSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	worker, err := NewProjectionWorker(db, &projectionEventInserterStub{})
	if err != nil {
		t.Fatalf("NewProjectionWorker: %v", err)
	}

	mock.ExpectBegin()
	mock.ExpectQuery("command.status='PENDING'(?s).*predecessor.status<>'APPLIED'(?s).*FOR UPDATE SKIP LOCKED(?s).*SET status='PENDING', next_attempt_at=NULL").
		WithArgs(worker.workerID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(9)))
	mock.ExpectQuery("FROM safeheron_transaction_routing_case_actions action").
		WithArgs(int64(9), worker.workerID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "action_type", "case_id", "command_id", "routing_identity_key",
			"safeheron_webhook_event_id", "event_id", "event_type", "payload_digest", "target_user_id", "target_company_fund_account_id",
		}).AddRow(9, "APPLY_COMPANY", 11, 12, "occurrence-1", 31, "provider-event", "TRANSACTION_STATUS_CHANGED", "digest", nil, 7))
	mock.ExpectCommit()

	action, err := worker.nextAction(context.Background())
	if err != nil {
		t.Fatalf("nextAction: %v", err)
	}
	if action.ID != 9 || action.Type != "APPLY_COMPANY" {
		t.Fatalf("action = %#v", action)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProjectionWorkerCustomerAdmissionRequiresTerminalStatusAndAssignmentTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker, err := NewProjectionWorker(db, &projectionEventInserterStub{})
	if err != nil {
		t.Fatal(err)
	}
	action := projectionAction{
		ID: 9, CaseID: 11, CommandID: 12,
		RoutingIdentityKey: "occurrence-1",
		TargetUserID:       sql.NullInt64{Int64: 7, Valid: true},
	}
	stop := errors.New("stop after admission SQL verification")
	mock.ExpectExec("effective_event_time >= pool.assigned_at(?s).*webhook.event_type\\)='TRANSACTION_STATUS_CHANGED'(?s).*transactionStatus").
		WithArgs("routing-customer:9", int64(11), int64(7), int64(9), worker.workerID).
		WillReturnError(stop)

	err = worker.applyCustomer(context.Background(), action)
	if err == nil {
		t.Fatal("applyCustomer() error = nil, want insertion failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProjectionWorkerCompleteCompanyRejectsConflictingStoredResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker, _ := NewProjectionWorker(db, &projectionEventInserterStub{})
	action := projectionAction{ID: 9, CaseID: 11, CommandID: 12, RoutingIdentityKey: "occurrence-1"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT action.id FROM safeheron_transaction_routing_case_actions action").
		WithArgs(int64(9), worker.workerID, int64(12), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_case_results").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT action_id, company_fund_transaction_id, result_digest").
		WithArgs(int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "company_fund_transaction_id", "result_digest"}).AddRow(8, 100, "different"))
	mock.ExpectRollback()
	err = worker.completeCompany(context.Background(), action, 100)
	if !errors.Is(err, errProjectionResultConflict) {
		t.Fatalf("completeCompany() error = %v", err)
	}
}

func TestProjectionWorkerBlocksCompanyResultOwnedByDifferentAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	inserter := &projectionEventInserterStub{}
	worker, err := NewProjectionWorker(db, inserter)
	if err != nil {
		t.Fatalf("NewProjectionWorker: %v", err)
	}
	action := projectionAction{
		ID: 9, Type: "APPLY_COMPANY", CaseID: 11, CommandID: 12,
		RoutingIdentityKey: "occurrence-1", WebhookEventID: 31,
		ProviderEventID: "provider-event", EventType: "TRANSACTION_STATUS_CHANGED",
		PayloadDigest: "digest", TargetCompanyID: sql.NullInt64{Int64: 7, Valid: true},
	}
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(9), "occurrence-1", 31).
		WillReturnRows(sqlmock.NewRows([]string{"ready", "conflict"}).AddRow(false, false))
	mock.ExpectQuery("SELECT movement.id").
		WithArgs("occurrence-1", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "exact_account", "exact_asset", "exact_amount", "exact_source", "exact_destination", "exact_direction"}).
			AddRow(100, false, true, true, true, true, true))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT action.id FROM safeheron_transaction_routing_case_actions action").
		WithArgs(int64(9), worker.workerID, int64(12), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_commands").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_alerts").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := worker.applyCompany(context.Background(), action); err != nil {
		t.Fatalf("applyCompany: %v", err)
	}
	if len(inserter.inputs) != 1 {
		t.Fatalf("provider event inputs = %#v", inserter.inputs)
	}
	input := inserter.inputs[0]
	if input.ProviderEventID != "routing-company:9" || input.AuthorizedSafeheronOccurrenceKey != action.RoutingIdentityKey {
		t.Fatalf("scoped provider event = %#v", input)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestProjectionWorkerReusesRecoveryBoundProviderEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inserter := &projectionEventInserterStub{}
	worker, _ := NewProjectionWorker(db, inserter)
	action := projectionAction{
		ID: 9, Type: "APPLY_COMPANY", CaseID: 11, CommandID: 12,
		RoutingIdentityKey: "occurrence-1", TargetCompanyID: sql.NullInt64{Int64: 7, Valid: true},
	}
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(9), "occurrence-1", 0).
		WillReturnRows(sqlmock.NewRows([]string{"ready", "conflict"}).AddRow(true, false))
	mock.ExpectQuery("SELECT movement.id").WithArgs("occurrence-1", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "exact_account", "exact_asset", "exact_amount", "exact_source", "exact_destination", "exact_direction"}))
	mock.ExpectQuery("SELECT attempt_count FROM safeheron_transaction_routing_case_actions").
		WithArgs(int64(9), worker.workerID).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(0))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := worker.applyCompany(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if len(inserter.inputs) != 0 {
		t.Fatalf("recovery-bound provider event must be reused: %#v", inserter.inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionWorkerQuarantinesConflictingLegacyProviderEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inserter := &projectionEventInserterStub{}
	worker, _ := NewProjectionWorker(db, inserter)
	action := projectionAction{
		ID: 9, Type: "APPLY_COMPANY", CaseID: 11, CommandID: 12, WebhookEventID: 31,
		RoutingIdentityKey: "occurrence-1", TargetCompanyID: sql.NullInt64{Int64: 7, Valid: true},
	}
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(9), "occurrence-1", 31).
		WillReturnRows(sqlmock.NewRows([]string{"ready", "conflict"}).AddRow(false, true))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT action.id FROM safeheron_transaction_routing_case_actions action").
		WithArgs(int64(9), worker.workerID, int64(12), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_commands").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_alerts").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := worker.applyCompany(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if len(inserter.inputs) != 0 {
		t.Fatalf("conflicting legacy event must not be replaced: %#v", inserter.inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionWorkerRetryExhaustionTerminatesCommandAndSiblingActions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker, _ := NewProjectionWorker(db, &projectionEventInserterStub{})
	action := projectionAction{ID: 9, CaseID: 11, CommandID: 12}
	mock.ExpectQuery("SELECT attempt_count FROM safeheron_transaction_routing_case_actions").
		WithArgs(int64(9), worker.workerID).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(maxProjectionActionAttempts - 1))
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT action.id FROM safeheron_transaction_routing_case_actions action").
		WithArgs(int64(9), worker.workerID, int64(12), int64(11)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").
		WithArgs(int64(9), "RETRY_EXHAUSTED_PROVIDER_EVENT_INSERT_FAILED", "still unavailable").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_commands").
		WithArgs(int64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").
		WithArgs(int64(12), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_alerts").
		WithArgs(int64(11), "command:12:action:9", "RETRY_EXHAUSTED_PROVIDER_EVENT_INSERT_FAILED").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	if err := worker.retryAction(context.Background(), action, "PROVIDER_EVENT_INSERT_FAILED", errors.New("still unavailable")); err != nil {
		t.Fatalf("retryAction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestProjectionWorkerWaitingForDownstreamResultNeverExhausts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	worker, _ := NewProjectionWorker(db, &projectionEventInserterStub{})
	action := projectionAction{ID: 9, CaseID: 11, CommandID: 12}
	mock.ExpectQuery("SELECT attempt_count FROM safeheron_transaction_routing_case_actions").
		WithArgs(int64(9), worker.workerID).
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count"}).AddRow(maxProjectionActionAttempts - 1))
	mock.ExpectExec("SET status='RETRYABLE'").
		WithArgs(int64(9), "WAITING_COMPANY_PROJECTION", "", worker.workerID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := worker.retryAction(context.Background(), action, "WAITING_COMPANY_PROJECTION", nil); err != nil {
		t.Fatalf("retryAction: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
