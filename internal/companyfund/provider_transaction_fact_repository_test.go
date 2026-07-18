package companyfund

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"
)

func TestInsertProviderTransactionFact_PersistsExactDecimals(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	input := newProviderTransactionFactInput()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventForFactSQL)).
		WithArgs(input.SourceProviderEventID).
		WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(input.Channel, input.SourcePayloadDigest))
	mock.ExpectQuery(regexp.QuoteMeta(insertProviderTransactionFactSQL)).
		WithArgs(
			input.Channel,
			input.ProviderAccountKey,
			input.ProviderTransactionID,
			nil,
			input.FactIdentityKey,
			input.FactVersion,
			input.SourceProviderEventID,
			input.SourcePayloadDigest,
			*input.ProviderOccurredAt,
			"1.234567890123456789",
			"ETH",
			"4321.123456789012345678",
			"ETH",
			"USD",
			"3500.123456789012345678",
			"1.234567890123456789",
			"4321.123456789012345678",
			ProviderValueScopeTransactionTotal,
			ProviderFactAllocationStateUnproven,
			nil,
			`{"kind":"batch","provider":"safeheron","sequence":9007199254740993,"unit":1.234567890123456789}`,
		).
		WillReturnRows(providerTransactionFactRows(71, input))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).InsertProviderTransactionFact(context.Background(), input)
	if err != nil {
		t.Fatalf("InsertProviderTransactionFact() error = %v", err)
	}
	if !result.Inserted || result.Fact.ID != 71 {
		t.Fatalf("InsertProviderTransactionFact() = %#v, want inserted fact ID 71", result)
	}
	if result.Fact.ProviderAmount == nil || !result.Fact.ProviderAmount.Equal(decimal.RequireFromString("1.234567890123456789")) {
		t.Fatalf("stored amount lost exact precision: %#v", result.Fact.ProviderAmount)
	}
	if result.Fact.ProviderReportedUSD == nil || !result.Fact.ProviderReportedUSD.Equal(decimal.RequireFromString("4321.123456789012345678")) {
		t.Fatalf("stored provider USD lost exact precision: %#v", result.Fact.ProviderReportedUSD)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderTransactionFact_DuplicateFromAnotherMovementEventReadsExistingFact(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	existing := newProviderTransactionFactInput()
	incoming := existing
	incoming.SourceProviderEventID++
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventForFactSQL)).
		WithArgs(incoming.SourceProviderEventID).
		WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(incoming.Channel, incoming.SourcePayloadDigest))
	mock.ExpectQuery(regexp.QuoteMeta(insertProviderTransactionFactSQL)).
		WillReturnRows(sqlmock.NewRows(providerTransactionFactColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderTransactionFactByIdentitySQL)).
		WithArgs(incoming.Channel, incoming.FactIdentityKey, incoming.FactVersion).
		WillReturnRows(providerTransactionFactRows(72, existing))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).InsertProviderTransactionFact(context.Background(), incoming)
	if err != nil {
		t.Fatalf("InsertProviderTransactionFact() duplicate error = %v", err)
	}
	if result.Inserted || result.Fact.ID != 72 {
		t.Fatalf("duplicate InsertProviderTransactionFact() = %#v, want existing fact ID 72", result)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestInsertProviderTransactionFact_DuplicateRejectsDifferentImmutableFact(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		wantField string
		mutate    func(*ProviderTransactionFactInput)
	}{
		{
			name:      "amount",
			wantField: "provider_amount",
			mutate: func(input *ProviderTransactionFactInput) {
				amount := decimal.RequireFromString("2.234567890123456789")
				input.ProviderAmount = &amount
			},
		},
		{
			name:      "value scope",
			wantField: "value_scope",
			mutate: func(input *ProviderTransactionFactInput) {
				input.ValueScope = ProviderValueScopeDirectItem
			},
		},
		{
			name:      "allocation and contract",
			wantField: "allocation_state",
			mutate: func(input *ProviderTransactionFactInput) {
				input.AllocationState = ProviderFactAllocationStateProvenDerivable
				input.DerivationContractVersion = "sandbox-contract-v1"
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			existing := newProviderTransactionFactInput()
			incoming := existing
			testCase.mutate(&incoming)

			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventForFactSQL)).
				WithArgs(incoming.SourceProviderEventID).
				WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(incoming.Channel, incoming.SourcePayloadDigest))
			mock.ExpectQuery(regexp.QuoteMeta(insertProviderTransactionFactSQL)).
				WillReturnRows(sqlmock.NewRows(providerTransactionFactColumnNames()))
			mock.ExpectQuery(regexp.QuoteMeta(selectProviderTransactionFactByIdentitySQL)).
				WithArgs(incoming.Channel, incoming.FactIdentityKey, incoming.FactVersion).
				WillReturnRows(providerTransactionFactRows(73, existing))
			mock.ExpectRollback()

			_, err := NewDBRepository(db).InsertProviderTransactionFact(context.Background(), incoming)
			if err == nil || !strings.Contains(err.Error(), testCase.wantField) {
				t.Fatalf("different immutable fact error = %v, want conflict naming %q", err, testCase.wantField)
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestInsertProviderTransactionFact_DuplicateCanonicalizesJSONExtras(t *testing.T) {
	db, mock := newCompanyFundMockDB(t)
	defer db.Close()

	existing := newProviderTransactionFactInput()
	existing.ProviderExtrasJSON = []byte(`{"outer":{"b":2,"a":1},"b":2,"a":1}`)
	incoming := existing
	incoming.ProviderExtrasJSON = []byte(`{"a":1,"outer":{"a":1,"b":2},"b":2}`)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventForFactSQL)).
		WithArgs(incoming.SourceProviderEventID).
		WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(incoming.Channel, incoming.SourcePayloadDigest))
	mock.ExpectQuery(regexp.QuoteMeta(insertProviderTransactionFactSQL)).
		WillReturnRows(sqlmock.NewRows(providerTransactionFactColumnNames()))
	mock.ExpectQuery(regexp.QuoteMeta(selectProviderTransactionFactByIdentitySQL)).
		WithArgs(incoming.Channel, incoming.FactIdentityKey, incoming.FactVersion).
		WillReturnRows(providerTransactionFactRows(74, existing))
	mock.ExpectCommit()

	result, err := NewDBRepository(db).InsertProviderTransactionFact(context.Background(), incoming)
	if err != nil || result.Inserted || result.Fact.ID != 74 {
		t.Fatalf("canonical JSON duplicate = %#v, %v; want existing fact ID 74", result, err)
	}
	assertCompanyFundMockExpectations(t, mock)
}

func TestNormalizedProviderFactExtras_PreservesHighPrecisionJSONNumbers(t *testing.T) {
	canonical, err := normalizedProviderFactExtras([]byte(`{"z":9007199254740993,"nested":{"decimal":1.234567890123456789},"a":1}`))
	if err != nil {
		t.Fatalf("normalizedProviderFactExtras() error = %v", err)
	}
	const want = `{"a":1,"nested":{"decimal":1.234567890123456789},"z":9007199254740993}`
	if canonical != want {
		t.Fatalf("normalizedProviderFactExtras() = %s, want exact high-precision JSON %s", canonical, want)
	}
}

func TestInsertProviderTransactionFact_RejectsInvalidInputBeforeDatabaseUse(t *testing.T) {
	valid := newProviderTransactionFactInput()
	tests := []struct {
		name   string
		mutate func(*ProviderTransactionFactInput)
	}{
		{name: "invalid digest", mutate: func(input *ProviderTransactionFactInput) { input.SourcePayloadDigest = "not-a-sha256" }},
		{name: "invalid version", mutate: func(input *ProviderTransactionFactInput) { input.FactVersion = 0 }},
		{name: "derivable without contract", mutate: func(input *ProviderTransactionFactInput) {
			input.AllocationState = ProviderFactAllocationStateProvenDerivable
			input.DerivationContractVersion = ""
		}},
		{name: "unsupported scope", mutate: func(input *ProviderTransactionFactInput) {
			input.ValueScope = ProviderValueScopeDerivedFromParent
		}},
		{name: "amount exceeds numeric scale", mutate: func(input *ProviderTransactionFactInput) {
			value := decimal.RequireFromString("0.0000000000000000001")
			input.ProviderAmount = &value
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := valid
			testCase.mutate(&input)
			if _, err := NewDBRepository(nil).InsertProviderTransactionFact(context.Background(), input); err == nil {
				t.Fatal("InsertProviderTransactionFact() should reject invalid input before database use")
			}
		})
	}
}

func TestInsertProviderTransactionFact_RejectsSourceEventChannelOrDigestMismatch(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		eventChannel Channel
		eventDigest  string
	}{
		{name: "channel", eventChannel: ChannelAirwallex},
		{name: "digest", eventChannel: ChannelSafeheron, eventDigest: strings.Repeat("b", 64)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			db, mock := newCompanyFundMockDB(t)
			defer db.Close()
			input := newProviderTransactionFactInput()
			digest := testCase.eventDigest
			if digest == "" {
				digest = input.SourcePayloadDigest
			}

			mock.ExpectBegin()
			mock.ExpectQuery(regexp.QuoteMeta(selectProviderEventForFactSQL)).
				WithArgs(input.SourceProviderEventID).
				WillReturnRows(sqlmock.NewRows([]string{"channel", "source_payload_digest"}).AddRow(testCase.eventChannel, digest))
			mock.ExpectRollback()

			if _, err := NewDBRepository(db).InsertProviderTransactionFact(context.Background(), input); err == nil {
				t.Fatal("source event mismatch must reject the fact before insert")
			}
			assertCompanyFundMockExpectations(t, mock)
		})
	}
}

func TestProviderTransactionFactSQL_DoesNotTouchCiphertextOrDepositState(t *testing.T) {
	for name, statement := range map[string]string{
		"source lookup": selectProviderEventForFactSQL,
		"insert":        insertProviderTransactionFactSQL,
		"readback":      selectProviderTransactionFactByIdentitySQL,
	} {
		lower := strings.ToLower(statement)
		if strings.Contains(lower, "ciphertext") || strings.Contains(lower, "process_status") {
			t.Fatalf("%s SQL must not access ciphertext or deposit-owned process state: %s", name, statement)
		}
	}
}

func newProviderTransactionFactInput() ProviderTransactionFactInput {
	occurredAt := time.Date(2026, time.July, 10, 3, 4, 5, 0, time.UTC)
	amount := decimal.RequireFromString("1.234567890123456789")
	providerUSD := decimal.RequireFromString("4321.123456789012345678")
	conversionRate := decimal.RequireFromString("3500.123456789012345678")
	buyAmount := decimal.RequireFromString("1.234567890123456789")
	sellAmount := decimal.RequireFromString("4321.123456789012345678")
	return ProviderTransactionFactInput{
		Channel:                ChannelSafeheron,
		ProviderAccountKey:     "wallet-a",
		ProviderTransactionID:  "transaction-a",
		FactIdentityKey:        "safeheron:transaction-a:total",
		FactVersion:            2,
		SourceProviderEventID:  41,
		SourcePayloadDigest:    strings.Repeat("a", 64),
		ProviderOccurredAt:     &occurredAt,
		ProviderAmount:         &amount,
		ProviderCurrency:       "ETH",
		ProviderReportedUSD:    &providerUSD,
		ConversionFromCurrency: "ETH",
		ConversionToCurrency:   "USD",
		ConversionRate:         &conversionRate,
		ConversionBuyAmount:    &buyAmount,
		ConversionSellAmount:   &sellAmount,
		ValueScope:             ProviderValueScopeTransactionTotal,
		AllocationState:        ProviderFactAllocationStateUnproven,
		ProviderExtrasJSON:     []byte(`{"provider":"safeheron","kind":"batch","sequence":9007199254740993,"unit":1.234567890123456789}`),
	}
}

func providerTransactionFactColumnNames() []string {
	return []string{
		"id", "channel", "provider_account_key", "provider_transaction_id", "provider_group_id",
		"fact_identity_key", "fact_version", "source_provider_event_id", "source_payload_digest",
		"provider_occurred_at", "provider_amount", "provider_currency", "provider_reported_usd_value",
		"conversion_from_currency", "conversion_to_currency", "conversion_rate", "conversion_buy_amount",
		"conversion_sell_amount", "value_scope", "allocation_state", "derivation_contract_version",
		"provider_extras", "created_at", "updated_at",
	}
}

func providerTransactionFactRows(id int64, input ProviderTransactionFactInput) *sqlmock.Rows {
	return sqlmock.NewRows(providerTransactionFactColumnNames()).AddRow(
		id, input.Channel, input.ProviderAccountKey, input.ProviderTransactionID, nil,
		input.FactIdentityKey, input.FactVersion, input.SourceProviderEventID, input.SourcePayloadDigest,
		*input.ProviderOccurredAt, input.ProviderAmount.String(), input.ProviderCurrency, input.ProviderReportedUSD.String(),
		input.ConversionFromCurrency, input.ConversionToCurrency, input.ConversionRate.String(), input.ConversionBuyAmount.String(),
		input.ConversionSellAmount.String(), input.ValueScope, input.AllocationState,
		nil, string(input.ProviderExtrasJSON), time.Date(2026, time.July, 10, 3, 5, 0, 0, time.UTC), time.Date(2026, time.July, 10, 3, 5, 0, 0, time.UTC),
	)
}
