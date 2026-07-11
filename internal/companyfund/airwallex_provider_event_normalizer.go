package companyfund

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// AirwallexRegistrySnapshotProvider exposes the current immutable account
// cache. AccountRegistry satisfies it; a provider event retains one returned
// snapshot for the complete normalization decision.
type AirwallexRegistrySnapshotProvider interface {
	Snapshot() *AccountRegistrySnapshot
}

// AirwallexProviderEventTransactionContext is the intentionally limited
// provider context available to mapping/relationship/counterparty resolvers.
// In particular it excludes source_id and raw payload bytes, so those
// resolvers cannot promote undocumented source_id relationships into movement
// identity or linkage.
type AirwallexProviderEventTransactionContext struct {
	FinancialTransactionID string
	TransactionType        string
	SourceType             string
	Currency               string
	BatchID                string
	Status                 string
}

// AirwallexProviderEventResolutionInput contains only configured-account and
// allowlisted Financial Transactions metadata. It never contains nullable
// webhook account_id/org_id or a source_id cross-resource identity hint.
type AirwallexProviderEventResolutionInput struct {
	ProviderEventID       string
	ProviderEventRecordID int64
	ProviderAccountKey    string
	ConfiguredAccount     CompanyFundAccount
	Registry              *AccountRegistrySnapshot
	Transaction           AirwallexProviderEventTransactionContext
}

// AirwallexProviderEventMapping is an explicitly approved context mapping.
// The strict Financial Transactions normalizer still owns transaction-type
// classification; this mapping only supplies facts that cannot be guessed
// from an API line, such as the configured side of an internal balance leg.
type AirwallexProviderEventMapping struct {
	ConfiguredAccountSide AirwallexConfiguredAccountSide
}

// AirwallexProviderEventMappingResolver must be backed by approved Sandbox
// evidence. Returning an error deliberately dead-letters the snapshot rather
// than allowing a heuristic mapping.
type AirwallexProviderEventMappingResolver interface {
	ResolveAirwallexProviderEventMapping(ctx context.Context, input AirwallexProviderEventResolutionInput) (AirwallexProviderEventMapping, error)
}

// AirwallexProviderEventRelationshipResolution supplies only resolver-proven
// fee/reversal/conversion linkage. The strict normalizer rejects missing links
// for movement kinds that require them.
type AirwallexProviderEventRelationshipResolution struct {
	Relationship AirwallexMovementRelationship
	Conversion   AirwallexConversionDetails
}

type AirwallexProviderEventRelationshipResolver interface {
	ResolveAirwallexProviderEventRelationship(ctx context.Context, input AirwallexProviderEventResolutionInput, mapping AirwallexProviderEventMapping) (AirwallexProviderEventRelationshipResolution, error)
}

// AirwallexProviderEventCounterpartyResolution may supply external display
// data and, for an internal transfer, a configured Airwallex account key. The
// normalizer resolves that key again against the same current snapshot; it
// never accepts a database ID or webhook-provided account identifier.
type AirwallexProviderEventCounterpartyResolution struct {
	Counterparty              *AirwallexCounterparty
	CompanyProviderAccountKey string
}

type AirwallexProviderEventCounterpartyResolver interface {
	ResolveAirwallexProviderEventCounterparty(ctx context.Context, input AirwallexProviderEventResolutionInput, mapping AirwallexProviderEventMapping) (AirwallexProviderEventCounterpartyResolution, error)
}

// AirwallexProviderEventNormalizerConfig composes the strict pure Financial
// Transactions normalizer with dynamic configured-account resolution. API,
// schema, and event versions are explicit pins; there are no production
// mapping defaults.
type AirwallexProviderEventNormalizerConfig struct {
	APIVersion    string
	SchemaVersion string
	EventVersion  string
	// LoginAsScope is optional for generic strict-normalizer use. The company
	// fund runtime supplies it so snapshots cannot enter the ledger unless the
	// current registry proves one exact account owns the x-login-as scope.
	LoginAsScope          string
	FinancialTransactions *AirwallexFinancialTransactionNormalizer
	RegistrySnapshots     AirwallexRegistrySnapshotProvider
	MappingResolver       AirwallexProviderEventMappingResolver
	RelationshipResolver  AirwallexProviderEventRelationshipResolver
	CounterpartyResolver  AirwallexProviderEventCounterpartyResolver
}

// AirwallexProviderEventNormalizer parses only reconciled Financial
// Transactions snapshots. Other verified Airwallex webhook event types are
// explicitly ignored: they remain auditable inbox deliveries and can trigger
// reconciliation, but never select an account or create a cash movement.
type AirwallexProviderEventNormalizer struct {
	apiVersion            string
	schemaVersion         string
	eventVersion          string
	loginAsScope          string
	financialTransactions *AirwallexFinancialTransactionNormalizer
	registries            AirwallexRegistrySnapshotProvider
	mappings              AirwallexProviderEventMappingResolver
	relationships         AirwallexProviderEventRelationshipResolver
	counterparties        AirwallexProviderEventCounterpartyResolver
}

func NewAirwallexProviderEventNormalizer(config AirwallexProviderEventNormalizerConfig) (*AirwallexProviderEventNormalizer, error) {
	apiVersion, err := parseAirwallexAPIVersion(config.APIVersion)
	if err != nil {
		return nil, err
	}
	schemaVersion, err := normalizeAirwallexReconcilerVersion("Airwallex provider-event schema version", config.SchemaVersion)
	if err != nil {
		return nil, err
	}
	eventVersion, err := normalizeAirwallexReconcilerVersion("Airwallex provider-event event version", config.EventVersion)
	if err != nil {
		return nil, err
	}
	loginAsScope := ""
	if config.LoginAsScope != "" {
		loginAsScope, err = normalizeAirwallexReconcilerAccountKey(config.LoginAsScope)
		if err != nil {
			return nil, fmt.Errorf("Airwallex provider-event login scope is invalid")
		}
	}
	if config.FinancialTransactions == nil || config.RegistrySnapshots == nil || config.MappingResolver == nil ||
		config.RelationshipResolver == nil || config.CounterpartyResolver == nil {
		return nil, fmt.Errorf("Airwallex provider-event normalizer requires strict normalizer, registry, mapping, relationship, and counterparty resolvers")
	}
	return &AirwallexProviderEventNormalizer{
		apiVersion:            apiVersion,
		schemaVersion:         schemaVersion,
		eventVersion:          eventVersion,
		loginAsScope:          loginAsScope,
		financialTransactions: config.FinancialTransactions,
		registries:            config.RegistrySnapshots,
		mappings:              config.MappingResolver,
		relationships:         config.RelationshipResolver,
		counterparties:        config.CounterpartyResolver,
	}, nil
}

func (normalizer *AirwallexProviderEventNormalizer) NormalizeProviderEvent(
	ctx context.Context,
	lease ProviderEventLease,
	sourceBytes []byte,
) (ProviderEventNormalizationResult, error) {
	if normalizer == nil || normalizer.financialTransactions == nil || normalizer.registries == nil ||
		normalizer.mappings == nil || normalizer.relationships == nil || normalizer.counterparties == nil {
		return ProviderEventNormalizationResult{}, fmt.Errorf("Airwallex provider event normalizer is not configured")
	}
	if err := validateAirwallexProviderEventLease(lease); err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	if lease.EventType != AirwallexFinancialTransactionSnapshotEventType {
		// Webhook envelope fields are intentionally not parsed here. The signed
		// delivery remains in the inbox as audit evidence; API reconciliation is
		// the only path that creates Financial Transactions facts.
		return ProviderEventNormalizationResult{Ignored: true}, nil
	}
	if lease.ProviderEventVersion != normalizer.apiVersion {
		return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("Financial Transactions snapshot API version does not match the normalizer pin")
	}
	if len(sourceBytes) == 0 || len(sourceBytes) > MaxOwnedProviderPayloadPlaintextBytes || !json.Valid(sourceBytes) {
		return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("invalid Airwallex Financial Transactions snapshot payload")
	}
	transaction, err := decodeAirwallexFinancialTransaction(json.RawMessage(sourceBytes))
	if err != nil {
		return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("invalid Airwallex Financial Transactions snapshot")
	}

	registry := normalizer.registries.Snapshot()
	if registry == nil {
		return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("Airwallex account registry snapshot is unavailable")
	}
	configuredAccount, found := registry.LookupAirwallex(lease.ProviderAccountKey)
	if !found {
		return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("Airwallex provider account key is not an enabled configured account")
	}
	if normalizer.loginAsScope != "" {
		scopedAccount, eligible := ResolveAirwallexSingleAccountScope(registry, normalizer.loginAsScope)
		if !eligible || scopedAccount.ID != configuredAccount.ID || lease.ProviderAccountKey != scopedAccount.ProviderAccountKey {
			return ProviderEventNormalizationResult{}, airwallexPermanentNormalizationError("Airwallex Financial Transactions scope is not eligible")
		}
	}
	resolutionInput := airwallexProviderEventResolutionInput(lease, configuredAccount, registry, transaction)
	mapping, err := normalizer.mappings.ResolveAirwallexProviderEventMapping(ctx, resolutionInput)
	if err != nil {
		return ProviderEventNormalizationResult{}, airwallexProviderEventResolverError(err, "Airwallex Financial Transactions mapping is unavailable")
	}
	relationship, err := normalizer.relationships.ResolveAirwallexProviderEventRelationship(ctx, resolutionInput, mapping)
	if err != nil {
		return ProviderEventNormalizationResult{}, airwallexProviderEventResolverError(err, "Airwallex Financial Transactions relationship mapping is unavailable")
	}
	counterparty, err := normalizer.counterparties.ResolveAirwallexProviderEventCounterparty(ctx, resolutionInput, mapping)
	if err != nil {
		return ProviderEventNormalizationResult{}, airwallexProviderEventResolverError(err, "Airwallex Financial Transactions counterparty mapping is unavailable")
	}
	counterpartyAccount, err := resolveAirwallexProviderEventCounterpartyAccount(registry, counterparty.CompanyProviderAccountKey)
	if err != nil {
		return ProviderEventNormalizationResult{}, err
	}
	assetPolicy := airwallexProviderEventAssetPolicy(registry, configuredAccount.ID, transaction.Currency)

	result := normalizer.financialTransactions.Normalize(AirwallexFinancialTransactionNormalizationInput{
		SchemaVersion:              normalizer.schemaVersion,
		EventVersion:               normalizer.eventVersion,
		ProviderAccountKey:         lease.ProviderAccountKey,
		ConfiguredAccount:          configuredAccount,
		Counterparty:               cloneAirwallexProviderEventCounterparty(counterparty.Counterparty),
		CounterpartyCompanyAccount: counterpartyAccount,
		ConfiguredAccountSide:      mapping.ConfiguredAccountSide,
		AssetPolicy:                assetPolicy,
		Source: AirwallexFinancialTransactionSourceMetadata{
			ProviderEventID:       lease.ProviderEventID,
			ProviderEventRecordID: lease.ID,
			PayloadDigest:         lease.SourcePayloadDigest,
			FactSource:            ProviderSourceReconciliation,
			SeenSource:            TransactionSeenSourceReconciliation,
		},
		Relationship:         relationship.Relationship,
		Conversion:           relationship.Conversion,
		FinancialTransaction: transaction,
	})
	return result.ProviderEventNormalization()
}

func validateAirwallexProviderEventLease(lease ProviderEventLease) error {
	if lease.Channel != ChannelAirwallex || lease.ID <= 0 || lease.ProviderEventID != strings.TrimSpace(lease.ProviderEventID) ||
		lease.EventType != strings.TrimSpace(lease.EventType) || !isLowerSHA256Hex(lease.SourcePayloadDigest) ||
		lease.SourceKind != ProviderEventSourceOwnedEncryptedPayload || lease.SafeheronWebhookEventID != nil {
		return airwallexPermanentNormalizationError("invalid Airwallex provider-event lease")
	}
	if err := validateRequiredString("Airwallex provider event ID", lease.ProviderEventID, maxProviderEventIDBytes); err != nil {
		return airwallexPermanentNormalizationError("invalid Airwallex provider-event lease")
	}
	if err := validateRequiredString("Airwallex provider event type", lease.EventType, maxProviderEventTypeBytes); err != nil {
		return airwallexPermanentNormalizationError("invalid Airwallex provider-event lease")
	}
	if lease.EventType == AirwallexFinancialTransactionSnapshotEventType {
		if _, err := normalizeAirwallexReconcilerAccountKey(lease.ProviderAccountKey); err != nil || lease.ProviderEventVersion == "" {
			return airwallexPermanentNormalizationError("invalid Airwallex Financial Transactions snapshot lease")
		}
	}
	return nil
}

func airwallexProviderEventResolutionInput(lease ProviderEventLease, account CompanyFundAccount, registry *AccountRegistrySnapshot, transaction AirwallexFinancialTransaction) AirwallexProviderEventResolutionInput {
	return AirwallexProviderEventResolutionInput{
		ProviderEventID:       lease.ProviderEventID,
		ProviderEventRecordID: lease.ID,
		ProviderAccountKey:    lease.ProviderAccountKey,
		ConfiguredAccount:     account,
		Registry:              registry,
		Transaction: AirwallexProviderEventTransactionContext{
			FinancialTransactionID: strings.TrimSpace(transaction.ProviderID),
			TransactionType:        strings.TrimSpace(transaction.TransactionType),
			SourceType:             strings.TrimSpace(transaction.SourceType),
			Currency:               strings.TrimSpace(transaction.Currency),
			BatchID:                strings.TrimSpace(transaction.BatchID),
			Status:                 strings.TrimSpace(transaction.Status),
		},
	}
}

func resolveAirwallexProviderEventCounterpartyAccount(registry *AccountRegistrySnapshot, providerAccountKey string) (*CompanyFundAccount, error) {
	if providerAccountKey == "" {
		return nil, nil
	}
	if providerAccountKey != strings.TrimSpace(providerAccountKey) {
		return nil, airwallexPermanentNormalizationError("Airwallex counterparty account key is invalid")
	}
	account, found := registry.LookupAirwallex(providerAccountKey)
	if !found {
		return nil, airwallexPermanentNormalizationError("Airwallex counterparty account key is not an enabled configured account")
	}
	return &account, nil
}

func airwallexProviderEventAssetPolicy(registry *AccountRegistrySnapshot, accountID int64, currency string) *AccountAssetPolicy {
	policy, found := registry.LookupAssetPolicyFields(accountID, currency, "", "", "")
	if !found {
		return nil
	}
	return &policy
}

func cloneAirwallexProviderEventCounterparty(source *AirwallexCounterparty) *AirwallexCounterparty {
	if source == nil {
		return nil
	}
	copy := *source
	return &copy
}

func airwallexProviderEventResolverError(err error, safeReason string) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return airwallexPermanentNormalizationError(safeReason)
}

func airwallexPermanentNormalizationError(reason string) error {
	return NewPermanentProviderEventError(fmt.Errorf("%s", reason))
}

var _ ProviderEventNormalizer = (*AirwallexProviderEventNormalizer)(nil)
