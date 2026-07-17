package companyfund

import (
	"fmt"
	"strings"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

// SafeheronAssetMapping explicitly binds a Safeheron coin key to the ledger's
// provider/chain/contract identity. A normalizer must never derive a chain or
// contract from a display symbol.
type SafeheronAssetMapping struct {
	CoinKey      string
	Asset        AssetIdentity
	Unrecognized bool
}

// SafeheronNormalizationInput contains only already-verified provider facts
// plus an immutable account-registry snapshot. SourcePayloadDigest is supplied
// by the verified inbox/raw-event boundary; RawPayload is deliberately not used
// to recreate it because history payload JSON is not an original byte stream.
type SafeheronNormalizationInput struct {
	Snapshot       safeheron.TransactionSnapshot
	NetworkFamily  string
	PrincipalAsset SafeheronAssetMapping
	FeeAsset       *SafeheronAssetMapping
	Registry       *AccountRegistrySnapshot
	// ProviderAccountKey is an optional, explicitly verified history-account
	// context. Webhook normalization leaves it empty and retains the existing
	// provider snapshot/account fallback behavior.
	ProviderAccountKey    string
	ProviderEventID       string
	LatestProviderEventID *int64
	SourcePayloadDigest   string
	Metadata              ProviderFactMetadata
	FirstSeenSource       TransactionSeenSource
	// AuthorizedOccurrenceKey constrains routing projections to one exact,
	// pre-authorized provider occurrence. Empty retains normal history/webhook
	// normalization behavior.
	AuthorizedOccurrenceKey string
}

// SafeheronNormalizedMovement is a pure adapter result. Future workers persist
// all principal rows before its linked fee row, so ParentMovementKey is always
// resolvable and a transaction-level fee cannot be duplicated for batch items.
type SafeheronNormalizedMovement struct {
	Movement            CompanyFundMovement
	UpsertInput         TransactionUpsertInput
	Risk                RiskAssessment
	FromAccountSnapshot *AccountSnapshot
	ToAccountSnapshot   *AccountSnapshot
}

type safeheronPrincipalLine struct {
	DestinationAddress  string
	Amount              decimal.Decimal
	DestinationPhishing *bool
}

type safeheronPrincipalDraft struct {
	line          safeheronPrincipalLine
	fromAccount   *CompanyFundAccount
	toAccount     *CompanyFundAccount
	direction     Direction
	identityInput MovementIdentityInput
	principal     SafeheronPrincipalOccurrence
}

// NormalizeSafeheronTransaction turns one Safeheron transaction snapshot into
// reportable company-fund movements. A transaction whose endpoints do not
// match the configured registry is intentionally ignored; malformed data and
// missing explicit mappings fail visibly rather than being guessed.
func NormalizeSafeheronTransaction(input SafeheronNormalizationInput) ([]SafeheronNormalizedMovement, error) {
	base, err := normalizeSafeheronBase(input)
	if err != nil {
		return nil, err
	}
	principals, err := EnumerateSafeheronPrincipalOccurrences(input.Snapshot, base.networkFamily)
	if err != nil {
		return nil, err
	}
	drafts := make([]safeheronPrincipalDraft, 0, len(principals))
	for _, principal := range principals {
		line := safeheronPrincipalLine{
			DestinationAddress:  principal.DestinationAddress,
			Amount:              principal.Amount,
			DestinationPhishing: principal.DestinationPhishing,
		}
		draft, matched, err := safeheronPrincipalDraftFor(input.Registry, base, line)
		if err != nil {
			return nil, err
		}
		if matched {
			draft.principal = principal
			drafts = append(drafts, draft)
		}
	}
	if len(drafts) == 0 {
		return nil, nil
	}
	primaryOccurrenceKey := drafts[0].principal.Occurrence.Key

	movements := make([]SafeheronNormalizedMovement, 0, len(drafts))
	for _, draft := range drafts {
		index := draft.principal.MovementIndex
		transferMode := draft.principal.TransferMode
		occurrenceInput := SafeheronOccurrenceInput{
			ProviderTransactionKey: base.txKey,
			MovementKind:           MovementKindPrincipal,
			RawCoinKey:             input.Snapshot.CoinKey,
			NormalizedSource:       normalizeSafeheronAddress(base.networkFamily, base.sourceAddress),
			NormalizedDestination:  normalizeSafeheronAddress(base.networkFamily, draft.line.DestinationAddress),
			Amount:                 draft.line.Amount,
			TransferMode:           transferMode,
			MovementIndex:          index,
		}
		identity, err := BuildSafeheronMovementIdentity(occurrenceInput)
		if err != nil {
			return nil, fmt.Errorf("build Safeheron movement identity: %w", err)
		}
		if input.AuthorizedOccurrenceKey != "" && draft.principal.Occurrence.Key != input.AuthorizedOccurrenceKey {
			continue
		}
		movement, err := buildSafeheronPrincipalMovement(input, base, draft, identity, draft.principal.Occurrence, transferMode, index)
		if err != nil {
			return nil, err
		}
		movements = append(movements, movement)
	}
	if input.AuthorizedOccurrenceKey != "" && len(movements) == 0 {
		return nil, fmt.Errorf("authorized Safeheron occurrence is absent from provider snapshot")
	}
	// Scoped routing executes one occurrence per action. Transaction-level fee
	// metadata belongs only to the deterministic first principal occurrence;
	// otherwise every scoped batch action would duplicate the full fee.
	if input.AuthorizedOccurrenceKey != "" && input.AuthorizedOccurrenceKey != primaryOccurrenceKey {
		return movements, nil
	}
	companyPaysFee := movements[0].Movement.FromAccountID != nil
	fee, err := safeheronTransactionFeeDisplay(input.Snapshot, input.FeeAsset, companyPaysFee)
	if err != nil {
		return nil, err
	}
	if fee != nil {
		// One tx hash represents one displayed normal/internal transfer. The
		// transaction-level network fee therefore belongs on one deterministic
		// principal row rather than becoming a synthetic FEE cash-flow line.
		// Principal occurrences are sorted before ownership filtering, so the
		// selected primary row is stable and independent of configuration.
		applySafeheronFeeDisplay(&movements[0], *fee)
	}
	return movements, nil
}

// NormalizeSafeheronProviderEvent converts the detailed pure movement proposal
// into the worker-facing fact/binding contract. The worker persists Facts first
// and injects returned fact IDs only for explicit bindings; batch children and
// FEE rows never inherit an unproven transaction-level USD total.
func NormalizeSafeheronProviderEvent(input SafeheronNormalizationInput) (ProviderEventNormalizationResult, error) {
	details, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	if len(details) == 0 {
		return ProviderEventNormalizationResult{Ignored: true}, nil
	}
	base, err := normalizeSafeheronBase(input)
	if err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	reference, fact, err := safeheronProviderTransactionFact(input, base, details[0], details[0].Movement.TransferMode)
	if err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	result := ProviderEventNormalizationResult{
		Facts:     []ProviderEventNormalizedFact{{Reference: reference, Input: fact}},
		Movements: make([]TransactionUpsertInput, 0, len(details)),
	}
	for _, detail := range details {
		result.Movements = append(result.Movements, detail.UpsertInput)
	}
	if details[0].Movement.TransferMode == TransferModeSingle {
		for _, detail := range details {
			if detail.Movement.MovementKind == MovementKindPrincipal {
				result.FactBindings = []ProviderEventMovementFactBinding{{
					MovementKey: detail.UpsertInput.MovementKey, FactReference: reference,
				}}
				break
			}
		}
	}
	if err := result.validate(); err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	return result, nil
}

func safeheronPrincipalLines(snapshot safeheron.TransactionSnapshot) ([]safeheronPrincipalLine, TransferMode, error) {
	if len(snapshot.DestinationAddressList) == 0 {
		amount, err := parseSafeheronAmount("Safeheron transaction amount", snapshot.TxAmount, true)
		if err != nil {
			return nil, "", err
		}
		address := strings.TrimSpace(snapshot.DestinationAddress)
		if address == "" {
			return nil, "", fmt.Errorf("Safeheron transaction destination address is required")
		}
		return []safeheronPrincipalLine{{
			DestinationAddress:  address,
			Amount:              amount,
			DestinationPhishing: safeheronTruePointer(snapshot.IsDestinationPhishing),
		}}, TransferModeSingle, nil
	}

	lines := make([]safeheronPrincipalLine, 0, len(snapshot.DestinationAddressList))
	for index, destination := range snapshot.DestinationAddressList {
		address := strings.TrimSpace(destination.Address)
		if address == "" {
			return nil, "", fmt.Errorf("Safeheron batch destination %d address is required", index)
		}
		amount, err := parseSafeheronAmount(fmt.Sprintf("Safeheron batch destination %d amount", index), destination.Amount, true)
		if err != nil {
			return nil, "", err
		}
		lines = append(lines, safeheronPrincipalLine{
			DestinationAddress:  address,
			Amount:              amount,
			DestinationPhishing: safeheronTruePointer(snapshot.IsDestinationPhishing || destination.IsDestinationPhishing),
		})
	}
	return lines, TransferModeBatch, nil
}
