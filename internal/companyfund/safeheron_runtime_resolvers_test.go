package companyfund

import (
	"context"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestRegistrySafeheronRuntimeResolvers_UseExactConfiguredAccountAndAssetPolicyKeys(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	provider := safeheronRegistrySnapshotProviderStub{snapshot: input.Registry}
	historyResolver, err := NewRegistrySafeheronHistoryAccountContextResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	contextValue, err := historyResolver.ResolveSafeheronHistoryAccount(context.Background(), "vault-from")
	if err != nil || contextValue.ProviderAccountKey != "vault-from" {
		t.Fatalf("ResolveSafeheronHistoryAccount() = %#v, %v", contextValue, err)
	}
	if _, err := historyResolver.ResolveSafeheronHistoryAccount(context.Background(), " vault-from "); err == nil {
		t.Fatal("history resolver must reject non-exact provider account keys")
	}

	mappingResolver, err := NewRegistrySafeheronTransactionMappingResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := mappingResolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{CoinKey: "USDT_ERC20", FeeCoinKey: "ETHEREUM_ETH"})
	if err != nil || mapping.NetworkFamily != "EVM" || mapping.PrincipalAsset.CoinKey != "USDT_ERC20" ||
		mapping.PrincipalAsset.Asset.ProviderAssetKey != "USDT_ERC20" || mapping.FeeAsset == nil || mapping.FeeAsset.Asset.Currency != "ETH" {
		t.Fatalf("ResolveSafeheronTransactionMapping() = %#v, %v", mapping, err)
	}
	if _, err := mappingResolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{CoinKey: "USDT"}); err == nil {
		t.Fatal("ticker-like currency must not substitute for the configured provider asset key")
	}
}

func TestRegistrySafeheronTransactionMappingResolver_FailsClosedOnAmbiguousCoinKey(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 41, Channel: ChannelSafeheron, ProviderAccountKey: "safe-evm", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
		{ID: 42, Channel: ChannelSafeheron, ProviderAccountKey: "safe-tron", NormalizedAddress: "TAbC", NetworkFamily: "TRON", Enabled: true},
	}, []AccountAssetPolicy{
		{ID: 51, AccountID: 41, Asset: AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "AMBIGUOUS"}, Enabled: true},
		{ID: 52, AccountID: 42, Asset: AssetIdentity{Currency: "USDT", ChainCode: "TRON", ProviderAssetKey: "AMBIGUOUS"}, Enabled: true},
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewRegistrySafeheronTransactionMappingResolver(safeheronRegistrySnapshotProviderStub{snapshot: registry})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{CoinKey: "AMBIGUOUS"}); err == nil {
		t.Fatal("different configured network/asset mappings for one coin key must fail closed")
	}
}
