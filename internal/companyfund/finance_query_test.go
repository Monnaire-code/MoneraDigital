package companyfund

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestFinanceFilter_BuildsOneCanonicalInclusionContractForDashboardAndDrilldown(t *testing.T) {
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	to := from.AddDate(0, 0, 7)
	operating := true
	filter, err := (FinanceTransactionFilter{
		DateFrom:                 &from,
		DateTo:                   &to,
		Channels:                 []TransactionSource{ChannelSafeheron, ChannelAirwallex, ChannelSafeheron},
		AccountIDs:               []int64{9, 3, 9},
		Directions:               []Direction{DirectionOutflow, DirectionInflow},
		Currencies:               []string{"usdt", "USD", "USDT"},
		FinanceCategoryLevel1IDs: []int64{11},
		FinanceCategoryLevel2IDs: []int64{22},
		OperatingIncomeExpense:   &operating,
	}).canonical()
	if err != nil {
		t.Fatalf("FinanceTransactionFilter.canonical() error = %v", err)
	}
	where, args, next := buildFinanceTransactionWhere(filter, 1)
	if next != len(args)+1 || !strings.Contains(where, financeSummaryIncludedSQL) {
		t.Fatalf("where next=%d args=%#v where=%s", next, args, where)
	}
	for _, required := range []string{
		financeTransactionDateSQL + " >= $1",
		financeTransactionDateSQL + " < $2",
		"transaction.channel IN ($3, $4)",
		"transaction.from_company_fund_account_id IN ($5, $6)",
		"transaction.to_company_fund_account_id IN ($7, $8)",
		"transaction.transaction_direction IN ($9, $10)",
		"transaction.currency IN ($11, $12)",
		"transaction.finance_category_level1_id IN ($13)",
		"transaction.finance_category_level2_id IN ($14)",
		"transaction.is_operating_income_expense = $15",
		financeSummaryIncludedSQL,
	} {
		if !strings.Contains(where, required) {
			t.Fatalf("shared finance filter missing %q from %s", required, where)
		}
	}
	if got, want := args[0].(time.Time), from.UTC(); !got.Equal(want) {
		t.Fatalf("canonical date start = %s, want %s", got, want)
	}
	if filter.Channels[0] != ChannelAirwallex || filter.AccountIDs[0] != 3 || filter.Currencies[0] != "USD" || filter.Currencies[1] != "USDT" {
		t.Fatalf("filter was not canonicalized: %#v", filter)
	}
}

func TestGetFinanceDashboard_ReturnsExactAggregateAndCanonicalDrilldown(t *testing.T) {
	db, mock := newFinanceMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	filter := FinanceTransactionFilter{DateFrom: &from, DateTo: &to}
	canonical, err := filter.canonical()
	if err != nil {
		t.Fatalf("filter.canonical() = %v", err)
	}
	where, args, _ := buildFinanceTransactionWhere(canonical, 1)
	query := financeDashboardSelectSQL + financeTransactionFromSQL + where + `
GROUP BY transaction.transaction_direction, transaction.currency
ORDER BY transaction.transaction_direction, transaction.currency`
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs(financeMockArgs(args)...).
		WillReturnRows(sqlmock.NewRows([]string{"transaction_direction", "currency", "count", "amount", "usd_value", "unpriced_count"}).
			AddRow("INFLOW", "USDT", int64(2), "10.000000000000000001", "10.123456789012345678", int64(0)))

	summary, err := repository.GetFinanceDashboard(context.Background(), filter)
	if err != nil || len(summary.Aggregates) != 1 {
		t.Fatalf("GetFinanceDashboard() = %#v, %v", summary, err)
	}
	aggregate := summary.Aggregates[0]
	if aggregate.Amount != "10.000000000000000001" || aggregate.USDValue == nil || *aggregate.USDValue != "10.123456789012345678" {
		t.Fatalf("exact aggregate values = %#v", aggregate)
	}
	if len(aggregate.Drilldown.Directions) != 1 || aggregate.Drilldown.Directions[0] != DirectionInflow || len(aggregate.Drilldown.Currencies) != 1 || aggregate.Drilldown.Currencies[0] != "USDT" {
		t.Fatalf("aggregate drilldown = %#v", aggregate.Drilldown)
	}
	assertFinanceMockExpectations(t, mock)
}

func TestListFinanceTransactionDetails_ReturnsFinancialDisplayWithoutRawPayload(t *testing.T) {
	db, mock := newFinanceMockDB(t)
	defer db.Close()
	repository := NewDBRepository(db)
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	request := FinanceTransactionDetailRequest{Filter: FinanceTransactionFilter{DateFrom: &from, DateTo: &to}, Limit: 50, Offset: 10}
	canonical, err := request.canonical()
	if err != nil {
		t.Fatalf("request.canonical() = %v", err)
	}
	where, args, next := buildFinanceTransactionWhere(canonical.Filter, 1)
	args = append(args, canonical.Limit, canonical.Offset)
	query := financeDetailSelectSQL + financeTransactionFromSQL + where + "\nORDER BY " + financeTransactionDateSQL + " DESC, transaction.id DESC\nLIMIT $" + strconv.Itoa(next) + " OFFSET $" + strconv.Itoa(next+1)
	date := time.Date(2026, time.July, 1, 2, 3, 4, 0, time.UTC)
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs(financeMockArgs(args)...).
		WillReturnRows(sqlmock.NewRows(financeDetailColumns()).AddRow(
			77, date, "SAFEHERON", "Monera HK", "Treasury", "Cold", "WALLET", "INTERNAL_TRANSFER", "BATCH", "PRINCIPAL", true,
			1, "OPERATING", "Operating", 2, "VENDOR", "Vendor payment",
			"USDT", "10.000000000000000001", "10.123456789012345678", "0.00021", "ETH",
			"Treasury", "Vendor Ltd", "Finance vendor alias", "0xfrom", "0xto", "finance@monera", "July invoice", "0xtx", "provider-tx-7",
			true, false, false, nil,
		))

	details, err := repository.ListFinanceTransactionDetails(context.Background(), request)
	if err != nil || len(details) != 1 {
		t.Fatalf("ListFinanceTransactionDetails() = %#v, %v", details, err)
	}
	detail := details[0]
	if detail.Amount != "10.000000000000000001" || detail.USDValue == nil || *detail.USDValue != "10.123456789012345678" || detail.FeeAmount == nil || *detail.FeeAmount != "0.00021" || detail.Payer != "Treasury" || detail.Payee != "Vendor Ltd" || detail.CounterpartyNameOverride == nil || *detail.CounterpartyNameOverride != "Finance vendor alias" || detail.EffectiveCounterpartyName != "Finance vendor alias" {
		t.Fatalf("financial display detail = %#v", detail)
	}
	if detail.Direction != DirectionInternalTransfer || detail.TransferMode != TransferModeBatch || detail.MovementKind != MovementKindPrincipal {
		t.Fatalf("transaction type detail = %#v, want internal batch principal", detail)
	}
	if strings.Contains(financeTransactionFromSQL+financeDetailSelectSQL, "owned_payload") || strings.Contains(financeTransactionFromSQL+financeDetailSelectSQL, "raw_payload") {
		t.Fatal("finance read query must not load retained provider payloads")
	}
	assertFinanceMockExpectations(t, mock)
}

func financeDetailColumns() []string {
	return []string{
		"id", "date", "channel", "company_entity", "fund_account_name", "sub_account_name", "account_type", "direction", "transfer_mode", "movement_kind", "operating",
		"level1_id", "level1_code", "level1_name", "level2_id", "level2_code", "level2_name", "currency", "amount", "usd_value", "fee_amount", "fee_currency",
		"payer", "payee", "counterparty_name_override", "from_address", "to_address", "applicant", "business_description", "tx_hash", "provider_transaction_id", "summary_included", "is_dust", "auto_excluded", "summary_override",
	}
}

func TestEffectiveFinanceCounterpartyName_PreservesProviderFactsAndUsesFinanceOverride(t *testing.T) {
	override := "Finance alias"
	for _, testCase := range []struct {
		name      string
		direction Direction
		payer     string
		payee     string
		override  *string
		want      string
	}{
		{"inflow provider payer", DirectionInflow, "External payer", "Treasury", nil, "External payer"},
		{"outflow provider payee", DirectionOutflow, "Treasury", "External payee", nil, "External payee"},
		{"inflow missing provider name", DirectionInflow, "", "Treasury", nil, "-"},
		{"outflow missing provider name", DirectionOutflow, "Treasury", "", nil, "-"},
		{"internal provider parties", DirectionInternalTransfer, "Treasury", "Operations", nil, "Treasury → Operations"},
		{"finance override wins", DirectionInflow, "External payer", "Treasury", &override, "Finance alias"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := effectiveFinanceCounterpartyName(testCase.direction, testCase.payer, testCase.payee, testCase.override); got != testCase.want {
				t.Fatalf("effectiveFinanceCounterpartyName() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func newFinanceMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	return db, mock
}

func assertFinanceMockExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet finance SQL mock expectation: %v", err)
	}
}

func financeMockArgs(values []any) []driver.Value {
	args := make([]driver.Value, 0, len(values))
	for _, value := range values {
		args = append(args, value)
	}
	return args
}
