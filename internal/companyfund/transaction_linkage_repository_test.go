package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestUpsertCompanyFundTransaction_PersistsFeeParentFromExplicitMovementKey(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validLinkedFeeTransactionInput("v1:fee-child", "v1:principal-parent")

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs(input.MovementKey).
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(501))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkageForUpdateSQL)).
		WithArgs(int64(501)).
		WillReturnRows(companyFundTransactionLinkageRows(nil, nil, "", "", ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkTargetSQL)).
		WithArgs(input.ParentMovementKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel"}).AddRow(401, "SAFEHERON"))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderLinkageSQL)).
		WithArgs(int64(501), int64(401), nil, nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(501))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || !result.Inserted || result.ID != 501 {
		t.Fatalf("UpsertCompanyFundTransaction() = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_RequiresPersistedFeeParentWithoutGuessing(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validLinkedFeeTransactionInput("v1:fee-child-missing-parent", "v1:principal-parent-missing")

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs(input.MovementKey).
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(502))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkageForUpdateSQL)).
		WithArgs(int64(502)).
		WillReturnRows(companyFundTransactionLinkageRows(nil, nil, "", "", ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkTargetSQL)).
		WithArgs(input.ParentMovementKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel"}))
	mock.ExpectRollback()

	if _, err := repository.UpsertCompanyFundTransaction(context.Background(), input); err == nil || !strings.Contains(err.Error(), "prerequisite") {
		t.Fatalf("UpsertCompanyFundTransaction() error = %v, want unresolved parent prerequisite", err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_PersistsExplicitReversalTargetByMovementKey(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	input := validLinkedReversalTransactionInput("v1:reversal-child", "v1:original-parent")

	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs(input.MovementKey).
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(503))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkageForUpdateSQL)).
		WithArgs(int64(503)).
		WillReturnRows(companyFundTransactionLinkageRows(nil, nil, "", "", ""))
	mock.ExpectQuery(regexp.QuoteMeta(selectCompanyFundTransactionLinkTargetSQL)).
		WithArgs(input.ReversalOfMovementKey).
		WillReturnRows(sqlmock.NewRows([]string{"id", "channel"}).AddRow(402, "SAFEHERON"))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderLinkageSQL)).
		WithArgs(int64(503), nil, int64(402), nil, nil, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(503))
	mock.ExpectCommit()

	result, err := repository.UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || !result.Inserted || result.ID != 503 {
		t.Fatalf("UpsertCompanyFundTransaction() = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestUpsertCompanyFundTransaction_RejectsMissingFeeParentBeforeDatabaseUse(t *testing.T) {
	input := validLinkedFeeTransactionInput("v1:fee-without-parent", "")
	if _, err := NewDBRepository(nil).UpsertCompanyFundTransaction(context.Background(), input); err == nil || !strings.Contains(err.Error(), "parent movement") {
		t.Fatalf("fee without parent error = %v, want validation failure", err)
	}
}

func TestTransactionProviderLinkageSQLExcludesManualFieldsAndResolvesOnlyMovementKeys(t *testing.T) {
	for _, required := range []string{
		"SELECT id, channel",
		"WHERE movement_key = $1",
		"FOR KEY SHARE",
		"parent_transaction_id = $2",
		"reversal_of_transaction_id = $3",
		"conversion_group_key = $4",
		"conversion_leg = $5",
		"conversion_group_status = $6",
	} {
		if !strings.Contains(selectCompanyFundTransactionLinkTargetSQL+updateCompanyFundTransactionProviderLinkageSQL, required) {
			t.Fatalf("provider linkage SQL missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"provider_account_key =",
		"provider_org_key",
		"finance_category",
		"is_operating_income_expense",
		"classification_",
		"risk_override",
		"risk_status",
	} {
		if strings.Contains(updateCompanyFundTransactionProviderLinkageSQL, forbidden) {
			t.Fatalf("provider linkage SQL must not write %q", forbidden)
		}
	}
}

func validLinkedFeeTransactionInput(movementKey, parentMovementKey string) TransactionUpsertInput {
	fromAccountID := int64(101)
	status := LifecycleStatusPending
	return TransactionUpsertInput{
		MovementKey:              movementKey,
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		ProviderAccountKey:       "vault-a",
		ProviderTransactionID:    "tx-parent",
		MovementKind:             MovementKindFee,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		ParentMovementKey:        parentMovementKey,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "ETH",
		Asset:                    AssetIdentity{Currency: "ETH", ChainCode: "ETHEREUM"},
		Amount:                   decimal.RequireFromString("0.00021"),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Status:   &status,
			Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook},
		},
	}
}

func validLinkedReversalTransactionInput(movementKey, originalMovementKey string) TransactionUpsertInput {
	toAccountID := int64(102)
	status := LifecycleStatusCancelled
	return TransactionUpsertInput{
		MovementKey:              movementKey,
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		ProviderAccountKey:       "vault-a",
		ProviderTransactionID:    "tx-reversal",
		MovementKind:             MovementKindReversal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionInflow,
		ReversalOfMovementKey:    originalMovementKey,
		ToCompanyFundAccountID:   &toAccountID,
		Currency:                 "USDT",
		Asset:                    AssetIdentity{Currency: "USDT", ChainCode: "ETHEREUM"},
		Amount:                   decimal.RequireFromString("10"),
		FirstSeenSource:          TransactionSeenSourceWebhook,
		Provider: ProviderOwnedFields{
			Status:   &status,
			Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook},
		},
	}
}

func companyFundTransactionLinkageRows(parentID, reversalID any, groupKey, leg, groupState string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"parent_transaction_id",
		"reversal_of_transaction_id",
		"conversion_group_key",
		"conversion_leg",
		"conversion_group_status",
	}).AddRow(parentID, reversalID, groupKey, leg, groupState)
}
