package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

type safeheronCompanyFundBridgeStub struct {
	inputs []companyfund.ProviderEventInput
	err    error
}

type safeheronCompanyFundEligibilityStub struct {
	inputs   []companyfund.SafeheronWebhookEligibilityInput
	decision companyfund.SafeheronWebhookEligibilityDecision
	err      error
}

func (s *safeheronCompanyFundEligibilityStub) AssessAndRecord(_ context.Context, input companyfund.SafeheronWebhookEligibilityInput) (companyfund.SafeheronWebhookEligibilityDecision, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return companyfund.SafeheronWebhookEligibilityDecision{}, s.err
	}
	return s.decision, nil
}

type safeheronCompanyFundSourceStub struct {
	source deposit.EventSource
	err    error
	calls  int
}

func (s *safeheronCompanyFundSourceStub) LookupEventSource(_ context.Context, _ string) (deposit.EventSource, error) {
	s.calls++
	if s.err != nil {
		return deposit.EventSource{}, s.err
	}
	return s.source, nil
}

func (s *safeheronCompanyFundBridgeStub) InsertProviderEvent(_ context.Context, input companyfund.ProviderEventInput) (companyfund.ProviderEventInsertResult, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return companyfund.ProviderEventInsertResult{}, s.err
	}
	return companyfund.ProviderEventInsertResult{ID: 17, Inserted: true}, nil
}

func safeheronBridgePayloadDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func safeheronBridgeEventID(body []byte) string {
	return safeheronWebhookContentEventID(
		"tx-company-fund",
		"TRANSACTION_STATUS_CHANGED",
		"COMPLETED",
		safeheronBridgePayloadDigest(body),
	)
}

func newSafeheronCompanyFundBridgeHandler(
	recorder WebhookEventRecorder,
	source SafeheronEventSourceLookup,
	bridge SafeheronCompanyFundProviderBridge,
	eligibility companyfund.SafeheronWebhookEligibility,
	body []byte,
) *SafeheronWebhookHandler {
	handler := NewSafeheronWebhookHandler(&fakeVerifier{convertFn: func(_ []byte) (*safeheron.WebhookEvent, error) {
		return &safeheron.WebhookEvent{
			EventType: "TRANSACTION_STATUS_CHANGED",
			EventDetail: safeheron.EventDetail{
				TxKey:             "tx-company-fund",
				TransactionStatus: "COMPLETED",
				CustomerRefID:     "customer-7",
			},
			RawBody: body,
		}, nil
	}}, recorder, nil)
	handler.SetCompanyFundBridge(source, bridge)
	handler.SetCompanyFundEligibility(eligibility)
	return handler
}

func assertSafeheronCompanyFundAck(t *testing.T, code int, body string) {
	t.Helper()
	if code != http.StatusOK || body != SafeheronAckBody {
		t.Fatalf("webhook response = %d %q, want 200 %q", code, body, SafeheronAckBody)
	}
}

func TestSafeheronCompanyFundBridge_VerifiedRawEventCreatesProviderEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-company-fund"}}`)
	digest := safeheronBridgePayloadDigest(body)
	eventID := safeheronBridgeEventID(body)
	deposits := deposit.NewRepository(db)
	funds := companyfund.NewDBRepository(db)

	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs(eventID, "TRANSACTION_STATUS_CHANGED", "tx-company-fund", "customer-7", body, digest).
		WillReturnResult(sqlmock.NewResult(91, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_digest"}).AddRow(91, digest))
	// The ledger repository independently verifies the source digest before it
	// can create the provider-event reference: raw record and ledger input must
	// therefore match twice, not merely in the handler.
	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE id = $1")).
		WithArgs(91).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(digest))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_provider_events")).
		WithArgs(
			companyfund.ChannelSafeheron, eventID, "TRANSACTION_STATUS_CHANGED", nil, nil, nil,
			companyfund.ProviderEventSourceExistingSafeheronWebhookRef, 91, digest,
			nil, nil, nil, int64(0), false,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(17))

	w := runWebhook(newSafeheronCompanyFundBridgeHandler(deposits, deposits, funds, &safeheronCompanyFundEligibilityStub{decision: companyfund.SafeheronWebhookEligibilityDecision{Candidate: true}}, body), `{"safeheron":"signed-envelope"}`)
	assertSafeheronCompanyFundAck(t, w.Code, w.Body.String())
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronCompanyFundBridge_DuplicateWebhookAcksAndBridgesIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","duplicate":true}`)
	digest := safeheronBridgePayloadDigest(body)
	eventID := safeheronBridgeEventID(body)
	deposits := deposit.NewRepository(db)
	bridge := &safeheronCompanyFundBridgeStub{}
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs(eventID, "TRANSACTION_STATUS_CHANGED", "tx-company-fund", "customer-7", body, digest).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(digest))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_digest"}).AddRow(91, digest))

	w := runWebhook(newSafeheronCompanyFundBridgeHandler(deposits, deposits, bridge, &safeheronCompanyFundEligibilityStub{decision: companyfund.SafeheronWebhookEligibilityDecision{Candidate: true}}, body), `{"safeheron":"retry"}`)
	assertSafeheronCompanyFundAck(t, w.Code, w.Body.String())
	if len(bridge.inputs) != 1 || bridge.inputs[0].ProviderEventID != eventID ||
		bridge.inputs[0].SourceKind != companyfund.ProviderEventSourceExistingSafeheronWebhookRef ||
		bridge.inputs[0].SafeheronWebhookEventID == nil || *bridge.inputs[0].SafeheronWebhookEventID != 91 ||
		bridge.inputs[0].SourcePayloadDigest != digest {
		t.Fatalf("duplicate bridge input = %#v", bridge.inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronCompanyFundBridge_LegacyMissingDigestIsNotBridged(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","legacy":true}`)
	digest := safeheronBridgePayloadDigest(body)
	eventID := safeheronBridgeEventID(body)
	deposits := deposit.NewRepository(db)
	bridge := &safeheronCompanyFundBridgeStub{}
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs(eventID, "TRANSACTION_STATUS_CHANGED", "tx-company-fund", "customer-7", body, digest).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(nil))

	w := runWebhook(newSafeheronCompanyFundBridgeHandler(deposits, deposits, bridge, &safeheronCompanyFundEligibilityStub{decision: companyfund.SafeheronWebhookEligibilityDecision{Candidate: true}}, body), `{"safeheron":"legacy-retry"}`)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "SUCCESS") {
		t.Fatalf("legacy NULL digest response = %d %q, want raw persistence failure without ack", w.Code, w.Body.String())
	}
	if len(bridge.inputs) != 0 {
		t.Fatalf("legacy NULL digest must not bridge: %#v", bridge.inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronCompanyFundBridge_DigestConflictReturnsRetryableFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","tampered":true}`)
	digest := safeheronBridgePayloadDigest(body)
	eventID := safeheronBridgeEventID(body)
	deposits := deposit.NewRepository(db)
	bridge := &safeheronCompanyFundBridgeStub{}
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs(eventID, "TRANSACTION_STATUS_CHANGED", "tx-company-fund", "customer-7", body, digest).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(strings.Repeat("b", 64)))

	w := runWebhook(newSafeheronCompanyFundBridgeHandler(deposits, deposits, bridge, &safeheronCompanyFundEligibilityStub{decision: companyfund.SafeheronWebhookEligibilityDecision{Candidate: true}}, body), `{"safeheron":"conflict"}`)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "SUCCESS") {
		t.Fatalf("digest conflict response = %d %q, want retryable 500 without ack", w.Code, w.Body.String())
	}
	if len(bridge.inputs) != 0 {
		t.Fatalf("digest conflict must not bridge: %#v", bridge.inputs)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronCompanyFundBridge_FailureDefersToCollectorAndAcknowledges(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","bridgeFailure":true}`)
	digest := safeheronBridgePayloadDigest(body)
	eventID := safeheronBridgeEventID(body)
	deposits := deposit.NewRepository(db)
	bridge := &safeheronCompanyFundBridgeStub{err: errors.New("company-fund database unavailable")}
	mock.ExpectExec("INSERT INTO safeheron_webhook_events").
		WithArgs(eventID, "TRANSACTION_STATUS_CHANGED", "tx-company-fund", "customer-7", body, digest).
		WillReturnResult(sqlmock.NewResult(91, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, payload_digest FROM safeheron_webhook_events WHERE event_id = $1")).
		WithArgs(eventID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "payload_digest"}).AddRow(91, digest))

	w := runWebhook(newSafeheronCompanyFundBridgeHandler(deposits, deposits, bridge, &safeheronCompanyFundEligibilityStub{decision: companyfund.SafeheronWebhookEligibilityDecision{Candidate: true}}, body), `{"safeheron":"bridge-failure"}`)
	assertSafeheronCompanyFundAck(t, w.Code, w.Body.String())
	if len(bridge.inputs) != 1 {
		t.Fatalf("bridge failure calls = %d, want 1", len(bridge.inputs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronCompanyFundBridge_DefaultWithoutEligibilityFailsClosed(t *testing.T) {
	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-company-fund"}}`)
	source := &safeheronCompanyFundSourceStub{err: errors.New("source lookup must not run")}
	bridge := &safeheronCompanyFundBridgeStub{}
	handler := newSafeheronCompanyFundBridgeHandler(
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		source,
		bridge,
		nil,
		body,
	)

	w := runWebhook(handler, `{"safeheron":"signed-envelope"}`)
	assertSafeheronCompanyFundAck(t, w.Code, w.Body.String())
	if source.calls != 0 || len(bridge.inputs) != 0 {
		t.Fatalf("bridge without eligibility must fail closed: source=%d bridge=%#v", source.calls, bridge.inputs)
	}
}

func TestSafeheronCompanyFundBridge_NonCandidateWritesMarkerAndDoesNotCreateProviderEvent(t *testing.T) {
	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-company-fund"}}`)
	digest := safeheronBridgePayloadDigest(body)
	eligibility := &safeheronCompanyFundEligibilityStub{}
	source := &safeheronCompanyFundSourceStub{source: deposit.EventSource{ID: 91, PayloadDigest: digest}}
	bridge := &safeheronCompanyFundBridgeStub{}
	handler := newSafeheronCompanyFundBridgeHandler(
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		source,
		bridge,
		eligibility,
		body,
	)

	w := runWebhook(handler, `{"safeheron":"signed-envelope"}`)
	assertSafeheronCompanyFundAck(t, w.Code, w.Body.String())
	if len(eligibility.inputs) != 1 || eligibility.inputs[0].SafeheronWebhookEventID != 91 ||
		eligibility.inputs[0].PayloadDigest != digest || string(eligibility.inputs[0].RawPayload) != string(body) {
		t.Fatalf("eligibility input = %#v", eligibility.inputs)
	}
	if len(bridge.inputs) != 0 {
		t.Fatalf("non-candidate must not create provider event: %#v", bridge.inputs)
	}
}

func TestSafeheronCompanyFundBridge_EligibilityMarkerFailureReturnsRetryableFailure(t *testing.T) {
	body := []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-company-fund"}}`)
	digest := safeheronBridgePayloadDigest(body)
	eligibility := &safeheronCompanyFundEligibilityStub{err: errors.New("marker storage unavailable")}
	source := &safeheronCompanyFundSourceStub{source: deposit.EventSource{ID: 91, PayloadDigest: digest}}
	bridge := &safeheronCompanyFundBridgeStub{}
	handler := newSafeheronCompanyFundBridgeHandler(
		&fakeRecorder{insertFn: func(_ context.Context, _ *deposit.Event) (bool, error) { return true, nil }},
		source,
		bridge,
		eligibility,
		body,
	)

	w := runWebhook(handler, `{"safeheron":"signed-envelope"}`)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "SUCCESS") {
		t.Fatalf("marker failure response = %d %q, want 500 without ack", w.Code, w.Body.String())
	}
	if len(bridge.inputs) != 0 {
		t.Fatalf("marker failure must not bridge: %#v", bridge.inputs)
	}
}
