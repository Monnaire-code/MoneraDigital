package fundrouting

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"monera-digital/internal/companyfund"
)

func TestHistoryInboxIngesterStoresCanonicalSnapshotAsPendingRoutingEnvelope(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ingester, err := NewHistoryInboxIngester(db)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("INSERT INTO safeheron_webhook_events").
		WithArgs("history-event", "history-tx", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(12))
	result, err := ingester.Ingest(context.Background(), companyfund.OwnedProviderPayloadInput{
		Channel: companyfund.ChannelSafeheron, ProviderEventID: "history-event",
		EventType:  companyfund.SafeheronTransactionHistorySnapshotEventType,
		Body:       []byte(`{"txKey":"history-tx","coinKey":"ETHEREUM_ETH","txAmount":"1","destinationAddress":"0xabc","transactionStatus":"COMPLETED"}`),
		KeyVersion: "unused", Retention: time.Hour,
	})
	if err != nil || !result.Inserted || result.ID != 12 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryInboxIngesterRejectsNonSafeheronInputBeforeDatabase(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ingester, _ := NewHistoryInboxIngester(db)
	_, err = ingester.Ingest(context.Background(), companyfund.OwnedProviderPayloadInput{
		Channel: companyfund.ChannelAirwallex, ProviderEventID: "wrong", Body: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("expected non-Safeheron input rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
