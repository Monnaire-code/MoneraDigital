package companyfund

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestBuildSafeheronMovementIdentity_UsesExactRawCoinKeyAndStableIndex(t *testing.T) {
	input := SafeheronOccurrenceInput{
		ProviderTransactionKey: "tx-1",
		MovementKind:           MovementKindPrincipal,
		RawCoinKey:             "ETHEREUM_USDT",
		NormalizedSource:       "0xfrom",
		NormalizedDestination:  "0xto",
		Amount:                 decimal.RequireFromString("1.2500"),
		TransferMode:           TransferModeBatch,
		MovementIndex:          3,
	}
	identity, err := BuildSafeheronMovementIdentity(input)
	if err != nil {
		t.Fatal(err)
	}
	if identity.AlgorithmVersion != SafeheronMovementIdentityAlgorithmVersion || identity.Key == "" {
		t.Fatalf("identity = %#v", identity)
	}

	caseChanged := input
	caseChanged.RawCoinKey = "ethereum_usdt"
	otherCoinKey, err := BuildSafeheronMovementIdentity(caseChanged)
	if err != nil {
		t.Fatal(err)
	}
	if otherCoinKey.Key == identity.Key {
		t.Fatal("exact raw CoinKey case must remain part of Safeheron v2 identity")
	}

	otherIndex := input
	otherIndex.MovementIndex++
	indexed, err := BuildSafeheronMovementIdentity(otherIndex)
	if err != nil {
		t.Fatal(err)
	}
	if indexed.Key == identity.Key {
		t.Fatal("stable movement index must distinguish duplicate batch occurrences")
	}
	invalid := input
	invalid.ProviderTransactionKey = ""
	if _, err := BuildSafeheronMovementIdentity(invalid); err == nil {
		t.Fatal("invalid occurrence input must not produce a v2 movement identity")
	}
}

func TestNormalizeSafeheronTransaction_EmitsV2AndOccurrencePair(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	movements, err := NormalizeSafeheronTransaction(input)
	if err != nil || len(movements) != 1 {
		t.Fatalf("NormalizeSafeheronTransaction() = %#v, %v", movements, err)
	}
	upsert := movements[0].UpsertInput
	if upsert.IdentityAlgorithmVersion != SafeheronMovementIdentityAlgorithmVersion ||
		upsert.ProviderOccurrenceAlgorithmVersion != SafeheronOccurrenceAlgorithmVersion ||
		upsert.ProviderOccurrenceKey == "" {
		t.Fatalf("Safeheron identity pair = %#v", upsert)
	}
	occurrence, err := BuildSafeheronOccurrence(SafeheronOccurrenceInput{
		ProviderTransactionKey: input.Snapshot.TxKey,
		MovementKind:           MovementKindPrincipal,
		RawCoinKey:             input.Snapshot.CoinKey,
		NormalizedSource:       "0xfrom",
		NormalizedDestination:  "0xexternal",
		Amount:                 decimal.RequireFromString("1"),
		TransferMode:           TransferModeSingle,
		MovementIndex:          0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if upsert.ProviderOccurrenceKey != occurrence.Key {
		t.Fatalf("occurrence key = %q, want %q", upsert.ProviderOccurrenceKey, occurrence.Key)
	}
}

func TestNormalizeSafeheronTransaction_AssetRecognitionIsIndependentFromOptionalPolicy(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	registry, err := buildAccountRegistrySnapshot(input.Registry.Accounts(), nil, input.Registry.LoadedAt())
	if err != nil {
		t.Fatal(err)
	}
	input.Registry = registry
	input.Snapshot.TxAmountToUSD = "12.34"

	movements, err := NormalizeSafeheronTransaction(input)
	if err != nil || len(movements) != 1 {
		t.Fatalf("recognized policyless movement = %#v, %v", movements, err)
	}
	risk := movements[0].UpsertInput.AutomaticRisk
	if risk.IsUnrecognizedAsset == nil || *risk.IsUnrecognizedAsset || movements[0].Risk.IsDust || movements[0].Risk.AutomaticExclusion {
		t.Fatalf("catalog-recognized zero-policy movement risk = %#v / %#v", risk, movements[0].Risk)
	}
	if movements[0].Movement.ProviderReportedUSD == nil || !movements[0].Movement.ProviderReportedUSD.Equal(decimal.RequireFromString("12.34")) {
		t.Fatalf("direct movement-scoped provider USD must survive without a finance policy: %#v", movements[0].Movement.ProviderReportedUSD)
	}

	input.PrincipalAsset = SafeheronAssetMapping{CoinKey: input.Snapshot.CoinKey, Unrecognized: true}
	movements, err = NormalizeSafeheronTransaction(input)
	if err != nil || len(movements) != 1 {
		t.Fatalf("fallback policyless movement = %#v, %v", movements, err)
	}
	fallback := movements[0]
	if fallback.Movement.Asset.Currency != input.Snapshot.CoinKey ||
		fallback.Movement.Asset.ProviderAssetKey != input.Snapshot.CoinKey ||
		fallback.Movement.Asset.ChainCode != "" || fallback.Movement.Asset.ContractAddress != "" ||
		fallback.UpsertInput.AutomaticRisk.IsUnrecognizedAsset == nil || !*fallback.UpsertInput.AutomaticRisk.IsUnrecognizedAsset ||
		fallback.Risk.AutomaticExclusion || fallback.Movement.ProviderReportedUSD == nil ||
		!fallback.Movement.ProviderReportedUSD.Equal(decimal.RequireFromString("12.34")) {
		t.Fatalf("fallback recognition = %#v / %#v", fallback.Movement.Asset, fallback.Risk)
	}
}
