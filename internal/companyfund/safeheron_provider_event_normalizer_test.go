package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"monera-digital/internal/safeheron"
)

func TestSafeheronProviderEventNormalizer_MapsVerifiedStatusPayloadThroughExplicitResolver(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-webhook-adapter"
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset, FeeAsset: input.FeeAsset,
	}}
	normalizer := newSafeheronProviderEventNormalizerForTest(t, resolver, input.Registry)
	lease := testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED")

	result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, testSafeheronTransactionStatusPayload(t, input.Snapshot))
	if err != nil {
		t.Fatalf("NormalizeProviderEvent() error = %v", err)
	}
	if result.Ignored || len(result.Facts) != 1 || len(result.Movements) != 1 || len(result.FactBindings) != 1 {
		t.Fatalf("normalization result = %#v", result)
	}
	movement := result.Movements[0]
	if movement.Channel != ChannelSafeheron || movement.RawSnapshotDigest != lease.SourcePayloadDigest ||
		movement.LatestProviderEventID == nil || *movement.LatestProviderEventID != lease.ID ||
		movement.ProviderEventID != lease.ProviderEventID ||
		movement.Asset.ChainCode != "ETHEREUM" || movement.FromCompanyFundAccountID == nil || *movement.FromCompanyFundAccountID != 1 {
		t.Fatalf("explicitly mapped movement = %#v", movement)
	}
	if result.Facts[0].Input.SourceProviderEventID != lease.ID || result.Facts[0].Input.SourcePayloadDigest != lease.SourcePayloadDigest {
		t.Fatalf("provider fact provenance = %#v", result.Facts[0].Input)
	}
	if len(resolver.snapshots) != 1 || resolver.snapshots[0].CoinKey != "USDT_ERC20" {
		t.Fatalf("resolver input = %#v", resolver.snapshots)
	}
	if err := result.validate(); err != nil {
		t.Fatalf("worker contract result must validate: %v", err)
	}
}

func TestSafeheronProviderEventNormalizer_RoutingScopeAllowsOnlyAuthorizedBatchOccurrence(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-routing-scoped-batch"
	input.Snapshot.SourceAddress = "0xExternalSender"
	input.Snapshot.DestinationAddress = ""
	input.Snapshot.TxAmount = "3.75"
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"
	input.Snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{
		{Address: "0xCompanyWallet", Amount: "1.25"},
		{Address: "0xSecondCompanyWallet", Amount: "2.50"},
	}
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, NormalizedAddress: "0xcompanywallet", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, NormalizedAddress: "0xsecondcompanywallet", NetworkFamily: "EVM", Enabled: true},
	}, nil, input.Registry.LoadedAt())
	if err != nil {
		t.Fatal(err)
	}
	principals, err := EnumerateSafeheronPrincipalOccurrences(input.Snapshot, input.NetworkFamily)
	if err != nil || len(principals) != 2 {
		t.Fatalf("principal occurrences = %#v, %v", principals, err)
	}
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset,
	}}
	normalizer := newSafeheronProviderEventNormalizerForTest(t, resolver, registry)
	lease := testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED")
	lease.AuthorizedSafeheronOccurrenceKey = principals[0].Occurrence.Key
	input.Registry = registry
	input.AuthorizedOccurrenceKey = lease.AuthorizedSafeheronOccurrenceKey
	if _, directErr := NormalizeSafeheronProviderEvent(input); directErr != nil {
		t.Fatalf("direct scoped normalization error = %v", directErr)
	}

	result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, testSafeheronTransactionStatusPayload(t, input.Snapshot))
	if err != nil {
		t.Fatalf("NormalizeProviderEvent() error = %v", err)
	}
	if len(result.Movements) != 1 || result.Movements[0].ProviderOccurrenceKey != principals[0].Occurrence.Key {
		t.Fatalf("scoped movements = %#v", result.Movements)
	}
	if result.Movements[0].ProviderDisplay.Fee.Amount == nil {
		t.Fatal("the deterministic first batch occurrence must retain the transaction fee")
	}
	lease.AuthorizedSafeheronOccurrenceKey = principals[1].Occurrence.Key
	second, err := normalizer.NormalizeProviderEvent(context.Background(), lease, testSafeheronTransactionStatusPayload(t, input.Snapshot))
	if err != nil {
		t.Fatalf("second scoped normalization error = %v", err)
	}
	if len(second.Movements) != 1 || second.Movements[0].ProviderDisplay.Fee.Amount != nil {
		t.Fatalf("non-primary scoped batch occurrence duplicated transaction fee: %#v", second.Movements)
	}
}

func TestSafeheronProviderEventNormalizer_IgnoresOnlyWellFormedNonReportableEventsAndUnmatchedTransactions(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset, FeeAsset: input.FeeAsset,
	}}
	normalizer := newSafeheronProviderEventNormalizerForTest(t, resolver, input.Registry)

	amlLease := testSafeheronProviderEventLease("AML_KYT_ALERT")
	amlPayload := []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-aml","amlList":[{"riskLevel":"HIGH"}]}}`)
	ignored, err := normalizer.NormalizeProviderEvent(context.Background(), amlLease, amlPayload)
	if err != nil || !ignored.Ignored || len(ignored.Movements) != 0 || len(ignored.Facts) != 0 {
		t.Fatalf("AML event result = %#v, %v", ignored, err)
	}

	createdLease := testSafeheronProviderEventLease("TRANSACTION_CREATED")
	createdPayload := []byte(`{"eventType":"TRANSACTION_CREATED","eventDetail":{"txKey":"tx-created"}}`)
	ignored, err = normalizer.NormalizeProviderEvent(context.Background(), createdLease, createdPayload)
	if err != nil || !ignored.Ignored || len(ignored.Movements) != 0 || len(ignored.Facts) != 0 {
		t.Fatalf("transaction-created result = %#v, %v", ignored, err)
	}

	unmatched := input.Snapshot
	unmatched.SourceAddress = "0xExternalSender"
	unmatched.DestinationAddress = "0xExternalRecipient"
	unmatched.TxFee = ""
	unmatched.FeeCoinKey = ""
	lease := testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED")
	ignored, err = normalizer.NormalizeProviderEvent(context.Background(), lease, testSafeheronTransactionStatusPayload(t, unmatched))
	if err != nil || !ignored.Ignored {
		t.Fatalf("unmatched transaction result = %#v, %v", ignored, err)
	}
}

func TestSafeheronProviderEventNormalizer_MakesMalformedOrUnmappedInputPermanent(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	resolver := &safeheronTransactionMappingResolverStub{mapping: SafeheronTransactionMapping{
		NetworkFamily: input.NetworkFamily, PrincipalAsset: input.PrincipalAsset,
	}}
	normalizer := newSafeheronProviderEventNormalizerForTest(t, resolver, input.Registry)

	testCases := []struct {
		name    string
		lease   ProviderEventLease
		payload []byte
	}{
		{"bad JSON", testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED"), []byte(`{"eventType":`)},
		{"mismatched event type", testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED"), []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{}}`)},
		{"unknown event type", testSafeheronProviderEventLease("TRANSACTION_REVISED"), []byte(`{"eventType":"TRANSACTION_REVISED","eventDetail":{"txKey":"tx-1"}}`)},
		{"malformed transaction-created detail", testSafeheronProviderEventLease("TRANSACTION_CREATED"), []byte(`{"eventType":"TRANSACTION_CREATED","eventDetail":{}}`)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := normalizer.NormalizeProviderEvent(context.Background(), testCase.lease, testCase.payload)
			if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored || len(result.Movements) != 0 {
				t.Fatalf("NormalizeProviderEvent() = %#v, %v; want permanent failure", result, err)
			}
		})
	}

	resolver.err = errors.New("mapping unavailable")
	result, err := normalizer.NormalizeProviderEvent(context.Background(), testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED"), testSafeheronTransactionStatusPayload(t, input.Snapshot))
	if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored {
		t.Fatalf("resolver failure = %#v, %v", result, err)
	}

	resolver.err = nil
	resolver.mapping.PrincipalAsset.CoinKey = "UNMAPPED_COIN"
	result, err = normalizer.NormalizeProviderEvent(context.Background(), testSafeheronProviderEventLease("TRANSACTION_STATUS_CHANGED"), testSafeheronTransactionStatusPayload(t, input.Snapshot))
	if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored {
		t.Fatalf("unmapped asset failure = %#v, %v", result, err)
	}

	result, err = normalizer.NormalizeProviderEvent(context.Background(), testSafeheronProviderEventLease("AML_KYT_ALERT"), []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"amlList":[]}}`))
	if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored {
		t.Fatalf("malformed AML failure = %#v, %v", result, err)
	}
}

type safeheronTransactionMappingResolverStub struct {
	mapping   SafeheronTransactionMapping
	err       error
	snapshots []safeheron.TransactionSnapshot
}

func (s *safeheronTransactionMappingResolverStub) ResolveSafeheronTransactionMapping(_ context.Context, snapshot safeheron.TransactionSnapshot) (SafeheronTransactionMapping, error) {
	s.snapshots = append(s.snapshots, snapshot)
	return s.mapping, s.err
}

type safeheronRegistrySnapshotProviderStub struct{ snapshot *AccountRegistrySnapshot }

func (s safeheronRegistrySnapshotProviderStub) Snapshot() *AccountRegistrySnapshot { return s.snapshot }

func newSafeheronProviderEventNormalizerForTest(t *testing.T, resolver SafeheronTransactionMappingResolver, snapshot *AccountRegistrySnapshot) *SafeheronProviderEventNormalizer {
	t.Helper()
	normalizer, err := NewSafeheronProviderEventNormalizer(SafeheronProviderEventNormalizerConfig{
		MappingResolver:   resolver,
		RegistrySnapshots: safeheronRegistrySnapshotProviderStub{snapshot: snapshot},
	})
	if err != nil {
		t.Fatalf("NewSafeheronProviderEventNormalizer() error = %v", err)
	}
	return normalizer
}

func testSafeheronProviderEventLease(eventType string) ProviderEventLease {
	rawID := 91
	return ProviderEventLease{
		ID: 71, Channel: ChannelSafeheron, ProviderEventID: strings.Repeat("b", 64), EventType: eventType,
		SourceKind: ProviderEventSourceExistingSafeheronWebhookRef, SafeheronWebhookEventID: &rawID,
		SourcePayloadDigest: strings.Repeat("a", 64),
	}
}

func testSafeheronTransactionStatusPayload(t *testing.T, snapshot safeheron.TransactionSnapshot) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		EventType   string                        `json:"eventType"`
		EventDetail safeheron.TransactionSnapshot `json:"eventDetail"`
	}{EventType: "TRANSACTION_STATUS_CHANGED", EventDetail: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
