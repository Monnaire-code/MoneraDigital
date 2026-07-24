package companyfund

import (
	"strings"
	"testing"
	"time"
)

func TestCanonicalizeFinanceTransactionFilter_PublicContractAndValidation(t *testing.T) {
	from := time.Date(2026, time.July, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	to := from.Add(24 * time.Hour)
	operating := true
	canonical, err := CanonicalizeFinanceTransactionFilter(FinanceTransactionFilter{
		DateFrom:                 &from,
		DateTo:                   &to,
		Channels:                 []TransactionSource{ChannelSafeheron, ChannelAirwallex, ChannelSafeheron},
		AccountIDs:               []int64{9, 3, 9},
		Directions:               []Direction{DirectionOutflow, DirectionInflow, DirectionOutflow},
		Currencies:               []string{" usdt ", "USD", "USDT"},
		FinanceCategoryLevel1IDs: []int64{11, 4, 11},
		FinanceCategoryLevel2IDs: []int64{22, 8, 22},
		OperatingIncomeExpense:   &operating,
	})
	if err != nil {
		t.Fatal(err)
	}
	if canonical.DateFrom == nil || !canonical.DateFrom.Equal(from.UTC()) || canonical.DateFrom.Location() != time.UTC ||
		len(canonical.Channels) != 2 || canonical.Channels[0] != ChannelAirwallex ||
		len(canonical.AccountIDs) != 2 || canonical.AccountIDs[0] != 3 ||
		len(canonical.Directions) != 2 || canonical.Directions[0] != DirectionInflow ||
		len(canonical.Currencies) != 2 || canonical.Currencies[0] != "USD" ||
		len(canonical.FinanceCategoryLevel1IDs) != 2 || canonical.FinanceCategoryLevel1IDs[0] != 4 ||
		len(canonical.FinanceCategoryLevel2IDs) != 2 || canonical.FinanceCategoryLevel2IDs[0] != 8 ||
		canonical.OperatingIncomeExpense == nil || !*canonical.OperatingIncomeExpense {
		t.Fatalf("canonical filter = %#v", canonical)
	}

	invalidUTF8 := string([]byte{0xff})
	for _, testCase := range []struct {
		name   string
		filter FinanceTransactionFilter
	}{
		{"non-increasing dates", FinanceTransactionFilter{DateFrom: &from, DateTo: &from}},
		{"unknown channel", FinanceTransactionFilter{Channels: []TransactionSource{"UNKNOWN"}}},
		{"unknown direction", FinanceTransactionFilter{Directions: []Direction{"UNKNOWN"}}},
		{"empty currency", FinanceTransactionFilter{Currencies: []string{" "}}},
		{"invalid utf8 currency", FinanceTransactionFilter{Currencies: []string{invalidUTF8}}},
		{"oversized currency", FinanceTransactionFilter{Currencies: []string{strings.Repeat("A", 65)}}},
		{"invalid account", FinanceTransactionFilter{AccountIDs: []int64{0}}},
		{"invalid level one", FinanceTransactionFilter{FinanceCategoryLevel1IDs: []int64{-1}}},
		{"invalid level two", FinanceTransactionFilter{FinanceCategoryLevel2IDs: []int64{0}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := CanonicalizeFinanceTransactionFilter(testCase.filter); err == nil {
				t.Fatal("CanonicalizeFinanceTransactionFilter() = nil, want validation error")
			}
		})
	}
}

func TestCanonicalizeFinanceTransactionDetailRequest_PaginationAndNestedFilter(t *testing.T) {
	canonical, err := CanonicalizeFinanceTransactionDetailRequest(FinanceTransactionDetailRequest{
		Filter: FinanceTransactionFilter{Currencies: []string{" usdt "}},
	})
	if err != nil || canonical.Limit != defaultFinanceDetailLimit || canonical.Offset != 0 || len(canonical.Filter.Currencies) != 1 || canonical.Filter.Currencies[0] != "USDT" {
		t.Fatalf("canonical detail request = %#v, %v", canonical, err)
	}

	for _, request := range []FinanceTransactionDetailRequest{
		{Limit: -1},
		{Limit: maxFinanceDetailLimit + 1},
		{Limit: 1, Offset: -1},
		{Filter: FinanceTransactionFilter{Channels: []TransactionSource{"INVALID"}}},
	} {
		if _, err := CanonicalizeFinanceTransactionDetailRequest(request); err == nil {
			t.Fatalf("CanonicalizeFinanceTransactionDetailRequest(%#v) = nil, want validation error", request)
		}
	}
}

func TestCanonicalizeFinanceClassificationUpdate_ManualFieldsAndValidation(t *testing.T) {
	level1 := int64(11)
	level2 := int64(22)
	applicant := "  finance@monera.example  "
	description := "  July vendor settlement  "
	counterpartyName := "  Vendor alias  "
	canonical, err := CanonicalizeFinanceClassificationUpdate(FinanceClassificationUpdate{
		TransactionID:            71,
		FinanceCategoryLevel1ID:  &level1,
		FinanceCategoryLevel2ID:  &level2,
		Applicant:                &applicant,
		BusinessDescription:      &description,
		CounterpartyNameOverride: &counterpartyName,
		UpdatedBy:                "  finance-admin  ",
	})
	if err != nil || canonical.UpdatedBy != "finance-admin" || canonical.Applicant == nil || *canonical.Applicant != "finance@monera.example" ||
		canonical.BusinessDescription == nil || *canonical.BusinessDescription != "July vendor settlement" || !canonical.CounterpartyNameOverrideSet || canonical.CounterpartyNameOverride == nil || *canonical.CounterpartyNameOverride != "Vendor alias" {
		t.Fatalf("canonical classification = %#v, %v", canonical, err)
	}

	blank := " \t "
	cleared, err := CanonicalizeFinanceClassificationUpdate(FinanceClassificationUpdate{
		TransactionID:            72,
		Applicant:                &blank,
		BusinessDescription:      &blank,
		CounterpartyNameOverride: &blank,
		UpdatedBy:                "finance-admin",
	})
	if err != nil || cleared.Applicant != nil || cleared.BusinessDescription != nil || !cleared.CounterpartyNameOverrideSet || cleared.CounterpartyNameOverride != nil {
		t.Fatalf("blank manual text must clear fields: %#v, %v", cleared, err)
	}

	omitted, err := CanonicalizeFinanceClassificationUpdate(FinanceClassificationUpdate{
		TransactionID: 73,
		UpdatedBy:     "finance-admin",
	})
	if err != nil || omitted.CounterpartyNameOverrideSet || omitted.CounterpartyNameOverride != nil {
		t.Fatalf("omitted counterparty override must preserve stored value: %#v, %v", omitted, err)
	}

	invalidUTF8 := string([]byte{0xff})
	zero := int64(0)
	for _, input := range []FinanceClassificationUpdate{
		{UpdatedBy: "finance-admin"},
		{TransactionID: 74, FinanceCategoryLevel1ID: &zero, UpdatedBy: "finance-admin"},
		{TransactionID: 74, UpdatedBy: " "},
		{TransactionID: 74, Applicant: &invalidUTF8, UpdatedBy: "finance-admin"},
		{TransactionID: 74, BusinessDescription: stringPointer(strings.Repeat("x", maxFinanceDescriptionBytes+1)), UpdatedBy: "finance-admin"},
		{TransactionID: 74, CounterpartyNameOverride: stringPointer(strings.Repeat("x", maxFinanceCounterpartyNameBytes+1)), UpdatedBy: "finance-admin"},
	} {
		if _, err := CanonicalizeFinanceClassificationUpdate(input); err == nil {
			t.Fatalf("CanonicalizeFinanceClassificationUpdate(%#v) = nil, want validation error", input)
		}
	}
}
