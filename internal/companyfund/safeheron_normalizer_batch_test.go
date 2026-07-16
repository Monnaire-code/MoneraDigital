package companyfund

import (
	"strings"
	"testing"

	"monera-digital/internal/safeheron"
)

func TestNormalizeSafeheronTransaction_BatchOrderIsStableAndFeeIsAttachedToOneDeterministicPrincipal(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-batch"
	input.Snapshot.TxAmount = "3"
	input.Snapshot.TxAmountToUSD = "300"
	input.Snapshot.DestinationAddress = ""
	input.Snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{
		{Address: "0xExternalB", Amount: "2"},
		{Address: "0xExternalA", Amount: "1"},
	}
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"
	input.Snapshot.GasFee = testSafeheronGasFees()

	first, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction(first): %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("batch movement count = %d, want two principal rows", len(first))
	}
	input.Snapshot.DestinationAddressList[0], input.Snapshot.DestinationAddressList[1] = input.Snapshot.DestinationAddressList[1], input.Snapshot.DestinationAddressList[0]
	second, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction(reordered): %v", err)
	}
	if got, want := testSafeheronMovementKeys(first), testSafeheronMovementKeys(second); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("batch reorder changed movement keys: %v != %v", got, want)
	}

	principalCount, feeDisplayCount := 0, 0
	feeMovementCount := 0
	feeMovementKey := ""
	for _, movement := range first {
		switch movement.Movement.MovementKind {
		case MovementKindPrincipal:
			principalCount++
			if movement.Movement.TransferMode != TransferModeBatch || movement.Movement.ProviderReportedUSD != nil {
				t.Fatalf("batch principal relation = %#v", movement)
			}
			if movement.UpsertInput.ProviderDisplay.Fee.Amount != nil {
				feeDisplayCount++
				feeMovementKey = movement.Movement.Identity.Key
				if movement.UpsertInput.ProviderDisplay.To.AddressOrAccount == nil || *movement.UpsertInput.ProviderDisplay.To.AddressOrAccount != "0xExternalA" {
					t.Fatalf("batch fee must attach to deterministic primary principal: %#v", movement)
				}
			}
		case MovementKindFee:
			feeMovementCount++
		}
	}
	if principalCount != 2 || feeDisplayCount != 1 || feeMovementCount != 0 || feeMovementKey == "" {
		t.Fatalf("batch fee placement principal=%d feeDisplay=%d feeMovements=%d", principalCount, feeDisplayCount, feeMovementCount)
	}
	for _, movement := range second {
		if movement.UpsertInput.ProviderDisplay.Fee.Amount != nil && movement.Movement.Identity.Key != feeMovementKey {
			t.Fatalf("batch reorder changed fee-bearing principal: %q != %q", movement.Movement.Identity.Key, feeMovementKey)
		}
	}
}

func TestNormalizeSafeheronTransaction_UnknownAssetAndInvalidMappingAreVisible(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-inbound-zero"
	input.Snapshot.SourceAddress = "0xExternal"
	input.Snapshot.DestinationAddress = "0xTo"
	input.Snapshot.CoinKey = "DAI_ERC20"
	input.Snapshot.TxAmount = "0"
	input.PrincipalAsset = SafeheronAssetMapping{
		CoinKey: "DAI_ERC20",
		Asset: AssetIdentity{
			Currency: "DAI", ChainCode: "ETHEREUM", ProviderAssetKey: "DAI_ERC20", ContractAddress: "0x00000000000000000000000000000000000000dA",
		},
	}
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""

	movements, err := NormalizeSafeheronTransaction(input)
	if err != nil || len(movements) != 1 {
		t.Fatalf("unknown asset result = %#v, %v", movements, err)
	}
	unknown := movements[0]
	if unknown.Movement.Direction != DirectionInflow || unknown.UpsertInput.AutomaticRisk.IsUnrecognizedAsset == nil ||
		*unknown.UpsertInput.AutomaticRisk.IsUnrecognizedAsset {
		t.Fatalf("catalog-mapped asset must stay recognized without a finance policy = %#v", unknown.UpsertInput.AutomaticRisk)
	}
	testSafeheronRequireRiskFlags(t, unknown.Risk.Flags, RiskFlagZeroAmount)
	for _, flag := range unknown.Risk.Flags {
		if flag == RiskFlagUnrecognizedAsset {
			t.Fatalf("unrecognized asset is display metadata, not an automatic risk flag: %#v", unknown.Risk)
		}
	}

	invalid := input
	invalid.NetworkFamily = ""
	if _, err := NormalizeSafeheronTransaction(invalid); err == nil {
		t.Fatal("missing explicit network family must fail visibly")
	}
	invalid = input
	invalid.PrincipalAsset.CoinKey = "WRONG_COIN"
	if _, err := NormalizeSafeheronTransaction(invalid); err == nil {
		t.Fatal("mismatched explicit asset mapping must fail visibly")
	}
	invalid = input
	invalid.Snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{{Address: "0xTo", Amount: "not-a-decimal"}}
	if _, err := NormalizeSafeheronTransaction(invalid); err == nil {
		t.Fatal("malformed batch amount must fail visibly")
	}
}

func TestNormalizeSafeheronTransaction_DuplicateBatchTupleKeepsRiskOnStableOccurrence(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-duplicate-batch"
	input.Snapshot.DestinationAddress = ""
	input.Snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{
		{Address: "0xExternal", Amount: "1", IsDestinationPhishing: true},
		{Address: "0xExternal", Amount: "1", IsDestinationPhishing: false},
	}
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""

	first, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction(first): %v", err)
	}
	input.Snapshot.DestinationAddressList[0], input.Snapshot.DestinationAddressList[1] = input.Snapshot.DestinationAddressList[1], input.Snapshot.DestinationAddressList[0]
	second, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction(reordered): %v", err)
	}
	firstKeys := testSafeheronRiskOccurrenceKeys(t, first)
	secondKeys := testSafeheronRiskOccurrenceKeys(t, second)
	if firstKeys[false] != secondKeys[false] || firstKeys[true] != secondKeys[true] {
		t.Fatalf("duplicate batch tuple changed risk-to-occurrence binding: %#v != %#v", firstKeys, secondKeys)
	}
}

func testSafeheronRiskOccurrenceKeys(t *testing.T, movements []SafeheronNormalizedMovement) map[bool]string {
	t.Helper()
	keys := make(map[bool]string, 2)
	for _, movement := range movements {
		if movement.Movement.MovementKind != MovementKindPrincipal {
			continue
		}
		phishing := movement.UpsertInput.AutomaticRisk.IsDestinationPhishing != nil && *movement.UpsertInput.AutomaticRisk.IsDestinationPhishing
		keys[phishing] = movement.Movement.Identity.Key
	}
	if len(keys) != 2 || keys[false] == "" || keys[true] == "" || keys[false] == keys[true] {
		t.Fatalf("risk occurrence keys = %#v", keys)
	}
	return keys
}
