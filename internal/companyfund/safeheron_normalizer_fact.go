package companyfund

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// safeheronProviderTransactionFact preserves the transaction-level total even
// when a batch has many ledger rows. Callers bind it only to a single direct
// principal movement; batch children intentionally retain no inferred parent
// allocation until a Sandbox-backed derivation contract exists.
func safeheronProviderTransactionFact(input SafeheronNormalizationInput, base safeheronNormalizationBase, principal SafeheronNormalizedMovement, transferMode TransferMode) (string, ProviderTransactionFactInput, error) {
	if input.LatestProviderEventID == nil || *input.LatestProviderEventID <= 0 {
		return "", ProviderTransactionFactInput{}, fmt.Errorf("Safeheron provider transaction fact requires a source provider event ID")
	}
	providerAccountKey := strings.TrimSpace(principal.UpsertInput.ProviderAccountKey)
	if providerAccountKey == "" {
		return "", ProviderTransactionFactInput{}, fmt.Errorf("Safeheron provider transaction fact requires a provider account key")
	}
	amount, err := parseSafeheronAmount("Safeheron provider transaction total", input.Snapshot.TxAmount, true)
	if err != nil {
		return "", ProviderTransactionFactInput{}, err
	}
	providerUSD, err := safeheronOptionalProviderUSD(input.Snapshot.TxAmountToUSD)
	if err != nil {
		return "", ProviderTransactionFactInput{}, err
	}
	scope := ProviderValueScopeDirectItem
	allocation := ProviderFactAllocationStateNotApplicable
	if transferMode == TransferModeBatch {
		scope = ProviderValueScopeTransactionTotal
		allocation = ProviderFactAllocationStateUnproven
	}
	feeDetails, err := safeheronFeeAuditDetails(input.Snapshot)
	if err != nil {
		return "", ProviderTransactionFactInput{}, err
	}
	extras, err := json.Marshal(struct {
		TransactionType      string          `json:"transactionType,omitempty"`
		TransactionDirection string          `json:"transactionDirection,omitempty"`
		TransactionSubStatus string          `json:"transactionSubStatus,omitempty"`
		CoinKey              string          `json:"coinKey"`
		FeeDetails           json.RawMessage `json:"feeDetails,omitempty"`
	}{
		TransactionType:      strings.TrimSpace(input.Snapshot.TransactionType),
		TransactionDirection: strings.TrimSpace(input.Snapshot.TransactionDirection),
		TransactionSubStatus: strings.TrimSpace(input.Snapshot.TransactionSubStatus),
		CoinKey:              strings.TrimSpace(input.Snapshot.CoinKey),
		FeeDetails:           feeDetails,
	})
	if err != nil {
		return "", ProviderTransactionFactInput{}, fmt.Errorf("encode Safeheron provider transaction fact extras: %w", err)
	}
	reference := safeheronProviderFactReference(base.txKey, input.SourcePayloadDigest)
	amountCopy := amount
	fact := ProviderTransactionFactInput{
		Channel:               ChannelSafeheron,
		ProviderAccountKey:    providerAccountKey,
		ProviderTransactionID: base.txKey,
		FactIdentityKey:       reference,
		FactVersion:           1,
		SourceProviderEventID: *input.LatestProviderEventID,
		SourcePayloadDigest:   input.SourcePayloadDigest,
		ProviderOccurredAt:    copyTime(base.occurredAt),
		ProviderAmount:        &amountCopy,
		ProviderCurrency:      base.asset.Currency,
		ProviderReportedUSD:   providerUSD,
		ValueScope:            scope,
		AllocationState:       allocation,
		ProviderExtrasJSON:    extras,
	}
	if _, err := fact.validate(); err != nil {
		return "", ProviderTransactionFactInput{}, fmt.Errorf("validate Safeheron provider transaction fact: %w", err)
	}
	return reference, fact, nil
}

func safeheronOptionalProviderUSD(value string) (*decimal.Decimal, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := parseSafeheronAmount("Safeheron provider transaction USD total", value, true)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func safeheronProviderFactReference(txKey, sourcePayloadDigest string) string {
	sum := sha256.Sum256([]byte(lengthDelimitedTuple([]string{
		"safeheron-provider-transaction-fact", "v1", strings.TrimSpace(txKey), sourcePayloadDigest,
	})))
	return "safeheron-parent:v1:" + hex.EncodeToString(sum[:])
}
