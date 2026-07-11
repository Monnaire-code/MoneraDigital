package companyfund

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUpdateFinanceTransactionClassification_UpdatesOnlyManualFieldsAndAudit(t *testing.T) {
	db, mock := newFinanceMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	level1ID := int64(11)
	level2ID := int64(22)
	operating := true
	override := true
	applicant := "  finance@monera  "
	description := "  July vendor settlement  "
	updatedAt := time.Date(2026, time.July, 10, 7, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(updateFinanceTransactionClassificationSQL)).
		WithArgs(int64(71), level1ID, level2ID, operating, "finance@monera", "July vendor settlement", override, "finance-admin").
		WillReturnRows(sqlmock.NewRows(financeClassificationColumns()).AddRow(
			71, level1ID, level2ID, operating, "finance@monera", "July vendor settlement", override, "CLASSIFIED", "finance-admin", updatedAt,
		))
	mock.ExpectCommit()

	result, err := repository.UpdateFinanceTransactionClassification(context.Background(), FinanceClassificationUpdate{
		TransactionID:            71,
		FinanceCategoryLevel1ID:  &level1ID,
		FinanceCategoryLevel2ID:  &level2ID,
		IsOperatingIncomeExpense: &operating,
		Applicant:                &applicant,
		BusinessDescription:      &description,
		SummaryInclusionOverride: &override,
		UpdatedBy:                "finance-admin",
	})
	if err != nil || result.TransactionID != 71 || result.FinanceCategoryLevel1ID == nil || *result.FinanceCategoryLevel1ID != level1ID || result.SummaryInclusionOverride == nil || !*result.SummaryInclusionOverride || result.UpdatedBy != "finance-admin" || !result.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("UpdateFinanceTransactionClassification() = %#v, %v", result, err)
	}
	assertFinanceMockExpectations(t, mock)
}

func TestUpdateFinanceTransactionClassification_PropagatesDeferredCategoryHierarchyConstraint(t *testing.T) {
	db, mock := newFinanceMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	level2ID := int64(22)
	updatedAt := time.Date(2026, time.July, 10, 7, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(updateFinanceTransactionClassificationSQL)).
		WithArgs(int64(72), nil, level2ID, nil, nil, nil, nil, "finance-admin").
		WillReturnRows(sqlmock.NewRows(financeClassificationColumns()).AddRow(
			72, nil, level2ID, nil, "", "", nil, "CLASSIFIED", "finance-admin", updatedAt,
		))
	mock.ExpectCommit().WillReturnError(errors.New("finance_category_level2_id must reference a level 2 child"))

	if _, err := repository.UpdateFinanceTransactionClassification(context.Background(), FinanceClassificationUpdate{
		TransactionID:           72,
		FinanceCategoryLevel2ID: &level2ID,
		UpdatedBy:               "finance-admin",
	}); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("classification hierarchy commit error = %v", err)
	}
	assertFinanceMockExpectations(t, mock)
}

func TestFinanceClassificationAndProviderSQLOwnershipContracts(t *testing.T) {
	for _, required := range []string{
		"finance_category_level1_id = $2",
		"finance_category_level2_id = $3",
		"is_operating_income_expense = $4",
		"applicant = $5",
		"business_description = $6",
		"summary_inclusion_override = $7",
		"classification_updated_by = $8",
		"classification_updated_at = NOW()",
	} {
		if !strings.Contains(updateFinanceTransactionClassificationSQL, required) {
			t.Fatalf("manual classification SQL missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"provider_", "amount =", "currency =", "tx_hash", "auto_excluded_from_summary", "is_dust", "risk_", "aml_",
	} {
		if strings.Contains(updateFinanceTransactionClassificationSQL, forbidden) {
			t.Fatalf("manual classification SQL must not update provider-owned field %q", forbidden)
		}
	}
	for _, forbidden := range []string{
		"finance_category", "is_operating_income_expense", "applicant", "business_description", "summary_inclusion_override",
	} {
		if strings.Contains(updateCompanyFundTransactionSQL+updateCompanyFundTransactionProviderSupplementSQL, forbidden) {
			t.Fatalf("provider write SQL must not update manual field %q", forbidden)
		}
	}
	if !strings.Contains(updateCompanyFundTransactionProviderSupplementSQL, "auto_excluded_from_summary") {
		t.Fatal("provider supplement SQL must persist the automatic summary exclusion snapshot")
	}
}

func financeClassificationColumns() []string {
	return []string{
		"id", "finance_category_level1_id", "finance_category_level2_id", "is_operating_income_expense", "applicant", "business_description", "summary_inclusion_override", "classification_status", "classification_updated_by", "classification_updated_at",
	}
}
