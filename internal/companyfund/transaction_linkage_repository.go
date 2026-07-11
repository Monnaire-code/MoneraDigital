package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const selectCompanyFundTransactionLinkageForUpdateSQL = `
SELECT parent_transaction_id,
       reversal_of_transaction_id,
       COALESCE(conversion_group_key, ''),
       COALESCE(conversion_leg, ''),
       COALESCE(conversion_group_status, '')
FROM company_fund_transactions
WHERE id = $1
FOR UPDATE`

const selectCompanyFundTransactionLinkTargetSQL = `
SELECT id, channel
FROM company_fund_transactions
WHERE movement_key = $1
FOR KEY SHARE`

// updateCompanyFundTransactionProviderLinkageSQL has no finance or manual
// risk columns. Provider movement keys are resolved to IDs in the same
// transaction rather than inferred from account, organization, or address.
const updateCompanyFundTransactionProviderLinkageSQL = `
UPDATE company_fund_transactions
SET parent_transaction_id = $2,
	reversal_of_transaction_id = $3,
	conversion_group_key = $4,
	conversion_leg = $5,
	conversion_group_status = $6,
	updated_at = NOW()
WHERE id = $1
RETURNING id`

type transactionProviderLinkage struct {
	ParentTransactionID     *int64
	ReversalOfTransactionID *int64
	ConversionGroupKey      string
	ConversionLeg           ConversionLeg
	ConversionGroupState    ConversionGroupState
}

func (input TransactionUpsertInput) movementRelation() MovementRelation {
	return MovementRelation{
		MovementKind:          input.MovementKind,
		TransferMode:          input.TransferMode,
		Direction:             input.Direction,
		HasFromAccount:        input.FromCompanyFundAccountID != nil,
		HasToAccount:          input.ToCompanyFundAccountID != nil,
		ParentMovementKey:     input.ParentMovementKey,
		ReversalOfMovementKey: input.ReversalOfMovementKey,
		ConversionGroupKey:    input.ConversionGroupKey,
		ConversionLeg:         input.ConversionLeg,
		ConversionGroupState:  input.ConversionGroupState,
	}
}

func (input TransactionUpsertInput) hasProviderLinkage() bool {
	return input.ParentMovementKey != "" ||
		input.ReversalOfMovementKey != "" ||
		input.ConversionGroupKey != "" ||
		input.ConversionLeg != "" ||
		input.ConversionGroupState != ""
}

func (input TransactionUpsertInput) validateProviderLinkage() error {
	if err := ValidateMovementRelationship(input.movementRelation()); err != nil {
		return err
	}
	for label, key := range map[string]string{
		"parent movement key":   input.ParentMovementKey,
		"reversal movement key": input.ReversalOfMovementKey,
		"conversion group key":  input.ConversionGroupKey,
	} {
		if key != "" {
			if err := validateRequiredString(label, key, 256); err != nil {
				return err
			}
		}
	}

	switch input.MovementKind {
	case MovementKindFee:
		if input.ReversalOfMovementKey != "" || input.ConversionGroupKey != "" || input.ConversionLeg != "" || input.ConversionGroupState != "" {
			return fmt.Errorf("fee movement may only carry a parent movement link")
		}
	case MovementKindReversal:
		if input.ParentMovementKey != "" || input.ConversionGroupKey != "" || input.ConversionLeg != "" || input.ConversionGroupState != "" {
			return fmt.Errorf("reversal movement may only carry a reversal target link")
		}
	case MovementKindConversion:
		if input.ParentMovementKey != "" || input.ReversalOfMovementKey != "" {
			return fmt.Errorf("conversion movement may not carry parent or reversal links")
		}
	default:
		if input.hasProviderLinkage() {
			return fmt.Errorf("%s movement may not carry provider linkage", input.MovementKind)
		}
	}
	return nil
}

func (r *DBRepository) applyCompanyFundTransactionProviderLinkage(ctx context.Context, tx *sql.Tx, transactionID int64, input TransactionUpsertInput) error {
	if !input.hasProviderLinkage() {
		return nil
	}
	existing, err := loadCompanyFundTransactionProviderLinkage(ctx, tx, transactionID)
	if err != nil {
		return err
	}
	resolved, err := r.resolveCompanyFundTransactionProviderLinkage(ctx, tx, transactionID, input, existing)
	if err != nil {
		return err
	}
	if existing.equal(resolved) {
		return nil
	}

	var updatedID int64
	if err := tx.QueryRowContext(ctx, updateCompanyFundTransactionProviderLinkageSQL,
		transactionID,
		nullableInt64(resolved.ParentTransactionID),
		nullableInt64(resolved.ReversalOfTransactionID),
		nullableString(resolved.ConversionGroupKey),
		nullableConversionLeg(resolved.ConversionLeg),
		nullableConversionGroupState(resolved.ConversionGroupState),
	).Scan(&updatedID); err != nil {
		return fmt.Errorf("update company-fund transaction provider linkage: %w", err)
	}
	if updatedID != transactionID {
		return fmt.Errorf("provider linkage updated transaction %d, want %d", updatedID, transactionID)
	}
	return nil
}

func loadCompanyFundTransactionProviderLinkage(ctx context.Context, tx *sql.Tx, transactionID int64) (transactionProviderLinkage, error) {
	var (
		linkage     transactionProviderLinkage
		parentID    sql.NullInt64
		reversalID  sql.NullInt64
		groupKey    string
		leg         string
		groupStatus string
	)
	if err := tx.QueryRowContext(ctx, selectCompanyFundTransactionLinkageForUpdateSQL, transactionID).Scan(
		&parentID,
		&reversalID,
		&groupKey,
		&leg,
		&groupStatus,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return transactionProviderLinkage{}, fmt.Errorf("company-fund transaction %d does not exist for provider linkage", transactionID)
		}
		return transactionProviderLinkage{}, fmt.Errorf("load company-fund transaction provider linkage: %w", err)
	}
	if parentID.Valid {
		value := parentID.Int64
		linkage.ParentTransactionID = &value
	}
	if reversalID.Valid {
		value := reversalID.Int64
		linkage.ReversalOfTransactionID = &value
	}
	linkage.ConversionGroupKey = groupKey
	linkage.ConversionLeg = ConversionLeg(leg)
	linkage.ConversionGroupState = ConversionGroupState(groupStatus)
	return linkage, nil
}

func (r *DBRepository) resolveCompanyFundTransactionProviderLinkage(
	ctx context.Context,
	tx *sql.Tx,
	transactionID int64,
	input TransactionUpsertInput,
	existing transactionProviderLinkage,
) (transactionProviderLinkage, error) {
	parentID, err := r.resolveCompanyFundTransactionLinkTarget(ctx, tx, transactionID, input.Channel, input.MovementKey, "parent", input.ParentMovementKey)
	if err != nil {
		return transactionProviderLinkage{}, err
	}
	reversalID, err := r.resolveCompanyFundTransactionLinkTarget(ctx, tx, transactionID, input.Channel, input.MovementKey, "reversal", input.ReversalOfMovementKey)
	if err != nil {
		return transactionProviderLinkage{}, err
	}

	resolved := existing
	if resolved.ParentTransactionID, err = resolveImmutableTransactionLinkID("parent", existing.ParentTransactionID, parentID); err != nil {
		return transactionProviderLinkage{}, err
	}
	if resolved.ReversalOfTransactionID, err = resolveImmutableTransactionLinkID("reversal", existing.ReversalOfTransactionID, reversalID); err != nil {
		return transactionProviderLinkage{}, err
	}
	if resolved.ConversionGroupKey, err = resolveImmutableTransactionLinkString("conversion group", existing.ConversionGroupKey, input.ConversionGroupKey); err != nil {
		return transactionProviderLinkage{}, err
	}
	if resolved.ConversionLeg, err = resolveImmutableTransactionLinkLeg(existing.ConversionLeg, input.ConversionLeg); err != nil {
		return transactionProviderLinkage{}, err
	}
	resolved.ConversionGroupState = resolveConversionGroupState(existing.ConversionGroupState, input.ConversionGroupState)
	return resolved, nil
}

func (r *DBRepository) resolveCompanyFundTransactionLinkTarget(
	ctx context.Context,
	tx *sql.Tx,
	transactionID int64,
	channel Channel,
	movementKey string,
	label string,
	targetMovementKey string,
) (*int64, error) {
	if targetMovementKey == "" {
		return nil, nil
	}
	if targetMovementKey == movementKey {
		return nil, fmt.Errorf("%s movement link cannot reference itself", label)
	}
	var (
		targetID      int64
		targetChannel string
	)
	if err := tx.QueryRowContext(ctx, selectCompanyFundTransactionLinkTargetSQL, targetMovementKey).Scan(&targetID, &targetChannel); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%s movement prerequisite %q has not been persisted", label, targetMovementKey)
		}
		return nil, fmt.Errorf("resolve %s movement key %q: %w", label, targetMovementKey, err)
	}
	if targetID == transactionID {
		return nil, fmt.Errorf("%s movement link cannot reference itself", label)
	}
	if Channel(targetChannel) != channel {
		return nil, fmt.Errorf("%s movement target channel %q does not match transaction channel %q", label, targetChannel, channel)
	}
	return &targetID, nil
}

func resolveImmutableTransactionLinkID(label string, existing, incoming *int64) (*int64, error) {
	if incoming == nil {
		return copyTransactionLinkID(existing), nil
	}
	if existing == nil || *existing == *incoming {
		return copyTransactionLinkID(incoming), nil
	}
	return nil, fmt.Errorf("immutable %s movement link conflicts with stored transaction linkage", label)
}

func resolveImmutableTransactionLinkString(label, existing, incoming string) (string, error) {
	if incoming == "" {
		return existing, nil
	}
	if existing == "" || existing == incoming {
		return incoming, nil
	}
	return "", fmt.Errorf("immutable %s link conflicts with stored transaction linkage", label)
}

func resolveImmutableTransactionLinkLeg(existing, incoming ConversionLeg) (ConversionLeg, error) {
	if incoming == "" {
		return existing, nil
	}
	if existing == "" || existing == incoming {
		return incoming, nil
	}
	return "", fmt.Errorf("immutable conversion leg conflicts with stored transaction linkage")
}

func resolveConversionGroupState(existing, incoming ConversionGroupState) ConversionGroupState {
	if existing == ConversionGroupComplete || incoming == ConversionGroupComplete {
		return ConversionGroupComplete
	}
	if incoming != "" {
		return incoming
	}
	return existing
}

func (linkage transactionProviderLinkage) equal(other transactionProviderLinkage) bool {
	return transactionLinkIDsEqual(linkage.ParentTransactionID, other.ParentTransactionID) &&
		transactionLinkIDsEqual(linkage.ReversalOfTransactionID, other.ReversalOfTransactionID) &&
		linkage.ConversionGroupKey == other.ConversionGroupKey &&
		linkage.ConversionLeg == other.ConversionLeg &&
		linkage.ConversionGroupState == other.ConversionGroupState
}

func transactionLinkIDsEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func copyTransactionLinkID(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func nullableConversionLeg(value ConversionLeg) any {
	if strings.TrimSpace(string(value)) == "" {
		return nil
	}
	return value
}

func nullableConversionGroupState(value ConversionGroupState) any {
	if strings.TrimSpace(string(value)) == "" {
		return nil
	}
	return value
}
