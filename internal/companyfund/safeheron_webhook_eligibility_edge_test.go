package companyfund

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestSafeheronWebhookEligibility_PureValidationAndRegistryMappingBoundaries(t *testing.T) {
	digest := strings.Repeat("a", 64)
	validInput := SafeheronWebhookEligibilityInput{
		SafeheronWebhookEventID: 1,
		EventType:               safeheronTransactionStatusChangedEventType,
		PayloadDigest:           digest,
	}
	if err := validInput.validate(); err != nil {
		t.Fatal(err)
	}
	for _, input := range []SafeheronWebhookEligibilityInput{
		{},
		{SafeheronWebhookEventID: 1, EventType: " ", PayloadDigest: digest},
		{SafeheronWebhookEventID: 1, EventType: " EVENT ", PayloadDigest: digest},
		{SafeheronWebhookEventID: 1, EventType: "EVENT", PayloadDigest: "not-a-digest"},
	} {
		if err := input.validate(); err == nil {
			t.Fatalf("eligibility input %#v unexpectedly validated", input)
		}
	}
	for _, evaluation := range []SafeheronWebhookCandidateEvaluation{
		{Candidate: true, ExclusionReason: SafeheronWebhookExclusionInvalidPayload},
		{ExclusionReason: SafeheronWebhookExclusionNoConfiguredAddress},
		{ExclusionReason: SafeheronWebhookExclusionInvalidPayload, ConfigurationFingerprint: digest},
		{ExclusionReason: SafeheronWebhookExclusionReason("UNKNOWN")},
	} {
		if err := evaluation.validate(); err == nil {
			t.Fatalf("evaluation %#v unexpectedly validated", evaluation)
		}
	}
	for _, marker := range []SafeheronWebhookRawEventExclusionInput{
		{},
		{SafeheronWebhookEventID: 1, PayloadDigest: digest, Reason: SafeheronWebhookExclusionNoConfiguredAddress},
		{SafeheronWebhookEventID: 1, PayloadDigest: digest, Reason: SafeheronWebhookExclusionInvalidPayload, ConfigurationFingerprint: digest},
	} {
		if err := marker.validate(); err == nil {
			t.Fatalf("marker %#v unexpectedly validated", marker)
		}
	}

	store := &safeheronWebhookExclusionStoreStub{}
	if _, err := NewSafeheronWebhookEligibilityService(nil, store); err == nil {
		t.Fatal("nil evaluator must fail construction")
	}
	if _, err := NewSafeheronWebhookEligibilityService(&safeheronWebhookCandidateEvaluatorStub{}, nil); err == nil {
		t.Fatal("nil exclusion store must fail construction")
	}
	service, err := NewSafeheronWebhookEligibilityService(&safeheronWebhookCandidateEvaluatorStub{evaluation: SafeheronWebhookCandidateEvaluation{
		ExclusionReason: SafeheronWebhookExclusionNoConfiguredAddress,
	}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssessAndRecord(context.Background(), validInput); err == nil {
		t.Fatal("configuration-dependent evaluation without fingerprint must fail closed")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.AssessAndRecord(canceled, validInput); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled eligibility context = %v", err)
	}
}

func TestRegistrySafeheronWebhookCandidateEvaluator_PermanentAndConfigurationBranches(t *testing.T) {
	base := testSafeheronNormalizationInput(t)
	evaluator, err := NewRegistrySafeheronWebhookCandidateEvaluator(safeheronRegistrySnapshotProviderStub{snapshot: base.Registry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistrySafeheronWebhookCandidateEvaluator(nil); err == nil {
		t.Fatal("nil registry provider must fail construction")
	}
	if _, err := (*RegistrySafeheronWebhookCandidateEvaluator)(nil).EvaluateSafeheronWebhookCandidate(context.Background(), safeheronTransactionStatusChangedEventType, nil); err == nil {
		t.Fatal("nil evaluator must fail closed")
	}

	for _, testCase := range []struct {
		name      string
		eventType string
		raw       []byte
		reason    SafeheronWebhookExclusionReason
	}{
		{"non transaction", "AML_KYT_ALERT", []byte(`{}`), SafeheronWebhookExclusionNonTransactionStatus},
		{"malformed payload", safeheronTransactionStatusChangedEventType, []byte(`not-json`), SafeheronWebhookExclusionInvalidPayload},
		{"event type mismatch", safeheronTransactionStatusChangedEventType, testSafeheronWebhookEligibilityPayload(t, "AML_KYT_ALERT", base.Snapshot), SafeheronWebhookExclusionEventTypeMismatch},
		{"invalid detail", safeheronTransactionStatusChangedEventType, []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx"}}`), SafeheronWebhookExclusionInvalidPayload},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			decision, err := evaluator.EvaluateSafeheronWebhookCandidate(context.Background(), testCase.eventType, testCase.raw)
			if err != nil || decision.Candidate || decision.ExclusionReason != testCase.reason || decision.ConfigurationFingerprint != "" {
				t.Fatalf("decision = %#v, %v", decision, err)
			}
		})
	}
	missingRegistry := &RegistrySafeheronWebhookCandidateEvaluator{registries: safeheronRegistrySnapshotProviderStub{}}
	if _, err := missingRegistry.EvaluateSafeheronWebhookCandidate(context.Background(), safeheronTransactionStatusChangedEventType, testSafeheronWebhookEligibilityPayload(t, safeheronTransactionStatusChangedEventType, base.Snapshot)); err == nil {
		t.Fatal("missing current registry snapshot must fail closed")
	}
}

func TestSafeheronWebhookTransactionMappingAndHistoryRegistryEdges(t *testing.T) {
	base := testSafeheronNormalizationInput(t)
	withoutFee := base.Snapshot
	withoutFee.FeeCoinKey = ""
	if mapping, err := safeheronWebhookTransactionMapping(base.Registry, withoutFee); err != nil || mapping.FeeAsset != nil || mapping.NetworkFamily != "EVM" {
		t.Fatalf("principal-only mapping = %#v, %v", mapping, err)
	}
	missingPrincipal := withoutFee
	missingPrincipal.CoinKey = "UNKNOWN"
	if _, err := safeheronWebhookTransactionMapping(base.Registry, missingPrincipal); err == nil {
		t.Fatal("unmapped principal must fail")
	}
	missingFee := base.Snapshot
	missingFee.FeeCoinKey = "UNKNOWN"
	if _, err := safeheronWebhookTransactionMapping(base.Registry, missingFee); err == nil {
		t.Fatal("unmapped fee must fail")
	}

	conflictingRegistry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, NormalizedAddress: "TAbC", NetworkFamily: "TRON", Enabled: true},
	}, []AccountAssetPolicy{
		{ID: 11, AccountID: 1, Asset: AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20"}, Enabled: true},
		{ID: 12, AccountID: 2, Asset: AssetIdentity{Currency: "TRX", ChainCode: "TRON", ProviderAssetKey: "TRON_TRX"}, Enabled: true},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := safeheronWebhookTransactionMapping(conflictingRegistry, safeheron.TransactionSnapshot{CoinKey: "USDT_ERC20", FeeCoinKey: "TRON_TRX"}); err == nil {
		t.Fatal("cross-network fee mapping must fail")
	}

	addresses := safeheronWebhookTransactionAddresses(safeheron.TransactionSnapshot{
		SourceAddress:          " from ",
		SourceAddressList:      []safeheron.TransactionSourceAddress{{Address: " "}, {Address: "list-from"}},
		DestinationAddress:     " to ",
		DestinationAddressList: []safeheron.TransactionDestinationAddress{{Address: "list-to"}},
	})
	if got, want := strings.Join(addresses, ","), "from,list-from,to,list-to"; got != want {
		t.Fatalf("transaction addresses = %q, want %q", got, want)
	}

	history, err := NewRegistrySafeheronHistoryAccountContextResolver(safeheronRegistrySnapshotProviderStub{snapshot: base.Registry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistrySafeheronHistoryAccountContextResolver(nil); err == nil {
		t.Fatal("nil history registry provider must fail construction")
	}
	if contextValue, err := history.ResolveSafeheronHistoryAccount(context.Background(), "vault-from"); err != nil || contextValue.ProviderAccountKey != "vault-from" {
		t.Fatalf("history context = %#v, %v", contextValue, err)
	}
	if _, err := history.ResolveSafeheronHistoryAccount(context.Background(), " missing "); err == nil {
		t.Fatal("non-canonical history account key must fail")
	}
	if _, err := history.ResolveSafeheronHistoryAccount(context.Background(), "not-configured"); err == nil {
		t.Fatal("unconfigured history account key must fail")
	}
}
