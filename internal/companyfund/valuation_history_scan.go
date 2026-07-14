package companyfund

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

type valuationHistoryScanner interface {
	Scan(dest ...any) error
}

func scanCompanyFundValuationHistory(row valuationHistoryScanner) (CompanyFundValuationHistoryRecord, error) {
	var history CompanyFundValuationHistoryRecord
	var (
		usdValueText              sql.NullString
		providerReportedUSDText   sql.NullString
		calculatedUSDText         sql.NullString
		unitPriceText             sql.NullString
		status                    sql.NullString
		reason                    sql.NullString
		basis                     sql.NullString
		valuationTime             sql.NullTime
		priceAt                   sql.NullTime
		source                    sql.NullString
		method                    sql.NullString
		granularity               sql.NullString
		providerValueScope        sql.NullString
		derivationMethod          sql.NullString
		rateSnapshotID            sql.NullInt64
		providerTransactionFactID sql.NullInt64
		supersedesHistoryID       sql.NullInt64
	)
	if err := row.Scan(
		&history.ID,
		&history.TransactionID,
		&history.ValuationVersion,
		&usdValueText,
		&providerReportedUSDText,
		&calculatedUSDText,
		&unitPriceText,
		&status,
		&reason,
		&basis,
		&valuationTime,
		&priceAt,
		&source,
		&method,
		&granularity,
		&providerValueScope,
		&derivationMethod,
		&rateSnapshotID,
		&providerTransactionFactID,
		&history.DependencyFingerprint,
		&history.PolicyVersion,
		&history.TransitionTrigger,
		&supersedesHistoryID,
		&history.AppliedAt,
	); err != nil {
		return CompanyFundValuationHistoryRecord{}, err
	}
	var err error
	if history.USDValue, err = parseNullableValuationDecimal("persisted valuation USD value", usdValueText); err != nil {
		return CompanyFundValuationHistoryRecord{}, err
	}
	if history.ProviderReportedUSDValue, err = parseNullableValuationDecimal("persisted provider-reported USD value", providerReportedUSDText); err != nil {
		return CompanyFundValuationHistoryRecord{}, err
	}
	if history.CalculatedUSDValue, err = parseNullableValuationDecimal("persisted calculated USD value", calculatedUSDText); err != nil {
		return CompanyFundValuationHistoryRecord{}, err
	}
	if history.USDUnitPrice, err = parseNullableValuationDecimal("persisted valuation unit price", unitPriceText); err != nil {
		return CompanyFundValuationHistoryRecord{}, err
	}
	history.Status = USDValuationStatus(status.String)
	history.Reason = USDValuationReason(reason.String)
	history.Basis = USDValuationBasis(basis.String)
	history.Source = USDValuationSource(source.String)
	history.Method = USDValuationMethod(method.String)
	history.Granularity = granularity.String
	history.ProviderValueScope = ProviderValueScope(providerValueScope.String)
	history.DerivationMethod = ValuationDerivationMethod(derivationMethod.String)
	history.ValuationTime = nullableValuationTime(valuationTime)
	history.PriceAt = nullableValuationTime(priceAt)
	history.RateSnapshotID = nullableValuationID(rateSnapshotID)
	history.ProviderTransactionFactID = nullableValuationID(providerTransactionFactID)
	history.SupersedesHistoryID = nullableValuationID(supersedesHistoryID)
	return history, nil
}

func parseNullableValuationDecimal(label string, value sql.NullString) (*decimal.Decimal, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := decimal.NewFromString(value.String)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	if err := validateValuationDecimal(label, parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func nullableValuationTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

func nullableValuationID(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copy := value.Int64
	return &copy
}

func valuationDecimalArg(value *decimal.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func valuationUnitPriceArg(result USDValuationResult) any {
	if result.UnitPrice.IsZero() {
		return nil
	}
	return result.UnitPrice.String()
}
