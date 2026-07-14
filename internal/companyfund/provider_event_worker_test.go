package companyfund

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestProviderEventWorker_ProcessNextWritesAllMovementsBeforeFinalizing(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	payloadReader := &providerEventPayloadReaderStub{payload: []byte(`{"event":"payment"}`)}
	normalizer := &providerEventNormalizerStub{result: ProviderEventNormalizationResult{Movements: []TransactionUpsertInput{
		validProviderEventWorkerMovement("movement-1"),
		validProviderEventWorkerMovement("movement-2"),
	}}}
	now := time.Date(2026, time.July, 10, 5, 0, 0, 0, time.UTC)
	worker := newProviderEventWorkerForTest(t, repository, payloadReader, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, now)

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if !result.Claimed || result.EventID != lease.ID || result.MovementCount != 2 || result.Outcome != ProviderEventFinalizeProcessed {
		t.Fatalf("ProcessNext() result = %#v", result)
	}
	if repository.renewCalls != 1 {
		t.Fatalf("renew calls = %d, want 1 before external payload processing", repository.renewCalls)
	}
	if payloadReader.calls != 1 || normalizer.calls != 1 {
		t.Fatalf("payload/normalizer calls = %d/%d, want 1/1", payloadReader.calls, normalizer.calls)
	}
	if len(repository.upserts) != 2 {
		t.Fatalf("movement upserts = %d, want 2", len(repository.upserts))
	}
	for _, movement := range repository.upserts {
		if movement.LatestProviderEventID == nil || *movement.LatestProviderEventID != lease.ID {
			t.Fatalf("movement provenance event ID = %#v, want %d", movement.LatestProviderEventID, lease.ID)
		}
		if movement.RawSnapshotDigest != lease.SourcePayloadDigest {
			t.Fatalf("movement provenance digest = %q, want %q", movement.RawSnapshotDigest, lease.SourcePayloadDigest)
		}
	}
	if len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeProcessed {
		t.Fatalf("finalizations = %#v, want one processed finalization after upserts", repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextInvokesOptionalValuatorAfterSuccessfulUpsertWithoutRetryingEvent(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	valuator := &providerEventWorkerValuatorStub{result: CompanyFundValuationProcessResult{Err: errors.New("temporary valuation cache failure")}}
	worker, err := NewProviderEventWorker(repository, &providerEventPayloadReaderStub{payload: []byte(`{"event":"payment"}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{result: ProviderEventNormalizationResult{Movements: []TransactionUpsertInput{
			validProviderEventWorkerMovement("valuation-hook"),
		}}},
	}, ProviderEventWorkerConfig{
		Owner:               "company-fund-test-worker",
		LeaseDuration:       time.Minute,
		RenewInterval:       30 * time.Second,
		RetryPolicy:         ProviderEventRetryPolicy{InitialDelay: time.Second, MaxDelay: 4 * time.Second},
		Now:                 time.Now,
		TransactionValuator: valuator,
	})
	if err != nil {
		t.Fatalf("NewProviderEventWorker() error = %v", err)
	}

	result, err := worker.ProcessNext(context.Background())
	if err != nil || result.Outcome != ProviderEventFinalizeProcessed || result.MovementCount != 1 {
		t.Fatalf("ProcessNext() = %#v, %v; valuation failure must not retry provider event", result, err)
	}
	if len(valuator.transactionIDs) != 1 || valuator.transactionIDs[0] != 1 {
		t.Fatalf("valuator transaction IDs = %#v, want successful upsert ID 1", valuator.transactionIDs)
	}
	if len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeProcessed {
		t.Fatalf("valuation failure must not alter finalization: %#v", repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextContainsOptionalValuatorPanicAfterLedgerCommit(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	valuator := &providerEventWorkerValuatorStub{panicValue: "valuation must not affect provider event"}
	worker, err := NewProviderEventWorker(repository, &providerEventPayloadReaderStub{payload: []byte(`{"event":"payment"}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{result: ProviderEventNormalizationResult{Movements: []TransactionUpsertInput{
			validProviderEventWorkerMovement("valuation-panic-hook"),
		}}},
	}, ProviderEventWorkerConfig{
		Owner:               "company-fund-test-worker",
		LeaseDuration:       time.Minute,
		RenewInterval:       30 * time.Second,
		RetryPolicy:         ProviderEventRetryPolicy{InitialDelay: time.Second, MaxDelay: 4 * time.Second},
		Now:                 time.Now,
		TransactionValuator: valuator,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := worker.ProcessNext(context.Background())
	if err != nil || result.Outcome != ProviderEventFinalizeProcessed || len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeProcessed {
		t.Fatalf("optional valuator panic must be contained: result=%#v err=%v finalizations=%#v", result, err, repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextDeadLettersPermanentNormalizationFailure(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	payloadReader := &providerEventPayloadReaderStub{payload: []byte(`{"schema":"unknown"}`)}
	normalizer := &providerEventNormalizerStub{err: NewPermanentProviderEventError(errors.New("unsupported payload schema version"))}
	worker := newProviderEventWorkerForTest(t, repository, payloadReader, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeFailed || result.MovementCount != 0 {
		t.Fatalf("ProcessNext() result = %#v, want dead-letter result", result)
	}
	if len(repository.upserts) != 0 {
		t.Fatalf("permanent parsing failure must not upsert movements: %#v", repository.upserts)
	}
	if len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeFailed || repository.finalizations[0].retryAt != nil {
		t.Fatalf("finalizations = %#v, want one permanent dead-letter", repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextSchedulesRetryForTransientFailure(t *testing.T) {
	lease := validProviderEventWorkerLease()
	lease.AttemptCount = 2
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	payloadReader := &providerEventPayloadReaderStub{err: errors.New("temporary provider payload read failure")}
	now := time.Date(2026, time.July, 10, 6, 0, 0, 0, time.UTC)
	worker := newProviderEventWorkerForTest(t, repository, payloadReader, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{},
	}, now)

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeRetry {
		t.Fatalf("ProcessNext() outcome = %q, want retry", result.Outcome)
	}
	if len(repository.finalizations) != 1 {
		t.Fatalf("finalizations = %#v, want retry finalization", repository.finalizations)
	}
	finalization := repository.finalizations[0]
	if finalization.outcome != ProviderEventFinalizeRetry || finalization.retryAt == nil {
		t.Fatalf("finalization = %#v, want retry with future time", finalization)
	}
	if want := now.Add(2 * time.Second); !finalization.retryAt.Equal(want) {
		t.Fatalf("retryAt = %s, want %s", finalization.retryAt, want)
	}
}

func TestProviderEventWorker_ProcessNextDoesNotLogProviderFailureDetailWhenRetryFinalizationFails(t *testing.T) {
	const sensitiveDetail = "provider payload wallet=0xSensitive transaction=secret-tx"
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{
		lease:       &lease,
		finalizeErr: errors.New("database unavailable"),
	}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{
		err: errors.New(sensitiveDetail),
	}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{},
	}, time.Date(2026, time.July, 10, 6, 0, 0, 0, time.UTC))

	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	result, err := worker.ProcessNext(t.Context())
	if err == nil || !result.Claimed {
		t.Fatalf("ProcessNext() = %#v, %v; want claimed finalization failure", result, err)
	}
	if strings.Contains(output.String(), sensitiveDetail) {
		t.Fatalf("process log leaked provider failure detail: %q", output.String())
	}
	if !strings.Contains(output.String(), "stage=finalize_retry") {
		t.Fatalf("process log lacks stable retry-finalization stage: %q", output.String())
	}
}

func TestProviderEventWorker_ProcessNextIgnoresSupportedNonMovementEvent(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{payload: []byte(`{}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{result: ProviderEventNormalizationResult{Ignored: true}},
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeIgnored || len(repository.upserts) != 0 {
		t.Fatalf("ProcessNext() = %#v, upserts = %#v", result, repository.upserts)
	}
	if len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeIgnored {
		t.Fatalf("finalizations = %#v, want ignored", repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextPersistsFactsBeforeBindingsWithoutCopyingBatchParentTotal(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{
		lease:   &lease,
		factIDs: map[string]int64{"parent-total": 901, "child-direct": 902},
	}
	childOne := validProviderEventWorkerMovement("batch-child-1")
	childOne.TransferMode = TransferModeBatch
	childTwo := validProviderEventWorkerMovement("batch-child-2")
	childTwo.TransferMode = TransferModeBatch
	normalizer := &providerEventNormalizerStub{result: ProviderEventNormalizationResult{
		Facts: []ProviderEventNormalizedFact{
			{Reference: "parent-total", Input: validProviderEventWorkerFact("parent-total", ProviderValueScopeTransactionTotal, ProviderFactAllocationStateUnproven)},
			{Reference: "child-direct", Input: validProviderEventWorkerFact("child-direct", ProviderValueScopeDirectItem, ProviderFactAllocationStateNotApplicable)},
		},
		Movements: []TransactionUpsertInput{childOne, childTwo},
		FactBindings: []ProviderEventMovementFactBinding{
			{MovementKey: childOne.MovementKey, FactReference: "child-direct"},
		},
	}}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{payload: []byte(`{"event":"batch"}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeProcessed || result.FactCount != 2 || result.MovementCount != 2 {
		t.Fatalf("ProcessNext() = %#v", result)
	}
	if len(repository.facts) != 2 {
		t.Fatalf("persisted facts = %#v, want both parent audit fact and child direct fact", repository.facts)
	}
	for _, fact := range repository.facts {
		if fact.SourceProviderEventID != lease.ID || fact.SourcePayloadDigest != lease.SourcePayloadDigest {
			t.Fatalf("fact provenance = %#v, want worker-bound source", fact)
		}
	}
	if len(repository.upserts) != 2 {
		t.Fatalf("movement upserts = %#v, want two batch children", repository.upserts)
	}
	if repository.upserts[0].ProviderTransactionFactID == nil || *repository.upserts[0].ProviderTransactionFactID != 902 {
		t.Fatalf("direct child fact binding = %#v, want 902", repository.upserts[0].ProviderTransactionFactID)
	}
	if repository.upserts[1].ProviderTransactionFactID != nil {
		t.Fatalf("unproven parent total must not be copied/bound to second batch child: %#v", repository.upserts[1].ProviderTransactionFactID)
	}
}

func TestProviderEventWorker_ProcessNextDeadLettersInvalidUnprovenParentTotalBinding(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease}
	movement := validProviderEventWorkerMovement("batch-child")
	movement.TransferMode = TransferModeBatch
	normalizer := &providerEventNormalizerStub{result: ProviderEventNormalizationResult{
		Facts: []ProviderEventNormalizedFact{
			{Reference: "parent-total", Input: validProviderEventWorkerFact("parent-total", ProviderValueScopeTransactionTotal, ProviderFactAllocationStateUnproven)},
		},
		Movements: []TransactionUpsertInput{movement},
		FactBindings: []ProviderEventMovementFactBinding{
			{MovementKey: movement.MovementKey, FactReference: "parent-total"},
		},
	}}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{payload: []byte(`{"event":"batch"}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeFailed || len(repository.facts) != 0 || len(repository.upserts) != 0 {
		t.Fatalf("invalid total binding result = %#v facts=%#v movements=%#v", result, repository.facts, repository.upserts)
	}
}

func TestProviderEventWorker_ProcessNextRetriesProviderFactInsertFailure(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease, factInsertErr: errors.New("temporary fact storage failure")}
	movement := validProviderEventWorkerMovement("movement-fact-retry")
	normalizer := &providerEventNormalizerStub{result: ProviderEventNormalizationResult{
		Facts: []ProviderEventNormalizedFact{
			{Reference: "direct", Input: validProviderEventWorkerFact("direct", ProviderValueScopeDirectItem, ProviderFactAllocationStateNotApplicable)},
		},
		Movements:    []TransactionUpsertInput{movement},
		FactBindings: []ProviderEventMovementFactBinding{{MovementKey: movement.MovementKey, FactReference: "direct"}},
	}}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{payload: []byte(`{"event":"payment"}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext() error = %v", err)
	}
	if result.Outcome != ProviderEventFinalizeRetry || len(repository.upserts) != 0 || len(repository.finalizations) != 1 || repository.finalizations[0].outcome != ProviderEventFinalizeRetry {
		t.Fatalf("fact insert failure result = %#v upserts=%#v finalizations=%#v", result, repository.upserts, repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextDoesNotFinalizeAfterLeaseRenewalLoss(t *testing.T) {
	lease := validProviderEventWorkerLease()
	repository := &providerEventWorkerRepositoryStub{lease: &lease, renewErr: ErrProviderEventLeaseNotOwned}
	worker := newProviderEventWorkerForTest(t, repository, &providerEventPayloadReaderStub{payload: []byte(`{}`)}, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: &providerEventNormalizerStub{},
	}, time.Now().UTC())

	if _, err := worker.ProcessNext(context.Background()); !errors.Is(err, ErrProviderEventLeaseNotOwned) {
		t.Fatalf("ProcessNext() error = %v, want lost lease", err)
	}
	if len(repository.finalizations) != 0 {
		t.Fatalf("lost lease must not be finalized by this worker: %#v", repository.finalizations)
	}
}

func TestProviderEventWorker_ProcessNextRecoversPayloadNormalizerAndUpsertPanicsAsSafeRetry(t *testing.T) {
	const secret = "raw-provider-body-must-not-leak"
	tests := []struct {
		name  string
		setup func(*providerEventWorkerRepositoryStub, *providerEventPayloadReaderStub, *providerEventNormalizerStub)
	}{
		{
			name: "payload reader",
			setup: func(_ *providerEventWorkerRepositoryStub, payload *providerEventPayloadReaderStub, _ *providerEventNormalizerStub) {
				payload.panicValue = secret
			},
		},
		{
			name: "normalizer",
			setup: func(_ *providerEventWorkerRepositoryStub, _ *providerEventPayloadReaderStub, normalizer *providerEventNormalizerStub) {
				normalizer.panicValue = secret
			},
		},
		{
			name: "upsert",
			setup: func(repository *providerEventWorkerRepositoryStub, _ *providerEventPayloadReaderStub, _ *providerEventNormalizerStub) {
				repository.upsertPanicValue = secret
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lease := validProviderEventWorkerLease()
			repository := &providerEventWorkerRepositoryStub{lease: &lease}
			payloadReader := &providerEventPayloadReaderStub{payload: []byte(`{"event":"payment"}`)}
			normalizer := &providerEventNormalizerStub{result: ProviderEventNormalizationResult{Movements: []TransactionUpsertInput{
				validProviderEventWorkerMovement("movement-panic"),
			}}}
			test.setup(repository, payloadReader, normalizer)
			worker := newProviderEventWorkerForTest(t, repository, payloadReader, map[Channel]ProviderEventNormalizer{
				ChannelAirwallex: normalizer,
			}, time.Now().UTC())

			result, err := worker.ProcessNext(context.Background())
			if err != nil {
				t.Fatalf("ProcessNext() error = %v", err)
			}
			if result.Outcome != ProviderEventFinalizeRetry || len(repository.finalizations) != 1 {
				t.Fatalf("ProcessNext() = %#v, finalizations = %#v; want one retry", result, repository.finalizations)
			}
			finalization := repository.finalizations[0]
			if finalization.outcome != ProviderEventFinalizeRetry || finalization.retryAt == nil {
				t.Fatalf("panic finalization = %#v, want retry with backoff", finalization)
			}
			if strings.Contains(finalization.failureDetail, secret) || finalization.failureDetail != "provider event processing panicked" {
				t.Fatalf("panic failure detail leaked or changed: %q", finalization.failureDetail)
			}
		})
	}
}

func TestProviderEventWorker_ProcessNextReturnsNoWorkWithoutCallingDependencies(t *testing.T) {
	repository := &providerEventWorkerRepositoryStub{}
	payloadReader := &providerEventPayloadReaderStub{}
	normalizer := &providerEventNormalizerStub{}
	worker := newProviderEventWorkerForTest(t, repository, payloadReader, map[Channel]ProviderEventNormalizer{
		ChannelAirwallex: normalizer,
	}, time.Now().UTC())

	result, err := worker.ProcessNext(context.Background())
	if err != nil || result.Claimed || result.EventID != 0 {
		t.Fatalf("ProcessNext() = %#v, %v", result, err)
	}
	if repository.renewCalls != 0 || payloadReader.calls != 0 || normalizer.calls != 0 || len(repository.finalizations) != 0 {
		t.Fatalf("no work must not process dependencies: repository=%#v payload=%#v normalizer=%#v", repository, payloadReader, normalizer)
	}
}

func TestProviderEventSourceBytesReaderRoutesSafeheronReferencesAndOwnedPayloads(t *testing.T) {
	rawReader := &safeheronRawPayloadReaderStub{payload: []byte("safeheron")}
	ownedReader := &ownedProviderPayloadReaderStub{payload: []byte("owned")}
	reader, err := NewProviderEventSourceBytesReader(rawReader, ownedReader)
	if err != nil {
		t.Fatalf("NewProviderEventSourceBytesReader() error = %v", err)
	}

	rawID := 88
	safeheronLease := ProviderEventLease{SourceKind: ProviderEventSourceExistingSafeheronWebhookRef, SafeheronWebhookEventID: &rawID}
	if payload, err := reader.ReadProviderEventPayload(context.Background(), safeheronLease); err != nil || string(payload) != "safeheron" || rawReader.eventID != rawID {
		t.Fatalf("safeheron source payload = %q, %v, reader=%#v", payload, err, rawReader)
	}
	ownedLease := ProviderEventLease{SourceKind: ProviderEventSourceOwnedEncryptedPayload}
	if payload, err := reader.ReadProviderEventPayload(context.Background(), ownedLease); err != nil || string(payload) != "owned" || ownedReader.calls != 1 {
		t.Fatalf("owned source payload = %q, %v, reader=%#v", payload, err, ownedReader)
	}
}

func TestProviderEventRetryPolicyBacksOffExponentiallyAndCaps(t *testing.T) {
	policy := ProviderEventRetryPolicy{InitialDelay: time.Second, MaxDelay: 4 * time.Second}
	for attempt, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 10: 4 * time.Second} {
		if got, err := policy.Delay(attempt); err != nil || got != want {
			t.Fatalf("Delay(%d) = %s, %v; want %s", attempt, got, err, want)
		}
	}
}

func newProviderEventWorkerForTest(
	t *testing.T,
	repository ProviderEventWorkerRepository,
	payloadReader ProviderEventPayloadReader,
	normalizers map[Channel]ProviderEventNormalizer,
	now time.Time,
) *ProviderEventWorker {
	t.Helper()
	worker, err := NewProviderEventWorker(repository, payloadReader, normalizers, ProviderEventWorkerConfig{
		Owner:         "company-fund-test-worker",
		LeaseDuration: time.Minute,
		RenewInterval: 30 * time.Second,
		RetryPolicy:   ProviderEventRetryPolicy{InitialDelay: time.Second, MaxDelay: 4 * time.Second},
		Now:           func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewProviderEventWorker() error = %v", err)
	}
	return worker
}

func validProviderEventWorkerLease() ProviderEventLease {
	return ProviderEventLease{
		ID:                   71,
		Channel:              ChannelAirwallex,
		ProviderEventID:      "payment-71",
		EventType:            "PAYMENT_UPDATED",
		ProviderEventVersion: "v1",
		SourceKind:           ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:  strings.Repeat("a", 64),
		AttemptCount:         1,
	}
}

func validProviderEventWorkerMovement(key string) TransactionUpsertInput {
	status := LifecycleStatusPending
	return TransactionUpsertInput{
		MovementKey:              key,
		Channel:                  ChannelAirwallex,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		ProviderAccountKey:       "account-a",
		ProviderTransactionID:    "payment-71",
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionInflow,
		Currency:                 "USD",
		Asset:                    AssetIdentity{Currency: "USD"},
		Amount:                   decimal.NewFromInt(100),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Status:   &status,
			Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook},
		},
	}
}

func validProviderEventWorkerFact(identity string, scope ProviderValueScope, allocation ProviderFactAllocationState) ProviderTransactionFactInput {
	amount := decimal.NewFromInt(100)
	return ProviderTransactionFactInput{
		Channel:               ChannelAirwallex,
		ProviderAccountKey:    "account-a",
		ProviderTransactionID: "payment-71",
		FactIdentityKey:       identity,
		FactVersion:           1,
		ProviderAmount:        &amount,
		ProviderCurrency:      "USD",
		ValueScope:            scope,
		AllocationState:       allocation,
	}
}

type providerEventWorkerRepositoryStub struct {
	lease            *ProviderEventLease
	claimErr         error
	renewErr         error
	upsertErr        error
	upsertPanicValue any
	factInsertErr    error
	factIDs          map[string]int64
	finalizeErr      error
	renewCalls       int
	facts            []ProviderTransactionFactInput
	upserts          []TransactionUpsertInput
	finalizations    []providerEventFinalizationCall
}

func (s *providerEventWorkerRepositoryStub) ClaimNextProviderEvent(_ context.Context, _ string, _ time.Duration) (*ProviderEventLease, error) {
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	if s.lease == nil {
		return nil, nil
	}
	lease := *s.lease
	return &lease, nil
}

func (s *providerEventWorkerRepositoryStub) RenewProviderEventLease(_ context.Context, _ int64, _ string, duration time.Duration) (time.Time, error) {
	s.renewCalls++
	if s.renewErr != nil {
		return time.Time{}, s.renewErr
	}
	return time.Now().UTC().Add(duration), nil
}

func (s *providerEventWorkerRepositoryStub) UpsertCompanyFundTransaction(_ context.Context, input TransactionUpsertInput) (TransactionUpsertResult, error) {
	if s.upsertPanicValue != nil {
		panic(s.upsertPanicValue)
	}
	s.upserts = append(s.upserts, input)
	if s.upsertErr != nil {
		return TransactionUpsertResult{}, s.upsertErr
	}
	return TransactionUpsertResult{ID: int64(len(s.upserts)), Inserted: true}, nil
}

func (s *providerEventWorkerRepositoryStub) InsertProviderTransactionFact(_ context.Context, input ProviderTransactionFactInput) (ProviderTransactionFactInsertResult, error) {
	s.facts = append(s.facts, input)
	if s.factInsertErr != nil {
		return ProviderTransactionFactInsertResult{}, s.factInsertErr
	}
	id := int64(len(s.facts))
	if configuredID, ok := s.factIDs[input.FactIdentityKey]; ok {
		id = configuredID
	}
	return ProviderTransactionFactInsertResult{Fact: ProviderTransactionFact{ID: id}, Inserted: true}, nil
}

func (s *providerEventWorkerRepositoryStub) FinalizeProviderEvent(_ context.Context, eventID int64, owner string, outcome ProviderEventFinalizeOutcome, retryAt *time.Time, failureDetail string) error {
	s.finalizations = append(s.finalizations, providerEventFinalizationCall{eventID: eventID, owner: owner, outcome: outcome, retryAt: retryAt, failureDetail: failureDetail})
	return s.finalizeErr
}

type providerEventFinalizationCall struct {
	eventID       int64
	owner         string
	outcome       ProviderEventFinalizeOutcome
	retryAt       *time.Time
	failureDetail string
}

type providerEventPayloadReaderStub struct {
	payload    []byte
	err        error
	panicValue any
	calls      int
}

func (s *providerEventPayloadReaderStub) ReadProviderEventPayload(_ context.Context, _ ProviderEventLease) ([]byte, error) {
	s.calls++
	if s.panicValue != nil {
		panic(s.panicValue)
	}
	if s.err != nil {
		return nil, s.err
	}
	return append([]byte(nil), s.payload...), nil
}

type providerEventNormalizerStub struct {
	result     ProviderEventNormalizationResult
	err        error
	panicValue any
	calls      int
}

type providerEventWorkerValuatorStub struct {
	transactionIDs []int64
	result         CompanyFundValuationProcessResult
	panicValue     any
}

func (stub *providerEventWorkerValuatorStub) ValueTransaction(_ context.Context, transactionID int64) CompanyFundValuationProcessResult {
	if stub.panicValue != nil {
		panic(stub.panicValue)
	}
	stub.transactionIDs = append(stub.transactionIDs, transactionID)
	return stub.result
}

func (stub *providerEventWorkerValuatorStub) Sweep(context.Context, int) CompanyFundValuationSweepResult {
	return CompanyFundValuationSweepResult{}
}

func (s *providerEventNormalizerStub) NormalizeProviderEvent(_ context.Context, _ ProviderEventLease, _ []byte) (ProviderEventNormalizationResult, error) {
	s.calls++
	if s.panicValue != nil {
		panic(s.panicValue)
	}
	return s.result, s.err
}

type safeheronRawPayloadReaderStub struct {
	payload []byte
	eventID int
}

func (s *safeheronRawPayloadReaderStub) ReadSafeheronWebhookPayload(_ context.Context, eventID int) ([]byte, error) {
	s.eventID = eventID
	return append([]byte(nil), s.payload...), nil
}

type ownedProviderPayloadReaderStub struct {
	payload []byte
	calls   int
}

func (s *ownedProviderPayloadReaderStub) DecryptLease(_ ProviderEventLease) ([]byte, error) {
	s.calls++
	return append([]byte(nil), s.payload...), nil
}

var _ ProviderEventWorkerRepository = (*providerEventWorkerRepositoryStub)(nil)
var _ ProviderEventPayloadReader = (*providerEventPayloadReaderStub)(nil)
var _ ProviderEventNormalizer = (*providerEventNormalizerStub)(nil)
var _ CompanyFundTransactionValuator = (*providerEventWorkerValuatorStub)(nil)
var _ SafeheronWebhookPayloadReader = (*safeheronRawPayloadReaderStub)(nil)
var _ OwnedProviderEventPayloadDecryptor = (*ownedProviderPayloadReaderStub)(nil)
