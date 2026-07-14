package companyfund

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

type safeheronRawEventCandidateReaderStub struct {
	candidates []SafeheronRawEventCandidate
	err        error
}

type safeheronRawEventCandidateReaderFunc func(context.Context, int) ([]SafeheronRawEventCandidate, error)

func (fn safeheronRawEventCandidateReaderFunc) ListUnbridgedSafeheronWebhookEvents(ctx context.Context, limit int) ([]SafeheronRawEventCandidate, error) {
	return fn(ctx, limit)
}

type safeheronWebhookFingerprintProviderStub struct {
	fingerprint string
	err         error
	calls       int
}

func (stub *safeheronWebhookFingerprintProviderStub) CurrentSafeheronWebhookEligibilityFingerprint() (string, error) {
	stub.calls++
	return stub.fingerprint, stub.err
}

type safeheronCollectorEligibilityStub struct {
	inputs   []SafeheronWebhookEligibilityInput
	decision SafeheronWebhookEligibilityDecision
	err      error
}

func (s *safeheronCollectorEligibilityStub) AssessAndRecord(_ context.Context, input SafeheronWebhookEligibilityInput) (SafeheronWebhookEligibilityDecision, error) {
	s.inputs = append(s.inputs, input)
	if s.err != nil {
		return SafeheronWebhookEligibilityDecision{}, s.err
	}
	return s.decision, nil
}

func (s safeheronRawEventCandidateReaderStub) ListUnbridgedSafeheronWebhookEvents(_ context.Context, _ int) ([]SafeheronRawEventCandidate, error) {
	return s.candidates, s.err
}

type safeheronProviderEventWriterStub struct {
	inputs []ProviderEventInput
	fn     func(int, ProviderEventInput) (ProviderEventInsertResult, error)
}

func (s *safeheronProviderEventWriterStub) InsertProviderEvent(_ context.Context, input ProviderEventInput) (ProviderEventInsertResult, error) {
	s.inputs = append(s.inputs, input)
	if s.fn == nil {
		return ProviderEventInsertResult{}, nil
	}
	return s.fn(len(s.inputs), input)
}

type safeheronWebhookExclusionAwareReader struct {
	candidate             SafeheronRawEventCandidate
	markers               *safeheronWebhookExclusionStoreStub
	fingerprints          *safeheronWebhookFingerprintProviderStub
	providerEventInserted bool
}

func (reader *safeheronWebhookExclusionAwareReader) ListUnbridgedSafeheronWebhookEvents(_ context.Context, _ int) ([]SafeheronRawEventCandidate, error) {
	if reader.providerEventInserted {
		return nil, nil
	}
	fingerprint, err := reader.fingerprints.CurrentSafeheronWebhookEligibilityFingerprint()
	if err != nil {
		return nil, err
	}
	for index := len(reader.markers.inputs) - 1; index >= 0; index-- {
		marker := reader.markers.inputs[index]
		if marker.SafeheronWebhookEventID != reader.candidate.SafeheronWebhookEventID || marker.PayloadDigest != reader.candidate.PayloadDigest {
			continue
		}
		if !safeheronWebhookExclusionReasonUsesConfigurationFingerprint(marker.Reason) || marker.ConfigurationFingerprint == fingerprint {
			return nil, nil
		}
		break
	}
	return []SafeheronRawEventCandidate{reader.candidate}, nil
}

func TestPostgresSafeheronRawEventCandidateReader_UsesVerifiedDigestAntiJoin(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	digest := strings.Repeat("a", 64)
	fingerprints := &safeheronWebhookFingerprintProviderStub{fingerprint: strings.Repeat("c", 64)}
	mock.ExpectQuery(regexp.QuoteMeta(selectUnbridgedSafeheronWebhookEventsSQL)).
		WithArgs(fingerprints.fingerprint, 2).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_id", "event_type", "payload_digest", "raw_payload"}).
			AddRow(81, strings.Repeat("b", 64), "TRANSACTION_STATUS_CHANGED", digest, []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)))

	candidates, err := NewPostgresSafeheronRawEventCandidateReader(db, fingerprints).ListUnbridgedSafeheronWebhookEvents(context.Background(), 2)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("ListUnbridgedSafeheronWebhookEvents() = %#v, %v", candidates, err)
	}
	if candidate := candidates[0]; candidate.SafeheronWebhookEventID != 81 || candidate.ProviderEventID != strings.Repeat("b", 64) || candidate.EventType != "TRANSACTION_STATUS_CHANGED" || candidate.PayloadDigest != digest || len(candidate.RawPayload) == 0 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if strings.Contains(selectUnbridgedSafeheronWebhookEventsSQL, "process_status") || !strings.Contains(selectUnbridgedSafeheronWebhookEventsSQL, "raw_event.raw_payload") {
		t.Fatal("collector must not touch deposit process_status and must read raw payload only for eligibility")
	}
	if fingerprints.calls != 1 {
		t.Fatalf("collector reader must resolve the current fingerprint once per query, calls=%d", fingerprints.calls)
	}
	for _, contract := range []string{
		"payload_digest ~ '^[0-9a-f]{64}$'",
		"NOT EXISTS",
		"provider_event.safeheron_webhook_event_id = raw_event.id",
		"provider_event.source_payload_digest = raw_event.payload_digest",
		"company_fund_safeheron_raw_event_exclusions",
		"exclusion.safeheron_webhook_event_id = raw_event.id",
		"exclusion.source_payload_digest = raw_event.payload_digest",
		"exclusion.exclusion_reason IN ('NON_TRANSACTION_STATUS', 'INVALID_PAYLOAD', 'EVENT_TYPE_MISMATCH')",
		"exclusion.configuration_fingerprint = $1",
		"LIMIT $2",
	} {
		if !strings.Contains(selectUnbridgedSafeheronWebhookEventsSQL, contract) {
			t.Errorf("collector anti-join is missing %q", contract)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSafeheronRawEventCandidateReader_UsesCurrentFingerprintOnEveryQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fingerprints := &safeheronWebhookFingerprintProviderStub{fingerprint: strings.Repeat("c", 64)}
	reader := NewPostgresSafeheronRawEventCandidateReader(db, fingerprints)
	mock.ExpectQuery(regexp.QuoteMeta(selectUnbridgedSafeheronWebhookEventsSQL)).
		WithArgs(strings.Repeat("c", 64), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_id", "event_type", "payload_digest", "raw_payload"}))
	if candidates, err := reader.ListUnbridgedSafeheronWebhookEvents(context.Background(), 1); err != nil || len(candidates) != 0 {
		t.Fatalf("same configuration query = %#v, %v", candidates, err)
	}

	fingerprints.fingerprint = strings.Repeat("d", 64)
	mock.ExpectQuery(regexp.QuoteMeta(selectUnbridgedSafeheronWebhookEventsSQL)).
		WithArgs(strings.Repeat("d", 64), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "event_id", "event_type", "payload_digest", "raw_payload"}).
			AddRow(81, strings.Repeat("b", 64), safeheronTransactionStatusChangedEventType, strings.Repeat("a", 64), []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)))
	candidates, err := reader.ListUnbridgedSafeheronWebhookEvents(context.Background(), 1)
	if err != nil || len(candidates) != 1 || fingerprints.calls != 2 {
		t.Fatalf("changed configuration query = %#v, %v; fingerprint calls=%d", candidates, err, fingerprints.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSafeheronProviderEventCollector_CreatesIdempotentSafeheronReferences(t *testing.T) {
	digest := strings.Repeat("a", 64)
	reader := safeheronRawEventCandidateReaderStub{candidates: []SafeheronRawEventCandidate{{
		SafeheronWebhookEventID: 81,
		ProviderEventID:         strings.Repeat("b", 64),
		EventType:               "TRANSACTION_STATUS_CHANGED",
		PayloadDigest:           digest,
		RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
	}}}
	writer := &safeheronProviderEventWriterStub{fn: func(_ int, _ ProviderEventInput) (ProviderEventInsertResult, error) {
		return ProviderEventInsertResult{ID: 91, Inserted: true}, nil
	}}
	eligibility := &safeheronCollectorEligibilityStub{decision: SafeheronWebhookEligibilityDecision{Candidate: true}}
	collector, err := NewSafeheronProviderEventCollector(reader, writer, eligibility)
	if err != nil {
		t.Fatal(err)
	}

	result, err := collector.Collect(context.Background(), 10)
	if err != nil || result != (SafeheronProviderEventCollectionResult{Scanned: 1, Inserted: 1}) {
		t.Fatalf("Collect() = %#v, %v", result, err)
	}
	if len(writer.inputs) != 1 {
		t.Fatalf("provider event writes = %#v", writer.inputs)
	}
	input := writer.inputs[0]
	if input.Channel != ChannelSafeheron || input.SourceKind != ProviderEventSourceExistingSafeheronWebhookRef ||
		input.SafeheronWebhookEventID == nil || *input.SafeheronWebhookEventID != 81 || input.SourcePayloadDigest != digest {
		t.Fatalf("collector provider event input = %#v", input)
	}
	if len(eligibility.inputs) != 1 || eligibility.inputs[0].RawPayload == nil {
		t.Fatalf("collector eligibility inputs = %#v", eligibility.inputs)
	}
}

func TestSafeheronProviderEventCollector_AcceptsAlreadyInsertedReference(t *testing.T) {
	reader := safeheronRawEventCandidateReaderStub{candidates: []SafeheronRawEventCandidate{{
		SafeheronWebhookEventID: 81,
		ProviderEventID:         strings.Repeat("b", 64),
		EventType:               "TRANSACTION_STATUS_CHANGED",
		PayloadDigest:           strings.Repeat("a", 64),
		RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
	}}}
	writer := &safeheronProviderEventWriterStub{fn: func(_ int, _ ProviderEventInput) (ProviderEventInsertResult, error) {
		return ProviderEventInsertResult{ID: 91, Inserted: false}, nil
	}}
	collector, err := NewSafeheronProviderEventCollector(reader, writer, &safeheronCollectorEligibilityStub{decision: SafeheronWebhookEligibilityDecision{Candidate: true}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := collector.Collect(context.Background(), 10)
	if err != nil || result != (SafeheronProviderEventCollectionResult{Scanned: 1}) {
		t.Fatalf("Collect() = %#v, %v; duplicate reference must converge", result, err)
	}
}

func TestSafeheronProviderEventCollector_RetainsProgressWhenWriterFails(t *testing.T) {
	digest := strings.Repeat("a", 64)
	reader := safeheronRawEventCandidateReaderStub{candidates: []SafeheronRawEventCandidate{
		{SafeheronWebhookEventID: 81, ProviderEventID: strings.Repeat("b", 64), EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: digest, RawPayload: []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)},
		{SafeheronWebhookEventID: 82, ProviderEventID: strings.Repeat("c", 64), EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: digest, RawPayload: []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`)},
	}}
	writer := &safeheronProviderEventWriterStub{fn: func(call int, _ ProviderEventInput) (ProviderEventInsertResult, error) {
		if call == 2 {
			return ProviderEventInsertResult{}, errors.New("database unavailable")
		}
		return ProviderEventInsertResult{ID: 91, Inserted: true}, nil
	}}
	collector, err := NewSafeheronProviderEventCollector(reader, writer, &safeheronCollectorEligibilityStub{decision: SafeheronWebhookEligibilityDecision{Candidate: true}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := collector.Collect(context.Background(), 10)
	if err == nil || result != (SafeheronProviderEventCollectionResult{Scanned: 2, Inserted: 1}) {
		t.Fatalf("Collect() = %#v, %v; want retained progress plus retryable error", result, err)
	}
}

func TestSafeheronProviderEventCollector_NegativeMarkerPreventsNextScanStarvation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	markerStore := &safeheronWebhookExclusionStoreStub{}
	eligibility, err := NewSafeheronWebhookEligibilityService(
		&safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
			ExclusionReason: SafeheronWebhookExclusionNonTransactionStatus,
		}},
		markerStore,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := safeheronRawEventCandidateReaderFunc(func(_ context.Context, _ int) ([]SafeheronRawEventCandidate, error) {
		// This models the SQL exclusion anti-join after the durable marker from
		// the first pass exists: the historical customer event is no longer a
		// batch candidate, so it cannot starve a later company-wallet event.
		if len(markerStore.inputs) > 0 {
			return nil, nil
		}
		return []SafeheronRawEventCandidate{{
			SafeheronWebhookEventID: 81,
			ProviderEventID:         strings.Repeat("b", 64),
			EventType:               safeheronTransactionStatusChangedEventType,
			PayloadDigest:           digest,
			RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
		}}, nil
	})
	writer := &safeheronProviderEventWriterStub{}
	collector, err := NewSafeheronProviderEventCollector(reader, writer, eligibility)
	if err != nil {
		t.Fatal(err)
	}

	first, err := collector.Collect(context.Background(), 1)
	if err != nil || first != (SafeheronProviderEventCollectionResult{Scanned: 1, Excluded: 1}) || len(writer.inputs) != 0 || len(markerStore.inputs) != 1 {
		t.Fatalf("first collection = %#v, %v; provider=%#v markers=%#v", first, err, writer.inputs, markerStore.inputs)
	}
	second, err := collector.Collect(context.Background(), 1)
	if err != nil || second != (SafeheronProviderEventCollectionResult{}) || len(writer.inputs) != 0 {
		t.Fatalf("next collection after exclusion = %#v, %v; provider=%#v", second, err, writer.inputs)
	}
}

func TestSafeheronProviderEventCollector_ReevaluatesConfigurationDependentMarkersAfterFingerprintChange(t *testing.T) {
	for _, reason := range []SafeheronWebhookExclusionReason{
		SafeheronWebhookExclusionNoConfiguredAddress,
		SafeheronWebhookExclusionUnmappedAsset,
	} {
		t.Run(string(reason), func(t *testing.T) {
			digest := strings.Repeat("a", 64)
			fingerprints := &safeheronWebhookFingerprintProviderStub{fingerprint: strings.Repeat("c", 64)}
			markers := &safeheronWebhookExclusionStoreStub{}
			evaluator := &safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
				ExclusionReason:          reason,
				ConfigurationFingerprint: fingerprints.fingerprint,
			}}
			eligibility, err := NewSafeheronWebhookEligibilityService(evaluator, markers)
			if err != nil {
				t.Fatal(err)
			}
			reader := &safeheronWebhookExclusionAwareReader{
				candidate: SafeheronRawEventCandidate{
					SafeheronWebhookEventID: 81,
					ProviderEventID:         strings.Repeat("b", 64),
					EventType:               safeheronTransactionStatusChangedEventType,
					PayloadDigest:           digest,
					RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
				},
				markers:      markers,
				fingerprints: fingerprints,
			}
			writer := &safeheronProviderEventWriterStub{fn: func(_ int, _ ProviderEventInput) (ProviderEventInsertResult, error) {
				reader.providerEventInserted = true
				return ProviderEventInsertResult{ID: 91, Inserted: true}, nil
			}}
			collector, err := NewSafeheronProviderEventCollector(reader, writer, eligibility)
			if err != nil {
				t.Fatal(err)
			}

			first, err := collector.Collect(context.Background(), 1)
			if err != nil || first != (SafeheronProviderEventCollectionResult{Scanned: 1, Excluded: 1}) || len(markers.inputs) != 1 {
				t.Fatalf("initial configuration-dependent exclusion = %#v, %v, markers=%#v", first, err, markers.inputs)
			}
			second, err := collector.Collect(context.Background(), 1)
			if err != nil || second != (SafeheronProviderEventCollectionResult{}) || len(evaluator.inputs) != 1 {
				t.Fatalf("same fingerprint refresh must not rescan marker: %#v, %v, evaluations=%d", second, err, len(evaluator.inputs))
			}

			fingerprints.fingerprint = strings.Repeat("d", 64)
			evaluator.evaluation = SafeheronWebhookCandidateEvaluation{Candidate: true}
			third, err := collector.Collect(context.Background(), 1)
			if err != nil || third != (SafeheronProviderEventCollectionResult{Scanned: 1, Inserted: 1}) || len(writer.inputs) != 1 || len(evaluator.inputs) != 2 {
				t.Fatalf("changed fingerprint must reappear and bridge once: %#v, %v, writes=%#v evaluations=%#v", third, err, writer.inputs, evaluator.inputs)
			}
			fourth, err := collector.Collect(context.Background(), 1)
			if err != nil || fourth != (SafeheronProviderEventCollectionResult{}) || len(writer.inputs) != 1 {
				t.Fatalf("bridged event must remain idempotently absent: %#v, %v, writes=%#v", fourth, err, writer.inputs)
			}
		})
	}
}

func TestSafeheronProviderEventCollector_PermanentMarkerNeverReappearsAfterFingerprintChange(t *testing.T) {
	digest := strings.Repeat("a", 64)
	fingerprints := &safeheronWebhookFingerprintProviderStub{fingerprint: strings.Repeat("c", 64)}
	markers := &safeheronWebhookExclusionStoreStub{}
	evaluator := &safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
		ExclusionReason: SafeheronWebhookExclusionInvalidPayload,
	}}
	eligibility, err := NewSafeheronWebhookEligibilityService(evaluator, markers)
	if err != nil {
		t.Fatal(err)
	}
	reader := &safeheronWebhookExclusionAwareReader{
		candidate: SafeheronRawEventCandidate{
			SafeheronWebhookEventID: 82,
			ProviderEventID:         strings.Repeat("b", 64),
			EventType:               safeheronTransactionStatusChangedEventType,
			PayloadDigest:           digest,
			RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
		},
		markers:      markers,
		fingerprints: fingerprints,
	}
	writer := &safeheronProviderEventWriterStub{}
	collector, err := NewSafeheronProviderEventCollector(reader, writer, eligibility)
	if err != nil {
		t.Fatal(err)
	}

	if result, err := collector.Collect(context.Background(), 1); err != nil || result != (SafeheronProviderEventCollectionResult{Scanned: 1, Excluded: 1}) {
		t.Fatalf("initial permanent exclusion = %#v, %v", result, err)
	}
	fingerprints.fingerprint = strings.Repeat("d", 64)
	evaluator.evaluation = SafeheronWebhookCandidateEvaluation{Candidate: true}
	if result, err := collector.Collect(context.Background(), 1); err != nil || result != (SafeheronProviderEventCollectionResult{}) || len(evaluator.inputs) != 1 || len(writer.inputs) != 0 {
		t.Fatalf("permanent marker must never reappear after settings change: %#v, %v, evaluations=%d writes=%d", result, err, len(evaluator.inputs), len(writer.inputs))
	}
}

func TestSafeheronProviderEventCollector_RequiresEligibilityService(t *testing.T) {
	if _, err := NewSafeheronProviderEventCollector(safeheronRawEventCandidateReaderStub{}, &safeheronProviderEventWriterStub{}); err == nil {
		t.Fatal("collector without eligibility must fail closed at construction")
	}
}
