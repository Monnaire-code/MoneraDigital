package companyfund

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestEvaluateRisk_DustPhishingAndManualInclusion(t *testing.T) {
	threshold := decimal.RequireFromString("0.01")
	assessment, err := EvaluateRisk(RiskInput{
		Channel:           ChannelSafeheron,
		Direction:         DirectionInflow,
		Amount:            decimal.RequireFromString("0.009999999999999999"),
		Asset:             AssetIdentity{Currency: "USDT", ChainCode: "ETH", ContractAddress: "0x1"},
		Policy:            DustPolicy{ID: 9, Enabled: true, Threshold: &threshold},
		SourcePhishing:    boolPointer(true),
		SummaryOverride:   boolPointer(true),
		ConfiguredFromID:  int64Pointer(1),
		ConfiguredToID:    int64Pointer(2),
		AMLLock:           boolPointer(true),
		AMLRiskLevel:      AMLRiskLevelHigh,
		UnrecognizedAsset: false,
	})
	if err != nil {
		t.Fatalf("EvaluateRisk: %v", err)
	}
	if !assessment.IsDust || !assessment.ReviewRequired || !assessment.AutomaticExclusion {
		t.Fatalf("expected dust/phishing/AML risk, got %#v", assessment)
	}
	if !assessment.EffectiveSummaryIncluded {
		t.Fatalf("manual inclusion override must win over automatic exclusion: %#v", assessment)
	}
	if assessment.ImmediateAlert {
		t.Fatalf("inbound source phishing is review-required but not outbound immediate alert: %#v", assessment)
	}

	equal, err := EvaluateRisk(RiskInput{
		Channel:   ChannelSafeheron,
		Direction: DirectionInflow,
		Amount:    threshold,
		Asset:     AssetIdentity{Currency: "USDT"},
		Policy:    DustPolicy{ID: 9, Enabled: true, Threshold: &threshold},
	})
	if err != nil {
		t.Fatal(err)
	}
	if equal.IsDust {
		t.Fatalf("amount equal to threshold must not be dust: %#v", equal)
	}
}

func TestEvaluateRisk_NoExplicitPolicyDoesNotClassifyDustAndOutboundPhishingAlerts(t *testing.T) {
	assessment, err := EvaluateRisk(RiskInput{
		Channel:             ChannelAirwallex,
		Direction:           DirectionOutflow,
		Amount:              decimal.RequireFromString("0.000000000000000001"),
		Asset:               AssetIdentity{Currency: "JPY"},
		DestinationPhishing: boolPointer(true),
		UnrecognizedAsset:   true,
		ConfiguredFromID:    int64Pointer(8),
		ConfiguredToID:      int64Pointer(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.IsDust {
		t.Fatalf("fiat without explicit policy must not be dust: %#v", assessment)
	}
	if !assessment.ImmediateAlert || !assessment.AutomaticExclusion {
		t.Fatalf("outbound phishing must immediately alert and exclude: %#v", assessment)
	}
	if assessment.AlertAggregationKey == "" {
		t.Fatal("risk assessment must expose an aggregation key")
	}

	subject, err := PolicySubjectAccountID(DirectionInternalTransfer, int64Pointer(8), int64Pointer(9))
	if err != nil || subject != 9 {
		t.Fatalf("internal transfer policy subject = %d, %v; want destination 9", subject, err)
	}
}

func TestEvaluateRisk_UnrecognizedAssetIsInformationalNotAutomaticRisk(t *testing.T) {
	assessment, err := EvaluateRisk(RiskInput{
		Channel:           ChannelSafeheron,
		Direction:         DirectionInflow,
		Amount:            decimal.RequireFromString("1"),
		Asset:             AssetIdentity{Currency: "UNKNOWN_COIN", ProviderAssetKey: "UNKNOWN_COIN"},
		UnrecognizedAsset: true,
		ConfiguredToID:    int64Pointer(9),
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.IsDust || assessment.AutomaticExclusion || assessment.ReviewRequired || len(assessment.Flags) != 0 {
		t.Fatalf("unknown asset without an explicit policy must remain an included non-risk movement: %#v", assessment)
	}
}

func TestEvaluateUSDValue_UnrecognizedUSDDoesNotGuessParButKeepsDirectProviderValue(t *testing.T) {
	amount := decimal.RequireFromString("2")
	unpriced, err := EvaluateUSDValue(USDValuationInput{
		Channel:           ChannelSafeheron,
		MovementKind:      MovementKindPrincipal,
		Currency:          "USD",
		UnrecognizedAsset: true,
		Amount:            amount,
		IngestionAt:       testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if unpriced.Value != nil || unpriced.Source == USDValuationSourceUSDPar || unpriced.Status != USDValuationStatusUnpriced {
		t.Fatalf("unknown raw CoinKey USD must not be assigned hardcoded par: %#v", unpriced)
	}

	providerValue := decimal.RequireFromString("3.5")
	provider, err := EvaluateUSDValue(USDValuationInput{
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		Currency:            "USD",
		UnrecognizedAsset:   true,
		Amount:              amount,
		ProviderReportedUSD: &providerValue,
		ProviderValueScope:  ProviderValueScopeDirectItem,
		IngestionAt:         testValuationIngestionAt(),
	})
	if err != nil || provider.Value == nil || !provider.Value.Equal(providerValue) || provider.Source != USDValuationSourceSafeheron {
		t.Fatalf("unknown asset direct provider USD = %#v, %v", provider, err)
	}
}

func TestEvaluateUSDValue_PrecedenceAndProviderScope(t *testing.T) {
	amount := decimal.RequireFromString("2")
	usd, err := EvaluateUSDValue(USDValuationInput{
		Channel:     ChannelAirwallex,
		Currency:    "USD",
		Amount:      amount,
		IngestionAt: testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if usd.Source != USDValuationSourceUSDPar || usd.Value == nil || !usd.Value.Equal(amount) || !usd.UnitPrice.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("USD par precedence failed: %#v", usd)
	}

	providerUSD := decimal.RequireFromString("20")
	provider, err := EvaluateUSDValue(USDValuationInput{
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		Currency:            "BTC",
		Amount:              amount,
		ProviderReportedUSD: &providerUSD,
		ProviderValueScope:  ProviderValueScopeDirectItem,
		ProviderScopeProven: true,
		CoinGeckoUnitPrice:  decimalPointer("9"),
		IngestionAt:         testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Source != USDValuationSourceSafeheron || provider.Value == nil || !provider.Value.Equal(providerUSD) {
		t.Fatalf("Safeheron direct provider fact must outrank market value: %#v", provider)
	}

	nonUSDCross, err := EvaluateUSDValue(USDValuationInput{
		Channel:                 ChannelAirwallex,
		MovementKind:            MovementKindConversion,
		Currency:                "JPY",
		Amount:                  amount,
		ProviderReportedUSD:     &providerUSD,
		AirwallexConversionFrom: "JPY",
		AirwallexConversionTo:   "SGD",
		CoinGeckoUnitPrice:      decimalPointer("0.006"),
		IngestionAt:             testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if nonUSDCross.Source != USDValuationSourceCoinGecko || nonUSDCross.Value == nil || !nonUSDCross.Value.Equal(decimal.RequireFromString("0.012")) {
		t.Fatalf("non-USD Airwallex cross must use market pricing, got %#v", nonUSDCross)
	}

	noZeroFallback, err := EvaluateUSDValue(USDValuationInput{
		Channel:                 ChannelAirwallex,
		MovementKind:            MovementKindConversion,
		Currency:                "JPY",
		Amount:                  amount,
		ProviderReportedUSD:     &providerUSD,
		ProviderValueScope:      ProviderValueScopeDirectItem,
		ProviderScopeProven:     true,
		AirwallexConversionFrom: "JPY",
		AirwallexConversionTo:   "SGD",
		CoinGeckoUnitPrice:      decimalPointer("0"),
		IngestionAt:             testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if noZeroFallback.Value != nil || noZeroFallback.Source == USDValuationSourceAirwallex || noZeroFallback.Reason != USDValuationReasonNonUSDCross {
		t.Fatalf("non-USD cross must never accept provider USD, even when scope is proven: %#v", noZeroFallback)
	}

	missingRate, err := EvaluateUSDValue(USDValuationInput{
		Channel:            ChannelSafeheron,
		MovementKind:       MovementKindPrincipal,
		Currency:           "ETH",
		Amount:             amount,
		CoinGeckoUnitPrice: decimalPointer("0"),
		IngestionAt:        testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if missingRate.Value != nil || missingRate.Status != USDValuationStatusUnpriced || missingRate.Reason != USDValuationReasonRateMissing {
		t.Fatalf("zero market price must remain an explicit missing rate, got %#v", missingRate)
	}
}

func TestSelectHistoricalPriceAndBTCCrossEligibility(t *testing.T) {
	target := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	selected, ok := SelectHistoricalPrice([]HistoricalPricePoint{
		{Price: decimal.RequireFromString("90"), PriceAt: target.Add(-2 * time.Minute)},
		{Price: decimal.RequireFromString("101"), PriceAt: target.Add(time.Minute)},
		{Price: decimal.RequireFromString("80"), PriceAt: target.Add(-5 * time.Minute)},
	}, target, 3*time.Minute)
	if !ok || !selected.Price.Equal(decimal.RequireFromString("90")) {
		t.Fatalf("must select newest non-future eligible point, got %#v %t", selected, ok)
	}
	if _, ok := SelectHistoricalPrice([]HistoricalPricePoint{{Price: decimal.NewFromInt(1), PriceAt: target.Add(-4 * time.Minute)}}, target, 3*time.Minute); ok {
		t.Fatal("over-gap historical point must be rejected")
	}

	numerator := BTCCrossLeg{Price: decimal.RequireFromString("60000"), PriceAt: target.Add(-time.Minute), AvailableAt: target.Add(-time.Minute), Provider: "COINGECKO", AssetID: "bitcoin", Quote: "USD", PolicyVersion: "v1", Granularity: "HOUR", BucketStart: target.Truncate(time.Hour), IsEligibleLeaf: true, IsFinal: true, PriceKind: MarketPriceKindHistorical}
	denominator := BTCCrossLeg{Price: decimal.RequireFromString("9000000"), PriceAt: target.Add(-2 * time.Minute), AvailableAt: target.Add(-2 * time.Minute), Provider: "COINGECKO", AssetID: "bitcoin", Quote: "JPY", PolicyVersion: "v1", Granularity: "HOUR", BucketStart: target.Truncate(time.Hour), IsEligibleLeaf: true, IsFinal: true, PriceKind: MarketPriceKindHistorical}
	cross, ok := DeriveBTCCross(numerator, denominator, target, 5*time.Minute)
	if !ok || !cross.UnitPrice.Equal(decimal.RequireFromString("0.006666666666666667")) || !cross.PriceAt.Equal(numerator.PriceAt) || !cross.AvailableAt.Equal(numerator.AvailableAt) {
		t.Fatalf("valid BTC cross = %#v, %t", cross, ok)
	}
	denominator.IsEligibleLeaf = false
	if _, ok := DeriveBTCCross(numerator, denominator, target, 5*time.Minute); ok {
		t.Fatal("superseded BTC input must invalidate derived cross")
	}
}

func int64Pointer(value int64) *int64 { return &value }
