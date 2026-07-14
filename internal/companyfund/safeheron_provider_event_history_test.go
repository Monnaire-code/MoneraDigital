package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"monera-digital/internal/safeheron"
)

func TestSafeheronProviderEventNormalizer_NormalizesOwnedHistorySnapshotWithExplicitAccountContext(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-history-adapter"
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset, FeeAsset: input.FeeAsset,
	}}
	accountResolver := &safeheronHistoryAccountContextResolverStub{account: SafeheronHistoryAccountContext{ProviderAccountKey: "safe-vault-main"}}
	normalizer := newSafeheronProviderEventNormalizerWithHistoryForTest(t, resolver, input.Registry, accountResolver)
	lease := testSafeheronHistoryProviderEventLease("safe-vault-main")
	payload := testSafeheronHistorySnapshotPayload(t, input.Snapshot)

	result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, payload)
	if err != nil {
		t.Fatalf("NormalizeProviderEvent() error = %v", err)
	}
	if result.Ignored || len(result.Facts) != 1 || len(result.Movements) != 1 || len(result.FactBindings) != 1 {
		t.Fatalf("history normalization result = %#v", result)
	}
	movement := result.Movements[0]
	if movement.ProviderAccountKey != "safe-vault-main" || movement.FirstSeenSource != TransactionSeenSourceReconciliation ||
		movement.Provider.Metadata.Source != ProviderSourceReconciliation || movement.RawSnapshotDigest != lease.SourcePayloadDigest {
		t.Fatalf("history movement provenance = %#v", movement)
	}
	if result.Facts[0].Input.ProviderAccountKey != "safe-vault-main" || result.Facts[0].Input.SourceProviderEventID != lease.ID ||
		result.Facts[0].Input.SourcePayloadDigest != lease.SourcePayloadDigest {
		t.Fatalf("history fact provenance = %#v", result.Facts[0].Input)
	}
	if accountResolver.providerAccountKey != "safe-vault-main" || len(resolver.snapshots) != 1 || resolver.snapshots[0].TxKey != input.Snapshot.TxKey {
		t.Fatalf("history context resolver input=%q mapping input=%#v", accountResolver.providerAccountKey, resolver.snapshots)
	}
}

func TestSafeheronProviderEventNormalizer_HistoryUnmatchedAddressIsIgnored(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.SourceAddress = "0xExternalSender"
	input.Snapshot.DestinationAddress = "0xExternalRecipient"
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset, FeeAsset: input.FeeAsset,
	}}
	normalizer := newSafeheronProviderEventNormalizerWithHistoryForTest(t, resolver, input.Registry, &safeheronHistoryAccountContextResolverStub{account: SafeheronHistoryAccountContext{ProviderAccountKey: "safe-vault-main"}})

	result, err := normalizer.NormalizeProviderEvent(context.Background(), testSafeheronHistoryProviderEventLease("safe-vault-main"), testSafeheronHistorySnapshotPayload(t, input.Snapshot))
	if err != nil || !result.Ignored || len(result.Facts) != 0 || len(result.Movements) != 0 {
		t.Fatalf("unmatched history result = %#v, %v", result, err)
	}
}

func TestSafeheronProviderEventNormalizer_HistoryRejectsInvalidOwnedContextAndPayload(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset,
	}}
	accountResolver := &safeheronHistoryAccountContextResolverStub{account: SafeheronHistoryAccountContext{ProviderAccountKey: "safe-vault-main"}}
	normalizer := newSafeheronProviderEventNormalizerWithHistoryForTest(t, resolver, input.Registry, accountResolver)

	for _, testCase := range []struct {
		name    string
		lease   ProviderEventLease
		payload []byte
	}{
		{"bad raw snapshot", testSafeheronHistoryProviderEventLease("safe-vault-main"), []byte(`{"txKey":`)},
		{"webhook envelope is not history snapshot", testSafeheronHistoryProviderEventLease("safe-vault-main"), testSafeheronTransactionStatusPayload(t, input.Snapshot)},
		{"mismatched configured account", testSafeheronHistoryProviderEventLease("safe-vault-other"), testSafeheronHistorySnapshotPayload(t, input.Snapshot)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := normalizer.NormalizeProviderEvent(context.Background(), testCase.lease, testCase.payload)
			if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored || len(result.Movements) != 0 {
				t.Fatalf("history NormalizeProviderEvent() = %#v, %v; want permanent failure", result, err)
			}
		})
	}

	accountResolver.err = errors.New("account mapping missing")
	result, err := normalizer.NormalizeProviderEvent(context.Background(), testSafeheronHistoryProviderEventLease("safe-vault-main"), testSafeheronHistorySnapshotPayload(t, input.Snapshot))
	if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored {
		t.Fatalf("history account resolver failure = %#v, %v", result, err)
	}
}

func newSafeheronProviderEventNormalizerWithHistoryForTest(
	t *testing.T,
	resolver SafeheronTransactionMappingResolver,
	snapshot *AccountRegistrySnapshot,
	historyAccounts SafeheronHistoryAccountContextResolver,
) *SafeheronProviderEventNormalizer {
	t.Helper()
	normalizer, err := NewSafeheronProviderEventNormalizer(SafeheronProviderEventNormalizerConfig{
		MappingResolver: resolver, RegistrySnapshots: safeheronRegistrySnapshotProviderStub{snapshot: snapshot}, HistoryAccountResolver: historyAccounts,
	})
	if err != nil {
		t.Fatalf("NewSafeheronProviderEventNormalizer() error = %v", err)
	}
	return normalizer
}

func testSafeheronHistoryProviderEventLease(providerAccountKey string) ProviderEventLease {
	return ProviderEventLease{
		ID: 72, Channel: ChannelSafeheron, ProviderEventID: "safeheron-history:v1:" + strings.Repeat("b", 64),
		EventType: SafeheronTransactionHistorySnapshotEventType, ProviderAccountKey: providerAccountKey,
		SourceKind: ProviderEventSourceOwnedEncryptedPayload, SourcePayloadDigest: strings.Repeat("a", 64),
	}
}

func testSafeheronHistorySnapshotPayload(t *testing.T, snapshot safeheron.TransactionSnapshot) []byte {
	t.Helper()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type safeheronHistoryAccountContextResolverStub struct {
	account            SafeheronHistoryAccountContext
	err                error
	providerAccountKey string
}

func (stub *safeheronHistoryAccountContextResolverStub) ResolveSafeheronHistoryAccount(_ context.Context, providerAccountKey string) (SafeheronHistoryAccountContext, error) {
	stub.providerAccountKey = providerAccountKey
	return stub.account, stub.err
}
