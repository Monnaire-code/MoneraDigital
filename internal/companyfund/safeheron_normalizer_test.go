package companyfund

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestNormalizeSafeheronTransaction_NormalOutflowCapturesFeeAndRisk(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-normal-outflow"
	input.Snapshot.TxAmount = "1.250000000000000001"
	input.Snapshot.TxAmountToUSD = "99.123456789"
	input.Snapshot.TxFee = "0.00021"
	input.Snapshot.FeeCoinKey = "ETHEREUM_ETH"
	input.Snapshot.GasFee = testSafeheronGasFees()
	input.Snapshot.BlockHeight = 123456
	input.Snapshot.IsDestinationPhishing = true
	input.Snapshot.AmlLock = "YES"
	input.Snapshot.AMLScreeningTriggeredState = "TRIGGERED"
	input.Snapshot.AMLList = testSafeheronHighAML()
	input.Snapshot.RawPayload = []byte(`{"same":"fields-but-not-the-source-digest"}`)

	movements, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction() error = %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("movement count = %d, want one principal row", len(movements))
	}

	principal := testSafeheronMovementByKind(t, movements, MovementKindPrincipal)
	if principal.Movement.Direction != DirectionOutflow || principal.Movement.TransferMode != TransferModeSingle {
		t.Fatalf("principal relation = %#v", principal.Movement)
	}
	if !principal.Movement.Amount.Equal(decimal.RequireFromString("1.250000000000000001")) ||
		principal.Movement.FromAccountID == nil || *principal.Movement.FromAccountID != 1 ||
		principal.Movement.ToAccountID != nil {
		t.Fatalf("principal amount/accounts = %#v", principal.Movement)
	}
	if principal.FromAccountSnapshot == nil || principal.ToAccountSnapshot != nil ||
		principal.FromAccountSnapshot.CompanyEntity != "Monera Singapore" {
		t.Fatalf("principal account snapshots = %#v / %#v", principal.FromAccountSnapshot, principal.ToAccountSnapshot)
	}
	if principal.UpsertInput.ProviderDisplay.From.AddressOrAccount == nil ||
		*principal.UpsertInput.ProviderDisplay.From.AddressOrAccount != "0xFrom" ||
		principal.UpsertInput.ProviderDisplay.To.AddressOrAccount == nil ||
		*principal.UpsertInput.ProviderDisplay.To.AddressOrAccount != "0xExternal" ||
		principal.UpsertInput.ProviderDisplay.BlockHeight == nil || *principal.UpsertInput.ProviderDisplay.BlockHeight != 123456 {
		t.Fatalf("principal display = %#v", principal.UpsertInput.ProviderDisplay)
	}
	if principal.UpsertInput.RawSnapshotDigest != input.SourcePayloadDigest {
		t.Fatalf("raw snapshot digest = %q, want trusted input digest %q", principal.UpsertInput.RawSnapshotDigest, input.SourcePayloadDigest)
	}
	rawDigest := sha256.Sum256(input.Snapshot.RawPayload)
	if principal.UpsertInput.RawSnapshotDigest == hex.EncodeToString(rawDigest[:]) {
		t.Fatal("normalizer must not recompute source digest from snapshot RawPayload")
	}
	if principal.Movement.ProviderReportedUSD == nil || !principal.Movement.ProviderReportedUSD.Equal(decimal.RequireFromString("99.123456789")) {
		t.Fatalf("principal provider USD = %#v", principal.Movement.ProviderReportedUSD)
	}
	testSafeheronRequireRiskFlags(t, principal.Risk.Flags, RiskFlagDust, RiskFlagDestinationPhishing, RiskFlagAMLLock, RiskFlagAMLHigh)
	if principal.UpsertInput.AutomaticRisk.IsDust == nil || !*principal.UpsertInput.AutomaticRisk.IsDust ||
		principal.UpsertInput.AutomaticRisk.AutoExcludedFromSummary == nil || !*principal.UpsertInput.AutomaticRisk.AutoExcludedFromSummary ||
		principal.UpsertInput.AutomaticRisk.IsDestinationPhishing == nil || !*principal.UpsertInput.AutomaticRisk.IsDestinationPhishing ||
		principal.UpsertInput.AutomaticRisk.AMLLock == nil || !*principal.UpsertInput.AutomaticRisk.AMLLock ||
		principal.UpsertInput.AutomaticRisk.AMLRiskLevel == nil || *principal.UpsertInput.AutomaticRisk.AMLRiskLevel != AMLRiskLevelHigh ||
		principal.UpsertInput.AutomaticRisk.AMLScreeningState == nil || *principal.UpsertInput.AutomaticRisk.AMLScreeningState != AMLScreeningStateScreened {
		t.Fatalf("principal automatic risk = %#v", principal.UpsertInput.AutomaticRisk)
	}
	if principal.UpsertInput.ProviderDisplay.Fee.Amount == nil ||
		!principal.UpsertInput.ProviderDisplay.Fee.Amount.Equal(decimal.RequireFromString("0.00021")) ||
		principal.UpsertInput.ProviderDisplay.Fee.Currency == nil || *principal.UpsertInput.ProviderDisplay.Fee.Currency != "ETH" {
		t.Fatalf("principal fee display = %#v", principal.UpsertInput.ProviderDisplay.Fee)
	}
	var details map[string]any
	if err := json.Unmarshal(principal.UpsertInput.ProviderDisplay.Fee.DetailsJSON, &details); err != nil || details["feeCoinKey"] != "ETHEREUM_ETH" {
		t.Fatalf("principal fee details = %s, %v", principal.UpsertInput.ProviderDisplay.Fee.DetailsJSON, err)
	}
}

func TestNormalizeSafeheronTransaction_InternalAndUnmatchedEndpoints(t *testing.T) {
	input := testSafeheronNormalizationInput(t)
	input.Snapshot.TxKey = "safeheron-internal"
	input.Snapshot.TxAmount = "5"
	input.Snapshot.DestinationAddress = "0xTo"
	input.Snapshot.TxHash = ""
	input.Snapshot.TxFee = ""
	input.Snapshot.FeeCoinKey = ""
	input.Snapshot.BlockHeight = 0

	movements, err := NormalizeSafeheronTransaction(input)
	if err != nil {
		t.Fatalf("NormalizeSafeheronTransaction() error = %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("internal movement count = %d", len(movements))
	}
	internal := movements[0]
	if internal.Movement.Direction != DirectionInternalTransfer || internal.Movement.FromAccountID == nil ||
		internal.Movement.ToAccountID == nil || *internal.Movement.FromAccountID != 1 || *internal.Movement.ToAccountID != 2 ||
		internal.FromAccountSnapshot == nil || internal.ToAccountSnapshot == nil ||
		internal.FromAccountSnapshot.FundAccountName != "Treasury" || internal.ToAccountSnapshot.FundAccountName != "Operations" ||
		internal.Risk.PolicySubjectAccountID == nil || *internal.Risk.PolicySubjectAccountID != 2 {
		t.Fatalf("internal movement = %#v", internal)
	}
	if internal.UpsertInput.Provider.TxHash != nil || internal.UpsertInput.ProviderDisplay.BlockHeight != nil ||
		internal.UpsertInput.AutomaticRisk.IsSourcePhishing != nil || internal.UpsertInput.AutomaticRisk.IsDestinationPhishing != nil ||
		internal.UpsertInput.AutomaticRisk.AMLLock != nil || internal.UpsertInput.AutomaticRisk.AMLScreeningState != nil {
		t.Fatalf("unknown conditional fields must remain nil: %#v", internal.UpsertInput)
	}

	input.Snapshot.SourceAddress = "0xUnrelatedSource"
	input.Snapshot.DestinationAddress = "0xUnrelatedDestination"
	movements, err = NormalizeSafeheronTransaction(input)
	if err != nil || len(movements) != 0 {
		t.Fatalf("unmatched transaction = %#v, %v; want no ledger movement", movements, err)
	}
}
