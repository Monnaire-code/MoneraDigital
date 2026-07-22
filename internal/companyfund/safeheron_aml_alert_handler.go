package companyfund

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"monera-digital/internal/wallet/deposit"
)

const selectSafeheronCompanyFundTransactionForAMLAlertSQL = `
SELECT COUNT(*) AS company_case_count,
       COUNT(*) FILTER (WHERE company_fund_transaction_id IS NULL) AS pending_company_projection_count,
       COUNT(*) FILTER (WHERE requires_customer_projection AND deposit_id IS NULL) AS pending_customer_projection_count
FROM safeheron_transaction_routing_cases
WHERE safeheron_tx_key = $1
  AND requires_company_projection`

const updateSafeheronCompanyFundTransactionAMLAlertSQL = `
UPDATE company_fund_transactions AS transaction
SET aml_screening_state = $2,
    aml_risk_level = $3,
    last_synced_at = NOW(),
    updated_at = NOW()
WHERE transaction.channel = 'SAFEHERON'
  AND transaction.provider_transaction_id = $1
  AND EXISTS (
    SELECT 1
    FROM safeheron_transaction_routing_cases AS routing
    WHERE routing.safeheron_tx_key = $1
      AND routing.requires_company_projection
      AND routing.company_fund_transaction_id = transaction.id
  )`

var ErrInvalidSafeheronAMLAlertRisk = errors.New("invalid Safeheron AML alert risk")

// SafeheronAMLAlertInput is the normalized AML result emitted by a Safeheron
// AML_KYT_ALERT webhook. It contains no customer-deposit state.
type SafeheronAMLAlertInput = deposit.CompanyFundAMLAlertInput

// SafeheronAMLAlertResult distinguishes customer events from company events
// whose routing projection has not yet persisted a company transaction.
const (
	SafeheronAMLAlertNotCompany = deposit.CompanyFundAMLAlertNotCompany
	SafeheronAMLAlertDeferred   = deposit.CompanyFundAMLAlertDeferred
	SafeheronAMLAlertApplied    = deposit.CompanyFundAMLAlertApplied
)

type SafeheronAMLAlertResult = deposit.CompanyFundAMLAlertResult

// SafeheronAMLAlertHandler writes provider-owned AML facts to a routed company
// movement. It deliberately excludes every manual finance and risk-review field.
type SafeheronAMLAlertHandler struct {
	db SQLDatabase
}

func NewSafeheronAMLAlertHandler(db SQLDatabase) *SafeheronAMLAlertHandler {
	return &SafeheronAMLAlertHandler{db: db}
}

func (h *SafeheronAMLAlertHandler) HandleCompanyFundAMLAlert(ctx context.Context, input SafeheronAMLAlertInput) (SafeheronAMLAlertResult, error) {
	if h == nil || h.db == nil {
		return SafeheronAMLAlertNotCompany, nil
	}
	transactionKey := strings.TrimSpace(input.TransactionKey)
	if transactionKey == "" {
		return SafeheronAMLAlertNotCompany, nil
	}

	var companyCaseCount, pendingCompanyProjectionCount, pendingCustomerProjectionCount int64
	err := h.db.QueryRowContext(ctx, selectSafeheronCompanyFundTransactionForAMLAlertSQL, transactionKey).Scan(
		&companyCaseCount,
		&pendingCompanyProjectionCount,
		&pendingCustomerProjectionCount,
	)
	if err != nil {
		return SafeheronAMLAlertNotCompany, fmt.Errorf("find company transaction for Safeheron AML alert: %w", err)
	}
	if companyCaseCount == 0 {
		return SafeheronAMLAlertNotCompany, nil
	}
	if pendingCompanyProjectionCount > 0 || pendingCustomerProjectionCount > 0 {
		return SafeheronAMLAlertDeferred, nil
	}

	screeningState, riskLevel, err := normalizeSafeheronAMLAlertState(input.RiskLevel)
	if err != nil {
		return SafeheronAMLAlertNotCompany, err
	}
	result, err := h.db.ExecContext(ctx, updateSafeheronCompanyFundTransactionAMLAlertSQL,
		transactionKey, screeningState, riskLevel)
	if err != nil {
		return SafeheronAMLAlertNotCompany, fmt.Errorf("update company transaction AML alert: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return SafeheronAMLAlertNotCompany, fmt.Errorf("read company transaction AML update result: %w", err)
	}
	if updated == 0 {
		return SafeheronAMLAlertNotCompany, fmt.Errorf("company transaction AML update affected %d rows", updated)
	}
	return SafeheronAMLAlertApplied, nil
}

func normalizeSafeheronAMLAlertState(risk string) (AMLScreeningState, AMLRiskLevel, error) {
	switch strings.ToUpper(strings.TrimSpace(risk)) {
	case "UNKNOWN":
		return AMLScreeningStateScreened, AMLRiskLevelUnknown, nil
	case "LOW":
		return AMLScreeningStateScreened, AMLRiskLevelLow, nil
	case "MEDIUM":
		return AMLScreeningStateScreened, AMLRiskLevelMedium, nil
	case "HIGH":
		return AMLScreeningStateScreened, AMLRiskLevelHigh, nil
	case "SEVERE", "CRITICAL":
		return AMLScreeningStateScreened, AMLRiskLevelCritical, nil
	case "PENDING":
		return AMLScreeningStatePending, AMLRiskLevelUnknown, nil
	case "FAILED", "SKIPPED", "EMPTY":
		return AMLScreeningStateReviewRequired, AMLRiskLevelUnknown, nil
	default:
		return "", "", fmt.Errorf("%w: %q", ErrInvalidSafeheronAMLAlertRisk, risk)
	}
}
