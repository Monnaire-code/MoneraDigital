package companyfund

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"monera-digital/internal/safeheron"
)

// RegistrySafeheronHistoryAccountContextResolver proves that an owned history
// event belongs to a currently configured Safeheron custody account. It does
// not select a wallet address; address association remains the normalizer's
// immutable registry lookup.
type RegistrySafeheronHistoryAccountContextResolver struct {
	registries SafeheronRegistrySnapshotProvider
}

func NewRegistrySafeheronHistoryAccountContextResolver(registries SafeheronRegistrySnapshotProvider) (*RegistrySafeheronHistoryAccountContextResolver, error) {
	if registries == nil {
		return nil, fmt.Errorf("Safeheron history account registry snapshot provider is required")
	}
	return &RegistrySafeheronHistoryAccountContextResolver{registries: registries}, nil
}

func (resolver *RegistrySafeheronHistoryAccountContextResolver) ResolveSafeheronHistoryAccount(ctx context.Context, providerAccountKey string) (SafeheronHistoryAccountContext, error) {
	if err := ctx.Err(); err != nil {
		return SafeheronHistoryAccountContext{}, err
	}
	if _, err := normalizeSafeheronHistoryRequired("Safeheron history provider account key", providerAccountKey, maxProviderFactAccountKeyBytes); err != nil {
		return SafeheronHistoryAccountContext{}, err
	}
	if resolver == nil || resolver.registries == nil {
		return SafeheronHistoryAccountContext{}, fmt.Errorf("Safeheron history account resolver is not configured")
	}
	registry := resolver.registries.Snapshot()
	if registry == nil || !registry.HasSafeheronProviderAccountKey(providerAccountKey) {
		return SafeheronHistoryAccountContext{}, fmt.Errorf("Safeheron history provider account key is not configured")
	}
	return SafeheronHistoryAccountContext{ProviderAccountKey: providerAccountKey}, nil
}

// RegistrySafeheronTransactionMappingResolver resolves exact Safeheron coin
// keys from the provider coin catalog when available, with enabled account
// asset policies as the legacy fallback. Symbols and ticker-like substrings
// are never consulted. Ambiguous network/asset mappings fail closed.
type RegistrySafeheronTransactionMappingResolver struct {
	registries SafeheronRegistrySnapshotProvider
	coins      SafeheronCoinLookup
}

func NewRegistrySafeheronTransactionMappingResolver(registries SafeheronRegistrySnapshotProvider, catalogs ...SafeheronCoinLookup) (*RegistrySafeheronTransactionMappingResolver, error) {
	if registries == nil {
		return nil, fmt.Errorf("Safeheron transaction mapping registry snapshot provider is required")
	}
	if len(catalogs) > 1 {
		return nil, fmt.Errorf("at most one Safeheron coin catalog may be configured")
	}
	resolver := &RegistrySafeheronTransactionMappingResolver{registries: registries}
	if len(catalogs) == 1 {
		resolver.coins = catalogs[0]
	}
	return resolver, nil
}

func (resolver *RegistrySafeheronTransactionMappingResolver) ResolveSafeheronTransactionMapping(ctx context.Context, snapshot safeheron.TransactionSnapshot) (SafeheronTransactionMapping, error) {
	if err := ctx.Err(); err != nil {
		return SafeheronTransactionMapping{}, err
	}
	if resolver == nil || resolver.registries == nil {
		return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron transaction mapping resolver is not configured")
	}
	registry := resolver.registries.Snapshot()
	if registry == nil {
		return SafeheronTransactionMapping{}, safeheronAccountContextError("transaction mapping registry snapshot is unavailable")
	}
	if resolver.coins != nil {
		return resolver.resolveCatalogSafeheronTransactionMapping(registry, snapshot)
	}
	networkFamily, principal, err := registrySafeheronAssetMapping(registry, snapshot.CoinKey)
	if err != nil {
		return SafeheronTransactionMapping{}, err
	}
	result := SafeheronTransactionMapping{NetworkFamily: networkFamily, PrincipalAsset: principal}
	if strings.TrimSpace(snapshot.FeeCoinKey) != "" {
		feeNetwork, fee, err := registrySafeheronAssetMapping(registry, snapshot.FeeCoinKey)
		if err != nil {
			return SafeheronTransactionMapping{}, err
		}
		if feeNetwork != networkFamily {
			return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron fee asset network family conflicts with principal asset")
		}
		result.FeeAsset = &fee
	}
	return result, nil
}

func (resolver *RegistrySafeheronTransactionMappingResolver) resolveCatalogSafeheronTransactionMapping(
	registry *AccountRegistrySnapshot,
	snapshot safeheron.TransactionSnapshot,
) (SafeheronTransactionMapping, error) {
	coin, lookupErr := resolver.coins.Lookup(snapshot.CoinKey)
	networkFamily := ""
	if lookupErr == nil {
		networkFamily = normalizeNetworkFamily(coin.BlockchainType)
		if networkFamily == "" {
			return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron catalog coin has no blockchain type")
		}
	} else if !errors.Is(lookupErr, ErrSafeheronCoinNotFound) {
		return SafeheronTransactionMapping{}, lookupErr
	}
	accountContext, err := ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		NetworkFamily:                 networkFamily,
		SourceProviderAccountKey:      strings.TrimSpace(snapshot.SourceAccountKey),
		DestinationProviderAccountKey: strings.TrimSpace(snapshot.DestinationAccountKey),
		SourceAddresses:               safeheronWebhookSourceAddresses(snapshot),
		DestinationAddresses:          safeheronWebhookDestinationAddresses(snapshot),
	})
	if err != nil {
		return SafeheronTransactionMapping{}, err
	}
	if accountContext.Source == nil && accountContext.Destination == nil {
		return SafeheronTransactionMapping{}, safeheronAccountContextError("transaction does not belong to an enabled company account in the current registry snapshot")
	}
	if networkFamily == "" {
		networkFamily, err = safeheronAccountContextNetworkFamily(accountContext)
		if err != nil {
			return SafeheronTransactionMapping{}, err
		}
	}
	principal := SafeheronAssetMapping{CoinKey: snapshot.CoinKey, Unrecognized: lookupErr != nil}
	if lookupErr == nil {
		principal.Asset, err = safeheronCatalogAssetIdentity(coin)
		if err != nil {
			return SafeheronTransactionMapping{}, err
		}
	}
	result := SafeheronTransactionMapping{NetworkFamily: networkFamily, PrincipalAsset: principal}
	feeCoinKey := strings.TrimSpace(snapshot.FeeCoinKey)
	if feeCoinKey == "" {
		return result, nil
	}
	feeCoin, feeErr := resolver.coins.Lookup(feeCoinKey)
	fee := SafeheronAssetMapping{CoinKey: feeCoinKey, Unrecognized: feeErr != nil}
	if feeErr == nil {
		feeNetwork := normalizeNetworkFamily(feeCoin.BlockchainType)
		if feeNetwork != networkFamily {
			return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron fee asset network family conflicts with principal account context")
		}
		fee.Asset, err = safeheronCatalogAssetIdentity(feeCoin)
		if err != nil {
			return SafeheronTransactionMapping{}, err
		}
	} else if !errors.Is(feeErr, ErrSafeheronCoinNotFound) {
		return SafeheronTransactionMapping{}, feeErr
	}
	result.FeeAsset = &fee
	return result, nil
}

func safeheronCatalogAssetIdentity(coin safeheron.Coin) (AssetIdentity, error) {
	currency := strings.TrimSpace(coin.Symbol)
	if currency == "" {
		currency = strings.TrimSpace(coin.CoinName)
	}
	if strings.TrimSpace(coin.CoinKey) == "" || currency == "" {
		return AssetIdentity{}, fmt.Errorf("Safeheron catalog coin identity is incomplete")
	}
	contract := strings.TrimSpace(coin.TokenIdentifier)
	if strings.EqualFold(contract, "NATIVE") {
		contract = ""
	}
	if normalizeNetworkFamily(coin.BlockchainType) == "EVM" {
		contract = strings.ToLower(contract)
	}
	return AssetIdentity{
		Currency:         currency,
		ChainCode:        strings.TrimSpace(coin.BlockChain),
		ProviderAssetKey: coin.CoinKey,
		ContractAddress:  contract,
	}, nil
}

func safeheronAccountContextNetworkFamily(context SafeheronAccountContext) (string, error) {
	family := ""
	for _, account := range []*CompanyFundAccount{context.Source, context.Destination} {
		if account == nil {
			continue
		}
		candidate := normalizeNetworkFamily(account.NetworkFamily)
		if family != "" && family != candidate {
			return "", safeheronAccountContextError("source and destination accounts use different network families")
		}
		family = candidate
	}
	if family == "" {
		return "", fmt.Errorf("Safeheron company account context has no network family")
	}
	return family, nil
}

func registrySafeheronAssetMapping(registry *AccountRegistrySnapshot, coinKey string) (string, SafeheronAssetMapping, error) {
	if registry == nil || coinKey == "" || coinKey != strings.TrimSpace(coinKey) {
		return "", SafeheronAssetMapping{}, fmt.Errorf("Safeheron asset coin key is required")
	}
	var (
		found         bool
		networkFamily string
		mapping       SafeheronAssetMapping
		mappingKey    string
	)
	for accountID, policies := range registry.policiesByAccount {
		account, exists := registry.accountsByID[accountID]
		if !exists || !account.Enabled || account.Channel != ChannelSafeheron {
			continue
		}
		candidateNetwork := normalizeNetworkFamily(account.NetworkFamily)
		if candidateNetwork == "" {
			return "", SafeheronAssetMapping{}, fmt.Errorf("configured Safeheron asset policy has no network family")
		}
		for _, policy := range policies {
			if policy.Asset.ProviderAssetKey != coinKey {
				continue
			}
			candidate := SafeheronAssetMapping{CoinKey: coinKey, Asset: policy.Asset}
			asset, err := normalizeSafeheronAssetMapping(coinKey, candidate, "configured")
			if err != nil {
				return "", SafeheronAssetMapping{}, err
			}
			candidate.Asset = asset
			candidateKey := candidateNetwork + "\x00" + asset.canonicalKey()
			if !found {
				found = true
				networkFamily = candidateNetwork
				mapping = candidate
				mappingKey = candidateKey
				continue
			}
			if candidateKey != mappingKey {
				return "", SafeheronAssetMapping{}, fmt.Errorf("Safeheron coin key %q has ambiguous configured network or asset mapping", coinKey)
			}
		}
	}
	if !found {
		return "", SafeheronAssetMapping{}, fmt.Errorf("Safeheron coin key %q is not configured in an enabled account asset policy", coinKey)
	}
	return networkFamily, mapping, nil
}

var _ SafeheronHistoryAccountContextResolver = (*RegistrySafeheronHistoryAccountContextResolver)(nil)
var _ SafeheronTransactionMappingResolver = (*RegistrySafeheronTransactionMappingResolver)(nil)
