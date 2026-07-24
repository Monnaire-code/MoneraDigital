package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const financeTransactionDateSQL = "COALESCE(transaction.occurred_at, transaction.completed_at, transaction.created_at)"

const financeSummaryIncludedSQL = "COALESCE(transaction.summary_inclusion_override, NOT transaction.auto_excluded_from_summary)"

const financeTransactionFromSQL = `
FROM company_fund_transactions AS transaction
LEFT JOIN finance_categories AS category_level1
	ON category_level1.id = transaction.finance_category_level1_id
LEFT JOIN finance_categories AS category_level2
	ON category_level2.id = transaction.finance_category_level2_id
LEFT JOIN company_fund_accounts AS from_account
	ON from_account.id = transaction.from_company_fund_account_id
LEFT JOIN company_fund_accounts AS to_account
	ON to_account.id = transaction.to_company_fund_account_id`

const financeDashboardSelectSQL = `
SELECT transaction.transaction_direction,
	transaction.currency,
	COUNT(*)::BIGINT,
	COALESCE(SUM(transaction.amount), 0)::TEXT,
	SUM(transaction.usd_value)::TEXT,
	COUNT(*) FILTER (WHERE transaction.usd_value IS NULL)::BIGINT`

const financeDetailSelectSQL = `
SELECT transaction.id,
	` + financeTransactionDateSQL + `,
	transaction.channel,
	CASE WHEN transaction.transaction_direction = 'INFLOW'
		THEN COALESCE(transaction.to_company_entity_snapshot, to_account.company_entity, '')
		ELSE COALESCE(transaction.from_company_entity_snapshot, from_account.company_entity, '') END,
	CASE WHEN transaction.transaction_direction = 'INFLOW'
		THEN COALESCE(transaction.to_fund_account_name_snapshot, to_account.fund_account_name, '')
		ELSE COALESCE(transaction.from_fund_account_name_snapshot, from_account.fund_account_name, '') END,
	CASE WHEN transaction.transaction_direction = 'INFLOW'
		THEN COALESCE(transaction.to_sub_account_name_snapshot, to_account.sub_account_name, '')
		ELSE COALESCE(transaction.from_sub_account_name_snapshot, from_account.sub_account_name, '') END,
	CASE WHEN transaction.transaction_direction = 'INFLOW'
		THEN COALESCE(transaction.to_account_type_snapshot, to_account.account_type, '')
		ELSE COALESCE(transaction.from_account_type_snapshot, from_account.account_type, '') END,
	transaction.transaction_direction,
	transaction.transfer_mode,
	transaction.movement_kind,
	transaction.is_operating_income_expense,
	category_level1.id,
	COALESCE(category_level1.code, ''),
	COALESCE(category_level1.name, ''),
	category_level2.id,
	COALESCE(category_level2.code, ''),
	COALESCE(category_level2.name, ''),
	transaction.currency,
	transaction.amount::TEXT,
	transaction.usd_value::TEXT,
	transaction.provider_reported_fee_amount::TEXT,
	COALESCE(transaction.provider_reported_fee_currency, ''),
	COALESCE(transaction.payer_name, transaction.from_address_or_account, ''),
	COALESCE(transaction.payee_name, transaction.to_address_or_account, ''),
	transaction.counterparty_name_override,
	COALESCE(transaction.from_address_or_account, ''),
	COALESCE(transaction.to_address_or_account, ''),
	COALESCE(transaction.applicant, ''),
	COALESCE(transaction.business_description, ''),
	COALESCE(transaction.tx_hash, ''),
	COALESCE(transaction.provider_transaction_id, ''),
	` + financeSummaryIncludedSQL + `,
	transaction.is_dust,
	transaction.auto_excluded_from_summary,
	transaction.summary_inclusion_override`

// GetFinanceDashboard and ListFinanceTransactionDetails share
// buildFinanceTransactionWhere. Do not add one-off dashboard predicates: that
// would break the guarantee that a displayed total can be drilled into using
// the returned aggregate's filter.
func (r *DBRepository) GetFinanceDashboard(ctx context.Context, filter FinanceTransactionFilter) (FinanceDashboardSummary, error) {
	canonical, err := filter.canonical()
	if err != nil {
		return FinanceDashboardSummary{}, err
	}
	if err := r.requireDB(); err != nil {
		return FinanceDashboardSummary{}, err
	}

	where, args, _ := buildFinanceTransactionWhere(canonical, 1)
	query := financeDashboardSelectSQL + financeTransactionFromSQL + where + `
GROUP BY transaction.transaction_direction, transaction.currency
ORDER BY transaction.transaction_direction, transaction.currency`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return FinanceDashboardSummary{}, fmt.Errorf("query company-fund finance dashboard: %w", err)
	}
	defer rows.Close()

	summary := FinanceDashboardSummary{Filter: canonical, Aggregates: make([]FinanceDashboardAggregate, 0)}
	for rows.Next() {
		aggregate, err := scanFinanceDashboardAggregate(rows, canonical)
		if err != nil {
			return FinanceDashboardSummary{}, err
		}
		summary.Aggregates = append(summary.Aggregates, aggregate)
	}
	if err := rows.Err(); err != nil {
		return FinanceDashboardSummary{}, fmt.Errorf("iterate company-fund finance dashboard: %w", err)
	}
	return summary, nil
}

func (r *DBRepository) ListFinanceTransactionDetails(ctx context.Context, request FinanceTransactionDetailRequest) ([]FinanceTransactionDetail, error) {
	canonical, err := request.canonical()
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}

	where, args, nextPlaceholder := buildFinanceTransactionWhere(canonical.Filter, 1)
	args = append(args, canonical.Limit, canonical.Offset)
	query := financeDetailSelectSQL + financeTransactionFromSQL + where + fmt.Sprintf(`
ORDER BY %s DESC, transaction.id DESC
LIMIT $%d OFFSET $%d`, financeTransactionDateSQL, nextPlaceholder, nextPlaceholder+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query company-fund finance transaction details: %w", err)
	}
	defer rows.Close()

	details := make([]FinanceTransactionDetail, 0)
	for rows.Next() {
		detail, err := scanFinanceTransactionDetail(rows)
		if err != nil {
			return nil, err
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate company-fund finance transaction details: %w", err)
	}
	return details, nil
}

func buildFinanceTransactionWhere(filter FinanceTransactionFilter, firstPlaceholder int) (string, []any, int) {
	conditions := make([]string, 0, 10)
	args := make([]any, 0, 16)
	next := firstPlaceholder
	appendArgument := func(value any) string {
		placeholder := fmt.Sprintf("$%d", next)
		next++
		args = append(args, value)
		return placeholder
	}
	appendIn := func(column string, values []any) {
		placeholders := make([]string, 0, len(values))
		for _, value := range values {
			placeholders = append(placeholders, appendArgument(value))
		}
		conditions = append(conditions, column+" IN ("+strings.Join(placeholders, ", ")+")")
	}

	if filter.DateFrom != nil {
		conditions = append(conditions, financeTransactionDateSQL+" >= "+appendArgument(*filter.DateFrom))
	}
	if filter.DateTo != nil {
		conditions = append(conditions, financeTransactionDateSQL+" < "+appendArgument(*filter.DateTo))
	}
	if len(filter.Channels) > 0 {
		values := make([]any, 0, len(filter.Channels))
		for _, value := range filter.Channels {
			values = append(values, value)
		}
		appendIn("transaction.channel", values)
	}
	if len(filter.AccountIDs) > 0 {
		fromPlaceholders := make([]string, 0, len(filter.AccountIDs))
		toPlaceholders := make([]string, 0, len(filter.AccountIDs))
		for _, accountID := range filter.AccountIDs {
			fromPlaceholders = append(fromPlaceholders, appendArgument(accountID))
		}
		for _, accountID := range filter.AccountIDs {
			toPlaceholders = append(toPlaceholders, appendArgument(accountID))
		}
		conditions = append(conditions, "(transaction.from_company_fund_account_id IN ("+strings.Join(fromPlaceholders, ", ")+") OR transaction.to_company_fund_account_id IN ("+strings.Join(toPlaceholders, ", ")+"))")
	}
	if len(filter.Directions) > 0 {
		values := make([]any, 0, len(filter.Directions))
		for _, value := range filter.Directions {
			values = append(values, value)
		}
		appendIn("transaction.transaction_direction", values)
	}
	if len(filter.Currencies) > 0 {
		values := make([]any, 0, len(filter.Currencies))
		for _, value := range filter.Currencies {
			values = append(values, value)
		}
		appendIn("transaction.currency", values)
	}
	if len(filter.FinanceCategoryLevel1IDs) > 0 {
		values := make([]any, 0, len(filter.FinanceCategoryLevel1IDs))
		for _, value := range filter.FinanceCategoryLevel1IDs {
			values = append(values, value)
		}
		appendIn("transaction.finance_category_level1_id", values)
	}
	if len(filter.FinanceCategoryLevel2IDs) > 0 {
		values := make([]any, 0, len(filter.FinanceCategoryLevel2IDs))
		for _, value := range filter.FinanceCategoryLevel2IDs {
			values = append(values, value)
		}
		appendIn("transaction.finance_category_level2_id", values)
	}
	if filter.OperatingIncomeExpense != nil {
		conditions = append(conditions, "transaction.is_operating_income_expense = "+appendArgument(*filter.OperatingIncomeExpense))
	}
	if !filter.IncludeSummaryExcluded {
		conditions = append(conditions, financeSummaryIncludedSQL)
	}
	if len(conditions) == 0 {
		return "", args, next
	}
	return "\nWHERE " + strings.Join(conditions, "\n\tAND "), args, next
}

func scanFinanceDashboardAggregate(rows *sql.Rows, filter FinanceTransactionFilter) (FinanceDashboardAggregate, error) {
	var (
		aggregate FinanceDashboardAggregate
		direction string
		usdValue  sql.NullString
	)
	if err := rows.Scan(
		&direction,
		&aggregate.Currency,
		&aggregate.TransactionCount,
		&aggregate.Amount,
		&usdValue,
		&aggregate.UnpricedCount,
	); err != nil {
		return FinanceDashboardAggregate{}, fmt.Errorf("scan company-fund finance dashboard aggregate: %w", err)
	}
	aggregate.Direction = Direction(direction)
	if usdValue.Valid {
		value := usdValue.String
		aggregate.USDValue = &value
	}
	aggregate.Drilldown = financeAggregateDrilldown(filter, aggregate.Direction, aggregate.Currency)
	return aggregate, nil
}

func financeAggregateDrilldown(filter FinanceTransactionFilter, direction Direction, currency string) FinanceTransactionFilter {
	drilldown := filter
	drilldown.Directions = []Direction{direction}
	drilldown.Currencies = []string{currency}
	return drilldown
}

func scanFinanceTransactionDetail(rows *sql.Rows) (FinanceTransactionDetail, error) {
	var (
		detail                   FinanceTransactionDetail
		channel                  string
		direction                string
		transferMode             string
		movementKind             string
		operating                sql.NullBool
		level1ID                 sql.NullInt64
		level2ID                 sql.NullInt64
		usdValue                 sql.NullString
		feeAmount                sql.NullString
		counterpartyNameOverride sql.NullString
		summaryOverride          sql.NullBool
	)
	if err := rows.Scan(
		&detail.ID,
		&detail.Date,
		&channel,
		&detail.CompanyEntity,
		&detail.FundAccountName,
		&detail.SubAccountName,
		&detail.AccountType,
		&direction,
		&transferMode,
		&movementKind,
		&operating,
		&level1ID,
		&detail.FinanceCategoryLevel1Code,
		&detail.FinanceCategoryLevel1Name,
		&level2ID,
		&detail.FinanceCategoryLevel2Code,
		&detail.FinanceCategoryLevel2Name,
		&detail.Currency,
		&detail.Amount,
		&usdValue,
		&feeAmount,
		&detail.FeeCurrency,
		&detail.Payer,
		&detail.Payee,
		&counterpartyNameOverride,
		&detail.FromAddressOrAccount,
		&detail.ToAddressOrAccount,
		&detail.Applicant,
		&detail.BusinessDescription,
		&detail.TxHash,
		&detail.ProviderTransactionID,
		&detail.SummaryIncluded,
		&detail.IsDust,
		&detail.AutoExcludedFromSummary,
		&summaryOverride,
	); err != nil {
		return FinanceTransactionDetail{}, fmt.Errorf("scan company-fund finance transaction detail: %w", err)
	}
	detail.Channel = TransactionSource(channel)
	detail.Direction = Direction(direction)
	detail.TransferMode = TransferMode(transferMode)
	detail.MovementKind = MovementKind(movementKind)
	detail.IsOperatingIncomeExpense = financeNullBoolPointer(operating)
	detail.FinanceCategoryLevel1ID = financeNullInt64Pointer(level1ID)
	detail.FinanceCategoryLevel2ID = financeNullInt64Pointer(level2ID)
	detail.USDValue = financeNullStringPointer(usdValue)
	detail.FeeAmount = financeNullStringPointer(feeAmount)
	detail.CounterpartyNameOverride = financeNullStringPointer(counterpartyNameOverride)
	detail.EffectiveCounterpartyName = effectiveFinanceCounterpartyName(detail.Direction, detail.Payer, detail.Payee, detail.CounterpartyNameOverride)
	detail.SummaryInclusionOverride = financeNullBoolPointer(summaryOverride)
	return detail, nil
}

func effectiveFinanceCounterpartyName(direction Direction, payer, payee string, override *string) string {
	if override != nil {
		if value := strings.TrimSpace(*override); value != "" {
			return value
		}
	}
	payer = strings.TrimSpace(payer)
	payee = strings.TrimSpace(payee)
	switch direction {
	case DirectionInflow:
		return financeCounterpartyPart(payer)
	case DirectionOutflow:
		return financeCounterpartyPart(payee)
	case DirectionInternalTransfer:
		return financeCounterpartyPart(payer) + " → " + financeCounterpartyPart(payee)
	default:
		if payer != "" {
			return payer
		}
		return financeCounterpartyPart(payee)
	}
}

func financeCounterpartyPart(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func financeNullBoolPointer(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	copy := value.Bool
	return &copy
}

func financeNullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func financeNullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	copy := value.String
	return &copy
}
