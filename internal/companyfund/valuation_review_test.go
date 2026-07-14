package companyfund

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestEvaluateUSDValue_ProviderZeroMeansMissingAndCanFallBackToMarket(t *testing.T) {
	zero := decimal.Zero
	price := decimal.RequireFromString("20")
	amount := decimal.NewFromInt(2)

	for _, input := range []USDValuationInput{
		{
			Channel:             ChannelSafeheron,
			MovementKind:        MovementKindPrincipal,
			Currency:            "ETH",
			Amount:              amount,
			ProviderReportedUSD: &zero,
			ProviderValueScope:  ProviderValueScopeDirectItem,
			CoinGeckoUnitPrice:  &price,
			IngestionAt:         testValuationIngestionAt(),
		},
		{
			Channel:                 ChannelAirwallex,
			MovementKind:            MovementKindConversion,
			Currency:                "JPY",
			Amount:                  amount,
			ProviderReportedUSD:     &zero,
			ProviderValueScope:      ProviderValueScopeDirectItem,
			AirwallexConversionFrom: "JPY",
			AirwallexConversionTo:   "USD",
			CoinGeckoUnitPrice:      &price,
			IngestionAt:             testValuationIngestionAt(),
		},
	} {
		result, err := EvaluateUSDValue(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.Source != USDValuationSourceCoinGecko || result.Value == nil || !result.Value.Equal(decimal.NewFromInt(40)) {
			t.Fatalf("zero provider USD must fall back to a non-zero market price: %#v", result)
		}
		if result.ProviderReportedUSD != nil {
			t.Fatalf("zero provider USD must be represented as missing in the valuation projection: %#v", result)
		}
	}

	result, err := EvaluateUSDValue(USDValuationInput{
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		Currency:            "ETH",
		Amount:              amount,
		ProviderReportedUSD: &zero,
		ProviderValueScope:  ProviderValueScopeDirectItem,
		IngestionAt:         testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != nil || result.Status != USDValuationStatusUnpriced || result.Reason != USDValuationReasonRateMissing {
		t.Fatalf("zero provider USD without a valid market price must be unpriced: %#v", result)
	}
}

func TestEvaluateUSDValue_ExplicitBankerQuantizationAtStorageScale(t *testing.T) {
	evenTie := decimal.RequireFromString("1.0000000000000000005")
	oddTie := decimal.RequireFromString("1.0000000000000000015")
	if got := quantizeDecimalBank(evenTie); !got.Equal(decimal.RequireFromString("1.000000000000000000")) {
		t.Fatalf("even half-ULP tie = %s, want 1.000000000000000000", got)
	}
	if got := quantizeDecimalBank(oddTie); !got.Equal(decimal.RequireFromString("1.000000000000000002")) {
		t.Fatalf("odd half-ULP tie = %s, want 1.000000000000000002", got)
	}

	market, err := EvaluateUSDValue(USDValuationInput{
		Channel:            ChannelSafeheron,
		MovementKind:       MovementKindPrincipal,
		Currency:           "ETH",
		Amount:             decimal.NewFromInt(2),
		CoinGeckoUnitPrice: &oddTie,
		IngestionAt:        testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !market.UnitPrice.Equal(decimal.RequireFromString("1.000000000000000002")) || market.Value == nil || !market.Value.Equal(decimal.RequireFromString("2.000000000000000004")) {
		t.Fatalf("market value must use the stored bank-quantized unit price: %#v", market)
	}

	provider, err := EvaluateUSDValue(USDValuationInput{
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		Currency:            "ETH",
		Amount:              decimal.NewFromInt(1),
		ProviderReportedUSD: &oddTie,
		ProviderValueScope:  ProviderValueScopeDirectItem,
		IngestionAt:         testValuationIngestionAt(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Value == nil || !provider.Value.Equal(decimal.RequireFromString("1.000000000000000002")) || provider.ProviderReportedUSD == nil || !provider.ProviderReportedUSD.Equal(*provider.Value) {
		t.Fatalf("provider USD must use the same storage quantization: %#v", provider)
	}
}

func TestEvaluateUSDValue_RequiresExplicitValuationTime(t *testing.T) {
	providerUSD := decimal.NewFromInt(10)
	provider, err := EvaluateUSDValue(USDValuationInput{
		Channel:             ChannelSafeheron,
		MovementKind:        MovementKindPrincipal,
		Currency:            "ETH",
		Amount:              decimal.NewFromInt(1),
		ProviderReportedUSD: &providerUSD,
		ProviderValueScope:  ProviderValueScopeDirectItem,
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Value != nil || provider.Status != USDValuationStatusUnpriced || provider.Reason != USDValuationReasonMissingValuationTime {
		t.Fatalf("provider valuation without an explicit time must be unpriced: %#v", provider)
	}

	price := decimal.NewFromInt(10)
	current, err := EvaluateUSDValue(USDValuationInput{
		Channel:            ChannelSafeheron,
		MovementKind:       MovementKindPrincipal,
		Currency:           "ETH",
		Amount:             decimal.NewFromInt(1),
		CoinGeckoUnitPrice: &price,
		CoinGeckoPriceKind: MarketPriceKindCurrent,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current.Value != nil || current.Status != USDValuationStatusUnpriced || current.Reason != USDValuationReasonMissingValuationTime {
		t.Fatalf("current market valuation without ingestion time must be unpriced: %#v", current)
	}
}

func TestEvaluateUSDValue_MarketTimeContract(t *testing.T) {
	target := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	priceAt := target.Add(-time.Minute)
	availableAt := target
	price := decimal.RequireFromString("10")

	validHistorical := USDValuationInput{
		Channel:              ChannelSafeheron,
		MovementKind:         MovementKindPrincipal,
		Currency:             "ETH",
		Amount:               decimal.NewFromInt(2),
		CoinGeckoUnitPrice:   &price,
		CoinGeckoPriceKind:   MarketPriceKindHistorical,
		CoinGeckoPriceAt:     &priceAt,
		CoinGeckoEffectiveAt: &priceAt,
		CoinGeckoAvailableAt: &availableAt,
		CoinGeckoGranularity: "MINUTE",
		ValuationTargetAt:    &target,
		HistoricalMaxGap:     2 * time.Minute,
	}
	result, err := EvaluateUSDValue(validHistorical)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != USDValuationStatusFinal || result.Basis != USDValuationBasisTransactionTime || result.PriceAt == nil || !result.PriceAt.Equal(priceAt) || result.AvailableAt == nil || !result.AvailableAt.Equal(availableAt) || result.Granularity != "MINUTE" {
		t.Fatalf("valid historical market price must be auditable final: %#v", result)
	}

	invalidHistorical := []USDValuationInput{
		func() USDValuationInput {
			value := validHistorical
			future := target.Add(time.Second)
			value.CoinGeckoPriceAt = &future
			return value
		}(),
		func() USDValuationInput {
			value := validHistorical
			missing := time.Time{}
			value.CoinGeckoAvailableAt = &missing
			return value
		}(),
		func() USDValuationInput {
			value := validHistorical
			stale := target.Add(-3 * time.Minute)
			value.CoinGeckoPriceAt = &stale
			return value
		}(),
		func() USDValuationInput {
			value := validHistorical
			late := target.Add(time.Second)
			value.CoinGeckoAvailableAt = &late
			value.AvailableAtCutoffAt = &target
			return value
		}(),
	}
	for _, input := range invalidHistorical {
		value, err := EvaluateUSDValue(input)
		if err != nil {
			t.Fatal(err)
		}
		if value.Value != nil || value.Status == USDValuationStatusFinal {
			t.Fatalf("invalid historical market observation must not be final: %#v", value)
		}
	}

	current := validHistorical
	current.CoinGeckoPriceKind = MarketPriceKindCurrent
	current.IngestionAt = &target
	current.ValuationTargetAt = nil
	result, err = EvaluateUSDValue(current)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != USDValuationStatusProvisional || result.Basis != USDValuationBasisIngestionTime || result.Value == nil || result.ValuationTargetAt == nil || !result.ValuationTargetAt.Equal(target) {
		t.Fatalf("current cache price must stay provisional/ingestion-time: %#v", result)
	}
}

func TestDeriveBTCCross_CurrentGroupAndHistoricalGroupRules(t *testing.T) {
	target := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	currentUSD, currentJPY := validBTCCrossLegs(target, MarketPriceKindCurrent)
	currentUSD.SnapshotGroupID = "response-1"
	currentJPY.SnapshotGroupID = "response-1"
	if _, ok := DeriveBTCCross(currentUSD, currentJPY, target, time.Minute); !ok {
		t.Fatal("same current response group must derive a BTC cross")
	}
	currentJPY.SnapshotGroupID = "response-2"
	if _, ok := DeriveBTCCross(currentUSD, currentJPY, target, time.Minute); ok {
		t.Fatal("different current response groups must not derive a BTC cross")
	}

	historicalUSD, historicalJPY := validBTCCrossLegs(target, MarketPriceKindHistorical)
	historicalUSD.SnapshotGroupID = "history-request-usd"
	historicalJPY.SnapshotGroupID = "history-request-jpy"
	if _, ok := DeriveBTCCross(historicalUSD, historicalJPY, target, time.Minute); !ok {
		t.Fatal("historical BTC inputs may use independent request groups when all series rules match")
	}
	historicalJPY.AvailableAt = time.Time{}
	if _, ok := DeriveBTCCross(historicalUSD, historicalJPY, target, time.Minute); ok {
		t.Fatal("missing BTC cross input availability must reject the derived rate")
	}
}

func validBTCCrossLegs(target time.Time, kind MarketPriceKind) (BTCCrossLeg, BTCCrossLeg) {
	usd := BTCCrossLeg{
		Price:          decimal.RequireFromString("60000"),
		PriceAt:        target.Add(-time.Minute),
		AvailableAt:    target.Add(-30 * time.Second),
		Provider:       "COINGECKO",
		AssetID:        "bitcoin",
		Quote:          "USD",
		PolicyVersion:  "v1",
		Granularity:    "HOUR",
		BucketStart:    target.Truncate(time.Hour),
		IsEligibleLeaf: true,
		IsFinal:        true,
		PriceKind:      kind,
	}
	jpy := usd
	jpy.Price = decimal.RequireFromString("9000000")
	jpy.Quote = "JPY"
	return usd, jpy
}

func testValuationIngestionAt() *time.Time {
	value := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	return &value
}
