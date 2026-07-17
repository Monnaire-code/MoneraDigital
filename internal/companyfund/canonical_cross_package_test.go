package companyfund_test

import (
	"testing"

	"monera-digital/internal/companyfundcontract"
	"monera-digital/internal/releasecontrol"
)

func TestReleaseAndScannerUseOneCanonicalAccountPolicyAlgorithm(t *testing.T) {
	records := []companyfundcontract.CanonicalAccountPolicyRecord{{AccountID: 1, Channel: " safeheron ", ProviderAccountKey: " account-a ", Address: " 0xabc ", NetworkFamily: " evm ", AccountEnabled: true}}
	contractExport, err := companyfundcontract.BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		t.Fatal(err)
	}
	releaseExport, err := releasecontrol.BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		t.Fatal(err)
	}
	if contractExport.SHA256 != releaseExport.SHA256 || string(contractExport.JSON) != string(releaseExport.JSON) {
		t.Fatalf("canonical algorithms drifted: %#v / %#v", contractExport, releaseExport)
	}
}
