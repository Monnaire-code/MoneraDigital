package fundrouting

import (
	"context"
	"strings"

	"monera-digital/internal/safeheron"
)

type CoinLookup interface {
	Lookup(string) (safeheron.Coin, error)
}

type CatalogNetworkResolver struct {
	coins CoinLookup
}

func NewCatalogNetworkResolver(coins CoinLookup) *CatalogNetworkResolver {
	return &CatalogNetworkResolver{coins: coins}
}

func (resolver *CatalogNetworkResolver) ResolveNetworkFamily(ctx context.Context, snapshot safeheron.TransactionSnapshot) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if resolver != nil && resolver.coins != nil {
		if coin, err := resolver.coins.Lookup(snapshot.CoinKey); err == nil {
			if family := normalizeNetworkFamily(coin.BlockchainType); family != "" {
				return family, nil
			}
		}
	}
	addresses := append([]string{snapshot.SourceAddress, snapshot.DestinationAddress}, safeheronDestinationAddresses(snapshot)...)
	for _, address := range addresses {
		trimmed := strings.TrimSpace(address)
		if len(trimmed) == 42 && strings.HasPrefix(strings.ToLower(trimmed), "0x") {
			return "EVM", nil
		}
	}
	return "UNKNOWN", nil
}

func safeheronDestinationAddresses(snapshot safeheron.TransactionSnapshot) []string {
	result := make([]string, 0, len(snapshot.DestinationAddressList))
	for _, destination := range snapshot.DestinationAddressList {
		result = append(result, destination.Address)
	}
	return result
}
