package companyfund

import (
	"sort"
	"testing"

	"github.com/shopspring/decimal"
)

func TestBuildMovementIdentity_IsVersionedAndStableAcrossSources(t *testing.T) {
	input := MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: "tx-123",
		MovementKind:     MovementKindPrincipal,
		Asset: AssetIdentity{
			Currency:         "USDT",
			ChainCode:        "ETH",
			ProviderAssetKey: "USDT_ETH",
			ContractAddress:  "0xdac17f958d2ee523a2206206994597c13d831ec7",
		},
		NormalizedFrom: "0xfrom",
		NormalizedTo:   "0xto",
		Amount:         decimal.RequireFromString("1.250000000000000000"),
		Occurrence:     1,
	}

	webhookIdentity, err := BuildMovementIdentity(input)
	if err != nil {
		t.Fatalf("BuildMovementIdentity(webhook): %v", err)
	}
	reconciliationIdentity, err := BuildMovementIdentity(input)
	if err != nil {
		t.Fatalf("BuildMovementIdentity(reconciliation): %v", err)
	}

	if webhookIdentity.Key != reconciliationIdentity.Key {
		t.Fatalf("same normalized movement keys differ: %q != %q", webhookIdentity.Key, reconciliationIdentity.Key)
	}
	if webhookIdentity.AlgorithmVersion != MovementIdentityAlgorithmVersion {
		t.Fatalf("algorithm version = %q, want %q", webhookIdentity.AlgorithmVersion, MovementIdentityAlgorithmVersion)
	}
	if len(webhookIdentity.Digest) != 64 {
		t.Fatalf("digest must be SHA-256 hex, got %q", webhookIdentity.Digest)
	}
}

func TestBuildMovementIdentity_DistinguishesMovementTuple(t *testing.T) {
	base := MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: "tx-123",
		MovementKind:     MovementKindPrincipal,
		Asset:            AssetIdentity{Currency: "USDT", ChainCode: "ETH", ContractAddress: "0x1"},
		NormalizedFrom:   "from-a",
		NormalizedTo:     "to-a",
		Amount:           decimal.RequireFromString("10"),
		Occurrence:       1,
	}
	baseIdentity, err := BuildMovementIdentity(base)
	if err != nil {
		t.Fatal(err)
	}

	variants := []MovementIdentityInput{
		func() MovementIdentityInput { value := base; value.Channel = ChannelAirwallex; return value }(),
		func() MovementIdentityInput { value := base; value.ProviderParentID = "tx-456"; return value }(),
		func() MovementIdentityInput { value := base; value.MovementKind = MovementKindFee; return value }(),
		func() MovementIdentityInput { value := base; value.Asset.ContractAddress = "0x2"; return value }(),
		func() MovementIdentityInput { value := base; value.NormalizedTo = "to-b"; return value }(),
		func() MovementIdentityInput {
			value := base
			value.Amount = decimal.RequireFromString("11")
			return value
		}(),
	}

	for _, variant := range variants {
		identity, err := BuildMovementIdentity(variant)
		if err != nil {
			t.Fatal(err)
		}
		if identity.Key == baseIdentity.Key {
			t.Fatalf("variant %+v collided with base identity", variant)
		}
	}
}

func TestBuildMovementIdentity_RejectsChainAssetWithoutProviderOrContractIdentity(t *testing.T) {
	_, err := BuildMovementIdentity(MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: "tx-123",
		MovementKind:     MovementKindPrincipal,
		Asset:            AssetIdentity{Currency: "USDT", ChainCode: "ETH"},
		NormalizedFrom:   "from",
		NormalizedTo:     "to",
		Amount:           decimal.RequireFromString("1"),
		Occurrence:       1,
	})
	if err == nil {
		t.Fatal("chain asset with only a ticker must be rejected")
	}

	if _, err := BuildMovementIdentity(MovementIdentityInput{
		Channel:          ChannelAirwallex,
		ProviderParentID: "payment-123",
		MovementKind:     MovementKindPrincipal,
		Asset:            AssetIdentity{Currency: "JPY"},
		NormalizedFrom:   "payer",
		NormalizedTo:     "account",
		Amount:           decimal.RequireFromString("100"),
		Occurrence:       1,
	}); err != nil {
		t.Fatalf("fiat identity without a chain asset key should remain valid: %v", err)
	}
}

func TestAssignBatchMovementIdentities_IsIndependentOfArrayOrder(t *testing.T) {
	items := []MovementIdentityInput{
		batchIdentityInput("to-b", "2"),
		batchIdentityInput("to-a", "1"),
		batchIdentityInput("to-a", "1"),
	}
	reordered := []MovementIdentityInput{items[2], items[0], items[1]}

	first, err := AssignBatchMovementIdentities(items)
	if err != nil {
		t.Fatalf("AssignBatchMovementIdentities(first): %v", err)
	}
	second, err := AssignBatchMovementIdentities(reordered)
	if err != nil {
		t.Fatalf("AssignBatchMovementIdentities(reordered): %v", err)
	}

	firstKeys := movementKeys(first)
	secondKeys := movementKeys(second)
	if len(firstKeys) != len(secondKeys) {
		t.Fatalf("key counts differ: %d != %d", len(firstKeys), len(secondKeys))
	}
	for index := range firstKeys {
		if firstKeys[index] != secondKeys[index] {
			t.Fatalf("reordered batch changed keys: %v != %v", firstKeys, secondKeys)
		}
	}

	occurrences := make(map[int]int)
	for _, identity := range first {
		if identity.Input.NormalizedTo == "to-a" {
			occurrences[identity.Occurrence]++
		}
	}
	if occurrences[1] != 1 || occurrences[2] != 1 {
		t.Fatalf("identical batch outputs need deterministic occurrences 1 and 2, got %#v", occurrences)
	}
}

func batchIdentityInput(destination, amount string) MovementIdentityInput {
	return MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: "batch-1",
		MovementKind:     MovementKindPrincipal,
		Asset:            AssetIdentity{Currency: "USDT", ChainCode: "ETH", ContractAddress: "0x1"},
		NormalizedFrom:   "from",
		NormalizedTo:     destination,
		Amount:           decimal.RequireFromString(amount),
	}
}

func movementKeys(identities []MovementIdentity) []string {
	keys := make([]string, 0, len(identities))
	for _, identity := range identities {
		keys = append(keys, identity.Key)
	}
	sort.Strings(keys)
	return keys
}
