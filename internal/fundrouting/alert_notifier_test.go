package fundrouting

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/alert"
)

func TestAlertNotifierNextDueReadsEarliestDurableRetryOrLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	notifier, err := NewAlertNotifier(db, routingAlertSenderStub{})
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(30 * time.Second).Round(time.Microsecond)
	mock.ExpectQuery("SELECT min\\(due_at\\)").WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(due))

	got, err := notifier.NextDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(due) {
		t.Fatalf("NextDue=%s, want %s", got, due)
	}
}

type routingAlertSenderStub struct {
	sinks   []alert.RoutingSink
	outcome alert.RoutingDeliveryOutcome
}

func (s routingAlertSenderStub) RoutingSinks() []alert.RoutingSink { return s.sinks }
func (s routingAlertSenderStub) SendRouting(context.Context, string, string, string, string, map[string]string) alert.RoutingDeliveryOutcome {
	return s.outcome
}

func TestAlertNotifierUnknownDeliveryBecomesAmbiguousWithoutRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	notifier, err := NewAlertNotifier(db, routingAlertSenderStub{})
	if err != nil {
		t.Fatal(err)
	}
	delivery := claimedDelivery{ID: 7, AttemptID: 8, Attempt: 1}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE safeheron_transaction_routing_alert_delivery_attempts").
		WithArgs(int64(8), "DELIVERY_UNKNOWN").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_alert_deliveries").
		WithArgs(int64(7), "AMBIGUOUS", true, false, notifier.workerID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := notifier.finish(context.Background(), delivery, alert.RoutingDeliveryUnknown); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlertNotifierSweepsExpiredDispatchToAmbiguous(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()
	notifier, _ := NewAlertNotifier(db, routingAlertSenderStub{})
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE safeheron_transaction_routing_alert_delivery_attempts attempt").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE safeheron_transaction_routing_alert_deliveries").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := notifier.sweepExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlertNotifierMaterializesAllCurrentSinksOnlyForAnUnfannedAlert(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	notifier, _ := NewAlertNotifier(db, routingAlertSenderStub{sinks: []alert.RoutingSink{
		{Kind: "LARK", Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{Kind: "EMAIL", Fingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}})
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT alert.id").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(5))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_alert_deliveries").
		WithArgs(int64(5), "LARK", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO safeheron_transaction_routing_alert_deliveries").
		WithArgs(int64(5), "EMAIL", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb").
		WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectCommit()
	if err := notifier.ensureDeliveries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
