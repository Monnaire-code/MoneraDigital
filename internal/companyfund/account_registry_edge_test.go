package companyfund

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAccountRegistry_ConvenienceSurfacesAndNilFallbacks(t *testing.T) {
	var nilSnapshot *AccountRegistrySnapshot
	if !nilSnapshot.LoadedAt().IsZero() || nilSnapshot.Accounts() != nil || nilSnapshot.AssetPolicies() != nil {
		t.Fatal("nil snapshot must expose only zero-value read surfaces")
	}
	if _, found := nilSnapshot.LookupSafeheron("EVM", "0xabc"); found {
		t.Fatal("nil snapshot must not resolve Safeheron accounts")
	}
	if nilSnapshot.IsCompanyFundDestination("0xabc") {
		t.Fatal("nil snapshot must not resolve company-fund destinations")
	}
	if _, found := nilSnapshot.LookupAirwallex("awx-main"); found {
		t.Fatal("nil snapshot must not resolve Airwallex accounts")
	}
	if _, found := nilSnapshot.LookupAssetPolicyFields(1, "USDT", "ETHEREUM", "USDT_ERC20", ""); found {
		t.Fatal("nil snapshot must not resolve asset policies")
	}

	var nilRegistry *AccountRegistry
	if nilRegistry.RefreshInterval() != defaultAccountRegistryRefreshInterval || nilRegistry.Status() != (AccountRegistryStatus{}) {
		t.Fatal("nil registry must return documented default read values")
	}
	if err := nilRegistry.Refresh(context.Background()); err == nil {
		t.Fatal("nil registry refresh must fail")
	}
	if snapshot := nilRegistry.Snapshot(); snapshot == nil || len(snapshot.Accounts()) != 0 {
		t.Fatalf("nil registry snapshot = %#v", snapshot)
	}
	if _, found := nilRegistry.LookupAssetPolicyFields(1, "USDT", "", "", ""); found {
		t.Fatal("nil registry must not resolve asset policies")
	}
	if nilRegistry.IsCompanyFundDestination("0xabc") {
		t.Fatal("nil registry must not resolve company-fund destinations")
	}
	nilRegistry.Start(nil)
	nilRegistry.Stop()

	registry := NewCompanyFundAccountRegistry(accountRegistryLoaderFunc(func(context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
		return registryFixtureAccounts(), registryFixturePolicies(), nil
	}), time.Minute)
	if err := registry.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if registry.Snapshot().LoadedAt().IsZero() {
		t.Fatal("successful Load alias must publish a timestamped snapshot")
	}
	if registry.IsCompanyFundDestination("   ") {
		t.Fatal("blank address must not resolve a company-fund destination")
	}
	policy, found := registry.LookupAssetPolicyFields(1, "usdt", "ethereum", "USDT_ERC20", "0xDac17F958D2ee523a2206206994597C13D831ec7")
	if !found || policy.ID != 12 {
		t.Fatalf("LookupAssetPolicyFields() = %#v, %v", policy, found)
	}
	fingerprint, err := registry.CurrentSafeheronWebhookEligibilityFingerprint()
	if err != nil || !isLowerSHA256Hex(fingerprint) {
		t.Fatalf("CurrentSafeheronWebhookEligibilityFingerprint() = %q, %v", fingerprint, err)
	}
	if _, err := nilRegistry.CurrentSafeheronWebhookEligibilityFingerprint(); err == nil {
		t.Fatal("nil registry fingerprint provider must fail closed")
	}
}

func TestBuildAccountRegistrySnapshot_RejectsUnsafeEnabledConfiguration(t *testing.T) {
	baseSafeheron := CompanyFundAccount{ID: 1, Channel: ChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true}
	baseAirwallex := CompanyFundAccount{ID: 2, Channel: ChannelAirwallex, ProviderAccountKey: "awx-main", Enabled: true}
	for _, testCase := range []struct {
		name     string
		accounts []CompanyFundAccount
		policies []AccountAssetPolicy
	}{
		{"non-positive ID", []CompanyFundAccount{{Channel: ChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true}}, nil},
		{"duplicate enabled ID", []CompanyFundAccount{baseSafeheron, {ID: 1, Channel: ChannelAirwallex, ProviderAccountKey: "awx-main", Enabled: true}}, nil},
		{"Safeheron missing address", []CompanyFundAccount{{ID: 1, Channel: ChannelSafeheron, NetworkFamily: "EVM", Enabled: true}}, nil},
		{"duplicate Safeheron identity", []CompanyFundAccount{baseSafeheron, {ID: 3, Channel: ChannelSafeheron, NormalizedAddress: "0xABC", NetworkFamily: "EVM", Enabled: true}}, nil},
		{"Safeheron provider key whitespace", []CompanyFundAccount{{ID: 1, Channel: ChannelSafeheron, NormalizedAddress: "0xabc", NetworkFamily: "EVM", ProviderAccountKey: " vault ", Enabled: true}}, nil},
		{"Airwallex missing key", []CompanyFundAccount{{ID: 2, Channel: ChannelAirwallex, Enabled: true}}, nil},
		{"duplicate Airwallex key", []CompanyFundAccount{baseAirwallex, {ID: 3, Channel: ChannelAirwallex, ProviderAccountKey: "awx-main", Enabled: true}}, nil},
		{"unsupported channel", []CompanyFundAccount{{ID: 1, Channel: "UNKNOWN", Enabled: true}}, nil},
		{"policy without currency", []CompanyFundAccount{baseSafeheron}, []AccountAssetPolicy{{ID: 11, AccountID: 1, Enabled: true}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := buildAccountRegistrySnapshot(testCase.accounts, testCase.policies, time.Now().UTC()); err == nil {
				t.Fatal("buildAccountRegistrySnapshot() = nil, want validation error")
			}
		})
	}
}

func TestAccountRegistrySnapshot_MatchingHelpersRejectNonMatchingConstraints(t *testing.T) {
	policy := AccountAssetPolicy{ID: 11, AccountID: 1, Asset: AssetIdentity{
		Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0xdAC17F958D2ee523a2206206994597C13D831ec7",
	}, Enabled: true}
	for _, asset := range []AssetIdentity{
		{Currency: "BTC", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: policy.Asset.ContractAddress},
		{Currency: "USDT", ChainCode: "TRON", ProviderAssetKey: "USDT_ERC20", ContractAddress: policy.Asset.ContractAddress},
		{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_TRC20", ContractAddress: policy.Asset.ContractAddress},
		{Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20", ContractAddress: "0x0000000000000000000000000000000000000001"},
	} {
		if score, matches := accountAssetPolicyMatchScore(policy, asset); matches || score != 0 {
			t.Fatalf("constraint mismatch score = %d, matches=%v for %#v", score, matches, asset)
		}
	}
	if key := safeheronAddressKey(" ", "0xabc"); key != "" {
		t.Fatalf("blank network address key = %q", key)
	}
	if key := safeheronAddressKey("EVM", " "); key != "" {
		t.Fatalf("blank address key = %q", key)
	}
	if !isEVMHexAddress("0x"+strings.Repeat("a", 40)) || isEVMHexAddress("0x"+strings.Repeat("g", 40)) {
		t.Fatal("EVM contract validation must distinguish hexadecimal characters")
	}
}
