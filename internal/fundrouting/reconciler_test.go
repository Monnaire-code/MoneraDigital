package fundrouting

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestReconcilerReservesCompanyProjectionForNewlyEnabledAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	reconciler, err := NewReconciler(db)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	snapshot := routingSnapshot()
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	monitoring := time.UnixMilli(snapshot.CreateTime).Add(-time.Hour)

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}).AddRow(11, candidates[0].RoutingIdentityKey, "EVM", 1, "TRANSACTION_STATUS_CHANGED", payload))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(
		ownershipRows().AddRow("COMPANY_ACCOUNT", nil, nil, int64(7), true, monitoring),
	)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_commands")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(21)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_actions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	processed, err := reconciler.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !processed {
		t.Fatal("expected an OPEN case to be processed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
