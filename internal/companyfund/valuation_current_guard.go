package companyfund

import "fmt"

// ValuationCurrentStateExpectation distinguishes an intentionally empty
// current projection from a legacy apply with no optimistic-concurrency guard.
// Jobs always persist one of these two concrete expectations.
type ValuationCurrentStateExpectation string

const (
	ValuationCurrentStateExpectationNone    ValuationCurrentStateExpectation = "NONE"
	ValuationCurrentStateExpectationHistory ValuationCurrentStateExpectation = "HISTORY"
)

func validateValuationApplyCurrentGuard(
	state *ValuationCurrentStateExpectation,
	historyID *int64,
	fingerprint string,
) error {
	if state == nil {
		if (historyID == nil) != (fingerprint == "") {
			return fmt.Errorf("expected current valuation history ID and dependency fingerprint must be supplied together")
		}
		if historyID == nil {
			return nil
		}
		return validateExpectedCurrentValuationHistory(historyID, fingerprint)
	}
	switch *state {
	case ValuationCurrentStateExpectationNone:
		if historyID != nil || fingerprint != "" {
			return fmt.Errorf("expected NONE current valuation state must not include a history pair")
		}
		return nil
	case ValuationCurrentStateExpectationHistory:
		if historyID == nil || fingerprint == "" {
			return fmt.Errorf("expected HISTORY current valuation state requires a history pair")
		}
		return validateExpectedCurrentValuationHistory(historyID, fingerprint)
	default:
		return fmt.Errorf("unsupported expected current valuation state %q", *state)
	}
}

func validateExpectedCurrentValuationHistory(historyID *int64, fingerprint string) error {
	if *historyID <= 0 {
		return fmt.Errorf("expected current valuation history ID must be positive")
	}
	if !isLowerSHA256Hex(fingerprint) {
		return fmt.Errorf("expected current valuation dependency fingerprint must be lowercase SHA-256 hex")
	}
	return nil
}
