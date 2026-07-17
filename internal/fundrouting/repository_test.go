package fundrouting

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRepositoryRouteVerifiedEventPersistsOpenCaseSourceAlertAndDoneAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewRepository(db)
	input := routingEventInput()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(ownershipRows())
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_cases")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_sources")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_alerts")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE safeheron_webhook_events").WithArgs(input.WebhookEventID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.RouteVerifiedEvent(context.Background(), input)
	if err != nil {
		t.Fatalf("RouteVerifiedEvent: %v", err)
	}
	if len(result) != 1 || result[0].CaseID != 11 || result[0].Decision.Decision != DecisionOpen {
		t.Fatalf("route result = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryRecoverySuppressesOpenAlertWithoutCreatingEmptyCommand(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	input := routingEventInput()
	input.SuppressOpenAlert = true
	input.PreserveRawEventStatus = true
	mock.ExpectBegin()
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(ownershipRows())
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_cases")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(13)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_sources")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	result, err := NewRepository(db).RouteVerifiedEvent(context.Background(), input)
	if err != nil {
		t.Fatalf("RouteVerifiedEvent: %v", err)
	}
	if len(result) != 1 || result[0].Decision.Decision != DecisionOpen || result[0].CommandID != 0 {
		t.Fatalf("route result = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryNextPendingTransactionEventExcludesCustomerProjectionEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("event_id NOT LIKE 'routing-customer:%'").
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_type", "payload_digest", "raw_payload"}))

	_, err = NewRepository(db).NextPendingTransactionEvent(context.Background())
	if !errors.Is(err, ErrNoPendingTransactionEvent) {
		t.Fatalf("NextPendingTransactionEvent() error = %v", err)
	}
}

func TestRepositoryRouteVerifiedEventReservesCompanyProjection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewRepository(db)
	input := routingEventInput()
	monitoring := time.UnixMilli(input.Snapshot.CreateTime).Add(-time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(
		ownershipRows().AddRow("COMPANY_ACCOUNT", nil, nil, int64(7), true, monitoring),
	)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_cases")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(12)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_sources")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_commands")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(21)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_actions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").WithArgs(int64(21), int64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_webhook_events").WithArgs(input.WebhookEventID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.RouteVerifiedEvent(context.Background(), input)
	if err != nil {
		t.Fatalf("RouteVerifiedEvent: %v", err)
	}
	if len(result) != 1 || result[0].Decision.Decision != DecisionCompany || result[0].CommandID != 21 {
		t.Fatalf("route result = %#v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRepositoryRouteVerifiedEventRollsBackWhenAnyOccurrenceFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	repo := NewRepository(db)
	input := routingEventInput()
	input.Snapshot.DestinationAddress = ""
	input.Snapshot.DestinationAddressList = nil

	mock.ExpectBegin()
	mock.ExpectRollback()
	_, err = repo.RouteVerifiedEvent(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("RouteVerifiedEvent error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRecoveryLinkResumesDualCaseAfterCustomerSideWasAlreadyCommitted(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("movement.channel='SAFEHERON'").
		WithArgs(int64(11), int64(12), int64(102)).
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "exact"}).AddRow(92, true))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_case_results").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_case_actions").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = validateAndLinkExistingProjection(context.Background(), tx, 11, 12, "DUAL",
		sql.NullInt64{Int64: 101, Valid: true}, sql.NullInt64{}, ExistingProjectionLink{
			RoutingIdentityKey: "safeheron-occurrence-v1:key", DepositID: 101, CompanyFundTransactionID: 102,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAndBindExistingProviderEventUsesCurrentCompanyAction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("event.channel='SAFEHERON'").WithArgs(int64(11), int64(12), int64(77)).
		WillReturnRows(sqlmock.NewRows([]string{"action_id", "exact"}).AddRow(92, true))
	mock.ExpectExec("UPDATE company_fund_provider_events").
		WithArgs("safeheron-occurrence-v1:key", int64(92), int64(77)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateAndBindExistingProviderEvent(context.Background(), tx, 11, 12, ExistingProjectionLink{
		RoutingIdentityKey: "safeheron-occurrence-v1:key", ProviderEventID: 77,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func routingEventInput() VerifiedEventInput {
	return VerifiedEventInput{
		WebhookEventID: 31,
		EventType:      "TRANSACTION_STATUS_CHANGED",
		PayloadDigest:  strings.Repeat("a", 64),
		NetworkFamily:  "EVM",
		Snapshot:       routingSnapshot(),
	}
}

func ownershipRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"owner_kind", "assigned_user_id", "assigned_at", "company_fund_account_id", "enabled", "monitoring_started_at",
	})
}
