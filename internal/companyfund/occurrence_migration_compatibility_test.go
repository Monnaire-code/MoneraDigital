package companyfund

import (
	"strings"
	"testing"
)

func TestMigrationAOldWriterInsertRemainsAliasNullCompatible(t *testing.T) {
	t.Parallel()
	for _, channel := range []TransactionSource{ChannelSafeheron, ChannelAirwallex} {
		t.Run(string(channel), func(t *testing.T) {
			if strings.Contains(insertCompanyFundTransactionSQL, "provider_occurrence_key") || strings.Contains(insertCompanyFundTransactionSQL, "provider_occurrence_algorithm_version") {
				t.Fatalf("old %s insert unexpectedly requires Migration A alias columns", channel)
			}
			if !strings.Contains(insertCompanyFundTransactionSQL, "ON CONFLICT (movement_key) DO NOTHING") {
				t.Fatalf("old %s insert movement-key contract changed", channel)
			}
		})
	}
}
