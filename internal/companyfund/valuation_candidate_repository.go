package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

const companyFundValuationCandidateColumns = `
movement.id,
movement.channel,
movement.movement_kind,
movement.transaction_direction,
movement.currency,
movement.amount::TEXT,
COALESCE(movement.chain_code, ''),
COALESCE(movement.provider_asset_key, ''),
COALESCE(movement.asset_contract, ''),
movement.is_unrecognized_asset,
movement.from_company_fund_account_id,
movement.to_company_fund_account_id,
movement.occurred_at,
movement.completed_at,
movement.first_seen_at,
movement.provider_transaction_fact_id,
fact.provider_reported_usd_value::TEXT,
COALESCE(fact.value_scope, ''),
COALESCE(fact.allocation_state, ''),
COALESCE(fact.conversion_from_currency, ''),
COALESCE(fact.conversion_to_currency, ''),
movement.current_valuation_history_id,
COALESCE(current_history.dependency_fingerprint, ''),
COALESCE(current_history.usd_valuation_status, ''),
COALESCE(current_history.usd_valuation_source, '')`

const companyFundValuationCandidateFromSQL = `
FROM company_fund_transactions AS movement
LEFT JOIN company_fund_provider_transaction_facts AS fact
	ON fact.id = movement.provider_transaction_fact_id
LEFT JOIN company_fund_transaction_valuation_history AS current_history
	ON current_history.transaction_id = movement.id
	AND current_history.id = movement.current_valuation_history_id`

// OTHER accounts are bookkeeping-only. A manual movement linked to one must
// never acquire an automatic valuation, including through the repair sweep.
const companyFundValuationCandidateExcludesOtherAccountsSQL = `
	AND NOT EXISTS (
		SELECT 1
		FROM company_fund_accounts AS account
		WHERE account.channel = 'OTHER'
			AND (
				account.id = movement.from_company_fund_account_id
				OR account.id = movement.to_company_fund_account_id
			)
	)`

const selectCompanyFundTransactionValuationCandidateSQL = `
SELECT ` + companyFundValuationCandidateColumns + companyFundValuationCandidateFromSQL + `
WHERE movement.id = $1
	AND current_history.usd_valuation_source IS DISTINCT FROM 'MANUAL'` +
	companyFundValuationCandidateExcludesOtherAccountsSQL

// Repair candidates are precisely rows that have never received a valuation
// history or whose latest durable state says a retry can improve it. Completed
// current/provisional values are deliberately excluded so a periodic sweep
// cannot churn history just because a new current quote arrives.
const selectCompanyFundValuationRepairCandidatesSQL = `
SELECT ` + companyFundValuationCandidateColumns + companyFundValuationCandidateFromSQL + `
WHERE (
	movement.current_valuation_history_id IS NULL
	OR current_history.usd_valuation_status IN ('UNPRICED', 'STALE')
)
	AND current_history.usd_valuation_source IS DISTINCT FROM 'MANUAL'` +
	companyFundValuationCandidateExcludesOtherAccountsSQL + `
ORDER BY movement.first_seen_at, movement.id
LIMIT $1`

const selectCompanyFundValuationRepairCandidatesAfterSQL = `
SELECT ` + companyFundValuationCandidateColumns + companyFundValuationCandidateFromSQL + `
WHERE (
	movement.current_valuation_history_id IS NULL
	OR current_history.usd_valuation_status IN ('UNPRICED', 'STALE')
)
	AND current_history.usd_valuation_source IS DISTINCT FROM 'MANUAL'
	` + companyFundValuationCandidateExcludesOtherAccountsSQL + `
	AND movement.id > $1
ORDER BY movement.id
LIMIT $2`

func (r *DBRepository) GetCompanyFundTransactionValuationCandidate(ctx context.Context, transactionID int64) (*CompanyFundTransactionValuationCandidate, error) {
	if transactionID <= 0 {
		return nil, fmt.Errorf("company-fund valuation transaction ID must be positive")
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	candidate, err := scanCompanyFundTransactionValuationCandidate(r.db.QueryRowContext(ctx, selectCompanyFundTransactionValuationCandidateSQL, transactionID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read company-fund valuation candidate: %w", err)
	}
	return &candidate, nil
}

func (r *DBRepository) ListCompanyFundValuationRepairCandidates(ctx context.Context, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	normalizedLimit, err := normalizeCompanyFundValuationRepairLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, selectCompanyFundValuationRepairCandidatesSQL, normalizedLimit)
	if err != nil {
		return nil, fmt.Errorf("list company-fund valuation repair candidates: %w", err)
	}
	defer rows.Close()

	result := make([]CompanyFundTransactionValuationCandidate, 0)
	for rows.Next() {
		candidate, err := scanCompanyFundTransactionValuationCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate company-fund valuation repair candidates: %w", err)
	}
	return result, nil
}

// ListCompanyFundValuationRepairCandidatesAfter is the cursor form used by
// the process-local repair loop. The cursor does not change the durable
// selection contract; it only ensures one permanently unpriceable early row
// cannot consume every bounded sweep batch forever.
func (r *DBRepository) ListCompanyFundValuationRepairCandidatesAfter(ctx context.Context, afterID int64, limit int) ([]CompanyFundTransactionValuationCandidate, error) {
	if afterID < 0 {
		return nil, fmt.Errorf("company-fund valuation repair cursor must be non-negative")
	}
	normalizedLimit, err := normalizeCompanyFundValuationRepairLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, selectCompanyFundValuationRepairCandidatesAfterSQL, afterID, normalizedLimit)
	if err != nil {
		return nil, fmt.Errorf("list company-fund valuation repair candidates after cursor: %w", err)
	}
	defer rows.Close()

	result := make([]CompanyFundTransactionValuationCandidate, 0)
	for rows.Next() {
		candidate, err := scanCompanyFundTransactionValuationCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate company-fund valuation repair candidates after cursor: %w", err)
	}
	return result, nil
}

type companyFundValuationCandidateScanner interface {
	Scan(dest ...any) error
}

func scanCompanyFundTransactionValuationCandidate(scanner companyFundValuationCandidateScanner) (CompanyFundTransactionValuationCandidate, error) {
	var (
		candidate               CompanyFundTransactionValuationCandidate
		channel                 string
		movementKind            string
		direction               string
		amountText              string
		fromAccountID           sql.NullInt64
		toAccountID             sql.NullInt64
		occurredAt              sql.NullTime
		completedAt             sql.NullTime
		providerFactID          sql.NullInt64
		providerReportedUSDText sql.NullString
		providerValueScope      string
		providerAllocationState string
		currentHistoryID        sql.NullInt64
		currentDependency       string
		currentStatus           string
		currentSource           string
	)
	if err := scanner.Scan(
		&candidate.ID,
		&channel,
		&movementKind,
		&direction,
		&candidate.Currency,
		&amountText,
		&candidate.Asset.ChainCode,
		&candidate.Asset.ProviderAssetKey,
		&candidate.Asset.ContractAddress,
		&candidate.IsUnrecognizedAsset,
		&fromAccountID,
		&toAccountID,
		&occurredAt,
		&completedAt,
		&candidate.FirstSeenAt,
		&providerFactID,
		&providerReportedUSDText,
		&providerValueScope,
		&providerAllocationState,
		&candidate.AirwallexConversionFrom,
		&candidate.AirwallexConversionTo,
		&currentHistoryID,
		&currentDependency,
		&currentStatus,
		&currentSource,
	); err != nil {
		return CompanyFundTransactionValuationCandidate{}, err
	}
	amount, err := decimal.NewFromString(amountText)
	if err != nil {
		return CompanyFundTransactionValuationCandidate{}, fmt.Errorf("parse company-fund valuation candidate amount: %w", err)
	}
	candidate.Amount = amount
	candidate.Channel = Channel(channel)
	candidate.MovementKind = MovementKind(movementKind)
	candidate.Direction = Direction(direction)
	candidate.Asset.Currency = candidate.Currency
	candidate.FromCompanyFundAccountID = nullableCompanyFundValuationID(fromAccountID)
	candidate.ToCompanyFundAccountID = nullableCompanyFundValuationID(toAccountID)
	candidate.OccurredAt = nullableCompanyFundValuationTime(occurredAt)
	candidate.CompletedAt = nullableCompanyFundValuationTime(completedAt)
	candidate.ProviderTransactionFactID = nullableCompanyFundValuationID(providerFactID)
	providerReportedUSD, err := parseNullableValuationDecimal("company-fund valuation candidate provider USD", providerReportedUSDText)
	if err != nil {
		return CompanyFundTransactionValuationCandidate{}, err
	}
	candidate.ProviderReportedUSD = providerReportedUSD
	candidate.ProviderValueScope = ProviderValueScope(providerValueScope)
	candidate.ProviderAllocationState = ProviderFactAllocationState(providerAllocationState)
	candidate.CurrentValuationHistoryID = nullableCompanyFundValuationID(currentHistoryID)
	candidate.CurrentValuationDependencyFingerprint = currentDependency
	candidate.CurrentValuationStatus = USDValuationStatus(currentStatus)
	candidate.CurrentValuationSource = USDValuationSource(currentSource)
	if err := candidate.validate(); err != nil {
		return CompanyFundTransactionValuationCandidate{}, fmt.Errorf("invalid persisted company-fund valuation candidate: %w", err)
	}
	return candidate, nil
}

func (candidate CompanyFundTransactionValuationCandidate) validate() error {
	if candidate.ID <= 0 {
		return fmt.Errorf("candidate transaction ID must be positive")
	}
	if !candidate.Channel.Valid() || !candidate.MovementKind.Valid() || !candidate.Direction.Valid() {
		return fmt.Errorf("candidate has unsupported channel, movement kind, or direction")
	}
	if candidate.Amount.IsNegative() {
		return fmt.Errorf("candidate amount must be non-negative")
	}
	if candidate.FirstSeenAt.IsZero() {
		return fmt.Errorf("candidate first-seen time is required")
	}
	if candidate.ProviderTransactionFactID != nil && *candidate.ProviderTransactionFactID <= 0 {
		return fmt.Errorf("candidate provider transaction fact ID must be positive")
	}
	if candidate.ProviderReportedUSD != nil && candidate.ProviderReportedUSD.IsNegative() {
		return fmt.Errorf("candidate provider-reported USD must be non-negative")
	}
	if candidate.CurrentValuationHistoryID == nil {
		if candidate.CurrentValuationDependencyFingerprint != "" || candidate.CurrentValuationStatus != "" || candidate.CurrentValuationSource != "" {
			return fmt.Errorf("candidate has a current valuation field without current history")
		}
		return nil
	}
	if *candidate.CurrentValuationHistoryID <= 0 || !isLowerSHA256Hex(candidate.CurrentValuationDependencyFingerprint) || !validUSDValuationStatus(candidate.CurrentValuationStatus) || !validCompanyFundValuationCandidateSource(candidate.CurrentValuationStatus, candidate.CurrentValuationSource) {
		return fmt.Errorf("candidate current valuation state is invalid")
	}
	return nil
}

func validCompanyFundValuationCandidateSource(status USDValuationStatus, source USDValuationSource) bool {
	if source == "" {
		return status == USDValuationStatusUnpriced
	}
	return validUSDValuationSource(source)
}

func nullableCompanyFundValuationID(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func nullableCompanyFundValuationTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time.UTC()
	return &copy
}
