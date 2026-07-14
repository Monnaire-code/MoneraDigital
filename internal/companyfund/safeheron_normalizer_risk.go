package companyfund

import (
	"encoding/json"
	"fmt"
	"strings"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

func safeheronAutomaticRisk(snapshot safeheron.TransactionSnapshot, destinationPhishing *bool, policy AccountAssetPolicy, recognized bool, risk RiskAssessment, amlRiskLevel AMLRiskLevel, amlRiskKnown bool) ProviderAutomaticRiskInput {
	flags := append([]RiskFlag(nil), risk.Flags...)
	autoExcluded := risk.AutomaticExclusion
	result := ProviderAutomaticRiskInput{
		IsSourcePhishing:        safeheronTruePointer(snapshot.IsSourcePhishing),
		IsDestinationPhishing:   destinationPhishing,
		IsUnrecognizedAsset:     safeheronBoolPointer(!recognized),
		AMLLock:                 safeheronAMLLock(snapshot.AmlLock),
		AMLScreeningState:       safeheronAMLScreeningState(snapshot.AMLScreeningTriggeredState, snapshot.AMLList),
		RiskFlags:               &flags,
		AutoExcludedFromSummary: &autoExcluded,
	}
	if amlRiskKnown {
		levelCopy := amlRiskLevel
		result.AMLRiskLevel = &levelCopy
	}
	if policy.Dust.Enabled && policy.Dust.Threshold != nil {
		policyID := policy.Dust.ID
		if policyID <= 0 {
			policyID = policy.ID
		}
		if policyID > 0 {
			thresholdCopy := *policy.Dust.Threshold
			isDust := risk.IsDust
			result.IsDust = &isDust
			result.DustPolicyID = &policyID
			result.DustThreshold = &thresholdCopy
		}
	}
	return result
}

func safeheronAMLLock(value string) *bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "YES":
		return safeheronBoolPointer(true)
	case "NO":
		return safeheronBoolPointer(false)
	default:
		return nil
	}
}

func safeheronAMLScreeningState(triggered string, records []safeheron.TransactionAMLRecord) *AMLScreeningState {
	if len(records) > 0 {
		state := AMLScreeningStateScreened
		return &state
	}
	switch strings.ToUpper(strings.TrimSpace(triggered)) {
	case "UNTRIGGERED":
		state := AMLScreeningStateNotScreened
		return &state
	case "IN_PROGRESS", "TRIGGERED":
		state := AMLScreeningStatePending
		return &state
	default:
		return nil
	}
}

func safeheronAMLRiskLevel(records []safeheron.TransactionAMLRecord) (AMLRiskLevel, bool) {
	highest := AMLRiskLevelUnknown
	highestRank := -1
	for _, record := range records {
		level, rank, ok := safeheronAMLRiskLevelValue(record.RiskLevel)
		if ok && rank > highestRank {
			highest = level
			highestRank = rank
		}
	}
	return highest, highestRank >= 0
}

func safeheronAMLRiskLevelValue(value string) (AMLRiskLevel, int, bool) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "UNKNOWN":
		return AMLRiskLevelUnknown, 0, true
	case "LOW":
		return AMLRiskLevelLow, 1, true
	case "MEDIUM":
		return AMLRiskLevelMedium, 2, true
	case "HIGH":
		return AMLRiskLevelHigh, 3, true
	case "SEVERE", "CRITICAL":
		return AMLRiskLevelCritical, 4, true
	default:
		return AMLRiskLevelUnknown, 0, false
	}
}

func safeheronBoolPointer(value bool) *bool {
	copy := value
	return &copy
}

func safeheronDirectProviderUSD(snapshot safeheron.TransactionSnapshot, kind MovementKind, transferMode TransferMode) (*decimal.Decimal, error) {
	if kind != MovementKindPrincipal || transferMode != TransferModeSingle || strings.TrimSpace(snapshot.TxAmountToUSD) == "" {
		return nil, nil
	}
	value, err := parseSafeheronAmount("Safeheron transaction USD amount", snapshot.TxAmountToUSD, true)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func safeheronTransactionFeeDisplay(snapshot safeheron.TransactionSnapshot, mapping *SafeheronAssetMapping, companyPaysFee bool) (*safeheronFeeDisplay, error) {
	feeText := strings.TrimSpace(snapshot.TxFee)
	feeCoinKey := strings.TrimSpace(snapshot.FeeCoinKey)
	if feeText == "" && feeCoinKey == "" && len(snapshot.GasFee) == 0 {
		return nil, nil
	}
	detailsJSON, err := safeheronFeeAuditDetails(snapshot)
	if err != nil {
		return nil, err
	}
	result := &safeheronFeeDisplay{detailsJSON: detailsJSON}
	if feeText == "" {
		if companyPaysFee && safeheronHasPositiveGasFee(snapshot.GasFee) {
			return nil, fmt.Errorf("Safeheron gas fee detail has an amount but transaction txFee is absent")
		}
		return result, nil
	}
	amount, err := parseSafeheronAmount("Safeheron transaction fee", feeText, true)
	if err != nil {
		return nil, err
	}
	result.amount = &amount
	if amount.IsZero() && companyPaysFee && safeheronHasPositiveGasFee(snapshot.GasFee) {
		return nil, fmt.Errorf("Safeheron gas fee detail has an amount but transaction txFee is zero")
	}
	if feeCoinKey == "" || mapping == nil {
		if companyPaysFee && !amount.IsZero() {
			return nil, fmt.Errorf("Safeheron non-zero transaction fee requires an explicit fee-coin mapping")
		}
		return result, nil
	}
	asset, err := normalizeSafeheronAssetMapping(feeCoinKey, *mapping, "fee")
	if err != nil {
		if companyPaysFee && !amount.IsZero() {
			return nil, err
		}
		return result, nil
	}
	result.asset = &asset
	return result, nil
}

func safeheronFeeAuditDetails(snapshot safeheron.TransactionSnapshot) (json.RawMessage, error) {
	if err := validateSafeheronGasFees(snapshot.GasFee); err != nil {
		return nil, err
	}
	feeText := strings.TrimSpace(snapshot.TxFee)
	if feeText != "" {
		if _, err := parseSafeheronAmount("Safeheron transaction fee", feeText, true); err != nil {
			return nil, err
		}
	}
	detailsJSON, err := json.Marshal(struct {
		TxFee      string                        `json:"txFee,omitempty"`
		FeeCoinKey string                        `json:"feeCoinKey,omitempty"`
		GasFee     []safeheron.TransactionGasFee `json:"gasFee,omitempty"`
	}{TxFee: feeText, FeeCoinKey: strings.TrimSpace(snapshot.FeeCoinKey), GasFee: snapshot.GasFee})
	if err != nil {
		return nil, fmt.Errorf("encode Safeheron fee details: %w", err)
	}
	return detailsJSON, nil
}

func validateSafeheronGasFees(fees []safeheron.TransactionGasFee) error {
	for index, fee := range fees {
		if strings.TrimSpace(fee.Symbol) == "" {
			return fmt.Errorf("Safeheron gas fee %d symbol is required", index)
		}
		if _, err := parseSafeheronAmount(fmt.Sprintf("Safeheron gas fee %d amount", index), fee.Amount, true); err != nil {
			return err
		}
	}
	return nil
}

func safeheronHasPositiveGasFee(fees []safeheron.TransactionGasFee) bool {
	for _, fee := range fees {
		value, err := decimal.NewFromString(strings.TrimSpace(fee.Amount))
		if err == nil && value.IsPositive() {
			return true
		}
	}
	return false
}
