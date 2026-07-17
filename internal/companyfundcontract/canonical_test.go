package companyfundcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestBuildCanonicalAccountPolicyExportNormalizesSortsHashesAndDoesNotMutateInput(t *testing.T) {
	records := []CanonicalAccountPolicyRecord{
		{
			AccountID: 2, Channel: " airwallex ", ProviderAccountKey: " account-b ",
			Address: " wallet-b ", NetworkFamily: " fiat ", AccountEnabled: true,
			AssetKey: " usd ", PolicyEnabled: true,
		},
		{
			AccountID: 1, Channel: " safeheron ", ProviderAccountKey: " account-a ",
			Address: " 0xabc ", NetworkFamily: " evm ", AssetKey: " ethereum_usdt ",
		},
	}
	original := append([]CanonicalAccountPolicyRecord(nil), records...)

	exported, err := BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `[{"account_id":1,"channel":"SAFEHERON","provider_account_key":"account-a","address":"0xabc","network_family":"EVM","account_enabled":false,"asset_key":"ethereum_usdt","policy_enabled":false},{"account_id":2,"channel":"AIRWALLEX","provider_account_key":"account-b","address":"wallet-b","network_family":"FIAT","account_enabled":true,"asset_key":"usd","policy_enabled":true}]`
	if string(exported.JSON) != wantJSON {
		t.Fatalf("canonical JSON = %s, want %s", exported.JSON, wantJSON)
	}
	digest := sha256.Sum256([]byte(wantJSON))
	if exported.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("canonical SHA = %s", exported.SHA256)
	}
	for index := range records {
		if records[index] != original[index] {
			t.Fatalf("input record %d mutated: %#v", index, records[index])
		}
	}
}

func TestBuildCanonicalAccountPolicyExportRejectsIncompleteRecords(t *testing.T) {
	valid := CanonicalAccountPolicyRecord{AccountID: 1, Channel: "SAFEHERON", ProviderAccountKey: "account-a"}
	for _, testCase := range []struct {
		name   string
		mutate func(*CanonicalAccountPolicyRecord)
	}{
		{name: "account id", mutate: func(record *CanonicalAccountPolicyRecord) { record.AccountID = 0 }},
		{name: "channel", mutate: func(record *CanonicalAccountPolicyRecord) { record.Channel = " " }},
		{name: "provider account key", mutate: func(record *CanonicalAccountPolicyRecord) { record.ProviderAccountKey = " " }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			record := valid
			testCase.mutate(&record)
			if _, err := BuildCanonicalAccountPolicyExport([]CanonicalAccountPolicyRecord{record}); err == nil || !strings.Contains(err.Error(), "record 0 is incomplete") {
				t.Fatalf("incomplete record error = %v", err)
			}
		})
	}
}

func TestBuildCanonicalAccountPolicyExportSupportsEmptySnapshot(t *testing.T) {
	exported, err := BuildCanonicalAccountPolicyExport([]CanonicalAccountPolicyRecord{})
	if err != nil {
		t.Fatal(err)
	}
	if string(exported.JSON) != "null" {
		t.Fatalf("empty canonical JSON = %s", exported.JSON)
	}
	digest := sha256.Sum256([]byte("null"))
	if exported.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("empty canonical SHA = %s", exported.SHA256)
	}
}
