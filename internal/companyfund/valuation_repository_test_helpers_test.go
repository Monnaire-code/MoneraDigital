package companyfund

import (
	"database/sql/driver"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func newCompanyFundValuationApplyInput() CompanyFundValuationApplyInput {
	valuationAt := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	priceAt := valuationAt.Add(-time.Minute)
	value := decimal.RequireFromString("2469.135780246913578024")
	calculated := value
	rateSnapshotID := int64(81)
	return CompanyFundValuationApplyInput{
		TransactionID:      71,
		CalculatedUSDValue: &calculated,
		RateSnapshotID:     &rateSnapshotID,
		Result: USDValuationResult{
			Value:             &value,
			UnitPrice:         decimal.RequireFromString("1234.567890123456789012"),
			Source:            USDValuationSourceCoinGecko,
			Method:            USDValuationMethodCoinGeckoDirect,
			Status:            USDValuationStatusFinal,
			Basis:             USDValuationBasisTransactionTime,
			ValuationTargetAt: &valuationAt,
			PriceAt:           &priceAt,
			Granularity:       "HOUR",
		},
		DerivationMethod:      ValuationDerivationMethodMarketPrice,
		DependencyFingerprint: strings.Repeat("a", 64),
		PolicyVersion:         "valuation-v1",
		TransitionTrigger:     "INITIAL_INGEST",
	}
}

func companyFundTransactionForValuationRows(id int64, currentHistoryID *int64, currentFingerprint string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "current_valuation_history_id", "dependency_fingerprint"}).AddRow(
		id,
		valuationTestID(currentHistoryID),
		currentFingerprint,
	)
}

func valuationHistoryFromInput(input CompanyFundValuationApplyInput, id, version int64, supersedes *int64) CompanyFundValuationHistoryRecord {
	return CompanyFundValuationHistoryRecord{
		ID:                        id,
		TransactionID:             input.TransactionID,
		ValuationVersion:          version,
		USDValue:                  input.Result.Value,
		ProviderReportedUSDValue:  input.Result.ProviderReportedUSD,
		CalculatedUSDValue:        input.CalculatedUSDValue,
		USDUnitPrice:              valuationUnitPricePointer(input.Result),
		Status:                    input.Result.Status,
		Reason:                    input.Result.Reason,
		Basis:                     input.Result.Basis,
		ValuationTime:             input.Result.ValuationTargetAt,
		PriceAt:                   input.Result.PriceAt,
		Source:                    input.Result.Source,
		Method:                    input.Result.Method,
		Granularity:               input.Result.Granularity,
		ProviderValueScope:        input.ProviderValueScope,
		DerivationMethod:          input.DerivationMethod,
		RateSnapshotID:            input.RateSnapshotID,
		ProviderTransactionFactID: input.ProviderTransactionFactID,
		DependencyFingerprint:     input.DependencyFingerprint,
		PolicyVersion:             input.PolicyVersion,
		TransitionTrigger:         input.TransitionTrigger,
		SupersedesHistoryID:       supersedes,
		AppliedAt:                 input.Result.ValuationTargetAt.UTC(),
	}
}

func companyFundValuationHistoryColumnNames() []string {
	return []string{
		"id", "transaction_id", "valuation_version", "usd_value", "provider_reported_usd_value",
		"calculated_usd_value", "usd_unit_price", "usd_valuation_status", "usd_valuation_reason_code",
		"usd_valuation_basis", "usd_valuation_time", "usd_valuation_price_at", "usd_valuation_source",
		"usd_valuation_method", "usd_valuation_granularity", "usd_provider_value_scope", "usd_derivation_method",
		"usd_rate_snapshot_id", "provider_transaction_fact_id", "dependency_fingerprint", "valuation_policy_version",
		"transition_trigger", "supersedes_history_id", "applied_at",
	}
}

func companyFundValuationHistoryRows(history CompanyFundValuationHistoryRecord) *sqlmock.Rows {
	return sqlmock.NewRows(companyFundValuationHistoryColumnNames()).AddRow(
		history.ID,
		history.TransactionID,
		history.ValuationVersion,
		valuationTestDecimal(history.USDValue),
		valuationTestDecimal(history.ProviderReportedUSDValue),
		valuationTestDecimal(history.CalculatedUSDValue),
		valuationTestDecimal(history.USDUnitPrice),
		string(history.Status),
		valuationTestString(string(history.Reason)),
		valuationTestString(string(history.Basis)),
		valuationTestTime(history.ValuationTime),
		valuationTestTime(history.PriceAt),
		valuationTestString(string(history.Source)),
		valuationTestString(string(history.Method)),
		valuationTestString(history.Granularity),
		valuationTestString(string(history.ProviderValueScope)),
		valuationTestString(string(history.DerivationMethod)),
		valuationTestID(history.RateSnapshotID),
		valuationTestID(history.ProviderTransactionFactID),
		history.DependencyFingerprint,
		history.PolicyVersion,
		history.TransitionTrigger,
		valuationTestID(history.SupersedesHistoryID),
		history.AppliedAt,
	)
}

func valuationHistoryInsertArgs(input CompanyFundValuationApplyInput, version int64, supersedes *int64) []driver.Value {
	return []driver.Value{
		input.TransactionID,
		version,
		valuationTestDecimal(input.Result.Value),
		valuationTestDecimal(input.Result.ProviderReportedUSD),
		valuationTestDecimal(input.CalculatedUSDValue),
		valuationTestDecimal(valuationUnitPricePointer(input.Result)),
		string(input.Result.Status),
		valuationTestString(string(input.Result.Reason)),
		valuationTestString(string(input.Result.Basis)),
		valuationTestTime(input.Result.ValuationTargetAt),
		valuationTestTime(input.Result.PriceAt),
		valuationTestString(string(input.Result.Source)),
		valuationTestString(string(input.Result.Method)),
		valuationTestString(input.Result.Granularity),
		valuationTestString(string(input.ProviderValueScope)),
		valuationTestString(string(input.DerivationMethod)),
		valuationTestID(input.RateSnapshotID),
		valuationTestID(input.ProviderTransactionFactID),
		input.DependencyFingerprint,
		input.PolicyVersion,
		input.TransitionTrigger,
		valuationTestID(supersedes),
	}
}

func valuationProjectionArgs(input CompanyFundValuationApplyInput, history CompanyFundValuationHistoryRecord) []driver.Value {
	return []driver.Value{
		input.TransactionID,
		valuationTestDecimal(input.Result.ProviderReportedUSD),
		valuationTestDecimal(input.CalculatedUSDValue),
		valuationTestDecimal(input.Result.Value),
		valuationTestDecimal(valuationUnitPricePointer(input.Result)),
		string(input.Result.Status),
		valuationTestString(string(input.Result.Reason)),
		valuationTestString(string(input.Result.Basis)),
		valuationTestTime(input.Result.ValuationTargetAt),
		valuationTestTime(input.Result.PriceAt),
		valuationTestString(string(input.Result.Source)),
		valuationTestString(string(input.Result.Method)),
		valuationTestString(input.Result.Granularity),
		valuationTestString(string(input.ProviderValueScope)),
		valuationTestString(string(input.DerivationMethod)),
		valuationTestID(input.RateSnapshotID),
		history.ID,
		input.PolicyVersion,
		history.ValuationVersion,
	}
}

func valuationTestDecimal(value *decimal.Decimal) driver.Value {
	if value == nil {
		return nil
	}
	return value.String()
}

func valuationTestTime(value *time.Time) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func valuationTestID(value *int64) driver.Value {
	if value == nil {
		return nil
	}
	return *value
}

func valuationTestString(value string) driver.Value {
	if value == "" {
		return nil
	}
	return value
}
