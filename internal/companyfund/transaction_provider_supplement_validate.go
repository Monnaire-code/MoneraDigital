package companyfund

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

const (
	transactionSupplementNumericScale  int32 = 18
	transactionSupplementIntegerDigits       = 47 // NUMERIC(65,18)
)

func validateTransactionSupplementDecimal(label string, value *decimal.Decimal) error {
	if value == nil {
		return nil
	}
	if value.IsNegative() {
		return fmt.Errorf("%s must be non-negative", label)
	}
	if value.Exponent() < -transactionSupplementNumericScale {
		return fmt.Errorf("%s exceeds NUMERIC(65,18) fractional precision", label)
	}
	integerDigits := len(strings.TrimLeft(value.Truncate(0).Abs().String(), "0"))
	if integerDigits > transactionSupplementIntegerDigits {
		return fmt.Errorf("%s exceeds NUMERIC(65,18) integer precision", label)
	}
	return nil
}

func (value AMLScreeningState) valid() bool {
	switch value {
	case AMLScreeningStateNotScreened, AMLScreeningStatePending, AMLScreeningStateScreened,
		AMLScreeningStateReviewRequired, AMLScreeningStateCleared, AMLScreeningStateBlocked, AMLScreeningStateError:
		return true
	default:
		return false
	}
}

func (value AMLRiskLevel) valid() bool {
	return value == AMLRiskLevelUnknown || value == AMLRiskLevelLow || value == AMLRiskLevelMedium ||
		value == AMLRiskLevelHigh || value == AMLRiskLevelCritical
}

func (value RiskFlag) valid() bool {
	switch value {
	case RiskFlagDust, RiskFlagSourcePhishing, RiskFlagDestinationPhishing, RiskFlagUnrecognizedAsset,
		RiskFlagZeroAmount, RiskFlagAMLLock, RiskFlagAMLHigh, RiskFlagAMLCritical:
		return true
	default:
		return false
	}
}

func copyTransactionSupplementBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyTransactionSupplementInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyTransactionSupplementDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyAMLScreeningState(value *AMLScreeningState) *AMLScreeningState {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyAMLRiskLevel(value *AMLRiskLevel) *AMLRiskLevel {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func boolPointerValue(value *bool) bool {
	return value != nil && *value
}
