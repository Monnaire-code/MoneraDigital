package companyfund

import (
	"encoding/json"
	"testing"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

func TestNormalizeSafeheronProviderEvent_EmitsDirectFactAndOnlyBindsDirectPrincipal(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-fact-single"
	input.Snapshot.TxAmount = "12.34"
	input.Snapshot.TxAmountToUSD = "123.45"
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"

	result, err := NormalizeSafeheronProviderEvent(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronProviderEvent() error = %v", err)
	}
	if result.Ignored || len(result.Facts) != 1 || len(result.Movements) != 1 || len(result.FactBindings) != 1 {
		t.Fatalf("provider event result = %#v", result)
	}
	fact := result.Facts[0]
	if fact.Input.ValueScope != ProviderValueScopeDirectItem || fact.Input.AllocationState != ProviderFactAllocationStateNotApplicable ||
		fact.Input.ProviderReportedUSD == nil || !fact.Input.ProviderReportedUSD.Equal(decimal.RequireFromString("123.45")) ||
		fact.Input.SourcePayloadDigest != input.SourcePayloadDigest || fact.Reference == "" {
		t.Fatalf("direct fact = %#v", fact)
	}
	if result.FactBindings[0].FactReference != fact.Reference || result.FactBindings[0].MovementKey == "" {
		t.Fatalf("direct fact binding = %#v", result.FactBindings[0])
	}
	if result.Movements[0].MovementKind != MovementKindPrincipal || result.Movements[0].ProviderDisplay.Fee.Amount == nil {
		t.Fatalf("single principal must retain its transaction fee display: %#v", result.Movements[0])
	}
	if err := result.validate(); err != nil {
		t.Fatalf("worker-facing result must validate: %v", err)
	}
}

func TestNormalizeSafeheronProviderEvent_PreservesBatchParentTotalWithoutChildBindings(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-fact-batch"
	input.Snapshot.TxAmount = "3"
	input.Snapshot.TxAmountToUSD = "300"
	input.Snapshot.DestinationAddress = ""
	input.Snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{
		{Address: "0xExternalA", Amount: "1"},
		{Address: "0xExternalB", Amount: "2"},
	}
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"

	result, err := NormalizeSafeheronProviderEvent(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronProviderEvent() error = %v", err)
	}
	if len(result.Facts) != 1 || len(result.Movements) != 2 || len(result.FactBindings) != 0 {
		t.Fatalf("batch provider event result = %#v", result)
	}
	fact := result.Facts[0].Input
	if fact.ValueScope != ProviderValueScopeTransactionTotal || fact.AllocationState != ProviderFactAllocationStateUnproven ||
		fact.ProviderReportedUSD == nil || !fact.ProviderReportedUSD.Equal(decimal.RequireFromString("300")) {
		t.Fatalf("batch parent fact = %#v", fact)
	}
	feeDisplayCount := 0
	for _, movement := range result.Movements {
		if movement.ProviderTransactionFactID != nil {
			t.Fatalf("normalizer must leave fact ID injection to worker: %#v", movement)
		}
		if movement.MovementKind != MovementKindPrincipal {
			t.Fatalf("batch must not create a separate fee movement: %#v", movement)
		}
		if movement.ProviderDisplay.Fee.Amount != nil {
			feeDisplayCount++
		}
	}
	if feeDisplayCount != 1 {
		t.Fatalf("batch must attach one transaction fee display to a principal, got %d", feeDisplayCount)
	}
}

func TestNormalizeSafeheronProviderEvent_MarksUnmatchedTransactionIgnored(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.SourceAddress = "0xUnrelatedSource"
	input.Snapshot.DestinationAddress = "0xUnrelatedDestination"
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""

	result, err := NormalizeSafeheronProviderEvent(input)
	if err != nil || !result.Ignored || len(result.Facts) != 0 || len(result.Movements) != 0 || len(result.FactBindings) != 0 {
		t.Fatalf("unmatched provider event = %#v, %v", result, err)
	}
}

func TestNormalizeSafeheronProviderEvent_DoesNotRecordExternalSenderFeeAsCompanyInflow(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-inflow-external-fee"
	input.Snapshot.SourceAddress = "0xExternalSender"
	input.Snapshot.DestinationAddress = "0xTo"
	input.Snapshot.TxAmount = "8"
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"
	input.Snapshot.GasFee = testSafeheronGasFees()
	input.FeeAsset = nil // External sender's fee must not require our asset mapping.

	result, err := NormalizeSafeheronProviderEvent(input)
	if err != nil || result.Ignored || len(result.Movements) != 1 || len(result.Facts) != 1 || len(result.FactBindings) != 1 {
		t.Fatalf("external-sender inflow result = %#v, %v", result, err)
	}
	if result.Movements[0].MovementKind != MovementKindPrincipal || result.Movements[0].Direction != DirectionInflow {
		t.Fatalf("inflow must contain only its principal movement: %#v", result.Movements)
	}
	if result.Movements[0].ProviderDisplay.Fee.Amount == nil || !result.Movements[0].ProviderDisplay.Fee.Amount.Equal(decimal.RequireFromString("0.00021")) ||
		result.Movements[0].ProviderDisplay.Fee.Currency != nil {
		t.Fatalf("external payer fee must remain display-only without an invented asset mapping: %#v", result.Movements[0].ProviderDisplay.Fee)
	}
	var extras map[string]json.RawMessage
	if err := json.Unmarshal(result.Facts[0].Input.ProviderExtrasJSON, &extras); err != nil {
		t.Fatalf("provider fact extras = %s, %v", result.Facts[0].Input.ProviderExtrasJSON, err)
	}
	var feeDetails map[string]any
	if err := json.Unmarshal(extras["feeDetails"], &feeDetails); err != nil || feeDetails["txFee"] != "0.00021" || feeDetails["feeCoinKey"] != "ETHEREUM_ETH" {
		t.Fatalf("inbound fee audit detail = %s, %v", extras["feeDetails"], err)
	}
}
