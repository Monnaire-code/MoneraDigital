package companyfund

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type safeheronNormalizationBase struct {
	networkFamily string
	txKey         string
	asset         AssetIdentity
	status        LifecycleStatus
	statusRank    int
	sourceAddress string
	occurredAt    *time.Time
	completedAt   *time.Time
	txHash        *string
}

type safeheronFeeDisplay struct {
	amount      *decimal.Decimal
	asset       *AssetIdentity
	detailsJSON json.RawMessage
}

func normalizeSafeheronBase(input SafeheronNormalizationInput) (safeheronNormalizationBase, error) {
	if input.Registry == nil {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron normalization requires an account registry snapshot")
	}
	if !input.FirstSeenSource.valid() {
		return safeheronNormalizationBase{}, fmt.Errorf("unsupported Safeheron first-seen source %q", input.FirstSeenSource)
	}
	if input.LatestProviderEventID != nil && *input.LatestProviderEventID <= 0 {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron latest provider event ID must be positive")
	}
	if !isLowerSHA256Hex(input.SourcePayloadDigest) {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron source payload digest must be lowercase SHA-256 hex")
	}
	if strings.TrimSpace(input.Snapshot.TxKey) == "" {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron transaction key is required")
	}
	networkFamily := normalizeNetworkFamily(input.NetworkFamily)
	if networkFamily == "" {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron network family must be explicit")
	}
	asset, err := normalizeSafeheronAssetMapping(input.Snapshot.CoinKey, input.PrincipalAsset, "principal")
	if err != nil {
		return safeheronNormalizationBase{}, err
	}
	status := normalizedLifecycleStatus(LifecycleStatus(input.Snapshot.TransactionStatus))
	statusRank, supported := safeheronStatusRank(status)
	if !supported {
		return safeheronNormalizationBase{}, fmt.Errorf("unsupported Safeheron transaction status %q", input.Snapshot.TransactionStatus)
	}
	sourceAddress := strings.TrimSpace(input.Snapshot.SourceAddress)
	if sourceAddress == "" {
		return safeheronNormalizationBase{}, fmt.Errorf("Safeheron transaction source address is required")
	}
	occurredAt, err := safeheronUnixMilliseconds(input.Snapshot.CreateTime, "create time")
	if err != nil {
		return safeheronNormalizationBase{}, err
	}
	completedAt, err := safeheronUnixMilliseconds(input.Snapshot.CompletedTime, "completed time")
	if err != nil {
		return safeheronNormalizationBase{}, err
	}
	return safeheronNormalizationBase{
		networkFamily: networkFamily,
		txKey:         strings.TrimSpace(input.Snapshot.TxKey),
		asset:         asset,
		status:        status,
		statusRank:    statusRank,
		sourceAddress: sourceAddress,
		occurredAt:    occurredAt,
		completedAt:   completedAt,
		txHash:        safeheronOptionalString(input.Snapshot.TxHash),
	}, nil
}

func safeheronPrincipalDraftFor(registry *AccountRegistrySnapshot, base safeheronNormalizationBase, line safeheronPrincipalLine) (safeheronPrincipalDraft, bool, error) {
	from, fromMatched := registry.LookupSafeheron(base.networkFamily, base.sourceAddress)
	to, toMatched := registry.LookupSafeheron(base.networkFamily, line.DestinationAddress)
	if !fromMatched && !toMatched {
		return safeheronPrincipalDraft{}, false, nil
	}
	direction := DirectionInflow
	if fromMatched && toMatched {
		direction = DirectionInternalTransfer
	} else if fromMatched {
		direction = DirectionOutflow
	}
	var fromAccount, toAccount *CompanyFundAccount
	if fromMatched {
		fromCopy := from
		fromAccount = &fromCopy
	}
	if toMatched {
		toCopy := to
		toAccount = &toCopy
	}
	identityInput := MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: base.txKey,
		MovementKind:     MovementKindPrincipal,
		Asset:            base.asset,
		NormalizedFrom:   normalizeSafeheronAddress(base.networkFamily, base.sourceAddress),
		NormalizedTo:     normalizeSafeheronAddress(base.networkFamily, line.DestinationAddress),
		Amount:           line.Amount,
	}
	baseTuple, normalizedIdentity, err := canonicalMovementTuple(identityInput, false)
	if err != nil {
		return safeheronPrincipalDraft{}, false, fmt.Errorf("normalize Safeheron principal identity: %w", err)
	}
	return safeheronPrincipalDraft{
		line:          line,
		fromAccount:   fromAccount,
		toAccount:     toAccount,
		direction:     direction,
		identityInput: normalizedIdentity,
		baseTuple:     baseTuple,
		detailKey:     fmt.Sprintf("%t", line.DestinationPhishing != nil && *line.DestinationPhishing),
	}, true, nil
}

func buildSafeheronPrincipalMovement(input SafeheronNormalizationInput, base safeheronNormalizationBase, draft safeheronPrincipalDraft, identity MovementIdentity, transferMode TransferMode, index int) (SafeheronNormalizedMovement, error) {
	return buildSafeheronMovement(input, base, draft, identity, transferMode, index)
}

func buildSafeheronMovement(input SafeheronNormalizationInput, base safeheronNormalizationBase, draft safeheronPrincipalDraft, identity MovementIdentity, transferMode TransferMode, index int) (SafeheronNormalizedMovement, error) {
	asset := base.asset
	amount := draft.line.Amount
	fromID, toID := safeheronAccountIDs(draft.fromAccount, draft.toAccount)
	policy, recognized := safeheronRiskPolicy(input.Registry, draft.direction, fromID, toID, asset)
	amlRiskLevel, amlRiskKnown := safeheronAMLRiskLevel(input.Snapshot.AMLList)
	risk, err := EvaluateRisk(RiskInput{
		Channel:             ChannelSafeheron,
		Direction:           draft.direction,
		Amount:              amount,
		Asset:               asset,
		Policy:              policy.Dust,
		SourcePhishing:      safeheronTruePointer(input.Snapshot.IsSourcePhishing),
		DestinationPhishing: draft.line.DestinationPhishing,
		AMLLock:             safeheronAMLLock(input.Snapshot.AmlLock),
		AMLRiskLevel:        amlRiskLevel,
		UnrecognizedAsset:   !recognized,
		ConfiguredFromID:    fromID,
		ConfiguredToID:      toID,
	})
	if err != nil {
		return SafeheronNormalizedMovement{}, fmt.Errorf("evaluate Safeheron movement risk: %w", err)
	}
	providerReportedUSD, err := safeheronDirectProviderUSD(input.Snapshot, MovementKindPrincipal, transferMode)
	if err != nil {
		return SafeheronNormalizedMovement{}, err
	}
	display := safeheronProviderDisplay(input.Snapshot, draft, nil)
	automaticRisk := safeheronAutomaticRisk(input.Snapshot, draft.line.DestinationPhishing, policy, recognized, risk, amlRiskLevel, amlRiskKnown)
	provider := safeheronProviderFields(input.Metadata, base, amount, asset)
	movement := CompanyFundMovement{
		Identity:            identity,
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		TransferMode:        transferMode,
		Direction:           draft.direction,
		Amount:              amount,
		Asset:               asset,
		FromAccountID:       fromID,
		ToAccountID:         toID,
		ParentMovementKey:   "",
		ProviderReportedUSD: providerReportedUSD,
		Provider:            provider,
	}
	if err := ValidateMovementRelationship(MovementRelation{
		MovementKind: MovementKindPrincipal, TransferMode: transferMode, Direction: draft.direction,
		HasFromAccount: fromID != nil, HasToAccount: toID != nil, ParentMovementKey: "",
	}); err != nil {
		return SafeheronNormalizedMovement{}, fmt.Errorf("validate Safeheron movement relation: %w", err)
	}
	upsert := TransactionUpsertInput{
		MovementKey:              identity.Key,
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: identity.AlgorithmVersion,
		ProviderAccountKey:       safeheronProviderAccountKey(input.ProviderAccountKey, input.Snapshot, draft.fromAccount, draft.toAccount),
		ProviderTransactionID:    strings.TrimSpace(input.Snapshot.TxKey),
		ProviderEventID:          strings.TrimSpace(input.ProviderEventID),
		MovementIndex:            index,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             transferMode,
		Direction:                draft.direction,
		ParentMovementKey:        "",
		FromCompanyFundAccountID: fromID,
		ToCompanyFundAccountID:   toID,
		Currency:                 asset.Currency,
		Asset:                    asset,
		Amount:                   amount,
		OccurredAt:               copyTime(base.occurredAt),
		LatestProviderEventID:    safeheronCopyInt64(input.LatestProviderEventID),
		RawSnapshotDigest:        input.SourcePayloadDigest,
		FirstSeenSource:          input.FirstSeenSource,
		Provider:                 provider,
		ProviderStatusRank:       base.statusRank,
		ProviderDisplay:          display,
		AutomaticRisk:            automaticRisk,
	}
	if err := upsert.validate(); err != nil {
		return SafeheronNormalizedMovement{}, fmt.Errorf("validate normalized Safeheron upsert: %w", err)
	}
	return SafeheronNormalizedMovement{
		Movement:            movement,
		UpsertInput:         upsert,
		Risk:                risk,
		FromAccountSnapshot: safeheronAccountSnapshot(draft.fromAccount),
		ToAccountSnapshot:   safeheronAccountSnapshot(draft.toAccount),
	}, nil
}

func applySafeheronFeeDisplay(movement *SafeheronNormalizedMovement, fee safeheronFeeDisplay) {
	if movement == nil {
		return
	}
	movement.UpsertInput.ProviderDisplay.Fee = safeheronProviderFeeInput(fee)
}

func safeheronProviderFeeInput(fee safeheronFeeDisplay) ProviderTransactionFeeInput {
	display := ProviderTransactionFeeInput{DetailsJSON: append(json.RawMessage(nil), fee.detailsJSON...)}
	if fee.amount != nil {
		amount := *fee.amount
		display.Amount = &amount
	}
	if fee.asset != nil {
		currency := fee.asset.Currency
		display.Currency = &currency
	}
	return display
}
