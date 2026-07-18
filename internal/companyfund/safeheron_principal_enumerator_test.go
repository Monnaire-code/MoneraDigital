package companyfund

import (
	"testing"

	"monera-digital/internal/safeheron"
)

func TestEnumerateSafeheronPrincipalOccurrencesIsConfigurationIndependentAndStable(t *testing.T) {
	snapshot := safeheron.TransactionSnapshot{
		TxKey:         "tx-batch",
		CoinKey:       "UNKNOWN_ERC20",
		SourceAddress: "0xSOURCE",
		DestinationAddressList: []safeheron.TransactionDestinationAddress{
			{Address: "0xBBBB", Amount: "2"},
			{Address: "0xAAAA", Amount: "1"},
		},
	}

	first, err := EnumerateSafeheronPrincipalOccurrences(snapshot, "EVM")
	if err != nil {
		t.Fatalf("EnumerateSafeheronPrincipalOccurrences(first): %v", err)
	}
	snapshot.DestinationAddressList[0], snapshot.DestinationAddressList[1] = snapshot.DestinationAddressList[1], snapshot.DestinationAddressList[0]
	second, err := EnumerateSafeheronPrincipalOccurrences(snapshot, "EVM")
	if err != nil {
		t.Fatalf("EnumerateSafeheronPrincipalOccurrences(reordered): %v", err)
	}

	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("line counts = %d / %d, want 2 / 2", len(first), len(second))
	}
	for index := range first {
		if first[index].Occurrence.Key != second[index].Occurrence.Key {
			t.Fatalf("reorder changed occurrence %d: %q != %q", index, first[index].Occurrence.Key, second[index].Occurrence.Key)
		}
		if first[index].MovementIndex != index {
			t.Fatalf("movement index = %d, want %d", first[index].MovementIndex, index)
		}
	}
	if first[0].RawCoinKey != "UNKNOWN_ERC20" || first[0].NormalizedSource != "0xsource" || first[0].NormalizedDestination != "0xaaaa" {
		t.Fatalf("first enumerated line = %#v", first[0])
	}
}

func TestEnumerateSafeheronPrincipalOccurrencesPreservesIdenticalBatchMovements(t *testing.T) {
	snapshot := safeheron.TransactionSnapshot{
		TxKey:         "tx-identical",
		CoinKey:       "ETHEREUM_ETH",
		SourceAddress: "0xSOURCE",
		DestinationAddressList: []safeheron.TransactionDestinationAddress{
			{Address: "0xDEST", Amount: "1"},
			{Address: "0xDEST", Amount: "1"},
		},
	}

	lines, err := EnumerateSafeheronPrincipalOccurrences(snapshot, "EVM")
	if err != nil {
		t.Fatalf("EnumerateSafeheronPrincipalOccurrences: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("line count = %d, want 2", len(lines))
	}
	if lines[0].Occurrence.Key == lines[1].Occurrence.Key {
		t.Fatalf("identical provider movements collapsed to %q", lines[0].Occurrence.Key)
	}
	if lines[0].DuplicateOrdinal != 0 || lines[1].DuplicateOrdinal != 1 {
		t.Fatalf("duplicate ordinals = %d / %d, want 0 / 1", lines[0].DuplicateOrdinal, lines[1].DuplicateOrdinal)
	}
}

func TestEnumerateSafeheronPrincipalOccurrencesIgnoresMutableStatusAndTxHash(t *testing.T) {
	snapshot := safeheron.TransactionSnapshot{
		TxKey:                "tx-status",
		TxHash:               "0xold",
		CoinKey:              "ETHEREUM_USDT",
		TxAmount:             "3",
		SourceAddress:        "0xSOURCE",
		DestinationAddress:   "0xDEST",
		TransactionStatus:    "PENDING",
		TransactionSubStatus: "SIGNING",
		TransactionDirection: "INFLOW",
	}
	first, err := EnumerateSafeheronPrincipalOccurrences(snapshot, "EVM")
	if err != nil {
		t.Fatalf("EnumerateSafeheronPrincipalOccurrences(first): %v", err)
	}
	snapshot.TxHash = "0xnew"
	snapshot.TransactionStatus = "COMPLETED"
	snapshot.TransactionSubStatus = "CONFIRMED"
	second, err := EnumerateSafeheronPrincipalOccurrences(snapshot, "EVM")
	if err != nil {
		t.Fatalf("EnumerateSafeheronPrincipalOccurrences(second): %v", err)
	}
	if first[0].Occurrence.Key != second[0].Occurrence.Key {
		t.Fatalf("status/txHash changed occurrence: %q != %q", first[0].Occurrence.Key, second[0].Occurrence.Key)
	}
}
