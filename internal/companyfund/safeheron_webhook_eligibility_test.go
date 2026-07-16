package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"monera-digital/internal/safeheron"
)

type safeheronWebhookCandidateEvaluatorStub struct {
	evaluation SafeheronWebhookCandidateEvaluation
	err        error
	inputs     []struct {
		eventType string
		raw       []byte
	}
}

func (s *safeheronWebhookCandidateEvaluatorStub) EvaluateSafeheronWebhookCandidate(_ context.Context, eventType string, rawPayload []byte) (SafeheronWebhookCandidateEvaluation, error) {
	s.inputs = append(s.inputs, struct {
		eventType string
		raw       []byte
	}{eventType: eventType, raw: append([]byte(nil), rawPayload...)})
	if s.err != nil {
		return SafeheronWebhookCandidateEvaluation{}, s.err
	}
	return s.evaluation, nil
}

type safeheronWebhookExclusionStoreStub struct {
	inputs []SafeheronWebhookRawEventExclusionInput
	err    error
}

func (s *safeheronWebhookExclusionStoreStub) RecordSafeheronWebhookRawEventExclusion(_ context.Context, input SafeheronWebhookRawEventExclusionInput) error {
	s.inputs = append(s.inputs, input)
	return s.err
}

func TestRegistrySafeheronWebhookCandidateEvaluator_RequiresMappedCompanyAddress(t *testing.T) {
	base := testSafeheronNormalizationInput(t)
	evaluator, err := NewRegistrySafeheronWebhookCandidateEvaluator(safeheronRegistrySnapshotProviderStub{snapshot: base.Registry})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("matches a batch destination under the mapped network", func(t *testing.T) {
		snapshot := base.Snapshot
		snapshot.SourceAddress = "0xExternal"
		snapshot.DestinationAddress = ""
		snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{{Address: "0xTO", Amount: "1"}}
		raw := testSafeheronWebhookEligibilityPayload(t, safeheronTransactionStatusChangedEventType, snapshot)

		decision, err := evaluator.EvaluateSafeheronWebhookCandidate(context.Background(), safeheronTransactionStatusChangedEventType, raw)
		if err != nil || !decision.Candidate || decision.ExclusionReason != "" {
			t.Fatalf("candidate decision = %#v, %v", decision, err)
		}
	})

	t.Run("unconfigured addresses become a durable negative eligibility", func(t *testing.T) {
		snapshot := base.Snapshot
		snapshot.SourceAddress = "0xExternalFrom"
		snapshot.DestinationAddress = "0xExternalTo"
		snapshot.DestinationAddressList = nil
		raw := testSafeheronWebhookEligibilityPayload(t, safeheronTransactionStatusChangedEventType, snapshot)

		decision, err := evaluator.EvaluateSafeheronWebhookCandidate(context.Background(), safeheronTransactionStatusChangedEventType, raw)
		if err != nil || decision.Candidate || decision.ExclusionReason != SafeheronWebhookExclusionNoConfiguredAddress || !isLowerSHA256Hex(decision.ConfigurationFingerprint) {
			t.Fatalf("non-company decision = %#v, %v", decision, err)
		}
	})

	t.Run("unmapped coin key still uses company account context", func(t *testing.T) {
		snapshot := base.Snapshot
		snapshot.CoinKey = "UNKNOWN_COIN"
		snapshot.SourceAccountKey = "vault-from"
		raw := testSafeheronWebhookEligibilityPayload(t, safeheronTransactionStatusChangedEventType, snapshot)

		decision, err := evaluator.EvaluateSafeheronWebhookCandidate(context.Background(), safeheronTransactionStatusChangedEventType, raw)
		if err != nil || !decision.Candidate || decision.ExclusionReason != "" || decision.ConfigurationFingerprint != "" {
			t.Fatalf("unmapped decision = %#v, %v", decision, err)
		}
	})
}

func TestSafeheronWebhookEligibilityService_PersistsOnlyNegativeMarker(t *testing.T) {
	digest := strings.Repeat("a", 64)
	evaluator := &safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
		ExclusionReason:          SafeheronWebhookExclusionNoConfiguredAddress,
		ConfigurationFingerprint: strings.Repeat("c", 64),
	}}
	store := &safeheronWebhookExclusionStoreStub{}
	service, err := NewSafeheronWebhookEligibilityService(evaluator, store)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := service.AssessAndRecord(context.Background(), SafeheronWebhookEligibilityInput{
		SafeheronWebhookEventID: 91,
		EventType:               safeheronTransactionStatusChangedEventType,
		PayloadDigest:           digest,
		RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
	})
	if err != nil || decision.Candidate || len(store.inputs) != 1 {
		t.Fatalf("AssessAndRecord() = %#v, %v; markers=%#v", decision, err, store.inputs)
	}
	marker := store.inputs[0]
	if marker.SafeheronWebhookEventID != 91 || marker.PayloadDigest != digest || marker.Reason != SafeheronWebhookExclusionNoConfiguredAddress || marker.ConfigurationFingerprint != strings.Repeat("c", 64) {
		t.Fatalf("negative marker = %#v", marker)
	}

	evaluator.evaluation = SafeheronWebhookCandidateEvaluation{Candidate: true}
	decision, err = service.AssessAndRecord(context.Background(), SafeheronWebhookEligibilityInput{
		SafeheronWebhookEventID: 92,
		EventType:               safeheronTransactionStatusChangedEventType,
		PayloadDigest:           digest,
		RawPayload:              []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED"}`),
	})
	if err != nil || !decision.Candidate || len(store.inputs) != 1 {
		t.Fatalf("positive candidate must not create an exclusion: %#v, %v; markers=%#v", decision, err, store.inputs)
	}
}

func TestSafeheronWebhookEligibilityService_ExclusionStoreFailureIsReturned(t *testing.T) {
	store := &safeheronWebhookExclusionStoreStub{err: errors.New("storage unavailable")}
	service, err := NewSafeheronWebhookEligibilityService(&safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
		ExclusionReason:          SafeheronWebhookExclusionNoConfiguredAddress,
		ConfigurationFingerprint: strings.Repeat("c", 64),
	}}, store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.AssessAndRecord(context.Background(), SafeheronWebhookEligibilityInput{
		SafeheronWebhookEventID: 91,
		EventType:               safeheronTransactionStatusChangedEventType,
		PayloadDigest:           strings.Repeat("a", 64),
	})
	if !errors.Is(err, store.err) {
		t.Fatalf("marker failure = %v, want storage error", err)
	}
}

func TestDBRepositoryRecordSafeheronWebhookRawEventExclusion_VerifiesSourceDigestAndConflict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	t.Run("insert", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadDigestSQL)).
			WithArgs(91).
			WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(digest))
		mock.ExpectQuery(regexp.QuoteMeta(insertSafeheronWebhookRawEventExclusionSQL)).
			WithArgs(91, digest, SafeheronWebhookExclusionNoConfiguredAddress, strings.Repeat("c", 64)).
			WillReturnRows(sqlmock.NewRows([]string{"safeheron_webhook_event_id"}).AddRow(91))

		err = repository.RecordSafeheronWebhookRawEventExclusion(context.Background(), SafeheronWebhookRawEventExclusionInput{
			SafeheronWebhookEventID:  91,
			PayloadDigest:            digest,
			Reason:                   SafeheronWebhookExclusionNoConfiguredAddress,
			ConfigurationFingerprint: strings.Repeat("c", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("existing marker with another digest is rejected", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadDigestSQL)).
			WithArgs(91).
			WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(digest))
		mock.ExpectQuery(regexp.QuoteMeta(insertSafeheronWebhookRawEventExclusionSQL)).
			WithArgs(91, digest, SafeheronWebhookExclusionNoConfiguredAddress, strings.Repeat("c", 64)).
			WillReturnRows(sqlmock.NewRows([]string{"safeheron_webhook_event_id"}))
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookRawEventExclusionDigestSQL)).
			WithArgs(91).
			WillReturnRows(sqlmock.NewRows([]string{"source_payload_digest"}).AddRow(strings.Repeat("b", 64)))

		err = repository.RecordSafeheronWebhookRawEventExclusion(context.Background(), SafeheronWebhookRawEventExclusionInput{
			SafeheronWebhookEventID:  91,
			PayloadDigest:            digest,
			Reason:                   SafeheronWebhookExclusionNoConfiguredAddress,
			ConfigurationFingerprint: strings.Repeat("c", 64),
		})
		if !errors.Is(err, ErrSafeheronWebhookExclusionIdentityConflict) {
			t.Fatalf("conflicting marker error = %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("same source updates a configuration-dependent marker fingerprint", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		repository := NewDBRepository(db)
		mock.ExpectQuery(regexp.QuoteMeta(selectSafeheronWebhookPayloadDigestSQL)).
			WithArgs(91).
			WillReturnRows(sqlmock.NewRows([]string{"payload_digest"}).AddRow(digest))
		mock.ExpectQuery(regexp.QuoteMeta(insertSafeheronWebhookRawEventExclusionSQL)).
			WithArgs(91, digest, SafeheronWebhookExclusionUnmappedAsset, strings.Repeat("d", 64)).
			WillReturnRows(sqlmock.NewRows([]string{"safeheron_webhook_event_id"}).AddRow(91))

		err = repository.RecordSafeheronWebhookRawEventExclusion(context.Background(), SafeheronWebhookRawEventExclusionInput{
			SafeheronWebhookEventID:  91,
			PayloadDigest:            digest,
			Reason:                   SafeheronWebhookExclusionUnmappedAsset,
			ConfigurationFingerprint: strings.Repeat("d", 64),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSafeheronWebhookExclusionInput_RequiresFingerprintOnlyForConfigurationDependentReasons(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, testCase := range []struct {
		name  string
		input SafeheronWebhookRawEventExclusionInput
		valid bool
	}{
		{
			name:  "configuration dependent with fingerprint",
			input: SafeheronWebhookRawEventExclusionInput{SafeheronWebhookEventID: 91, PayloadDigest: digest, Reason: SafeheronWebhookExclusionNoConfiguredAddress, ConfigurationFingerprint: strings.Repeat("c", 64)},
			valid: true,
		},
		{
			name:  "configuration dependent without fingerprint",
			input: SafeheronWebhookRawEventExclusionInput{SafeheronWebhookEventID: 91, PayloadDigest: digest, Reason: SafeheronWebhookExclusionNoConfiguredAddress},
		},
		{
			name:  "permanent without fingerprint",
			input: SafeheronWebhookRawEventExclusionInput{SafeheronWebhookEventID: 91, PayloadDigest: digest, Reason: SafeheronWebhookExclusionInvalidPayload},
			valid: true,
		},
		{
			name:  "permanent with fingerprint",
			input: SafeheronWebhookRawEventExclusionInput{SafeheronWebhookEventID: 91, PayloadDigest: digest, Reason: SafeheronWebhookExclusionInvalidPayload, ConfigurationFingerprint: strings.Repeat("c", 64)},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.input.validate()
			if testCase.valid && err != nil {
				t.Fatalf("validate() = %v", err)
			}
			if !testCase.valid && err == nil {
				t.Fatal("validate() = nil, want rejection")
			}
		})
	}
}

func TestSafeheronWebhookEligibilityConfigurationFingerprint_IsStableAcrossSameContentRefresh(t *testing.T) {
	base := testSafeheronNormalizationInput(t).Registry
	accounts := base.Accounts()
	policies := base.AssetPolicies()
	for left, right := 0, len(accounts)-1; left < right; left, right = left+1, right-1 {
		accounts[left], accounts[right] = accounts[right], accounts[left]
	}
	for left, right := 0, len(policies)-1; left < right; left, right = left+1, right-1 {
		policies[left], policies[right] = policies[right], policies[left]
	}
	refreshed, err := buildAccountRegistrySnapshot(accounts, policies, time.Date(2026, time.July, 12, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	before, err := safeheronWebhookEligibilityConfigurationFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	after, err := safeheronWebhookEligibilityConfigurationFingerprint(refreshed)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("same settings with another refresh timestamp/order changed fingerprint: %s != %s", before, after)
	}

	// A Safeheron account may receive webhook events by address even when the
	// provider account key is intentionally unset for history reconciliation.
	accounts = refreshed.Accounts()
	accounts[0].ProviderAccountKey = ""
	withoutProviderKey, err := buildAccountRegistrySnapshot(accounts, refreshed.AssetPolicies(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint, err := safeheronWebhookEligibilityConfigurationFingerprint(withoutProviderKey); err != nil || !isLowerSHA256Hex(fingerprint) {
		t.Fatalf("empty Safeheron provider account key must remain fingerprintable: %q, %v", fingerprint, err)
	}
	accounts = refreshed.Accounts()
	accounts[0].ProviderAccountKey = " vault-main "
	if _, err := buildAccountRegistrySnapshot(accounts, refreshed.AssetPolicies(), time.Now().UTC()); err == nil {
		t.Fatal("registry must reject a non-empty Safeheron provider account key with surrounding whitespace")
	}
	if _, err := safeheronWebhookEligibilityConfigurationFingerprint(&AccountRegistrySnapshot{
		accountsByID: map[int64]CompanyFundAccount{accounts[0].ID: accounts[0]},
	}); err == nil {
		t.Fatal("non-empty Safeheron provider account key with surrounding whitespace must fail closed")
	}

	accounts = refreshed.Accounts()
	accounts[0].NormalizedAddress = "0xchanged"
	changed, err := buildAccountRegistrySnapshot(accounts, refreshed.AssetPolicies(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	changedFingerprint, err := safeheronWebhookEligibilityConfigurationFingerprint(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedFingerprint == before {
		t.Fatal("Safeheron address change must change eligibility configuration fingerprint")
	}

	changedPolicies := refreshed.AssetPolicies()
	changedPolicies[0].Asset.ProviderAssetKey = "ANOTHER_COIN"
	policyOnlyChange, err := buildAccountRegistrySnapshot(refreshed.Accounts(), changedPolicies, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	policyFingerprint, err := safeheronWebhookEligibilityConfigurationFingerprint(policyOnlyChange)
	if err != nil {
		t.Fatal(err)
	}
	if policyFingerprint != before {
		t.Fatalf("asset policy change must not alter account-ownership fingerprint: %s != %s", policyFingerprint, before)
	}
}

func testSafeheronWebhookEligibilityPayload(t *testing.T, eventType string, snapshot safeheron.TransactionSnapshot) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		EventType   string                        `json:"eventType"`
		EventDetail safeheron.TransactionSnapshot `json:"eventDetail"`
	}{EventType: eventType, EventDetail: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
