package companyfund

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestDomainEnumAndIdentityValidation(t *testing.T) {
	if Channel("UNSUPPORTED").Valid() || TransferMode("STREAM").Valid() || MovementKind("HOLD").Valid() || Direction("NEUTRAL").Valid() || ConversionLeg("MIDDLE").Valid() || ConversionGroupState("PENDING").Valid() {
		t.Fatal("unsupported enum values must be rejected")
	}

	base := MovementIdentityInput{
		Channel:          ChannelSafeheron,
		ProviderParentID: "parent",
		MovementKind:     MovementKindPrincipal,
		Asset:            AssetIdentity{Currency: "BTC"},
		Amount:           decimal.NewFromInt(1),
	}
	invalid := []MovementIdentityInput{
		func() MovementIdentityInput { value := base; value.Channel = Channel("OTHER"); return value }(),
		func() MovementIdentityInput { value := base; value.MovementKind = MovementKind("HOLD"); return value }(),
		func() MovementIdentityInput { value := base; value.Amount = decimal.NewFromInt(-1); return value }(),
		func() MovementIdentityInput { value := base; value.ProviderParentID = " "; return value }(),
		func() MovementIdentityInput { value := base; value.Asset = AssetIdentity{}; return value }(),
	}
	for _, input := range invalid {
		if _, err := BuildMovementIdentity(input); err == nil {
			t.Fatalf("invalid identity input %+v unexpectedly succeeded", input)
		}
	}
	if _, err := AssignBatchMovementIdentities([]MovementIdentityInput{invalid[0]}); err == nil {
		t.Fatal("invalid batch identity input must fail")
	}
}

func TestLifecycleAndRelationshipErrorPaths(t *testing.T) {
	safeheron := SafeheronLifecyclePolicy{}
	if safeheron.Channel() != ChannelSafeheron {
		t.Fatal("Safeheron policy channel mismatch")
	}
	if decision := safeheron.Transition("", LifecycleStatusPending); decision.Disposition != LifecycleDispositionApply {
		t.Fatalf("empty -> pending = %#v", decision)
	}
	if decision := safeheron.Transition(LifecycleStatusPending, "UNKNOWN"); decision.Disposition != LifecycleDispositionQuarantine {
		t.Fatalf("unknown incoming Safeheron status = %#v", decision)
	}
	if decision := safeheron.Transition("UNKNOWN", LifecycleStatusPending); decision.Disposition != LifecycleDispositionQuarantine {
		t.Fatalf("unknown stored Safeheron status = %#v", decision)
	}
	if decision := safeheron.Transition(LifecycleStatusBroadcasting, LifecycleStatusSigning); decision.Disposition != LifecycleDispositionKeep {
		t.Fatalf("out-of-order Safeheron state = %#v", decision)
	}
	if decision := safeheron.Transition(LifecycleStatusCompleted, LifecycleStatusFailed); decision.Disposition != LifecycleDispositionKeep {
		t.Fatalf("terminal Safeheron state must win = %#v", decision)
	}

	airwallex := AirwallexLifecyclePolicy{}
	if airwallex.Channel() != ChannelAirwallex {
		t.Fatal("Airwallex policy channel mismatch")
	}
	if decision := airwallex.Transition("", LifecycleStatusPaid); decision.Disposition != LifecycleDispositionApply {
		t.Fatalf("empty -> paid = %#v", decision)
	}
	if decision := airwallex.Transition(LifecycleStatusPaid, LifecycleStatusPaid); decision.Disposition != LifecycleDispositionKeep {
		t.Fatalf("same Airwallex state = %#v", decision)
	}
	if decision := airwallex.Transition(LifecycleStatusPaid, "UNKNOWN"); decision.Disposition != LifecycleDispositionQuarantine {
		t.Fatalf("unknown incoming Airwallex status = %#v", decision)
	}
	if decision := airwallex.Transition("UNKNOWN", LifecycleStatusPaid); decision.Disposition != LifecycleDispositionQuarantine {
		t.Fatalf("unknown stored Airwallex status = %#v", decision)
	}

	invalidRelations := []MovementRelation{
		{MovementKind: MovementKind("UNKNOWN"), TransferMode: TransferModeSingle, Direction: DirectionInflow, HasToAccount: true},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferMode("UNKNOWN"), Direction: DirectionInflow, HasToAccount: true},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferModeSingle, Direction: Direction("UNKNOWN"), HasToAccount: true},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferModeSingle, Direction: DirectionInflow},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferModeSingle, Direction: DirectionInflow, HasFromAccount: true},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferModeSingle, Direction: DirectionOutflow, HasToAccount: true},
		{MovementKind: MovementKindPrincipal, TransferMode: TransferModeSingle, Direction: DirectionInternalTransfer, HasToAccount: true},
		{MovementKind: MovementKindReversal, TransferMode: TransferModeSingle, Direction: DirectionInflow, HasToAccount: true},
		{MovementKind: MovementKindConversion, TransferMode: TransferModeSingle, Direction: DirectionInflow, HasToAccount: true},
		{MovementKind: MovementKindConversion, TransferMode: TransferModeSingle, Direction: DirectionInflow, HasToAccount: true, ConversionGroupKey: "g"},
		{MovementKind: MovementKindConversion, TransferMode: TransferModeSingle, Direction: DirectionInflow, HasToAccount: true, ConversionGroupKey: "g", ConversionLeg: ConversionLegBuy},
	}
	for _, relation := range invalidRelations {
		if err := ValidateMovementRelationship(relation); err == nil {
			t.Fatalf("invalid relation %+v unexpectedly succeeded", relation)
		}
	}
}

func TestMergeProviderFields_OrdersByTimeAndFillsMissingFields(t *testing.T) {
	oldTime := time.Date(2026, time.July, 10, 1, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Minute)
	oldAmount := decimal.RequireFromString("1")
	newAmount := decimal.RequireFromString("2")
	existing := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{UpdatedAt: &oldTime, Source: ProviderSourceWebhook},
		Amount:   &oldAmount,
		Currency: stringPointer("USDT"),
		Asset:    &AssetIdentity{Currency: "USDT", ChainCode: "ETH", ContractAddress: "0x1"},
		Status:   lifecycleStatusPointer(LifecycleStatusPending),
	}
	incoming := ProviderOwnedFields{
		Metadata:    ProviderFactMetadata{UpdatedAt: &newTime, Source: ProviderSourceWebhook},
		Amount:      &newAmount,
		Currency:    stringPointer("USDC"),
		Asset:       &AssetIdentity{Currency: "USDC", ChainCode: "ETH", ContractAddress: "0x2"},
		TxHash:      stringPointer("0xfill"),
		Status:      lifecycleStatusPointer(LifecycleStatusConfirming),
		CompletedAt: &newTime,
	}
	merged, decision := MergeProviderFields(existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || merged.Amount == nil || !merged.Amount.Equal(newAmount) || merged.TxHash == nil || *merged.TxHash != "0xfill" || merged.CompletedAt == nil || !merged.CompletedAt.Equal(newTime) {
		t.Fatalf("newer timestamp must win provider fields: %#v %#v", merged, decision)
	}

	lowerRevision := int64(1)
	higherRevision := int64(2)
	existing.Metadata.Revision = &higherRevision
	incoming.Metadata.Revision = &lowerRevision
	retained, decision := MergeProviderFields(existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || retained.Amount == nil || !retained.Amount.Equal(oldAmount) || retained.TxHash == nil || *retained.TxHash != "0xfill" {
		t.Fatalf("lower revision must retain known money while it may fill an absent non-money field: %#v %#v", retained, decision)
	}

	empty := ProviderOwnedFields{}
	filled, decision := MergeProviderFields(empty, ProviderOwnedFields{Metadata: ProviderFactMetadata{Source: ProviderSourceWebhook}, TxHash: stringPointer("0xfirst")})
	if decision.Outcome != MergeOutcomeApplied || filled.TxHash == nil || *filled.TxHash != "0xfirst" {
		t.Fatalf("incoming non-null must fill an absent provider field: %#v %#v", filled, decision)
	}

	revision := int64(3)
	conflictBase := ProviderOwnedFields{Metadata: ProviderFactMetadata{Revision: &revision, Source: ProviderSourceWebhook}, Currency: stringPointer("USDT")}
	if _, decision := MergeProviderFields(conflictBase, ProviderOwnedFields{Metadata: conflictBase.Metadata, Currency: stringPointer("USDC")}); decision.Outcome != MergeOutcomeQuarantine {
		t.Fatalf("equal priority currency conflict must quarantine: %#v", decision)
	}
}

func TestRiskAndValuationErrorAndUnpricedPaths(t *testing.T) {
	if _, err := PolicySubjectAccountID(DirectionInflow, nil, nil); err == nil {
		t.Fatal("inflow without destination account must fail policy subject selection")
	}
	if _, err := PolicySubjectAccountID(DirectionOutflow, nil, nil); err == nil {
		t.Fatal("outflow without source account must fail policy subject selection")
	}
	if _, err := PolicySubjectAccountID(Direction("OTHER"), nil, nil); err == nil {
		t.Fatal("unknown direction must fail policy subject selection")
	}
	if _, err := EvaluateRisk(RiskInput{Channel: Channel("OTHER"), Direction: DirectionInflow}); err == nil {
		t.Fatal("unknown risk channel must fail")
	}
	if _, err := EvaluateRisk(RiskInput{Channel: ChannelSafeheron, Direction: Direction("OTHER")}); err == nil {
		t.Fatal("unknown risk direction must fail")
	}
	if _, err := EvaluateRisk(RiskInput{Channel: ChannelSafeheron, Direction: DirectionInflow, Amount: decimal.NewFromInt(-1)}); err == nil {
		t.Fatal("negative risk amount must fail")
	}
	negativeThreshold := decimal.NewFromInt(-1)
	if _, err := EvaluateRisk(RiskInput{Channel: ChannelSafeheron, Direction: DirectionInflow, Policy: DustPolicy{ID: 1, Enabled: true, Threshold: &negativeThreshold}}); err == nil {
		t.Fatal("negative dust threshold must fail")
	}

	amount := decimal.NewFromInt(1)
	if _, err := EvaluateUSDValue(USDValuationInput{Channel: Channel("OTHER"), Amount: amount}); err == nil {
		t.Fatal("unknown valuation channel must fail")
	}
	if _, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelSafeheron, Amount: decimal.NewFromInt(-1)}); err == nil {
		t.Fatal("negative valuation amount must fail")
	}
	negative := decimal.NewFromInt(-1)
	if _, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelSafeheron, Amount: amount, ProviderReportedUSD: &negative}); err == nil {
		t.Fatal("negative provider USD must fail")
	}
	if _, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelSafeheron, Amount: amount, CoinGeckoUnitPrice: &negative}); err == nil {
		t.Fatal("negative market price must fail")
	}

	zeroAmount, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelSafeheron, Currency: "ETH", Amount: decimal.Zero})
	if err != nil || zeroAmount.Status != USDValuationStatusUnpriced || zeroAmount.Reason != USDValuationReasonZeroAmount {
		t.Fatalf("zero non-USD amount must remain unpriced: %#v, %v", zeroAmount, err)
	}
	parentUSD := decimal.NewFromInt(10)
	unproven, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelSafeheron, MovementKind: MovementKindPrincipal, Currency: "BTC", Amount: amount, ProviderReportedUSD: &parentUSD, ProviderValueScope: ProviderValueScopeTransactionTotal})
	if err != nil || unproven.Value != nil || unproven.Reason != USDValuationReasonUnprovenProviderScope {
		t.Fatalf("parent total must not value a child: %#v, %v", unproven, err)
	}
	usdSided, err := EvaluateUSDValue(USDValuationInput{Channel: ChannelAirwallex, MovementKind: MovementKindConversion, Currency: "JPY", Amount: amount, ProviderReportedUSD: &parentUSD, ProviderValueScope: ProviderValueScopeDirectItem, AirwallexConversionFrom: "JPY", AirwallexConversionTo: "USD", IngestionAt: testValuationIngestionAt()})
	if err != nil || usdSided.Source != USDValuationSourceAirwallex || usdSided.Value == nil || !usdSided.Value.Equal(parentUSD) {
		t.Fatalf("USD-sided Airwallex conversion must use its direct provider value: %#v, %v", usdSided, err)
	}
}

func TestHistoricalAndBTCCrossInvalidPaths(t *testing.T) {
	target := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	if _, ok := SelectHistoricalPrice(nil, time.Time{}, time.Minute); ok {
		t.Fatal("zero target must be rejected")
	}
	if _, ok := SelectHistoricalPrice(nil, target, -time.Minute); ok {
		t.Fatal("negative gap must be rejected")
	}
	if _, ok := SelectHistoricalPrice([]HistoricalPricePoint{{Price: decimal.Zero, PriceAt: target}}, target, time.Minute); ok {
		t.Fatal("zero price must not represent a usable rate")
	}

	valid := BTCCrossLeg{Price: decimal.RequireFromString("60000"), PriceAt: target, AvailableAt: target, Provider: "COINGECKO", AssetID: "bitcoin", Quote: "USD", PolicyVersion: "v1", Granularity: "HOUR", BucketStart: target.Truncate(time.Hour), IsEligibleLeaf: true, IsFinal: true, PriceKind: MarketPriceKindHistorical}
	denominator := valid
	denominator.Price = decimal.RequireFromString("9000000")
	denominator.Quote = "JPY"
	if _, ok := DeriveBTCCross(valid, denominator, target, time.Minute); !ok {
		t.Fatal("baseline historical BTC cross must be eligible before guard mutations")
	}
	denominator.BucketStart = denominator.BucketStart.Add(time.Hour)
	if _, ok := DeriveBTCCross(valid, denominator, target, time.Minute); ok {
		t.Fatal("different buckets must not derive a BTC cross")
	}
	denominator.BucketStart = valid.BucketStart
	denominator.IsFinal = false
	if _, ok := DeriveBTCCross(valid, denominator, target, time.Minute); ok {
		t.Fatal("non-final BTC input must not derive a final cross")
	}
	if _, err := decimalDivideBank(decimal.NewFromInt(1), decimal.Zero); err == nil {
		t.Fatal("zero denominator must fail exact division")
	}
	if _, err := decimalDivideBank(decimal.NewFromInt(-1), decimal.NewFromInt(1)); err == nil {
		t.Fatal("negative exact division input must fail")
	}
}

func TestDecimalDivideBank_MatchesStageBTCCrossPrecision(t *testing.T) {
	numerator := decimal.RequireFromString("64671.596099883160000000")
	denominator := decimal.RequireFromString("437742.632521279040000000")

	actual, err := decimalDivideBank(numerator, denominator)
	if err != nil {
		t.Fatalf("decimalDivideBank() error = %v", err)
	}
	if expected := decimal.RequireFromString("0.147738856796726142"); !actual.Equal(expected) {
		t.Fatalf("decimalDivideBank() = %s, want %s", actual, expected)
	}
}

func lifecycleStatusPointer(value LifecycleStatus) *LifecycleStatus { return &value }
