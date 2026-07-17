package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresSafeheronWebhookPayloadReader_ReadsOnlyJSONBPayload(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	payload := `{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-1"}}`
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadSQL)).WithArgs(91).
		WillReturnRows(sqlmock.NewRows([]string{"raw_payload"}).AddRow(payload))

	reader := NewPostgresSafeheronWebhookPayloadReader(db)
	got, err := reader.ReadSafeheronWebhookPayload(context.Background(), 91)
	if err != nil || string(got) != payload {
		t.Fatalf("ReadSafeheronWebhookPayload() = %q, %v", got, err)
	}
	if strings.Contains(selectSafeheronWebhookPayloadSQL, "process_status") ||
		strings.Contains(selectSafeheronWebhookPayloadSQL, "payload_digest") ||
		!strings.Contains(selectSafeheronWebhookPayloadSQL, "raw_payload::text") {
		t.Fatalf("reader SQL must only read JSONB payload: %s", selectSafeheronWebhookPayloadSQL)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSafeheronWebhookPayloadReader_ClassifiesUnavailablePayloadAsPermanent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadSQL)).WithArgs(91).
		WillReturnError(sql.ErrNoRows)

	_, err = NewPostgresSafeheronWebhookPayloadReader(db).ReadSafeheronWebhookPayload(context.Background(), 91)
	if !errors.Is(err, ErrProviderEventPermanent) || !errors.Is(err, ErrSafeheronWebhookPayloadUnavailable) {
		t.Fatalf("missing raw payload error = %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSafeheronWebhookPayloadReader_RejectsInvalidInputAndJSONBRepresentation(t *testing.T) {
	if _, err := NewPostgresSafeheronWebhookPayloadReader(nil).ReadSafeheronWebhookPayload(context.Background(), 0); err == nil {
		t.Fatal("invalid event ID must fail")
	}

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadSQL)).WithArgs(91).
		WillReturnRows(sqlmock.NewRows([]string{"raw_payload"}).AddRow("not-json"))
	_, err = NewPostgresSafeheronWebhookPayloadReader(db).ReadSafeheronWebhookPayload(context.Background(), 91)
	if !errors.Is(err, ErrProviderEventPermanent) {
		t.Fatalf("invalid JSONB representation error = %v", err)
	}
}
