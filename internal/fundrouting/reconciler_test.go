package fundrouting

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"monera-digital/internal/adaptiveschedule"
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
	wakeCount := 0
	reconciler.SetOnProjectionReady(func() { wakeCount++ })
	snapshot := routingSnapshot()
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	monitoring := time.UnixMilli(snapshot.CreateTime).Add(-time.Hour)
	scanCutoff := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT clock_timestamp").WillReturnRows(sqlmock.NewRows([]string{"clock_timestamp"}).AddRow(scanCutoff))
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
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
	if wakeCount != 1 {
		t.Fatalf("projection wakes=%d, want 1 after committed reconciliation", wakeCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconcilerCountsUnresolvedCaseAsScannedWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	reconciler, err := NewReconciler(db)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	wakeCount := 0
	reconciler.SetOnProjectionReady(func() { wakeCount++ })
	snapshot := routingSnapshot()
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	scanCutoff := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT clock_timestamp").WillReturnRows(sqlmock.NewRows([]string{"clock_timestamp"}).AddRow(scanCutoff))
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}).AddRow(11, candidates[0].RoutingIdentityKey, "EVM", 1, "TRANSACTION_STATUS_CHANGED", payload))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(ownershipRows())
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}))
	mock.ExpectRollback()

	outcome, err := adaptiveschedule.DrainProcessOne(context.Background(), reconciler.ProcessOne, 100)
	if err != nil {
		t.Fatalf("DrainProcessOne: %v", err)
	}
	if !outcome.Worked || outcome.MoreWork {
		t.Fatalf("drain outcome = %#v, want one scan without an immediate hot-loop retry", outcome)
	}
	if wakeCount != 0 {
		t.Fatalf("unresolved case emitted %d projection wakes", wakeCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconcilerDrainContinuesPastUnresolvedCaseToTerminalCase(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	reconciler, err := NewReconciler(db)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}
	wakeCount := 0
	reconciler.SetOnProjectionReady(func() { wakeCount++ })
	snapshot := routingSnapshot()
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	monitoring := time.UnixMilli(snapshot.CreateTime).Add(-time.Hour)
	scanCutoff := time.Now().UTC()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT clock_timestamp").WillReturnRows(sqlmock.NewRows([]string{"clock_timestamp"}).AddRow(scanCutoff))
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}).AddRow(11, candidates[0].RoutingIdentityKey, "EVM", 1, "TRANSACTION_STATUS_CHANGED", payload))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(ownershipRows())
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}).AddRow(12, candidates[0].RoutingIdentityKey, "EVM", 1, "TRANSACTION_STATUS_CHANGED", payload))
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xsource").WillReturnRows(ownershipRows())
	mock.ExpectQuery("FROM safeheron_address_ownerships").WithArgs("EVM", "0xdest").WillReturnRows(
		ownershipRows().AddRow("COMPANY_ACCOUNT", nil, nil, int64(7), true, monitoring),
	)
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_commands")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(21)))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO safeheron_transaction_routing_case_actions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_cases").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	mock.ExpectBegin()
	mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).WillReturnRows(sqlmock.NewRows([]string{
		"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
	}))
	mock.ExpectRollback()

	outcome, err := adaptiveschedule.DrainProcessOne(context.Background(), reconciler.ProcessOne, 100)
	if err != nil {
		t.Fatalf("DrainProcessOne: %v", err)
	}
	if !outcome.Worked || outcome.MoreWork {
		t.Fatalf("drain outcome = %#v, want worked without remaining work", outcome)
	}
	if wakeCount != 1 {
		t.Fatalf("projection wakes=%d, want terminal case to reconcile in the same drain", wakeCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestReconcilerNotifyWakesIdleRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	reconciler, err := NewReconciler(db)
	if err != nil {
		t.Fatalf("NewReconciler: %v", err)
	}

	expectEmptyReconcileCycle := func() {
		scanCutoff := time.Now().UTC()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT clock_timestamp").WillReturnRows(sqlmock.NewRows([]string{"clock_timestamp"}).AddRow(scanCutoff))
		mock.ExpectQuery("FOR UPDATE OF routing SKIP LOCKED").WithArgs(scanCutoff).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "routing_identity_key", "network_family", "version", "event_type", "raw_payload",
			}))
		mock.ExpectRollback()
	}
	waitForExpectations := func(deadline time.Time) bool {
		for time.Now().Before(deadline) {
			if mock.ExpectationsWereMet() == nil {
				return true
			}
			time.Sleep(5 * time.Millisecond)
		}
		return mock.ExpectationsWereMet() == nil
	}

	expectEmptyReconcileCycle()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	panicValue := make(chan any, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicValue <- recovered
			}
			close(done)
		}()
		reconciler.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
		select {
		case recovered := <-panicValue:
			t.Errorf("reconciler run panicked: %v", recovered)
		default:
		}
	}()

	if !waitForExpectations(time.Now().Add(time.Second)) {
		t.Fatal("startup reconciliation cycle did not run")
	}
	expectEmptyReconcileCycle()
	if !reconciler.Notify() {
		t.Fatal("Notify should queue a wake while reconciler is idle")
	}
	if !waitForExpectations(time.Now().Add(time.Second)) {
		t.Fatal("Notify did not wake the reconciler before its 30-second idle interval")
	}
}
