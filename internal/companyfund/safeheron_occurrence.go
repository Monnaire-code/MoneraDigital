package companyfund

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

const SafeheronOccurrenceAlgorithmVersion = "safeheron-occurrence-v1"

// SafeheronOccurrenceInput is the complete provider occurrence tuple. RawCoinKey
// is deliberately exact: catalog symbols, chain labels, contracts and TxHash
// are not occurrence identity inputs.
type SafeheronOccurrenceInput struct {
	ProviderTransactionKey string          `json:"provider_transaction_key"`
	MovementKind           MovementKind    `json:"movement_kind"`
	RawCoinKey             string          `json:"raw_coin_key"`
	NormalizedSource       string          `json:"normalized_source"`
	NormalizedDestination  string          `json:"normalized_destination"`
	Amount                 decimal.Decimal `json:"amount"`
	TransferMode           TransferMode    `json:"transfer_mode"`
	MovementIndex          int             `json:"movement_index"`
}

type SafeheronOccurrence struct {
	AlgorithmVersion string
	Digest           string
	Key              string
	Input            SafeheronOccurrenceInput
}

// NormalizeSafeheronOccurrenceAddress applies the same provider endpoint rule
// before an address enters the occurrence tuple. Network family selects the
// normalization rule but is not itself hashed.
func NormalizeSafeheronOccurrenceAddress(networkFamily, address string) (string, error) {
	family := strings.ToUpper(strings.TrimSpace(networkFamily))
	normalized := strings.TrimSpace(address)
	if family == "" {
		return "", fmt.Errorf("Safeheron occurrence network family is required")
	}
	if normalized == "" {
		return "", fmt.Errorf("Safeheron occurrence address is required")
	}
	if family == "EVM" {
		normalized = strings.ToLower(normalized)
	}
	return normalized, nil
}

func BuildSafeheronOccurrence(input SafeheronOccurrenceInput) (SafeheronOccurrence, error) {
	normalized, err := validateSafeheronOccurrenceInput(input)
	if err != nil {
		return SafeheronOccurrence{}, err
	}
	canonical := safeheronOccurrenceCanonicalTuple(normalized)
	digest := sha256.Sum256([]byte(canonical))
	digestHex := hex.EncodeToString(digest[:])
	return SafeheronOccurrence{
		AlgorithmVersion: SafeheronOccurrenceAlgorithmVersion,
		Digest:           digestHex,
		Key:              SafeheronOccurrenceAlgorithmVersion + ":" + digestHex,
		Input:            normalized,
	}, nil
}

func validateSafeheronOccurrenceInput(input SafeheronOccurrenceInput) (SafeheronOccurrenceInput, error) {
	input.ProviderTransactionKey = strings.TrimSpace(input.ProviderTransactionKey)
	input.NormalizedSource = strings.TrimSpace(input.NormalizedSource)
	input.NormalizedDestination = strings.TrimSpace(input.NormalizedDestination)
	switch {
	case input.ProviderTransactionKey == "":
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence provider transaction key is required")
	case strings.TrimSpace(input.RawCoinKey) == "":
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence exact raw CoinKey is required")
	case input.NormalizedSource == "":
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence normalized source is required")
	case input.NormalizedDestination == "":
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence normalized destination is required")
	case !input.MovementKind.Valid():
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence movement kind %q is unsupported", input.MovementKind)
	case !input.TransferMode.Valid():
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence transfer mode %q is unsupported", input.TransferMode)
	case input.Amount.IsNegative():
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence amount must be non-negative")
	case input.MovementIndex < 0:
		return SafeheronOccurrenceInput{}, fmt.Errorf("Safeheron occurrence movement index must be non-negative")
	default:
		return input, nil
	}
}

func safeheronOccurrenceCanonicalTuple(input SafeheronOccurrenceInput) string {
	return lengthDelimitedTuple([]string{
		"company-fund-safeheron-occurrence",
		SafeheronOccurrenceAlgorithmVersion,
		input.ProviderTransactionKey,
		string(input.MovementKind),
		input.RawCoinKey,
		input.NormalizedSource,
		input.NormalizedDestination,
		input.Amount.String(),
		string(input.TransferMode),
		fmt.Sprintf("%d", input.MovementIndex),
	})
}
