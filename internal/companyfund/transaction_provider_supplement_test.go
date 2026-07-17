package companyfund

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestUpsertCompanyFundTransaction_PersistsProviderDisplayAndAutomaticRisk(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := completeProviderSupplementTransactionInput()
	previousRevision := int64(1)
	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs(input.MovementKey).
		WillReturnRows(lockedTransactionRows(
			701, "SAFEHERON", MovementIdentityAlgorithmVersion, "", "", "", "", nil, nil, "",
			"10", "USDT", "", "", "", "PENDING", previousRevision, "WEBHOOK", 0, "WEBHOOK", "", nil, nil, nil,
		))
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE company_fund_transactions")).
		WithArgs(
			int64(701), nil, nil, nil, nil, nil,
			"10", "USDT", false, nil, nil, nil, nil,
			LifecycleStatusPending, *input.Provider.Metadata.Revision, nil, ProviderSourceReconciliation, 0,
			nil, nil, nil, nil, TransactionSeenSourceReconciliation,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(701))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderSupplementSQL)).
		WithArgs(providerSupplementExpectedArgs(701, true)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(701))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || result.ID != 701 || result.Inserted || result.Quarantined {
		t.Fatalf("UpsertCompanyFundTransaction() = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestNormalizeTransactionProviderSupplement_CanonicalizesExactJSONAndRejectsUnsafeValues(t *testing.T) {
	input := completeProviderSupplementTransactionInput()
	supplement, err := normalizeTransactionProviderSupplement(input.ProviderDisplay, input.AutomaticRisk)
	if err != nil {
		t.Fatalf("normalizeTransactionProviderSupplement() error = %v", err)
	}
	if supplement.Display.FeeDetailsJSON == nil || *supplement.Display.FeeDetailsJSON != `{"components":[{"amount":0.000000000000000001,"kind":"network"}],"sequence":9007199254740993}` {
		t.Fatalf("fee details lost JSON number precision or canonical ordering: %v", supplement.Display.FeeDetailsJSON)
	}
	if supplement.Risk.RiskFlagsJSON == nil || *supplement.Risk.RiskFlagsJSON != `["AML_LOCK","DUST","SOURCE_PHISHING"]` {
		t.Fatalf("risk flags must canonicalize as a deterministic set: %v", supplement.Risk.RiskFlagsJSON)
	}

	negativeFee := decimal.NewFromInt(-1)
	invalid := []struct {
		name   string
		mutate func(*TransactionUpsertInput)
	}{
		{
			name: "negative fee",
			mutate: func(value *TransactionUpsertInput) {
				value.ProviderDisplay.Fee.Amount = &negativeFee
			},
		},
		{
			name: "non-object fee details",
			mutate: func(value *TransactionUpsertInput) {
				value.ProviderDisplay.Fee.DetailsJSON = json.RawMessage(`[1,2]`)
			},
		},
		{
			name: "negative block height",
			mutate: func(value *TransactionUpsertInput) {
				height := int64(-1)
				value.ProviderDisplay.BlockHeight = &height
			},
		},
		{
			name: "dust lacks policy evidence",
			mutate: func(value *TransactionUpsertInput) {
				value.AutomaticRisk.DustPolicyID = nil
			},
		},
		{
			name: "unknown risk flag",
			mutate: func(value *TransactionUpsertInput) {
				flags := []RiskFlag{"UNSUPPORTED"}
				value.AutomaticRisk.RiskFlags = &flags
			},
		},
	}
	for _, testCase := range invalid {
		t.Run(testCase.name, func(t *testing.T) {
			value := completeProviderSupplementTransactionInput()
			testCase.mutate(&value)
			if _, err := normalizeTransactionProviderSupplement(value.ProviderDisplay, value.AutomaticRisk); err == nil {
				t.Fatalf("%s unexpectedly normalized", testCase.name)
			}
		})
	}
}

func TestProviderTransactionSupplementSQL_ExcludesManualFinanceAndManualRiskOverride(t *testing.T) {
	for _, required := range []string{
		"from_address_or_account", "to_address_or_account", "payer_name", "payee_name",
		"provider_reported_fee_amount", "provider_reported_fee_currency", "fee_details",
		"block_height", "block_hash", "is_dust", "dust_policy_id", "dust_threshold",
		"is_source_phishing", "is_destination_phishing", "is_unrecognized_asset", "aml_lock",
		"aml_screening_state", "aml_risk_level", "risk_flags", "auto_excluded_from_summary",
	} {
		if !strings.Contains(updateCompanyFundTransactionProviderSupplementSQL, required) {
			t.Fatalf("provider supplement SQL is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"finance_category", "is_operating_income_expense", "applicant", "business_description", "summary_inclusion_override", "counterparty_name_override",
		"classification_", "risk_override_", "risk_status", "risk_reason_code",
	} {
		if strings.Contains(updateCompanyFundTransactionProviderSupplementSQL, forbidden) {
			t.Fatalf("provider supplement must not update manual finance/risk field %q", forbidden)
		}
	}
	for _, contract := range []string{
		"CASE WHEN $2 THEN COALESCE", "WHEN $20::boolean = true THEN true", "::jsonb",
	} {
		if !strings.Contains(updateCompanyFundTransactionProviderSupplementSQL, contract) {
			t.Fatalf("provider supplement SQL is missing merge contract %q", contract)
		}
	}
}

func TestUpsertCompanyFundTransaction_PersistsSupplementAfterInitialInsert(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := completeProviderSupplementTransactionInput()
	mock.ExpectBegin()
	mock.ExpectQuery(transactionForUpdateQueryPattern()).
		WithArgs(input.MovementKey).
		WillReturnRows(sqlmock.NewRows(transactionForUpdateColumns()))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO company_fund_transactions")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(702))
	mock.ExpectQuery(regexp.QuoteMeta(updateCompanyFundTransactionProviderSupplementSQL)).
		WithArgs(providerSupplementExpectedArgs(702, true)...).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(702))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).UpsertCompanyFundTransaction(context.Background(), input)
	if err != nil || result.ID != 702 || !result.Inserted || result.Quarantined {
		t.Fatalf("UpsertCompanyFundTransaction(initial supplement) = %#v, %v", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func completeProviderSupplementTransactionInput() TransactionUpsertInput {
	fromAccountID := int64(101)
	revision := int64(2)
	status := LifecycleStatusPending
	fee := decimal.RequireFromString("0.000000000000000001")
	dustThreshold := decimal.RequireFromString("0.01")
	dustPolicyID := int64(97)
	blockHeight := int64(23456789)
	flags := []RiskFlag{RiskFlagSourcePhishing, RiskFlagDust, RiskFlagAMLLock}
	return TransactionUpsertInput{
		MovementKey:              "v1:provider-supplement",
		Channel:                  ChannelSafeheron,
		IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion,
		MovementKind:             MovementKindPrincipal,
		TransferMode:             TransferModeSingle,
		Direction:                DirectionOutflow,
		FromCompanyFundAccountID: &fromAccountID,
		Currency:                 "USDT",
		Amount:                   decimal.NewFromInt(10),
		FirstSeenSource:          TransactionSeenSourceReconciliation,
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceReconciliation},
			Status:   &status,
		},
		ProviderDisplay: ProviderTransactionDisplayInput{
			From: ProviderTransactionPartyDisplayInput{
				AddressOrAccount: testProviderString("0xfrom"),
				CompanyEntity:    testProviderString("Monera HK"),
				FundAccountName:  testProviderString("Treasury"),
				SubAccountName:   testProviderString("Cold"),
				AccountType:      testProviderString("SAFEHERON_WALLET"),
			},
			To: ProviderTransactionPartyDisplayInput{
				AddressOrAccount: testProviderString("0xto"),
				CompanyEntity:    testProviderString("Vendor Ltd"),
				FundAccountName:  testProviderString("Operating"),
				SubAccountName:   testProviderString("Main"),
				AccountType:      testProviderString("BANK_ACCOUNT"),
			},
			PayerName: testProviderString("Treasury"),
			PayeeName: testProviderString("Vendor Ltd"),
			Fee: ProviderTransactionFeeInput{
				Amount:      &fee,
				Currency:    testProviderString("ETH"),
				DetailsJSON: json.RawMessage(`{"sequence":9007199254740993,"components":[{"kind":"network","amount":0.000000000000000001}]}`),
			},
			BlockHeight: &blockHeight,
			BlockHash:   testProviderString("0xblock"),
		},
		AutomaticRisk: ProviderAutomaticRiskInput{
			IsDust:                  testProviderBool(true),
			AutoExcludedFromSummary: testProviderBool(true),
			DustPolicyID:            &dustPolicyID,
			DustThreshold:           &dustThreshold,
			IsSourcePhishing:        testProviderBool(true),
			IsDestinationPhishing:   testProviderBool(false),
			IsUnrecognizedAsset:     testProviderBool(true),
			AMLLock:                 testProviderBool(true),
			AMLScreeningState:       testAMLScreeningState(AMLScreeningStateReviewRequired),
			AMLRiskLevel:            testAMLRiskLevel(AMLRiskLevelHigh),
			RiskFlags:               &flags,
		},
	}
}

func testProviderString(value string) *string { return &value }

func testProviderBool(value bool) *bool { return &value }

func testAMLScreeningState(value AMLScreeningState) *AMLScreeningState { return &value }

func testAMLRiskLevel(value AMLRiskLevel) *AMLRiskLevel { return &value }

func providerSupplementExpectedArgs(id int64, metadataWins bool) []driver.Value {
	return []driver.Value{
		id, metadataWins,
		"0xfrom", "0xto", "Treasury", "Vendor Ltd",
		"Monera HK", "Treasury", "Cold", "SAFEHERON_WALLET",
		"Vendor Ltd", "Operating", "Main", "BANK_ACCOUNT",
		"0.000000000000000001", "ETH",
		`{"components":[{"amount":0.000000000000000001,"kind":"network"}],"sequence":9007199254740993}`,
		int64(23456789), "0xblock",
		true, int64(97), "0.01", true, false, true, true,
		string(AMLScreeningStateReviewRequired), string(AMLRiskLevelHigh),
		`["AML_LOCK","DUST","SOURCE_PHISHING"]`, true,
	}
}
