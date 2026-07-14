package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// updateFinanceTransactionClassificationSQL is the only write path for
// finance-owned transaction data. Provider upsert and supplement SQL must not
// gain any of these columns; category hierarchy remains enforced by the
// existing deferred database constraint trigger.
const updateFinanceTransactionClassificationSQL = `
UPDATE company_fund_transactions
SET finance_category_level1_id = $2,
	finance_category_level2_id = $3,
	is_operating_income_expense = $4,
	applicant = $5,
	business_description = $6,
	summary_inclusion_override = $7,
	classification_status = CASE
		WHEN $2 IS NULL AND $3 IS NULL THEN 'UNCLASSIFIED'
		ELSE 'CLASSIFIED'
	END,
	classification_updated_by = $8,
	classification_updated_at = NOW(),
	updated_at = NOW()
WHERE id = $1
RETURNING id,
	finance_category_level1_id,
	finance_category_level2_id,
	is_operating_income_expense,
	COALESCE(applicant, ''),
	COALESCE(business_description, ''),
	summary_inclusion_override,
	classification_status,
	classification_updated_by,
	classification_updated_at`

// UpdateFinanceTransactionClassification applies a complete finance-owned
// classification update inside an explicit transaction. The existing category
// hierarchy constraint trigger is intentionally authoritative; this method
// does not recreate its category-parent logic in Go.
func (r *DBRepository) UpdateFinanceTransactionClassification(ctx context.Context, input FinanceClassificationUpdate) (FinanceClassificationResult, error) {
	canonical, err := input.canonical()
	if err != nil {
		return FinanceClassificationResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return FinanceClassificationResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FinanceClassificationResult{}, fmt.Errorf("begin finance transaction classification update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	result, err := scanFinanceClassificationResult(tx.QueryRowContext(ctx, updateFinanceTransactionClassificationSQL,
		canonical.TransactionID,
		nullableInt64(canonical.FinanceCategoryLevel1ID),
		nullableInt64(canonical.FinanceCategoryLevel2ID),
		nullableFinanceBool(canonical.IsOperatingIncomeExpense),
		nullableStringPointer(canonical.Applicant),
		nullableStringPointer(canonical.BusinessDescription),
		nullableFinanceBool(canonical.SummaryInclusionOverride),
		canonical.UpdatedBy,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FinanceClassificationResult{}, fmt.Errorf("company-fund transaction %d does not exist", canonical.TransactionID)
		}
		return FinanceClassificationResult{}, fmt.Errorf("update finance transaction classification: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return FinanceClassificationResult{}, fmt.Errorf("commit finance transaction classification update: %w", err)
	}
	committed = true
	return result, nil
}

func scanFinanceClassificationResult(row *sql.Row) (FinanceClassificationResult, error) {
	var (
		result          FinanceClassificationResult
		level1ID        sql.NullInt64
		level2ID        sql.NullInt64
		operating       sql.NullBool
		summaryOverride sql.NullBool
	)
	if err := row.Scan(
		&result.TransactionID,
		&level1ID,
		&level2ID,
		&operating,
		&result.Applicant,
		&result.BusinessDescription,
		&summaryOverride,
		&result.ClassificationStatus,
		&result.UpdatedBy,
		&result.UpdatedAt,
	); err != nil {
		return FinanceClassificationResult{}, err
	}
	result.FinanceCategoryLevel1ID = financeNullInt64Pointer(level1ID)
	result.FinanceCategoryLevel2ID = financeNullInt64Pointer(level2ID)
	result.IsOperatingIncomeExpense = financeNullBoolPointer(operating)
	result.SummaryInclusionOverride = financeNullBoolPointer(summaryOverride)
	return result, nil
}

func nullableFinanceBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

var _ CompanyFundFinanceStore = (*DBRepository)(nil)
