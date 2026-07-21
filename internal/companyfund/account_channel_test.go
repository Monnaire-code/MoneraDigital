package companyfund

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestAccountChannelAndTransactionSourceHaveSeparateValidityRules(t *testing.T) {
	for _, channel := range []AccountChannel{
		AccountChannelSafeheron,
		AccountChannelAirwallex,
		AccountChannelOther,
	} {
		if !channel.Valid() {
			t.Fatalf("AccountChannel(%q).Valid() = false, want true", channel)
		}
	}
	if AccountChannel("MANUAL").Valid() {
		t.Fatal("MANUAL must not be a valid account channel")
	}

	for _, source := range []TransactionSource{
		TransactionSourceSafeheron,
		TransactionSourceAirwallex,
		TransactionSourceManual,
	} {
		if !source.Valid() {
			t.Fatalf("TransactionSource(%q).Valid() = false, want true", source)
		}
	}
	if TransactionSource("OTHER").Valid() {
		t.Fatal("OTHER must not be a valid transaction source")
	}
}

func TestAccountOnlyChannelCannotEnterTransactionOrProviderPaths(t *testing.T) {
	other := TransactionSource("OTHER")
	if err := (ProviderEventInput{Channel: other}).validate(); err == nil {
		t.Fatal("provider events must reject account-only channel")
	}
	if _, err := (CompanyFundSyncRunInput{Channel: other}).canonical(); err == nil {
		t.Fatal("sync runs must reject account-only channel")
	}
	if err := (TransactionUpsertInput{Channel: other}).validate(); err == nil {
		t.Fatal("transaction persistence must reject account-only channel")
	}
	if _, err := EvaluateRisk(RiskInput{Channel: other}); err == nil {
		t.Fatal("risk evaluation must reject account-only channel")
	}
	if _, err := EvaluateUSDValue(USDValuationInput{Channel: other, Amount: decimal.Zero}); err == nil {
		t.Fatal("valuation must reject account-only channel")
	}
}
