package companyfund

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func occurrenceTestInput() SafeheronOccurrenceInput {
	return SafeheronOccurrenceInput{
		ProviderTransactionKey: "provider-tx-1", MovementKind: MovementKindPrincipal,
		RawCoinKey: "ETHEREUM_USDT", NormalizedSource: "0xabc", NormalizedDestination: "0xdef",
		Amount: decimal.RequireFromString("1.23"), TransferMode: TransferModeBatch, MovementIndex: 2,
	}
}

func TestBuildSafeheronOccurrenceCanonicalizesOnlyDeclaredTuple(t *testing.T) {
	t.Parallel()
	input := occurrenceTestInput()
	input.ProviderTransactionKey = "  provider-tx-1  "
	input.NormalizedSource = "  0xabc  "
	input.NormalizedDestination = "  0xdef  "
	input.Amount = decimal.RequireFromString("1.2300")
	got, err := BuildSafeheronOccurrence(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlgorithmVersion != SafeheronOccurrenceAlgorithmVersion || got.Key != got.AlgorithmVersion+":"+got.Digest || len(got.Digest) != 64 {
		t.Fatalf("unexpected occurrence identity: %+v", got)
	}
	if got.Input.ProviderTransactionKey != "provider-tx-1" || got.Input.NormalizedSource != "0xabc" || got.Input.NormalizedDestination != "0xdef" || got.Input.RawCoinKey != input.RawCoinKey || got.Input.Amount.String() != "1.23" {
		t.Fatalf("normalized input = %+v", got.Input)
	}
	want, err := BuildSafeheronOccurrence(occurrenceTestInput())
	if err != nil || want.Key != got.Key {
		t.Fatalf("canonical equivalent = %+v, %v", want, err)
	}
}

func TestBuildSafeheronOccurrenceDistinguishesEveryStableOccurrenceFact(t *testing.T) {
	t.Parallel()
	base := occurrenceTestInput()
	baseline, err := BuildSafeheronOccurrence(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*SafeheronOccurrenceInput){
		"provider tx key": func(v *SafeheronOccurrenceInput) { v.ProviderTransactionKey = "provider-tx-2" },
		"movement kind":   func(v *SafeheronOccurrenceInput) { v.MovementKind = MovementKindFee },
		"exact CoinKey":   func(v *SafeheronOccurrenceInput) { v.RawCoinKey = "ethereum_usdt" },
		"source":          func(v *SafeheronOccurrenceInput) { v.NormalizedSource = "0xaaa" },
		"destination":     func(v *SafeheronOccurrenceInput) { v.NormalizedDestination = "0xbbb" },
		"amount":          func(v *SafeheronOccurrenceInput) { v.Amount = decimal.RequireFromString("1.24") },
		"transfer mode":   func(v *SafeheronOccurrenceInput) { v.TransferMode = TransferModeSingle },
		"movement index":  func(v *SafeheronOccurrenceInput) { v.MovementIndex = 3 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			got, err := BuildSafeheronOccurrence(input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Key == baseline.Key {
				t.Fatalf("changed %s did not change key", name)
			}
		})
	}
}

func TestBuildSafeheronOccurrenceRejectsIncompleteOrInvalidTuple(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*SafeheronOccurrenceInput){
		"missing provider tx": func(v *SafeheronOccurrenceInput) { v.ProviderTransactionKey = " " },
		"missing CoinKey":     func(v *SafeheronOccurrenceInput) { v.RawCoinKey = "" },
		"blank CoinKey":       func(v *SafeheronOccurrenceInput) { v.RawCoinKey = "  " },
		"missing source":      func(v *SafeheronOccurrenceInput) { v.NormalizedSource = " " },
		"missing destination": func(v *SafeheronOccurrenceInput) { v.NormalizedDestination = " " },
		"invalid kind":        func(v *SafeheronOccurrenceInput) { v.MovementKind = "UNKNOWN" },
		"invalid mode":        func(v *SafeheronOccurrenceInput) { v.TransferMode = "UNKNOWN" },
		"negative amount":     func(v *SafeheronOccurrenceInput) { v.Amount = decimal.RequireFromString("-1") },
		"negative index":      func(v *SafeheronOccurrenceInput) { v.MovementIndex = -1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			input := occurrenceTestInput()
			mutate(&input)
			if _, err := BuildSafeheronOccurrence(input); err == nil {
				t.Fatalf("error = nil")
			}
		})
	}
}

func TestSafeheronOccurrenceAlgorithmDoesNotMentionForbiddenIdentityInputs(t *testing.T) {
	t.Parallel()
	canonical := safeheronOccurrenceCanonicalTuple(occurrenceTestInput())
	for _, forbidden := range []string{"tx_hash", "currency", "chain_code", "asset_contract", "catalog"} {
		if strings.Contains(strings.ToLower(canonical), forbidden) {
			t.Fatalf("canonical tuple contains %q", forbidden)
		}
	}
}

func TestNormalizeSafeheronOccurrenceAddress(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ family, address, want string }{
		{family: " evm ", address: " 0xAbC ", want: "0xabc"},
		{family: "TRON", address: " TAbC ", want: "TAbC"},
	} {
		got, err := NormalizeSafeheronOccurrenceAddress(tc.family, tc.address)
		if err != nil || got != tc.want {
			t.Fatalf("Normalize(%q,%q) = %q,%v", tc.family, tc.address, got, err)
		}
	}
	for _, tc := range []struct{ family, address string }{{family: "", address: "0xabc"}, {family: "EVM", address: " "}} {
		if _, err := NormalizeSafeheronOccurrenceAddress(tc.family, tc.address); err == nil {
			t.Fatalf("Normalize(%q,%q) error=nil", tc.family, tc.address)
		}
	}
}
