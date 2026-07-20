package companyfund

import (
	"fmt"
	"sort"
	"strings"
)

// SafeheronAccountContextInput contains only provider-owned account identity
// facts. Asset policy is intentionally absent: ownership must be resolved
// before an asset is recognized or priced.
type SafeheronAccountContextInput struct {
	NetworkFamily                 string
	SourceProviderAccountKey      string
	DestinationProviderAccountKey string
	SourceAddresses               []string
	DestinationAddresses          []string
}

type SafeheronAccountContext struct {
	Source      *CompanyFundAccount
	Destination *CompanyFundAccount
}

// SafeheronAccountContextConfigurationError is retriable because registry
// ambiguity or a provider-key/address mismatch may be corrected without a new
// webhook delivery. Callers must not persist a permanent negative marker.
type SafeheronAccountContextConfigurationError struct {
	Reason string
}

func (e *SafeheronAccountContextConfigurationError) Error() string {
	if e == nil {
		return "Safeheron account context configuration error"
	}
	return "Safeheron account context configuration error: " + e.Reason
}

func (*SafeheronAccountContextConfigurationError) Retriable() bool { return true }

func ResolveSafeheronAccountContext(
	snapshot *AccountRegistrySnapshot,
	input SafeheronAccountContextInput,
) (SafeheronAccountContext, error) {
	if snapshot == nil {
		return SafeheronAccountContext{}, fmt.Errorf("Safeheron account registry snapshot is unavailable")
	}
	family := normalizeNetworkFamily(input.NetworkFamily)
	source, err := snapshot.resolveSafeheronAccountSide(family, input.SourceProviderAccountKey, input.SourceAddresses, "source")
	if err != nil {
		return SafeheronAccountContext{}, err
	}
	destination, err := snapshot.resolveSafeheronAccountSide(family, input.DestinationProviderAccountKey, input.DestinationAddresses, "destination")
	if err != nil {
		return SafeheronAccountContext{}, err
	}
	return SafeheronAccountContext{Source: source, Destination: destination}, nil
}

func (s *AccountRegistrySnapshot) resolveSafeheronAccountSide(
	networkFamily string,
	providerAccountKey string,
	addresses []string,
	side string,
) (*CompanyFundAccount, error) {
	providerAccountKey = strings.TrimSpace(providerAccountKey)
	addressCandidates := make(map[int64]CompanyFundAccount)
	hasUsableAddress := false
	for _, rawAddress := range addresses {
		if strings.TrimSpace(rawAddress) == "" {
			continue
		}
		hasUsableAddress = true
		matches := s.safeheronAddressCandidates(networkFamily, rawAddress)
		if len(matches) > 1 {
			return nil, safeheronAccountContextError("%s address %q matches multiple enabled accounts", side, strings.TrimSpace(rawAddress))
		}
		if len(matches) == 1 {
			addressCandidates[matches[0].ID] = matches[0]
		}
	}
	if len(addressCandidates) > 1 {
		return nil, safeheronAccountContextError("%s endpoints match multiple enabled accounts", side)
	}

	var addressAccount *CompanyFundAccount
	for _, candidate := range addressCandidates {
		copy := candidate
		addressAccount = &copy
	}
	if addressAccount != nil {
		if providerAccountKey != "" && addressAccount.ProviderAccountKey != providerAccountKey {
			return nil, safeheronAccountContextError("%s provider account key does not match address ownership", side)
		}
		return addressAccount, nil
	}
	if providerAccountKey == "" {
		return nil, nil
	}
	keyCandidates := s.safeheronByProviderKey[providerAccountKey]
	if hasUsableAddress {
		if len(keyCandidates) > 0 {
			return nil, safeheronAccountContextError("%s provider account key has no matching configured address", side)
		}
		return nil, nil
	}
	if len(keyCandidates) == 0 {
		return nil, nil
	}
	if len(keyCandidates) != 1 {
		return nil, safeheronAccountContextError("%s provider account key maps to multiple enabled wallets", side)
	}
	copy := keyCandidates[0]
	return &copy, nil
}

func (s *AccountRegistrySnapshot) safeheronAddressCandidates(networkFamily, address string) []CompanyFundAccount {
	if networkFamily != "" {
		if account, found := s.LookupSafeheron(networkFamily, address); found {
			return []CompanyFundAccount{account}
		}
		return nil
	}
	ids := make([]int64, 0)
	byID := make(map[int64]CompanyFundAccount)
	for _, account := range s.accountsByID {
		if account.Channel != AccountChannelSafeheron || !account.Enabled {
			continue
		}
		configuredAddress := account.NormalizedAddress
		if strings.TrimSpace(configuredAddress) == "" {
			configuredAddress = account.WalletAddress
		}
		if normalizeSafeheronAddress(account.NetworkFamily, configuredAddress) != normalizeSafeheronAddress(account.NetworkFamily, address) {
			continue
		}
		ids = append(ids, account.ID)
		byID[account.ID] = account
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	matches := make([]CompanyFundAccount, 0, len(ids))
	for _, id := range ids {
		matches = append(matches, byID[id])
	}
	return matches
}

func safeheronAccountContextError(format string, args ...any) error {
	return &SafeheronAccountContextConfigurationError{Reason: fmt.Sprintf(format, args...)}
}
