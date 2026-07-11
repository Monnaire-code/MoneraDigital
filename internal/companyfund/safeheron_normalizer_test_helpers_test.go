package companyfund

import (
	"sort"
	"strings"
	"testing"
	"time"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

func testSafeheronNormalizationInput(t *testing.T) SafeheronNormalizationInput {
	t.Helper()
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "vault-from", NormalizedAddress: "0xfrom", NetworkFamily: "EVM", CompanyEntity: "Monera Singapore", FundAccountName: "Treasury", SubAccountName: "Main", AccountType: "VAULT", AccountName: "Treasury EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, ProviderAccountKey: "vault-to", NormalizedAddress: "0xto", NetworkFamily: "EVM", CompanyEntity: "Monera Hong Kong", FundAccountName: "Operations", SubAccountName: "Settlement", AccountType: "VAULT", AccountName: "Operations EVM", Enabled: true},
	}, []AccountAssetPolicy{
		testSafeheronAssetPolicy(11, 1, testSafeheronPrincipalAsset(), "2"),
		testSafeheronAssetPolicy(12, 1, testSafeheronFeeAsset(), "0.001"),
		testSafeheronAssetPolicy(13, 2, testSafeheronPrincipalAsset(), "2"),
		testSafeheronAssetPolicy(14, 2, testSafeheronFeeAsset(), "0.001"),
	}, time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("buildAccountRegistrySnapshot: %v", err)
	}
	eventID := int64(71)
	return SafeheronNormalizationInput{
		Snapshot: safeheron.TransactionSnapshot{
			TxKey: "safeheron-default", TxHash: "0xabc", CoinKey: "USDT_ERC20", TxAmount: "1",
			SourceAccountKey: "provider-vault-from", SourceAddress: "0xFrom", DestinationAddress: "0xExternal",
			TransactionStatus: "COMPLETED", CreateTime: 1722470400000, CompletedTime: 1722470460000,
		},
		NetworkFamily:         "EVM",
		PrincipalAsset:        SafeheronAssetMapping{CoinKey: "USDT_ERC20", Asset: testSafeheronPrincipalAsset()},
		FeeAsset:              testSafeheronFeeMapping(),
		Registry:              registry,
		ProviderEventID:       strings.Repeat("b", 64),
		LatestProviderEventID: &eventID,
		SourcePayloadDigest:   strings.Repeat("a", 64),
		FirstSeenSource:       TransactionSeenSourceWebhook,
	}
}

func testSafeheronPrincipalAsset() AssetIdentity {
	return AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7"}
}

func testSafeheronFeeAsset() AssetIdentity {
	return AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM", ProviderAssetKey: "ETHEREUM_ETH"}
}

func testSafeheronFeeMapping() *SafeheronAssetMapping {
	return &SafeheronAssetMapping{CoinKey: "ETHEREUM_ETH", Asset: testSafeheronFeeAsset()}
}

func testSafeheronAssetPolicy(id, accountID int64, asset AssetIdentity, threshold string) AccountAssetPolicy {
	value := decimal.RequireFromString(threshold)
	return AccountAssetPolicy{ID: id, AccountID: accountID, Asset: asset, Dust: DustPolicy{ID: id, Enabled: true, Threshold: &value}, Enabled: true}
}

func testSafeheronGasFees() []safeheron.TransactionGasFee {
	return []safeheron.TransactionGasFee{{Symbol: "ETH", Amount: "0.00021"}}
}

func testSafeheronHighAML() []safeheron.TransactionAMLRecord {
	return []safeheron.TransactionAMLRecord{{Provider: "CHAINALYSIS", RiskLevel: "HIGH", Status: "MATCHED"}}
}

func testSafeheronMovementByKind(t *testing.T, movements []SafeheronNormalizedMovement, kind MovementKind) SafeheronNormalizedMovement {
	t.Helper()
	for _, movement := range movements {
		if movement.Movement.MovementKind == kind {
			return movement
		}
	}
	t.Fatalf("movement kind %s not found in %#v", kind, movements)
	return SafeheronNormalizedMovement{}
}

func testSafeheronMovementKeys(movements []SafeheronNormalizedMovement) []string {
	keys := make([]string, 0, len(movements))
	for _, movement := range movements {
		keys = append(keys, movement.Movement.Identity.Key)
	}
	sort.Strings(keys)
	return keys
}

func testSafeheronRequireRiskFlags(t *testing.T, actual []RiskFlag, expected ...RiskFlag) {
	t.Helper()
	found := make(map[RiskFlag]bool, len(actual))
	for _, flag := range actual {
		found[flag] = true
	}
	for _, flag := range expected {
		if !found[flag] {
			t.Fatalf("risk flags %v do not include %q", actual, flag)
		}
	}
}
