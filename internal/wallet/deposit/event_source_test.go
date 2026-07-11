package deposit

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestInsertEventOrSkip_WritesVerifiedPayloadDigest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)
	digest := eventPayloadDigest(body)
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs("evt-digest", "T", "tx", "ref", body, digest).
		WillReturnResult(sqlmock.NewResult(1, 1))
	repository := NewRepository(db)
	inserted, err := repository.InsertEventOrSkip(context.Background(), &Event{
		EventID: "evt-digest", EventType: "T", SafeheronTxKey: "tx", CustomerRefID: "ref", RawPayload: body, PayloadDigest: digest,
	})
	if err != nil || !inserted {
		t.Fatalf("InsertEventOrSkip() = %v, %v", inserted, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertEventOrSkip_RejectsClaimedDigestMismatch(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = NewRepository(db).InsertEventOrSkip(context.Background(), &Event{
		EventID: "evt-digest", RawPayload: []byte(`{}`), PayloadDigest: strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("mismatched claimed digest must be rejected before database write")
	}
}

func TestInsertEventOrSkip_RejectsLegacyMissingPayloadDigestWithoutBackfill(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)
	digest := eventPayloadDigest(body)
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs("evt-legacy", "T", "tx", "ref", body, digest).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(selectEventPayloadDigestSQL)).WithArgs("evt-legacy").
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(nil))

	inserted, err := NewRepository(db).InsertEventOrSkip(context.Background(), &Event{
		EventID: "evt-legacy", EventType: "T", SafeheronTxKey: "tx", CustomerRefID: "ref", RawPayload: body,
	})
	if !errors.Is(err, ErrEventPayloadDigestUnavailable) || inserted {
		t.Fatalf("InsertEventOrSkip() = %v, %v; want unverifiable duplicate rejection", inserted, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertEventOrSkip_RejectsExistingPayloadDigestMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)
	digest := eventPayloadDigest(body)
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs("evt-conflict", "T", "tx", "ref", body, digest).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta(selectEventPayloadDigestSQL)).WithArgs("evt-conflict").
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(strings.Repeat("b", 64)))

	_, err = NewRepository(db).InsertEventOrSkip(context.Background(), &Event{
		EventID: "evt-conflict", EventType: "T", SafeheronTxKey: "tx", CustomerRefID: "ref", RawPayload: body,
	})
	if !errors.Is(err, ErrEventPayloadDigestMismatch) {
		t.Fatalf("InsertEventOrSkip() error = %v, want ErrEventPayloadDigestMismatch", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLookupEventSource_ReturnsOnlyIDAndDigest(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	digest := strings.Repeat("a", 64)
	mock.ExpectQuery(regexp.QuoteMeta(selectEventSourceSQL)).WithArgs("evt-source").
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_digest"}).AddRow(42, digest))
	source, err := NewRepository(db).LookupEventSource(context.Background(), "evt-source")
	if err != nil || source != (EventSource{ID: 42, PayloadDigest: digest}) {
		t.Fatalf("LookupEventSource() = %#v, %v", source, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLookupEventSource_RejectsMissingOrUnavailableDigest(t *testing.T) {
	for _, testCase := range []struct {
		name string
		rows *sqlmock.Rows
	}{
		{name: "missing row", rows: sqlmock.NewRows([]string{"id", "payload_digest"})},
		{name: "empty digest", rows: sqlmock.NewRows([]string{"id", "payload_digest"}).AddRow(42, "")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mock.ExpectQuery(regexp.QuoteMeta(selectEventSourceSQL)).WithArgs("evt-source").WillReturnRows(testCase.rows)
			if _, err := NewRepository(db).LookupEventSource(context.Background(), "evt-source"); err == nil {
				t.Fatalf("LookupEventSource() error = %v, want unavailable source", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
