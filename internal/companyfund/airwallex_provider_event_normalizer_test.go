package companyfund

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestAirwallexProviderEventNormalizer_NormalizesOnlyReconciledSnapshotWithDynamicRegistry(t *testing.T) {
	snapshot := testAirwallexProviderEventRegistrySnapshot(t, true)
	registry := &airwallexProviderEventRegistryStub{snapshot: snapshot}
	var gotResolution AirwallexProviderEventResolutionInput
	normalizer := newAirwallexProviderEventNormalizerForTest(t, AirwallexProviderEventNormalizerConfig{
		FinancialTransactions: testAirwallexProviderEventStrictNormalizer(t),
		RegistrySnapshots:     registry,
		MappingResolver: airwallexProviderEventMappingResolverFunc(func(_ context.Context, input AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
			gotResolution = input
			return AirwallexProviderEventMapping{}, nil
		}),
		RelationshipResolver: airwallexProviderEventRelationshipResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error) {
			return AirwallexProviderEventRelationshipResolution{}, nil
		}),
		CounterpartyResolver: airwallexProviderEventCounterpartyResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
			return AirwallexProviderEventCounterpartyResolution{Counterparty: &AirwallexCounterparty{Name: "Confirmed payer"}}, nil
		}),
	})

	lease := testAirwallexFinancialTransactionProviderEventLease("awx-usd")
	result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, testAirwallexFinancialTransactionSnapshotPayload())
	if err != nil {
		t.Fatalf("NormalizeProviderEvent() error = %v", err)
	}
	if err := result.validate(); err != nil || len(result.Facts) != 1 || len(result.Movements) != 1 || len(result.FactBindings) != 1 {
		t.Fatalf("NormalizeProviderEvent() = %#v, validate=%v", result, err)
	}
	movement := result.Movements[0]
	if movement.ProviderAccountKey != "awx-usd" || movement.FromCompanyFundAccountID != nil || movement.ToCompanyFundAccountID == nil ||
		*movement.ToCompanyFundAccountID != 7 || movement.Provider.Metadata.Source != ProviderSourceReconciliation ||
		movement.FirstSeenSource != TransactionSeenSourceReconciliation {
		t.Fatalf("reconciled movement = %#v", movement)
	}
	if movement.AutomaticRisk.IsDust == nil || !*movement.AutomaticRisk.IsDust ||
		movement.AutomaticRisk.AutoExcludedFromSummary == nil || !*movement.AutomaticRisk.AutoExcludedFromSummary {
		t.Fatalf("registry-derived USD risk policy was not applied: %#v", movement.AutomaticRisk)
	}
	if gotResolution.ProviderAccountKey != lease.ProviderAccountKey || gotResolution.ConfiguredAccount.ID != 7 ||
		gotResolution.Transaction.FinancialTransactionID != "ft_123" || gotResolution.Transaction.TransactionType != "DEPOSIT_CREDIT" ||
		gotResolution.Transaction.SourceType != "BANK_FEED" || gotResolution.Transaction.Currency != "USD" {
		t.Fatalf("resolver input = %#v", gotResolution)
	}
	if _, hasSourceID := reflect.TypeOf(AirwallexProviderEventTransactionContext{}).FieldByName("SourceID"); hasSourceID {
		t.Fatal("provider-event resolvers must not receive source_id as an identity/linkage input")
	}

	// The resolver reads the current immutable snapshot on every event. A
	// registry refresh removing this account must prevent a later snapshot from
	// creating a movement rather than retaining a stale account mapping.
	registry.snapshot = testAirwallexProviderEventRegistrySnapshot(t, false)
	if _, err := normalizer.NormalizeProviderEvent(context.Background(), lease, testAirwallexFinancialTransactionSnapshotPayload()); !errors.Is(err, ErrProviderEventPermanent) {
		t.Fatalf("removed configured account error = %v, want permanent failure", err)
	}
}

func TestAirwallexProviderEventNormalizer_ScopedRuntimeFailsClosedAfterRegistryBecomesMultiAccount(t *testing.T) {
	registry := &airwallexProviderEventRegistryStub{snapshot: testAirwallexProviderEventRegistrySnapshot(t, true)}
	normalizer := newAirwallexProviderEventNormalizerForTest(t, AirwallexProviderEventNormalizerConfig{
		LoginAsScope:          "awx-usd",
		FinancialTransactions: testAirwallexProviderEventStrictNormalizer(t),
		RegistrySnapshots:     registry,
		MappingResolver: airwallexProviderEventMappingResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
			return AirwallexProviderEventMapping{}, nil
		}),
		RelationshipResolver: airwallexProviderEventRelationshipResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error) {
			return AirwallexProviderEventRelationshipResolution{}, nil
		}),
		CounterpartyResolver: airwallexProviderEventCounterpartyResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
			return AirwallexProviderEventCounterpartyResolution{}, nil
		}),
	})

	accounts := append(registry.snapshot.Accounts(), CompanyFundAccount{
		ID: 8, Channel: AccountChannelAirwallex, ProviderAccountKey: "awx-secondary", Enabled: true,
	})
	multiAccountSnapshot, err := buildAccountRegistrySnapshot(accounts, registry.snapshot.AssetPolicies(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	registry.snapshot = multiAccountSnapshot
	result, err := normalizer.NormalizeProviderEvent(
		context.Background(),
		testAirwallexFinancialTransactionProviderEventLease("awx-usd"),
		testAirwallexFinancialTransactionSnapshotPayload(),
	)
	if !errors.Is(err, ErrProviderEventPermanent) || len(result.Movements) != 0 || len(result.Facts) != 0 {
		t.Fatalf("multi-account scoped normalization = %#v, %v; want permanent no-ledger result", result, err)
	}
}

func TestAirwallexProviderEventNormalizer_IgnoresWebhookWithoutUsingNullableAccountFields(t *testing.T) {
	called := false
	normalizer := newAirwallexProviderEventNormalizerForTest(t, AirwallexProviderEventNormalizerConfig{
		FinancialTransactions: testAirwallexProviderEventStrictNormalizer(t),
		RegistrySnapshots:     &airwallexProviderEventRegistryStub{},
		MappingResolver: airwallexProviderEventMappingResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
			called = true
			return AirwallexProviderEventMapping{}, nil
		}),
		RelationshipResolver: airwallexProviderEventRelationshipResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error) {
			called = true
			return AirwallexProviderEventRelationshipResolution{}, nil
		}),
		CounterpartyResolver: airwallexProviderEventCounterpartyResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
			called = true
			return AirwallexProviderEventCounterpartyResolution{}, nil
		}),
	})
	lease := ProviderEventLease{
		ID:                  88,
		Channel:             ChannelAirwallex,
		ProviderEventID:     "evt_webhook_123",
		EventType:           "deposit.created",
		SourceKind:          ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest: strings.Repeat("b", 64),
		// provider_account_key and provider_org_key are intentionally absent.
	}
	result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, []byte(`not-json`))
	if err != nil || !result.Ignored || called {
		t.Fatalf("webhook NormalizeProviderEvent() = %#v, %v, resolverCalled=%v", result, err, called)
	}
}

func TestAirwallexProviderEventNormalizer_FailsClosedForUnprovenSnapshotContext(t *testing.T) {
	baseConfig := func() AirwallexProviderEventNormalizerConfig {
		return AirwallexProviderEventNormalizerConfig{
			APIVersion:            airwallexTestAPIVersion,
			SchemaVersion:         "schema-v1",
			EventVersion:          "event-v1",
			FinancialTransactions: testAirwallexProviderEventStrictNormalizer(t),
			RegistrySnapshots:     &airwallexProviderEventRegistryStub{snapshot: testAirwallexProviderEventRegistrySnapshot(t, true)},
			MappingResolver: airwallexProviderEventMappingResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
				return AirwallexProviderEventMapping{}, nil
			}),
			RelationshipResolver: airwallexProviderEventRelationshipResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error) {
				return AirwallexProviderEventRelationshipResolution{}, nil
			}),
			CounterpartyResolver: airwallexProviderEventCounterpartyResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
				return AirwallexProviderEventCounterpartyResolution{}, nil
			}),
		}
	}

	testCases := []struct {
		name   string
		mutate func(*AirwallexProviderEventNormalizerConfig, *ProviderEventLease, *[]byte)
	}{
		{
			name: "API version mismatch",
			mutate: func(_ *AirwallexProviderEventNormalizerConfig, lease *ProviderEventLease, _ *[]byte) {
				lease.ProviderEventVersion = "2026-01-01"
			},
		},
		{
			name: "unconfigured explicit account key",
			mutate: func(_ *AirwallexProviderEventNormalizerConfig, lease *ProviderEventLease, _ *[]byte) {
				lease.ProviderAccountKey = "awx-other"
			},
		},
		{
			name: "missing sandbox mapping",
			mutate: func(config *AirwallexProviderEventNormalizerConfig, _ *ProviderEventLease, _ *[]byte) {
				config.MappingResolver = airwallexProviderEventMappingResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
					return AirwallexProviderEventMapping{}, errors.New("mapping absent")
				})
			},
		},
		{
			name: "counterparty account not in dynamic registry",
			mutate: func(config *AirwallexProviderEventNormalizerConfig, _ *ProviderEventLease, _ *[]byte) {
				config.CounterpartyResolver = airwallexProviderEventCounterpartyResolverFunc(func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
					return AirwallexProviderEventCounterpartyResolution{CompanyProviderAccountKey: "awx-missing"}, nil
				})
			},
		},
		{
			name: "unknown strict transaction classification",
			mutate: func(_ *AirwallexProviderEventNormalizerConfig, _ *ProviderEventLease, payload *[]byte) {
				*payload = []byte(strings.Replace(string(*payload), "DEPOSIT_CREDIT", "UNPROVEN_TYPE", 1))
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			config := baseConfig()
			lease := testAirwallexFinancialTransactionProviderEventLease("awx-usd")
			payload := append([]byte(nil), testAirwallexFinancialTransactionSnapshotPayload()...)
			testCase.mutate(&config, &lease, &payload)
			normalizer, err := NewAirwallexProviderEventNormalizer(config)
			if err != nil {
				t.Fatalf("NewAirwallexProviderEventNormalizer() error = %v", err)
			}
			result, err := normalizer.NormalizeProviderEvent(context.Background(), lease, payload)
			if !errors.Is(err, ErrProviderEventPermanent) || result.Ignored || len(result.Facts) != 0 || len(result.Movements) != 0 || len(result.FactBindings) != 0 {
				t.Fatalf("NormalizeProviderEvent() = %#v, %v; want permanent fail-closed result", result, err)
			}
		})
	}

	config := baseConfig()
	config.MappingResolver = nil
	if _, err := NewAirwallexProviderEventNormalizer(config); err == nil {
		t.Fatal("missing explicit mapping resolver must prevent normalizer construction")
	}
}

func newAirwallexProviderEventNormalizerForTest(t *testing.T, config AirwallexProviderEventNormalizerConfig) *AirwallexProviderEventNormalizer {
	t.Helper()
	if config.APIVersion == "" {
		config.APIVersion = airwallexTestAPIVersion
	}
	if config.SchemaVersion == "" {
		config.SchemaVersion = "schema-v1"
	}
	if config.EventVersion == "" {
		config.EventVersion = "event-v1"
	}
	normalizer, err := NewAirwallexProviderEventNormalizer(config)
	if err != nil {
		t.Fatal(err)
	}
	return normalizer
}

func testAirwallexProviderEventStrictNormalizer(t *testing.T) *AirwallexFinancialTransactionNormalizer {
	t.Helper()
	normalizer, err := NewAirwallexFinancialTransactionNormalizer(AirwallexFinancialTransactionNormalizerConfig{
		SchemaVersion:  "schema-v1",
		EventVersion:   "event-v1",
		MappingVersion: "mapping-v1",
		FactVersion:    1,
		Classifications: []AirwallexFinancialTransactionClassification{{
			TransactionType: "DEPOSIT_CREDIT",
			SourceType:      "BANK_FEED",
			Action:          AirwallexFinancialTransactionActionApply,
			MovementKind:    MovementKindPrincipal,
			Direction:       DirectionInflow,
			TransferMode:    TransferModeSingle,
			AmountField:     AirwallexFinancialAmountFieldAmount,
			ExpectedSign:    AirwallexFinancialValueSignPositive,
			OccurredAtField: AirwallexFinancialOccurredAtCreated,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return normalizer
}

func testAirwallexProviderEventRegistrySnapshot(t *testing.T, enabled bool) *AccountRegistrySnapshot {
	t.Helper()
	threshold := decimal.RequireFromString("20")
	accounts := []CompanyFundAccount{{
		ID:                 7,
		Channel:            AccountChannelAirwallex,
		ProviderAccountKey: "awx-usd",
		CompanyEntity:      "Monera Ltd",
		FundAccountName:    "Treasury",
		SubAccountName:     "USD",
		AccountType:        "BANK",
		AccountName:        "USD account",
		Enabled:            enabled,
	}}
	policies := []AccountAssetPolicy{{
		ID:                         19,
		AccountID:                  7,
		Asset:                      AssetIdentity{Currency: "USD"},
		Dust:                       DustPolicy{ID: 19, Enabled: true, Threshold: &threshold},
		AutoExcludeDustFromSummary: true,
		Enabled:                    true,
	}}
	snapshot, err := buildAccountRegistrySnapshot(accounts, policies, time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testAirwallexFinancialTransactionProviderEventLease(providerAccountKey string) ProviderEventLease {
	return ProviderEventLease{
		ID:                   77,
		Channel:              ChannelAirwallex,
		ProviderEventID:      "airwallex-financial-transaction:v1:abcd",
		EventType:            AirwallexFinancialTransactionSnapshotEventType,
		ProviderEventVersion: airwallexTestAPIVersion,
		ProviderAccountKey:   providerAccountKey,
		SourceKind:           ProviderEventSourceOwnedEncryptedPayload,
		SourcePayloadDigest:  strings.Repeat("a", 64),
	}
}

func testAirwallexFinancialTransactionSnapshotPayload() []byte {
	return []byte(`{"id":"ft_123","amount":12.34,"fee":0,"net":12.34,"created_at":"2026-07-10T01:02:03Z","currency":"USD","source_id":"deposit_123","source_type":"BANK_FEED","status":"SETTLED","transaction_type":"DEPOSIT_CREDIT"}`)
}

type airwallexProviderEventRegistryStub struct {
	snapshot *AccountRegistrySnapshot
}

func (stub *airwallexProviderEventRegistryStub) Snapshot() *AccountRegistrySnapshot {
	return stub.snapshot
}

type airwallexProviderEventMappingResolverFunc func(context.Context, AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error)

func (fn airwallexProviderEventMappingResolverFunc) ResolveAirwallexProviderEventMapping(ctx context.Context, input AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error) {
	return fn(ctx, input)
}

type airwallexProviderEventRelationshipResolverFunc func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error)

func (fn airwallexProviderEventRelationshipResolverFunc) ResolveAirwallexProviderEventRelationship(ctx context.Context, input AirwallexProviderEventResolutionInput, mapping AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error) {
	return fn(ctx, input, mapping)
}

type airwallexProviderEventCounterpartyResolverFunc func(context.Context, AirwallexProviderEventResolutionInput, AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error)

func (fn airwallexProviderEventCounterpartyResolverFunc) ResolveAirwallexProviderEventCounterparty(ctx context.Context, input AirwallexProviderEventResolutionInput, mapping AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error) {
	return fn(ctx, input, mapping)
}
