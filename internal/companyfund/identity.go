package companyfund

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const SafeheronMovementIdentityAlgorithmVersion = "safeheron-v2"

// BuildMovementIdentity creates a versioned SHA-256 fallback movement key.
// Adapters should pass normalized endpoints and a provider/chain/contract asset
// identity; display symbols alone are intentionally insufficient.
func BuildMovementIdentity(input MovementIdentityInput) (MovementIdentity, error) {
	canonical, normalized, err := canonicalMovementTuple(input, true)
	if err != nil {
		return MovementIdentity{}, err
	}
	return buildMovementIdentity(canonical, normalized), nil
}

func buildMovementIdentity(canonical string, normalized MovementIdentityInput) MovementIdentity {
	digest := sha256.Sum256([]byte(canonical))
	digestHex := hex.EncodeToString(digest[:])
	return MovementIdentity{
		AlgorithmVersion: MovementIdentityAlgorithmVersion,
		Digest:           digestHex,
		Key:              MovementIdentityAlgorithmVersion + ":" + digestHex,
		Occurrence:       normalized.Occurrence,
		Input:            normalized,
	}
}

// BuildSafeheronMovementIdentity keeps adapter identity independent from
// mutable catalog display metadata. It deliberately shares the occurrence
// tuple's exact raw CoinKey and stable movement index, while using its own
// versioned namespace.
func BuildSafeheronMovementIdentity(input SafeheronOccurrenceInput) (MovementIdentity, error) {
	normalized, err := validateSafeheronOccurrenceInput(input)
	if err != nil {
		return MovementIdentity{}, err
	}
	return buildSafeheronMovementIdentity(normalized), nil
}

func buildSafeheronMovementIdentity(normalized SafeheronOccurrenceInput) MovementIdentity {
	canonical := lengthDelimitedTuple([]string{
		"company-fund-safeheron-movement",
		SafeheronMovementIdentityAlgorithmVersion,
		normalized.ProviderTransactionKey,
		string(normalized.MovementKind),
		normalized.RawCoinKey,
		normalized.NormalizedSource,
		normalized.NormalizedDestination,
		normalized.Amount.String(),
		string(normalized.TransferMode),
		fmt.Sprintf("%d", normalized.MovementIndex),
	})
	digest := sha256.Sum256([]byte(canonical))
	digestHex := hex.EncodeToString(digest[:])
	return MovementIdentity{
		AlgorithmVersion: SafeheronMovementIdentityAlgorithmVersion,
		Digest:           digestHex,
		Key:              SafeheronMovementIdentityAlgorithmVersion + ":" + digestHex,
		Occurrence:       normalized.MovementIndex + 1,
		Input: MovementIdentityInput{
			Channel:          ChannelSafeheron,
			ProviderParentID: normalized.ProviderTransactionKey,
			MovementKind:     normalized.MovementKind,
			Asset:            AssetIdentity{ProviderAssetKey: normalized.RawCoinKey},
			NormalizedFrom:   normalized.NormalizedSource,
			NormalizedTo:     normalized.NormalizedDestination,
			Amount:           normalized.Amount,
			Occurrence:       normalized.MovementIndex + 1,
		},
	}
}

// AssignBatchMovementIdentities sorts the complete fallback tuple before it
// assigns occurrence numbers. The returned slice is canonical order rather
// than provider array order, so a reordered batch yields the same keys. Truly
// indistinguishable duplicate outputs still receive distinct occurrences.
func AssignBatchMovementIdentities(inputs []MovementIdentityInput) ([]MovementIdentity, error) {
	type sortableInput struct {
		baseTuple string
		input     MovementIdentityInput
	}

	sortable := make([]sortableInput, 0, len(inputs))
	for _, input := range inputs {
		baseTuple, normalized, err := canonicalMovementTuple(input, false)
		if err != nil {
			return nil, err
		}
		sortable = append(sortable, sortableInput{baseTuple: baseTuple, input: normalized})
	}

	// Keep caller-established order for identical identity tuples. Adapters may
	// deterministically order non-identity metadata (for example a phishing
	// flag) before requesting occurrences; an unstable sort here would attach
	// occurrence-derived movement keys to the wrong metadata row.
	sort.SliceStable(sortable, func(i, j int) bool {
		return sortable[i].baseTuple < sortable[j].baseTuple
	})

	identities := make([]MovementIdentity, 0, len(sortable))
	lastTuple := ""
	occurrence := 0
	for _, item := range sortable {
		if item.baseTuple != lastTuple {
			lastTuple = item.baseTuple
			occurrence = 0
		}
		occurrence++
		item.input.Occurrence = occurrence
		identity := buildMovementIdentity(canonicalNormalizedMovementTuple(item.input, true), item.input)
		identities = append(identities, identity)
	}

	return identities, nil
}

func canonicalMovementTuple(input MovementIdentityInput, includeOccurrence bool) (string, MovementIdentityInput, error) {
	if !input.Channel.Valid() {
		return "", MovementIdentityInput{}, fmt.Errorf("movement identity channel %q is unsupported", input.Channel)
	}
	if !input.MovementKind.Valid() {
		return "", MovementIdentityInput{}, fmt.Errorf("movement identity kind %q is unsupported", input.MovementKind)
	}
	if input.Amount.IsNegative() {
		return "", MovementIdentityInput{}, fmt.Errorf("movement identity amount must be non-negative")
	}

	normalized := input
	normalized.ProviderParentID = strings.TrimSpace(input.ProviderParentID)
	normalized.NormalizedFrom = strings.TrimSpace(input.NormalizedFrom)
	normalized.NormalizedTo = strings.TrimSpace(input.NormalizedTo)
	normalized.Asset = normalizeAssetIdentity(input.Asset)
	if normalized.ProviderParentID == "" {
		return "", MovementIdentityInput{}, fmt.Errorf("movement identity provider parent ID is required")
	}
	if normalized.Asset.empty() {
		return "", MovementIdentityInput{}, fmt.Errorf("movement identity requires provider/chain/contract asset identity")
	}
	if normalized.Asset.ChainCode != "" && normalized.Asset.ProviderAssetKey == "" && normalized.Asset.ContractAddress == "" {
		return "", MovementIdentityInput{}, fmt.Errorf("chain asset identity requires provider asset key or contract address")
	}
	if includeOccurrence {
		if normalized.Occurrence <= 0 {
			normalized.Occurrence = 1
		}
	} else {
		normalized.Occurrence = 0
	}

	return canonicalNormalizedMovementTuple(normalized, includeOccurrence), normalized, nil
}

func canonicalNormalizedMovementTuple(normalized MovementIdentityInput, includeOccurrence bool) string {
	components := []string{
		"company-fund-movement",
		MovementIdentityAlgorithmVersion,
		string(normalized.Channel),
		normalized.ProviderParentID,
		string(normalized.MovementKind),
		normalized.Asset.canonicalKey(),
		normalized.NormalizedFrom,
		normalized.NormalizedTo,
		normalized.Amount.String(),
	}
	if includeOccurrence {
		components = append(components, fmt.Sprintf("%d", normalized.Occurrence))
	}
	return lengthDelimitedTuple(components)
}

func normalizeAssetIdentity(asset AssetIdentity) AssetIdentity {
	return AssetIdentity{
		Currency:         strings.ToUpper(strings.TrimSpace(asset.Currency)),
		ChainCode:        strings.ToUpper(strings.TrimSpace(asset.ChainCode)),
		ProviderAssetKey: strings.TrimSpace(asset.ProviderAssetKey),
		ContractAddress:  strings.TrimSpace(asset.ContractAddress),
	}
}

func (asset AssetIdentity) empty() bool {
	return asset.Currency == "" && asset.ChainCode == "" && asset.ProviderAssetKey == "" && asset.ContractAddress == ""
}

func (asset AssetIdentity) canonicalKey() string {
	return lengthDelimitedTuple([]string{
		asset.Currency,
		asset.ChainCode,
		asset.ProviderAssetKey,
		asset.ContractAddress,
	})
}

func lengthDelimitedTuple(values []string) string {
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(fmt.Sprintf("%d:", len(value)))
		builder.WriteString(value)
	}
	return builder.String()
}
