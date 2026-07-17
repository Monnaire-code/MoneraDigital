package companyfund

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

// SafeheronPrincipalOccurrence is a configuration-independent provider line.
// MovementIndex and DuplicateOrdinal are assigned before any ownership or
// account filtering so the identity remains stable as configuration changes.
type SafeheronPrincipalOccurrence struct {
	DestinationAddress    string
	Amount                decimal.Decimal
	DestinationPhishing   *bool
	RawCoinKey            string
	NormalizedSource      string
	NormalizedDestination string
	TransferMode          TransferMode
	MovementIndex         int
	DuplicateOrdinal      int
	Occurrence            SafeheronOccurrence
}

type safeheronEnumeratedDraft struct {
	line      safeheronPrincipalLine
	baseTuple string
	detailKey string
}

// EnumerateSafeheronPrincipalOccurrences parses and orders every provider
// principal line without consulting customer or company configuration.
func EnumerateSafeheronPrincipalOccurrences(snapshot safeheron.TransactionSnapshot, networkFamily string) ([]SafeheronPrincipalOccurrence, error) {
	lines, transferMode, err := safeheronPrincipalLines(snapshot)
	if err != nil {
		return nil, err
	}
	source, err := NormalizeSafeheronOccurrenceAddress(networkFamily, snapshot.SourceAddress)
	if err != nil {
		return nil, err
	}
	rawCoinKey := snapshot.CoinKey
	if strings.TrimSpace(rawCoinKey) == "" {
		return nil, fmt.Errorf("Safeheron transaction CoinKey is required")
	}
	drafts := make([]safeheronEnumeratedDraft, 0, len(lines))
	for _, line := range lines {
		destination, normalizeErr := NormalizeSafeheronOccurrenceAddress(networkFamily, line.DestinationAddress)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		baseTuple := lengthDelimitedTuple([]string{
			"safeheron-principal-line-v1",
			snapshot.TxKey,
			rawCoinKey,
			source,
			destination,
			line.Amount.String(),
			string(transferMode),
		})
		drafts = append(drafts, safeheronEnumeratedDraft{
			line:      line,
			baseTuple: baseTuple,
			detailKey: strconv.FormatBool(line.DestinationPhishing != nil && *line.DestinationPhishing),
		})
	}
	sort.SliceStable(drafts, func(left, right int) bool {
		if drafts[left].baseTuple == drafts[right].baseTuple {
			return drafts[left].detailKey < drafts[right].detailKey
		}
		return drafts[left].baseTuple < drafts[right].baseTuple
	})

	result := make([]SafeheronPrincipalOccurrence, 0, len(drafts))
	previousTuple := ""
	duplicateOrdinal := 0
	for index, draft := range drafts {
		if draft.baseTuple != previousTuple {
			duplicateOrdinal = 0
		} else {
			duplicateOrdinal++
		}
		previousTuple = draft.baseTuple
		destination, _ := NormalizeSafeheronOccurrenceAddress(networkFamily, draft.line.DestinationAddress)
		occurrence, buildErr := BuildSafeheronOccurrence(SafeheronOccurrenceInput{
			ProviderTransactionKey: snapshot.TxKey,
			MovementKind:           MovementKindPrincipal,
			RawCoinKey:             rawCoinKey,
			NormalizedSource:       source,
			NormalizedDestination:  destination,
			Amount:                 draft.line.Amount,
			TransferMode:           transferMode,
			MovementIndex:          index,
		})
		if buildErr != nil {
			return nil, fmt.Errorf("build Safeheron provider occurrence: %w", buildErr)
		}
		result = append(result, SafeheronPrincipalOccurrence{
			DestinationAddress:    draft.line.DestinationAddress,
			Amount:                draft.line.Amount,
			DestinationPhishing:   draft.line.DestinationPhishing,
			RawCoinKey:            rawCoinKey,
			NormalizedSource:      source,
			NormalizedDestination: destination,
			TransferMode:          transferMode,
			MovementIndex:         index,
			DuplicateOrdinal:      duplicateOrdinal,
			Occurrence:            occurrence,
		})
	}
	return result, nil
}
