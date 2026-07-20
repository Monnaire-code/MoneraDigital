package companyfund

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAirwallexFinancialTransactionNormalizer_AppliesPinnedPrincipalWithExactDecimalsAndStableIdentity(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t,
		AirwallexFinancialTransactionClassification{
			TransactionType:   "DEPOSIT_CREDIT",
			SourceType:        "BANK_FEED",
			Action:            AirwallexFinancialTransactionActionApply,
			MovementKind:      MovementKindPrincipal,
			Direction:         DirectionInflow,
			TransferMode:      TransferModeSingle,
			AmountField:       AirwallexFinancialAmountFieldAmount,
			ExpectedSign:      AirwallexFinancialValueSignPositive,
			OccurredAtField:   AirwallexFinancialOccurredAtCreated,
			IncludeFeeDisplay: true,
			FeeDisplaySign:    AirwallexFinancialValueSignPositive,
		},
	)
	input := validAirwallexFinancialTransactionInput()
	input.Counterparty = &AirwallexCounterparty{AddressOrAccount: "payer_1", Name: "Supplier One"}
	input.FinancialTransaction.Amount = json.RawMessage("12.3400")
	input.FinancialTransaction.Fee = json.RawMessage("0.100000000000000001")
	input.FinancialTransaction.Net = json.RawMessage("12.239999999999999999")
	input.FinancialTransaction.ClientRate = json.RawMessage("1.000000000000000001")

	result := normalizer.Normalize(input)
	if result.Disposition != AirwallexFinancialTransactionDispositionApply {
		t.Fatalf("Normalize() disposition = %#v, want APPLY; reason=%s", result.Disposition, result.Reason)
	}
	if result.Movement == nil || !result.Movement.Amount.Equal(decimal.RequireFromString("12.34")) {
		t.Fatalf("movement amount = %#v, want exact 12.34", result.Movement)
	}
	if result.Movement.Direction != DirectionInflow || result.Movement.ToAccountID == nil || *result.Movement.ToAccountID != input.ConfiguredAccount.ID {
		t.Fatalf("inflow account direction = %#v", result.Movement)
	}
	if result.ProviderFact == nil || !result.ProviderFact.ProviderAmount.Equal(decimal.RequireFromString("12.34")) {
		t.Fatalf("provider fact = %#v, want exact direct amount", result.ProviderFact)
	}
	if _, err := result.ProviderFact.validate(); err != nil {
		t.Fatalf("normalized provider fact must be persistable: %v", err)
	}
	if result.Transaction == nil || result.Transaction.ProviderTransactionID != "ft_123" || result.Transaction.ProviderMovementID != "ft_123" {
		t.Fatalf("transaction must use the financial transaction ID as its stable line identity: %#v", result.Transaction)
	}
	if result.Transaction.ProviderTransactionID == input.FinancialTransaction.SourceID {
		t.Fatal("unverified source_id must not be promoted into the stable transaction identity")
	}
	if result.SourceID != "deposit_123" {
		t.Fatalf("source_id must remain an explicit provider fact, got %q", result.SourceID)
	}
	if err := result.Transaction.validate(); err != nil {
		t.Fatalf("normalized transaction must be persistable: %v", err)
	}
	workerResult, err := result.ProviderEventNormalization()
	if err != nil {
		t.Fatalf("ProviderEventNormalization() error = %v", err)
	}
	if len(workerResult.Facts) != 1 || len(workerResult.Movements) != 1 || len(workerResult.FactBindings) != 1 ||
		workerResult.FactBindings[0].MovementKey != result.Transaction.MovementKey ||
		workerResult.FactBindings[0].FactReference != workerResult.Facts[0].Reference {
		t.Fatalf("direct balance fact must bind only its own movement: %#v", workerResult)
	}
	if err := workerResult.validate(); err != nil {
		t.Fatalf("worker result must satisfy facts-first binding contract: %v", err)
	}
	if result.Transaction.ProviderDisplay.Fee.Amount == nil || !result.Transaction.ProviderDisplay.Fee.Amount.Equal(decimal.RequireFromString("0.100000000000000001")) {
		t.Fatalf("explicit fee display must preserve exact decimal: %#v", result.Transaction.ProviderDisplay.Fee)
	}
	if result.Risk == nil || result.Risk.IsDust || result.Transaction.AutomaticRisk.IsDust != nil {
		t.Fatalf("fiat without an explicit asset policy must not auto-classify as dust: %#v %#v", result.Risk, result.Transaction.AutomaticRisk)
	}
	if !strings.Contains(string(result.ProviderFact.ProviderExtrasJSON), `"client_rate":1.000000000000000001`) {
		t.Fatalf("provider extras must preserve client_rate as a JSON number, got %s", result.ProviderFact.ProviderExtrasJSON)
	}

	changedCounterparty := input
	changedCounterparty.Counterparty = &AirwallexCounterparty{AddressOrAccount: "later_enrichment", Name: "Supplier Renamed"}
	changed := normalizer.Normalize(changedCounterparty)
	if changed.Disposition != AirwallexFinancialTransactionDispositionApply || changed.Movement.Identity.Key != result.Movement.Identity.Key {
		t.Fatalf("optional counterparty enrichment must not change financial-transaction movement identity: %#v", changed)
	}
	batchSibling := input
	batchSibling.FinancialTransaction.ProviderID = "ft_124"
	batchSibling.FinancialTransaction.BatchID = "batch_1"
	sibling := normalizer.Normalize(batchSibling)
	if sibling.Disposition != AirwallexFinancialTransactionDispositionApply || sibling.Movement.Identity.Key == result.Movement.Identity.Key {
		t.Fatalf("same-amount batch items must use distinct financial transaction IDs, never provider array positions: %#v", sibling)
	}
	zeroFee := input
	zeroFee.FinancialTransaction.Fee = json.RawMessage("0")
	withoutFeeDisplay := normalizer.Normalize(zeroFee)
	if withoutFeeDisplay.Disposition != AirwallexFinancialTransactionDispositionApply || withoutFeeDisplay.Transaction.ProviderDisplay.Fee.Amount != nil {
		t.Fatalf("zero optional fee must not turn a valid principal into a quarantined or duplicate fee movement: %#v", withoutFeeDisplay)
	}
}

func TestAirwallexFinancialTransactionNormalizer_RequiresExplicitAccountVersionsAndSourceMetadata(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t, testAirwallexPrincipalClassification())

	testCases := []struct {
		name   string
		mutate func(*AirwallexFinancialTransactionNormalizationInput)
	}{
		{
			name: "missing configured account",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.ConfiguredAccount = CompanyFundAccount{}
			},
		},
		{
			name: "provider account key mismatch",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.ProviderAccountKey = "another-account"
			},
		},
		{
			name: "schema version mismatch",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.SchemaVersion = "2025-01-01"
			},
		},
		{
			name: "event version mismatch",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.EventVersion = "event-v0"
			},
		},
		{
			name: "missing source event metadata",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.Source.ProviderEventRecordID = 0
			},
		},
		{
			name: "invalid source payload digest",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.Source.PayloadDigest = "not-a-digest"
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := validAirwallexFinancialTransactionInput()
			testCase.mutate(&input)
			result := normalizer.Normalize(input)
			if result.Disposition != AirwallexFinancialTransactionDispositionQuarantine || result.Movement != nil || result.Transaction != nil {
				t.Fatalf("Normalize() = %#v, want quarantine with no emitted movement", result)
			}
		})
	}
}

func TestAirwallexFinancialTransactionNormalizer_QuarantinesUnknownClassificationAndNonNumericValues(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t, testAirwallexPrincipalClassification())

	testCases := []struct {
		name   string
		mutate func(*AirwallexFinancialTransactionNormalizationInput)
	}{
		{
			name: "unknown transaction type",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "UNPINNED_TYPE"
			},
		},
		{
			name: "amount string instead of JSON number",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.Amount = json.RawMessage(`"12.34"`)
			},
		},
		{
			name: "fee object instead of JSON number",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.Fee = json.RawMessage(`{"value": "1"}`)
			},
		},
		{
			name: "net null instead of JSON number",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.Net = json.RawMessage("null")
			},
		},
		{
			name: "client rate array instead of JSON number",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.ClientRate = json.RawMessage("[1]")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := validAirwallexFinancialTransactionInput()
			testCase.mutate(&input)
			result := normalizer.Normalize(input)
			if result.Disposition != AirwallexFinancialTransactionDispositionQuarantine || result.Movement != nil {
				t.Fatalf("Normalize() = %#v, want numeric/classification quarantine", result)
			}
		})
	}
}

func TestAirwallexFinancialTransactionNormalizer_ExplicitlyIgnoresReserveHold(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t,
		AirwallexFinancialTransactionClassification{
			TransactionType: "RESERVE",
			SourceType:      "HOLD",
			Action:          AirwallexFinancialTransactionActionIgnore,
			Reason:          "RESERVE_HOLD_NOT_A_BALANCE_MOVEMENT",
		},
	)
	input := validAirwallexFinancialTransactionInput()
	input.FinancialTransaction.TransactionType = "RESERVE"
	input.FinancialTransaction.SourceType = "HOLD"

	result := normalizer.Normalize(input)
	if result.Disposition != AirwallexFinancialTransactionDispositionIgnore || result.Movement != nil || result.ProviderFact != nil {
		t.Fatalf("Normalize() = %#v, want explicitly ignored reserve/hold", result)
	}
}

func TestAirwallexFinancialTransactionNormalizer_MapsFeeAdjustmentReversalAndConversionOnlyWithExplicitRules(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t,
		AirwallexFinancialTransactionClassification{
			TransactionType: "FEE_LINE", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindFee, Direction: DirectionOutflow, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldFee, ExpectedSign: AirwallexFinancialValueSignPositive,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		},
		AirwallexFinancialTransactionClassification{
			TransactionType: "ADJUSTMENT", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindAdjustment, Direction: DirectionOutflow, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldNet, ExpectedSign: AirwallexFinancialValueSignNegative,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		},
		AirwallexFinancialTransactionClassification{
			TransactionType: "REVERSAL", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindReversal, Direction: DirectionInflow, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldAmount, ExpectedSign: AirwallexFinancialValueSignPositive,
			OccurredAtField: AirwallexFinancialOccurredAtSettled,
		},
		AirwallexFinancialTransactionClassification{
			TransactionType: "CONVERSION_LEG", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindConversion, Direction: DirectionInternalTransfer, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldAmount, ExpectedSign: AirwallexFinancialValueSignNegative,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
			ClientRateUse:   AirwallexFinancialClientRateUseConversionRate,
		},
	)

	testCases := []struct {
		name     string
		mutate   func(*AirwallexFinancialTransactionNormalizationInput)
		kind     MovementKind
		amount   string
		validate func(t *testing.T, result AirwallexFinancialTransactionNormalizationResult)
	}{
		{
			name:   "fee linked to parent",
			kind:   MovementKindFee,
			amount: "1.25",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "FEE_LINE"
				input.FinancialTransaction.Fee = json.RawMessage("1.25")
				input.Relationship.ParentMovementKey = "v1:principal"
			},
			validate: func(t *testing.T, result AirwallexFinancialTransactionNormalizationResult) {
				if result.Movement.ParentMovementKey != "v1:principal" {
					t.Fatalf("fee parent link = %q", result.Movement.ParentMovementKey)
				}
			},
		},
		{
			name:   "adjustment uses explicitly configured net sign",
			kind:   MovementKindAdjustment,
			amount: "5.75",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "ADJUSTMENT"
				input.FinancialTransaction.Net = json.RawMessage("-5.75")
			},
		},
		{
			name:   "reversal is a linked movement",
			kind:   MovementKindReversal,
			amount: "20",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "REVERSAL"
				input.FinancialTransaction.Amount = json.RawMessage("20")
				input.FinancialTransaction.SettledAt = "2026-07-10T01:02:03Z"
				input.Relationship.ReversalOfMovementKey = "v1:original"
			},
			validate: func(t *testing.T, result AirwallexFinancialTransactionNormalizationResult) {
				if result.Movement.ReversalOfMovementKey != "v1:original" {
					t.Fatalf("reversal link = %q", result.Movement.ReversalOfMovementKey)
				}
			},
		},
		{
			name:   "conversion needs both explicit accounts relation and rate contract",
			kind:   MovementKindConversion,
			amount: "100",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "CONVERSION_LEG"
				input.FinancialTransaction.Amount = json.RawMessage("-100")
				input.FinancialTransaction.ClientRate = json.RawMessage("0.00725")
				input.CounterpartyCompanyAccount = &CompanyFundAccount{ID: 8, Channel: AccountChannelAirwallex, ProviderAccountKey: "awx-jpy", Enabled: true}
				input.ConfiguredAccountSide = AirwallexConfiguredAccountSideFrom
				input.Relationship.ConversionGroupKey = "conversion_1"
				input.Relationship.ConversionLeg = ConversionLegSell
				input.Relationship.ConversionGroupState = ConversionGroupIncomplete
				input.Conversion = AirwallexConversionDetails{FromCurrency: "JPY", ToCurrency: "USD"}
			},
			validate: func(t *testing.T, result AirwallexFinancialTransactionNormalizationResult) {
				if result.Movement.ConversionGroupKey != "conversion_1" || result.Movement.FromAccountID == nil || result.Movement.ToAccountID == nil {
					t.Fatalf("conversion relation = %#v", result.Movement)
				}
				if result.ProviderFact.ConversionRate == nil || !result.ProviderFact.ConversionRate.Equal(decimal.RequireFromString("0.00725")) {
					t.Fatalf("conversion rate = %#v", result.ProviderFact)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := validAirwallexFinancialTransactionInput()
			testCase.mutate(&input)
			result := normalizer.Normalize(input)
			if result.Disposition != AirwallexFinancialTransactionDispositionApply || result.Movement == nil {
				t.Fatalf("Normalize() = %#v, want applied %s", result, testCase.kind)
			}
			if result.Movement.MovementKind != testCase.kind || !result.Movement.Amount.Equal(decimal.RequireFromString(testCase.amount)) {
				t.Fatalf("movement = %#v, want %s %s", result.Movement, testCase.kind, testCase.amount)
			}
			if result.Transaction.ParentMovementKey != result.Movement.ParentMovementKey ||
				result.Transaction.ReversalOfMovementKey != result.Movement.ReversalOfMovementKey ||
				result.Transaction.ConversionGroupKey != result.Movement.ConversionGroupKey ||
				result.Transaction.ConversionLeg != result.Movement.ConversionLeg ||
				result.Transaction.ConversionGroupState != result.Movement.ConversionGroupState {
				t.Fatalf("transaction linkage must match the normalized movement: %#v %#v", result.Transaction, result.Movement)
			}
			if testCase.validate != nil {
				testCase.validate(t, result)
			}
		})
	}
}

func TestAirwallexFinancialTransactionNormalizer_QuarantinesUnlinkedFeeReversalAndConversion(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t,
		AirwallexFinancialTransactionClassification{
			TransactionType: "FEE_LINE", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindFee, Direction: DirectionOutflow, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldFee, ExpectedSign: AirwallexFinancialValueSignPositive,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		},
		AirwallexFinancialTransactionClassification{
			TransactionType: "REVERSAL", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindReversal, Direction: DirectionInflow, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldAmount, ExpectedSign: AirwallexFinancialValueSignPositive,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		},
		AirwallexFinancialTransactionClassification{
			TransactionType: "CONVERSION_LEG", SourceType: "BANK_FEED", Action: AirwallexFinancialTransactionActionApply,
			MovementKind: MovementKindConversion, Direction: DirectionInternalTransfer, TransferMode: TransferModeSingle,
			AmountField: AirwallexFinancialAmountFieldAmount, ExpectedSign: AirwallexFinancialValueSignNegative,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		},
	)
	testCases := []struct {
		name   string
		mutate func(*AirwallexFinancialTransactionNormalizationInput)
	}{
		{
			name: "fee without parent",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "FEE_LINE"
				input.FinancialTransaction.Fee = json.RawMessage("1")
			},
		},
		{
			name: "reversal without original",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "REVERSAL"
			},
		},
		{
			name: "conversion without two explicit accounts and group",
			mutate: func(input *AirwallexFinancialTransactionNormalizationInput) {
				input.FinancialTransaction.TransactionType = "CONVERSION_LEG"
				input.FinancialTransaction.Amount = json.RawMessage("-1")
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := validAirwallexFinancialTransactionInput()
			testCase.mutate(&input)
			result := normalizer.Normalize(input)
			if result.Disposition != AirwallexFinancialTransactionDispositionQuarantine || result.Movement != nil {
				t.Fatalf("Normalize() = %#v, want unlinked movement quarantine", result)
			}
		})
	}
}

func TestAirwallexFinancialTransactionNormalizer_UsesExplicitFiatDustPolicyOnly(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t, testAirwallexPrincipalClassification())
	input := validAirwallexFinancialTransactionInput()
	input.FinancialTransaction.Amount = json.RawMessage("0.0001")

	withoutPolicy := normalizer.Normalize(input)
	if withoutPolicy.Disposition != AirwallexFinancialTransactionDispositionApply || withoutPolicy.Risk == nil || withoutPolicy.Risk.IsDust {
		t.Fatalf("fiat without policy must not be dust: %#v", withoutPolicy)
	}

	threshold := decimal.RequireFromString("0.01")
	input.AssetPolicy = &AccountAssetPolicy{
		ID:        9,
		AccountID: input.ConfiguredAccount.ID,
		Asset:     AssetIdentity{Currency: "USD"},
		Enabled:   true,
		Dust: DustPolicy{
			ID:        9,
			Enabled:   true,
			Threshold: &threshold,
		},
	}
	withPolicy := normalizer.Normalize(input)
	if withPolicy.Disposition != AirwallexFinancialTransactionDispositionApply || withPolicy.Risk == nil || !withPolicy.Risk.IsDust || withPolicy.Transaction.AutomaticRisk.IsDust == nil || !*withPolicy.Transaction.AutomaticRisk.IsDust {
		t.Fatalf("explicit matching fiat policy must be applied: %#v", withPolicy)
	}
}

func TestNewAirwallexFinancialTransactionNormalizer_RejectsUnversionedWildcardAndDuplicateRules(t *testing.T) {
	base := AirwallexFinancialTransactionNormalizerConfig{
		SchemaVersion:   "2026-07-01",
		EventVersion:    "event-v1",
		MappingVersion:  "mapping-v1",
		FactVersion:     1,
		Classifications: []AirwallexFinancialTransactionClassification{testAirwallexPrincipalClassification()},
	}
	testCases := []struct {
		name   string
		mutate func(*AirwallexFinancialTransactionNormalizerConfig)
	}{
		{
			name: "missing mapping version",
			mutate: func(config *AirwallexFinancialTransactionNormalizerConfig) {
				config.MappingVersion = ""
			},
		},
		{
			name: "wildcard transaction type",
			mutate: func(config *AirwallexFinancialTransactionNormalizerConfig) {
				config.Classifications[0].TransactionType = "*"
			},
		},
		{
			name: "duplicate mapping",
			mutate: func(config *AirwallexFinancialTransactionNormalizerConfig) {
				config.Classifications = append(config.Classifications, testAirwallexPrincipalClassification())
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := base
			config.Classifications = append([]AirwallexFinancialTransactionClassification(nil), base.Classifications...)
			testCase.mutate(&config)
			if _, err := NewAirwallexFinancialTransactionNormalizer(config); err == nil {
				t.Fatal("NewAirwallexFinancialTransactionNormalizer() error = nil, want invalid versioned allowlist rejection")
			}
		})
	}
}

func newAirwallexFinancialTransactionNormalizerForTest(t *testing.T, classifications ...AirwallexFinancialTransactionClassification) *AirwallexFinancialTransactionNormalizer {
	t.Helper()
	normalizer, err := NewAirwallexFinancialTransactionNormalizer(AirwallexFinancialTransactionNormalizerConfig{
		SchemaVersion:   "2026-07-01",
		EventVersion:    "event-v1",
		MappingVersion:  "mapping-v1",
		FactVersion:     1,
		Classifications: classifications,
	})
	if err != nil {
		t.Fatalf("NewAirwallexFinancialTransactionNormalizer() error = %v", err)
	}
	return normalizer
}

func testAirwallexPrincipalClassification() AirwallexFinancialTransactionClassification {
	return AirwallexFinancialTransactionClassification{
		TransactionType: "DEPOSIT_CREDIT",
		SourceType:      "BANK_FEED",
		Action:          AirwallexFinancialTransactionActionApply,
		MovementKind:    MovementKindPrincipal,
		Direction:       DirectionInflow,
		TransferMode:    TransferModeSingle,
		AmountField:     AirwallexFinancialAmountFieldAmount,
		ExpectedSign:    AirwallexFinancialValueSignPositive,
		OccurredAtField: AirwallexFinancialOccurredAtCreated,
	}
}

func validAirwallexFinancialTransactionInput() AirwallexFinancialTransactionNormalizationInput {
	return AirwallexFinancialTransactionNormalizationInput{
		SchemaVersion:      "2026-07-01",
		EventVersion:       "event-v1",
		ProviderAccountKey: "awx-usd",
		ConfiguredAccount: CompanyFundAccount{
			ID:                 7,
			Channel:            AccountChannelAirwallex,
			ProviderAccountKey: "awx-usd",
			CompanyEntity:      "Monera Ltd",
			FundAccountName:    "Operating",
			SubAccountName:     "USD",
			AccountType:        "BANK",
			Enabled:            true,
		},
		Source: AirwallexFinancialTransactionSourceMetadata{
			ProviderEventID:       "evt_123",
			ProviderEventRecordID: 42,
			PayloadDigest:         strings.Repeat("a", 64),
			FactSource:            ProviderSourceProductDetail,
			SeenSource:            TransactionSeenSourceWebhook,
		},
		FinancialTransaction: AirwallexFinancialTransaction{
			ProviderID:      "ft_123",
			Amount:          json.RawMessage("12.34"),
			Fee:             json.RawMessage("0"),
			Net:             json.RawMessage("12.34"),
			ClientRate:      json.RawMessage("1"),
			CreatedAt:       "2026-07-10T01:02:03Z",
			SettledAt:       "2026-07-10T02:02:03Z",
			Currency:        "USD",
			SourceID:        "deposit_123",
			SourceType:      "BANK_FEED",
			Status:          "SETTLED",
			TransactionType: "DEPOSIT_CREDIT",
		},
	}
}

func TestAirwallexFinancialTransactionNormalizer_UsesTransactionTimeNotWallClock(t *testing.T) {
	normalizer := newAirwallexFinancialTransactionNormalizerForTest(t, testAirwallexPrincipalClassification())
	input := validAirwallexFinancialTransactionInput()
	result := normalizer.Normalize(input)
	if result.Disposition != AirwallexFinancialTransactionDispositionApply || result.Movement.Provider.OccurredAt == nil {
		t.Fatalf("Normalize() = %#v", result)
	}
	want, err := time.Parse(time.RFC3339, input.FinancialTransaction.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Movement.Provider.OccurredAt.Equal(want) {
		t.Fatalf("occurred at = %s, want %s", result.Movement.Provider.OccurredAt, want)
	}
}
