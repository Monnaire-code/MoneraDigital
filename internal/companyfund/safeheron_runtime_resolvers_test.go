package companyfund

import (
	"context"
	"errors"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

func TestRegistrySafeheronTransactionMappingResolver_TreatsStaleRegistryAsRetriableConfiguration(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot(nil, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{
		{CoinKey: "USDT_BEP20", Symbol: "USDT", BlockChain: "BSC", BlockchainType: "EVM", TokenIdentifier: "0x55d398326f99059ff775485246999027b3197955"},
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil || catalog.Refresh(context.Background()) != nil {
		t.Fatalf("catalog setup: %v", err)
	}
	resolver, err := NewRegistrySafeheronTransactionMappingResolver(
		safeheronRegistrySnapshotProviderStub{snapshot: registry},
		catalog,
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{
		CoinKey: "USDT_BEP20", DestinationAccountKey: "new-safeheron-account", DestinationAddress: "0xc4f60c9b02edabba16c9df0afaccec8acf67381f",
	})
	var configurationError *SafeheronAccountContextConfigurationError
	if !errors.As(err, &configurationError) || !configurationError.Retriable() {
		t.Fatalf("ResolveSafeheronTransactionMapping() error = %v, want retriable account configuration error", err)
	}
}

func TestRegistrySafeheronTransactionMappingResolver_TreatsMissingSnapshotAsRetriableConfiguration(t *testing.T) {
	resolver, err := NewRegistrySafeheronTransactionMappingResolver(safeheronRegistrySnapshotProviderStub{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{CoinKey: "USDT_BEP20"})
	var configurationError *SafeheronAccountContextConfigurationError
	if !errors.As(err, &configurationError) || !configurationError.Retriable() {
		t.Fatalf("ResolveSafeheronTransactionMapping() error = %v, want retriable account configuration error", err)
	}
}

func TestRegistrySafeheronTransactionMappingResolver_SucceedsAfterRegistryRefresh(t *testing.T) {
	var accounts []CompanyFundAccount
	registry := NewAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return accounts, nil, nil
	}), time.Minute)
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{
		{CoinKey: "USDT_BEP20", Symbol: "USDT", BlockChain: "BSC", BlockchainType: "EVM", TokenIdentifier: "0x55d398326f99059ff775485246999027b3197955"},
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil || catalog.Refresh(context.Background()) != nil {
		t.Fatalf("catalog setup: %v", err)
	}
	resolver, err := NewRegistrySafeheronTransactionMappingResolver(registry, catalog)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := safeheron.TransactionSnapshot{
		CoinKey: "USDT_BEP20", DestinationAccountKey: "new-safeheron-account", DestinationAddress: "0xc4f60c9b02edabba16c9df0afaccec8acf67381f",
	}
	if _, err := resolver.ResolveSafeheronTransactionMapping(context.Background(), snapshot); err == nil {
		t.Fatal("stale empty registry must not resolve a company transaction")
	}
	accounts = []CompanyFundAccount{{
		ID: 77, Channel: ChannelSafeheron, ProviderAccountKey: "new-safeheron-account",
		NormalizedAddress: "0xc4f60c9b02edabba16c9df0afaccec8acf67381f", NetworkFamily: "EVM", Enabled: true,
	}}
	if err := registry.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	mapping, err := resolver.ResolveSafeheronTransactionMapping(context.Background(), snapshot)
	if err != nil || mapping.NetworkFamily != "EVM" || mapping.PrincipalAsset.Asset.Currency != "USDT" {
		t.Fatalf("ResolveSafeheronTransactionMapping() after refresh = %#v, %v", mapping, err)
	}
}

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

func TestRegistrySafeheronTransactionMappingResolver_CatalogHitAndPolicylessFallback(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 41, Channel: ChannelSafeheron, ProviderAccountKey: "safe-evm", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
	}, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeSafeheronCoinLister{coins: []safeheron.Coin{
		{CoinKey: "ETHEREUM_USDT", Symbol: "USDT", BlockChain: "Ethereum", BlockchainType: "EVM", TokenIdentifier: "0xDaC"},
	}}
	catalog, err := NewSafeheronCoinCatalog(client, SafeheronCoinCatalogConfig{})
	if err != nil || catalog.Refresh(context.Background()) != nil {
		t.Fatalf("catalog setup: %v", err)
	}
	resolver, err := NewRegistrySafeheronTransactionMappingResolver(safeheronRegistrySnapshotProviderStub{snapshot: registry}, catalog)
	if err != nil {
		t.Fatal(err)
	}

	recognized, err := resolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{
		CoinKey: "ETHEREUM_USDT", SourceAccountKey: "safe-evm", SourceAddress: "0xABC",
	})
	if err != nil || recognized.PrincipalAsset.Unrecognized || recognized.PrincipalAsset.Asset.Currency != "USDT" ||
		recognized.PrincipalAsset.Asset.ContractAddress != "0xdac" {
		t.Fatalf("catalog mapping = %#v, %v", recognized, err)
	}

	fallback, err := resolver.ResolveSafeheronTransactionMapping(context.Background(), safeheron.TransactionSnapshot{
		CoinKey: "UNKNOWN_EXACT", FeeCoinKey: "UNKNOWN_FEE", SourceAccountKey: "safe-evm", SourceAddress: "0xABC",
	})
	if err != nil || !fallback.PrincipalAsset.Unrecognized || fallback.PrincipalAsset.CoinKey != "UNKNOWN_EXACT" ||
		fallback.FeeAsset == nil || !fallback.FeeAsset.Unrecognized || fallback.NetworkFamily != "EVM" {
		t.Fatalf("policyless fallback mapping = %#v, %v", fallback, err)
	}
}
