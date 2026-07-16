package companyfund

import (
	"errors"
	"testing"
	"time"
)

func TestResolveSafeheronAccountContext_AccountOwnershipPrecedesAssetPolicy(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "vault-evm", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, ProviderAccountKey: "vault-tron", NormalizedAddress: "TAbC", NetworkFamily: "TRON", Enabled: true},
	}, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	context, err := ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{SourceAddresses: []string{"0xABC"}})
	if err != nil {
		t.Fatalf("resolve unknown-asset account context: %v", err)
	}
	if context.Source == nil || context.Source.ID != 1 || context.Destination != nil {
		t.Fatalf("account context = %#v", context)
	}

	context, err = ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		NetworkFamily: "TRON", DestinationAddresses: []string{"TAbC"},
	})
	if err != nil || context.Destination == nil || context.Destination.ID != 2 {
		t.Fatalf("catalog-constrained account context = %#v, %v", context, err)
	}
}

func TestResolveSafeheronAccountContext_KeyOnlyAndMismatchAreFailClosed(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "shared-vault", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, ProviderAccountKey: "shared-vault", NormalizedAddress: "0xdef", NetworkFamily: "EVM", Enabled: true},
		{ID: 3, Channel: ChannelSafeheron, ProviderAccountKey: "unique-vault", NormalizedAddress: "0x123", NetworkFamily: "EVM", Enabled: true},
		{ID: 4, Channel: ChannelSafeheron, ProviderAccountKey: "disabled-vault", NormalizedAddress: "0xabc", NetworkFamily: "EVM", Enabled: false},
	}, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	context, err := ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{SourceProviderAccountKey: "unique-vault"})
	if err != nil || context.Source == nil || context.Source.ID != 3 {
		t.Fatalf("unique key-only context = %#v, %v", context, err)
	}

	for _, input := range []SafeheronAccountContextInput{
		{SourceProviderAccountKey: "shared-vault"},
		{SourceProviderAccountKey: "unique-vault", SourceAddresses: []string{"0xabc"}},
	} {
		_, err := ResolveSafeheronAccountContext(registry, input)
		var configErr *SafeheronAccountContextConfigurationError
		if !errors.As(err, &configErr) || !configErr.Retriable() {
			t.Fatalf("input %#v error = %v, want typed retriable configuration error", input, err)
		}
	}
}

func TestResolveSafeheronAccountContext_UnknownNetworkAmbiguityAndInternalTransfer(t *testing.T) {
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "evm", NormalizedAddress: "same", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, ProviderAccountKey: "tron", NormalizedAddress: "same", NetworkFamily: "TRON", Enabled: true},
		{ID: 3, Channel: ChannelSafeheron, ProviderAccountKey: "to", NormalizedAddress: "0xto", NetworkFamily: "EVM", Enabled: true},
	}, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	_, err = ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{SourceAddresses: []string{"same"}})
	var configErr *SafeheronAccountContextConfigurationError
	if !errors.As(err, &configErr) || !configErr.Retriable() {
		t.Fatalf("cross-family ambiguity error = %v", err)
	}

	context, err := ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		NetworkFamily: "EVM", SourceProviderAccountKey: "evm", SourceAddresses: []string{"same"},
		DestinationProviderAccountKey: "to", DestinationAddresses: []string{"0xTO"},
	})
	if err != nil || context.Source == nil || context.Source.ID != 1 || context.Destination == nil || context.Destination.ID != 3 {
		t.Fatalf("internal transfer context = %#v, %v", context, err)
	}
}

func TestSafeheronAccountContext_EdgeContracts(t *testing.T) {
	var nilError *SafeheronAccountContextConfigurationError
	if nilError.Error() == "" || (&SafeheronAccountContextConfigurationError{Reason: "ambiguous"}).Error() == "" {
		t.Fatal("typed configuration errors must remain diagnosable")
	}
	if _, err := ResolveSafeheronAccountContext(nil, SafeheronAccountContextInput{}); err == nil {
		t.Fatal("nil snapshot must fail closed")
	}
	registry, err := buildAccountRegistrySnapshot([]CompanyFundAccount{
		{ID: 1, Channel: ChannelSafeheron, ProviderAccountKey: "evm-a", WalletAddress: "0xabc", NetworkFamily: "EVM", Enabled: true},
		{ID: 2, Channel: ChannelSafeheron, ProviderAccountKey: "evm-b", NormalizedAddress: "0xdef", NetworkFamily: "EVM", Enabled: true},
		{ID: 3, Channel: ChannelAirwallex, ProviderAccountKey: "fiat", NormalizedAddress: "0xabc", Enabled: true},
	}, nil, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if context, err := ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		SourceAddresses: []string{" ", "0xmissing"}, DestinationProviderAccountKey: "unknown",
	}); err != nil || context.Source != nil || context.Destination != nil {
		t.Fatalf("non-company context = %#v, %v", context, err)
	}
	if matches := registry.safeheronAddressCandidates("EVM", "0xmissing"); len(matches) != 0 {
		t.Fatalf("known-family miss = %#v", matches)
	}
	_, err = ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		SourceAddresses: []string{"0xabc", "0xdef"},
	})
	var configErr *SafeheronAccountContextConfigurationError
	if !errors.As(err, &configErr) {
		t.Fatalf("multi-endpoint ambiguity = %v", err)
	}
	_, err = ResolveSafeheronAccountContext(registry, SafeheronAccountContextInput{
		DestinationProviderAccountKey: "evm-a", DestinationAddresses: []string{"0xmissing"},
	})
	if !errors.As(err, &configErr) {
		t.Fatalf("known key with unmatched address = %v", err)
	}
}
