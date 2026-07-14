package companyfund

import (
	"strings"
	"testing"

	"monera-digital/internal/safeheron"

	"github.com/shopspring/decimal"
)

func TestSafeheronNormalizerHelpers_RiskAndAmountBoundaries(t *testing.T) {
	for _, testCase := range []struct {
		input string
		want  *bool
	}{
		{input: "YES", want: safeheronBoolPointer(true)},
		{input: " no ", want: safeheronBoolPointer(false)},
		{input: "unknown", want: nil},
	} {
		got := safeheronAMLLock(testCase.input)
		if (got == nil) != (testCase.want == nil) || got != nil && *got != *testCase.want {
			t.Fatalf("AML lock %q = %#v, want %#v", testCase.input, got, testCase.want)
		}
	}

	for _, testCase := range []struct {
		triggered string
		records   []safeheron.TransactionAMLRecord
		want      AMLScreeningState
		known     bool
	}{
		{triggered: "UNTRIGGERED", want: AMLScreeningStateNotScreened, known: true},
		{triggered: "IN_PROGRESS", want: AMLScreeningStatePending, known: true},
		{triggered: "TRIGGERED", want: AMLScreeningStatePending, known: true},
		{triggered: "unknown", known: false},
		{records: []safeheron.TransactionAMLRecord{{RiskLevel: "LOW"}}, want: AMLScreeningStateScreened, known: true},
	} {
		got := safeheronAMLScreeningState(testCase.triggered, testCase.records)
		if (got != nil) != testCase.known || got != nil && *got != testCase.want {
			t.Fatalf("AML screening state %#v = %#v, want %q known=%t", testCase, got, testCase.want, testCase.known)
		}
	}

	for _, testCase := range []struct {
		input string
		want  AMLRiskLevel
		known bool
	}{
		{input: "UNKNOWN", want: AMLRiskLevelUnknown, known: true},
		{input: "LOW", want: AMLRiskLevelLow, known: true},
		{input: "MEDIUM", want: AMLRiskLevelMedium, known: true},
		{input: "HIGH", want: AMLRiskLevelHigh, known: true},
		{input: "CRITICAL", want: AMLRiskLevelCritical, known: true},
		{input: "invalid", want: AMLRiskLevelUnknown, known: false},
	} {
		got, _, known := safeheronAMLRiskLevelValue(testCase.input)
		if got != testCase.want || known != testCase.known {
			t.Fatalf("AML risk %q = %q known=%t, want %q known=%t", testCase.input, got, known, testCase.want, testCase.known)
		}
	}

	threshold := decimal.RequireFromString("0.01")
	risk := RiskAssessment{Flags: []RiskFlag{RiskFlagDust}, IsDust: true, AutomaticExclusion: true}
	automatic := safeheronAutomaticRisk(
		safeheron.TransactionSnapshot{IsSourcePhishing: true, AmlLock: "YES"},
		safeheronBoolPointer(false),
		AccountAssetPolicy{ID: 7, Dust: DustPolicy{Enabled: true, Threshold: &threshold}},
		true,
		risk,
		AMLRiskLevelMedium,
		true,
	)
	if automatic.AMLRiskLevel == nil || *automatic.AMLRiskLevel != AMLRiskLevelMedium ||
		automatic.DustPolicyID == nil || *automatic.DustPolicyID != 7 ||
		automatic.DustThreshold == nil || !automatic.DustThreshold.Equal(threshold) ||
		automatic.RiskFlags == nil || len(*automatic.RiskFlags) != 1 {
		t.Fatalf("automatic risk = %#v", automatic)
	}

	if _, err := safeheronDirectProviderUSD(safeheron.TransactionSnapshot{TxAmountToUSD: "-1"}, MovementKindPrincipal, TransferModeSingle); err == nil {
		t.Fatal("negative direct USD must fail")
	}
	if value, err := safeheronDirectProviderUSD(safeheron.TransactionSnapshot{TxAmountToUSD: "1.25"}, MovementKindPrincipal, TransferModeBatch); err != nil || value != nil {
		t.Fatalf("batch direct USD = %#v, %v; want nil", value, err)
	}

	for _, testCase := range []struct {
		name string
		call func() error
	}{
		{"missing coin key", func() error {
			_, err := normalizeSafeheronAssetMapping("", SafeheronAssetMapping{}, "principal")
			return err
		}},
		{"incomplete asset mapping", func() error {
			_, err := normalizeSafeheronAssetMapping("USDT", SafeheronAssetMapping{CoinKey: "USDT", Asset: AssetIdentity{Currency: "USDT"}}, "principal")
			return err
		}},
		{"missing amount", func() error { _, err := parseSafeheronAmount("amount", " ", true); return err }},
		{"negative amount", func() error { _, err := parseSafeheronAmount("amount", "-1", true); return err }},
		{"negative millisecond timestamp", func() error { _, err := safeheronUnixMilliseconds(-1, "time"); return err }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.call(); err == nil {
				t.Fatal("invalid input unexpectedly succeeded")
			}
		})
	}
	if value, err := safeheronUnixMilliseconds(0, "time"); err != nil || value != nil {
		t.Fatalf("zero millisecond timestamp = %#v, %v; want nil", value, err)
	}
	if value, err := parseSafeheronAmount("optional", " ", false); err != nil || !value.IsZero() {
		t.Fatalf("optional blank amount = %s, %v", value, err)
	}
	if safeheronCopyInt64(nil) != nil {
		t.Fatal("nil ID copy must stay nil")
	}
	accountID := int64(1)
	if _, recognized := safeheronRiskPolicy(nil, DirectionOutflow, &accountID, nil, testSafeheronPrincipalAsset()); recognized {
		t.Fatal("nil registry must not resolve a risk policy")
	}
}

func TestSafeheronNormalizerHelpers_FeeAuditFailClosed(t *testing.T) {
	positiveGas := []safeheron.TransactionGasFee{{Symbol: "ETH", Amount: "0.01"}}
	for _, testCase := range []struct {
		name     string
		snapshot safeheron.TransactionSnapshot
		mapping  *SafeheronAssetMapping
	}{
		{
			name:     "missing transaction fee with positive gas",
			snapshot: safeheron.TransactionSnapshot{GasFee: positiveGas},
		},
		{
			name:     "zero transaction fee with positive gas",
			snapshot: safeheron.TransactionSnapshot{TxFee: "0", GasFee: positiveGas},
		},
		{
			name:     "nonzero transaction fee without mapping",
			snapshot: safeheron.TransactionSnapshot{TxFee: "0.01"},
		},
		{
			name:     "malformed gas detail",
			snapshot: safeheron.TransactionSnapshot{GasFee: []safeheron.TransactionGasFee{{Amount: "0.01"}}},
		},
		{
			name:     "malformed transaction fee",
			snapshot: safeheron.TransactionSnapshot{TxFee: "invalid"},
		},
		{
			name:     "mismatched nonzero fee mapping",
			snapshot: safeheron.TransactionSnapshot{TxFee: "0.01", FeeCoinKey: "ETHEREUM_ETH"},
			mapping:  &SafeheronAssetMapping{CoinKey: "OTHER", Asset: testSafeheronFeeAsset()},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := safeheronTransactionFeeDisplay(testCase.snapshot, testCase.mapping, true); err == nil {
				t.Fatal("invalid fee record unexpectedly succeeded")
			}
		})
	}

	if safeheronHasPositiveGasFee([]safeheron.TransactionGasFee{{Symbol: "ETH", Amount: "invalid"}, {Symbol: "ETH", Amount: "0"}}) {
		t.Fatal("non-positive or invalid gas must not be positive")
	}
	if !safeheronHasPositiveGasFee(positiveGas) {
		t.Fatal("positive gas must be recognized")
	}
	if _, err := safeheronFeeAuditDetails(safeheron.TransactionSnapshot{GasFee: []safeheron.TransactionGasFee{{Symbol: "ETH", Amount: "-1"}}}); err == nil {
		t.Fatal("negative gas fee must fail")
	}
	if _, err := safeheronFeeAuditDetails(safeheron.TransactionSnapshot{TxFee: strings.Repeat("9", 1000)}); err == nil {
		t.Fatal("oversized transaction fee must fail")
	}
}
