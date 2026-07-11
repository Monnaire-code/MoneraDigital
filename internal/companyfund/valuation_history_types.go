package companyfund

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	maxValuationPolicyVersionBytes       = 64
	maxValuationTriggerBytes             = 64
	maxValuationReasonBytes              = 64
	maxValuationGranularityBytes         = 16
	valuationNumericScale          int32 = 18
	valuationIntegerDigits               = 47 // NUMERIC(65,18)
)

// ValuationDerivationMethod records how a movement-level value was selected;
// it is distinct from the provider value scope retained for audit.
type ValuationDerivationMethod string

const (
	ValuationDerivationMethodDirectItem        ValuationDerivationMethod = "DIRECT_ITEM"
	ValuationDerivationMethodDerivedFromParent ValuationDerivationMethod = "DERIVED_FROM_PARENT"
	ValuationDerivationMethodMarketPrice       ValuationDerivationMethod = "MARKET_PRICE"
)

// CompanyFundValuationApplyInput is the durable boundary between deterministic
// valuation calculation and persistence. This repository never fetches rates
// or computes a value; it records this supplied result atomically.
type CompanyFundValuationApplyInput struct {
	TransactionID int64
	// ExpectedCurrentHistoryID and ExpectedCurrentDependencyFingerprint are an
	// optional worker guard. ExpectedCurrentState=NONE means the worker expects
	// no current history; HISTORY means it expects the supplied pair. A nil
	// state retains compatibility for direct applies without a guard.
	ExpectedCurrentState                 *ValuationCurrentStateExpectation
	ExpectedCurrentHistoryID             *int64
	ExpectedCurrentDependencyFingerprint string
	Result                               USDValuationResult
	CalculatedUSDValue                   *decimal.Decimal
	RateSnapshotID                       *int64
	ProviderTransactionFactID            *int64
	ProviderValueScope                   ProviderValueScope
	DerivationMethod                     ValuationDerivationMethod
	DependencyFingerprint                string
	PolicyVersion                        string
	TransitionTrigger                    string
}

type CompanyFundValuationApplyResult struct {
	History  CompanyFundValuationHistoryRecord
	Inserted bool
	// Superseded means the optional expected-current guard no longer matched;
	// no history or transaction projection row was changed.
	Superseded bool
}

// CompanyFundValuationHistoryRecord exposes the append-only audit row without
// pretending nullable values are numeric zeroes.
type CompanyFundValuationHistoryRecord struct {
	ID                        int64
	TransactionID             int64
	ValuationVersion          int64
	USDValue                  *decimal.Decimal
	ProviderReportedUSDValue  *decimal.Decimal
	CalculatedUSDValue        *decimal.Decimal
	USDUnitPrice              *decimal.Decimal
	Status                    USDValuationStatus
	Reason                    USDValuationReason
	Basis                     USDValuationBasis
	ValuationTime             *time.Time
	PriceAt                   *time.Time
	Source                    USDValuationSource
	Method                    USDValuationMethod
	Granularity               string
	ProviderValueScope        ProviderValueScope
	DerivationMethod          ValuationDerivationMethod
	RateSnapshotID            *int64
	ProviderTransactionFactID *int64
	DependencyFingerprint     string
	PolicyVersion             string
	TransitionTrigger         string
	SupersedesHistoryID       *int64
	AppliedAt                 time.Time
}

func (input CompanyFundValuationApplyInput) validate() error {
	if input.TransactionID <= 0 {
		return fmt.Errorf("valuation transaction ID must be positive")
	}
	if err := validateValuationApplyCurrentGuard(
		input.ExpectedCurrentState,
		input.ExpectedCurrentHistoryID,
		input.ExpectedCurrentDependencyFingerprint,
	); err != nil {
		return err
	}
	if !isLowerSHA256Hex(input.DependencyFingerprint) {
		return fmt.Errorf("valuation dependency fingerprint must be lowercase SHA-256 hex")
	}
	if err := validateRequiredString("valuation policy version", input.PolicyVersion, maxValuationPolicyVersionBytes); err != nil {
		return err
	}
	if err := validateRequiredString("valuation transition trigger", input.TransitionTrigger, maxValuationTriggerBytes); err != nil {
		return err
	}
	if input.RateSnapshotID != nil && *input.RateSnapshotID <= 0 {
		return fmt.Errorf("valuation rate snapshot ID must be positive")
	}
	if input.ProviderTransactionFactID != nil && *input.ProviderTransactionFactID <= 0 {
		return fmt.Errorf("valuation provider transaction fact ID must be positive")
	}
	if err := validateValuationResult(input.Result, input.CalculatedUSDValue); err != nil {
		return err
	}
	if input.ProviderValueScope != "" && !validPersistedProviderValueScope(input.ProviderValueScope) {
		return fmt.Errorf("unsupported persisted provider value scope %q", input.ProviderValueScope)
	}
	if input.DerivationMethod != "" && !input.DerivationMethod.valid() {
		return fmt.Errorf("unsupported valuation derivation method %q", input.DerivationMethod)
	}
	return nil
}

func validateValuationResult(result USDValuationResult, calculatedUSDValue *decimal.Decimal) error {
	if !validUSDValuationStatus(result.Status) {
		return fmt.Errorf("unsupported valuation status %q", result.Status)
	}
	if result.Source != "" && !validUSDValuationSource(result.Source) {
		return fmt.Errorf("unsupported valuation source %q", result.Source)
	}
	if result.Method != "" && !validUSDValuationMethod(result.Method) {
		return fmt.Errorf("unsupported valuation method %q", result.Method)
	}
	if result.Basis != "" && !validUSDValuationBasis(result.Basis) {
		return fmt.Errorf("unsupported valuation basis %q", result.Basis)
	}
	if err := validateOptionalValuationString("valuation reason", string(result.Reason), maxValuationReasonBytes); err != nil {
		return err
	}
	if err := validateOptionalValuationString("valuation granularity", result.Granularity, maxValuationGranularityBytes); err != nil {
		return err
	}
	for _, field := range []struct {
		label string
		value *decimal.Decimal
	}{
		{"valuation USD value", result.Value},
		{"valuation provider-reported USD value", result.ProviderReportedUSD},
		{"valuation calculated USD value", calculatedUSDValue},
	} {
		if err := validateNullableValuationDecimal(field.label, field.value); err != nil {
			return err
		}
	}
	if !result.UnitPrice.IsZero() {
		if err := validateValuationDecimal("valuation unit price", result.UnitPrice); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		label string
		value *time.Time
	}{
		{"valuation time", result.ValuationTargetAt},
		{"valuation price time", result.PriceAt},
	} {
		if field.value != nil && field.value.IsZero() {
			return fmt.Errorf("%s cannot be zero", field.label)
		}
	}
	switch result.Status {
	case USDValuationStatusFinal, USDValuationStatusProvisional:
		if result.Value == nil || !result.UnitPrice.IsPositive() {
			return fmt.Errorf("priced valuation requires a value and positive unit price")
		}
	case USDValuationStatusUnpriced, USDValuationStatusStale:
		if result.Value != nil || calculatedUSDValue != nil || !result.UnitPrice.IsZero() {
			return fmt.Errorf("%s valuation must not persist a synthetic USD value or unit price", result.Status)
		}
	}
	return nil
}

func validUSDValuationStatus(status USDValuationStatus) bool {
	return status == USDValuationStatusFinal || status == USDValuationStatusProvisional ||
		status == USDValuationStatusUnpriced || status == USDValuationStatusStale
}

func validUSDValuationSource(source USDValuationSource) bool {
	return source == USDValuationSourceUSDPar || source == USDValuationSourceSafeheron ||
		source == USDValuationSourceAirwallex || source == USDValuationSourceCoinGecko
}

func validUSDValuationMethod(method USDValuationMethod) bool {
	switch method {
	case USDValuationMethodUSDPar, USDValuationMethodProviderTransaction,
		USDValuationMethodProviderConversion, USDValuationMethodCoinGeckoDirect,
		USDValuationMethodCoinGeckoBTCCross:
		return true
	default:
		return false
	}
}

func validUSDValuationBasis(basis USDValuationBasis) bool {
	return basis == USDValuationBasisTransactionTime || basis == USDValuationBasisIngestionTime
}

func validPersistedProviderValueScope(scope ProviderValueScope) bool {
	return scope == ProviderValueScopeTransactionTotal || scope == ProviderValueScopeDirectItem ||
		scope == ProviderValueScopeConversionGroup
}

func (method ValuationDerivationMethod) valid() bool {
	return method == ValuationDerivationMethodDirectItem ||
		method == ValuationDerivationMethodDerivedFromParent ||
		method == ValuationDerivationMethodMarketPrice
}

func validateNullableValuationDecimal(label string, value *decimal.Decimal) error {
	if value == nil {
		return nil
	}
	return validateValuationDecimal(label, *value)
}

func validateValuationDecimal(label string, value decimal.Decimal) error {
	if value.IsNegative() {
		return fmt.Errorf("%s must be non-negative", label)
	}
	if value.Exponent() < -valuationNumericScale {
		return fmt.Errorf("%s exceeds NUMERIC(65,18) fractional precision", label)
	}
	integerDigits := len(strings.TrimLeft(value.Truncate(0).Abs().String(), "0"))
	if integerDigits > valuationIntegerDigits {
		return fmt.Errorf("%s exceeds NUMERIC(65,18) integer precision", label)
	}
	return nil
}

func validateOptionalValuationString(label, value string, maxBytes int) error {
	if value == "" {
		return nil
	}
	return validateRequiredString(label, value, maxBytes)
}
