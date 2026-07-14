package companyfund

import (
	"context"
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

// RegistrySafeheronTransactionMappingResolver derives a mapping only from
// enabled Safeheron account asset-policy records. Coin keys must match the
// provider_asset_key exactly; symbols and ticker-like substrings are never
// consulted. Ambiguous network/asset mappings fail closed.
type RegistrySafeheronTransactionMappingResolver struct {
	registries SafeheronRegistrySnapshotProvider
}

func NewRegistrySafeheronTransactionMappingResolver(registries SafeheronRegistrySnapshotProvider) (*RegistrySafeheronTransactionMappingResolver, error) {
	if registries == nil {
		return nil, fmt.Errorf("Safeheron transaction mapping registry snapshot provider is required")
	}
	return &RegistrySafeheronTransactionMappingResolver{registries: registries}, nil
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
		return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron transaction mapping registry snapshot is unavailable")
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
