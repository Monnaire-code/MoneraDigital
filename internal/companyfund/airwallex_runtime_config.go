package companyfund

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

const (
	maxAirwallexRuntimeConfigBytes     = 256 << 10
	maxAirwallexRuntimeRuleCount       = 256
	maxAirwallexRuntimeEvidenceBytes   = 512
	maxAirwallexRuntimeDisplayFieldLen = 512
)

var (
	// ErrAirwallexRuntimeMappingsDisabled is returned by the resolver surface
	// when a caller wires an explicitly disabled configuration. The provider
	// event normalizer converts this into a permanent quarantine rather than
	// creating a movement from an implicit fallback.
	ErrAirwallexRuntimeMappingsDisabled = errors.New("airwallex runtime mappings are disabled")

	// ErrAirwallexRuntimeMappingNotFound means that an otherwise valid
	// Financial Transactions snapshot did not match one exact, review-approved
	// account/type/source/currency/status rule. It is intentionally not a
	// retryable provider error.
	ErrAirwallexRuntimeMappingNotFound = errors.New("airwallex runtime mapping is not configured")
)

// AirwallexFinancialTransactionsRuntimeConfig is the complete, version-pinned
// configuration needed to turn a reconciled Airwallex Financial Transactions
// snapshot into a ledger proposal. It deliberately has no source_id lookup,
// no provider-account ID lookup, and no inferred counterparty relationship.
//
// Rules are expected to be created only after a Sandbox fixture or another
// reviewable provider contract has been approved. The evidence reference is
// persisted only in configuration/audit tooling; it is not copied into a
// provider fact or exposed as a provider assertion.
type AirwallexFinancialTransactionsRuntimeConfig struct {
	Enabled        bool
	APIVersion     string
	SchemaVersion  string
	EventVersion   string
	MappingVersion string
	FactVersion    int
	Rules          []AirwallexFinancialTransactionsRuntimeRule
}

// AirwallexFinancialTransactionsRuntimeRule joins one exact normalizer
// classification with the account-specific context the API line cannot safely
// supply by itself. All lookup dimensions are exact for APPLY rules:
// configured provider account key, transaction type, source type, currency,
// and status. There are no wildcards.
type AirwallexFinancialTransactionsRuntimeRule struct {
	EvidenceReference                     string
	ProviderAccountKey                    string
	Currency                              string
	Status                                string
	Classification                        AirwallexFinancialTransactionClassification
	ConfiguredAccountSide                 AirwallexConfiguredAccountSide
	CounterpartyCompanyProviderAccountKey string
	Counterparty                          *AirwallexRuntimeManualCounterparty
}

// AirwallexRuntimeManualCounterparty is an operator-maintained display value,
// not a value extracted from source_id, a webhook envelope, or an undocumented
// Financial Transactions field. It is permitted only for external movements
// and always needs its own evidence reference.
type AirwallexRuntimeManualCounterparty struct {
	EvidenceReference string
	AddressOrAccount  string
	Name              string
	CompanyEntity     string
	FundAccountName   string
	SubAccountName    string
	AccountType       string
}

// AirwallexFinancialTransactionsRuntimeBundle is the ready-to-wire runtime
// surface. When Enabled is false, all normalizer pointers are nil; callers
// must not register an Airwallex provider-event worker in that state.
type AirwallexFinancialTransactionsRuntimeBundle struct {
	Enabled               bool
	FinancialTransactions *AirwallexFinancialTransactionNormalizer
	ProviderEvents        *AirwallexProviderEventNormalizer
	Resolvers             *AirwallexFinancialTransactionsRuntimeResolvers
}

// AirwallexFinancialTransactionsRuntimeResolvers implements the three
// explicit resolver interfaces required by AirwallexProviderEventNormalizer.
// It is immutable after construction and only returns data from an exact
// configured rule.
type AirwallexFinancialTransactionsRuntimeResolvers struct {
	enabled bool
	rules   map[airwallexFinancialTransactionsRuntimeRuleKey]airwallexFinancialTransactionsRuntimeRule
}

type airwallexFinancialTransactionsRuntimeRule struct {
	configuredAccountSide                 AirwallexConfiguredAccountSide
	counterpartyCompanyProviderAccountKey string
	counterparty                          *AirwallexCounterparty
}

type airwallexFinancialTransactionsRuntimeRuleKey struct {
	providerAccountKey string
	transactionType    string
	sourceType         string
	currency           string
	status             string
}

// ParseAirwallexFinancialTransactionsRuntimeConfigJSON parses a deliberately
// narrow snake_case JSON form suitable for a single backend-only environment
// value or a managed configuration source. Empty input means explicitly
// disabled; non-empty input must include enabled and rejects unknown fields.
func ParseAirwallexFinancialTransactionsRuntimeConfigJSON(raw []byte) (AirwallexFinancialTransactionsRuntimeConfig, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return AirwallexFinancialTransactionsRuntimeConfig{}, nil
	}
	if len(trimmed) > maxAirwallexRuntimeConfigBytes {
		return AirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime configuration exceeds %d bytes", maxAirwallexRuntimeConfigBytes)
	}

	var decoded airwallexFinancialTransactionsRuntimeConfigJSON
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return AirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("decode Airwallex runtime configuration: %w", err)
	}
	if err := ensureAirwallexRuntimeJSONEOF(decoder); err != nil {
		return AirwallexFinancialTransactionsRuntimeConfig{}, err
	}
	if decoded.Enabled == nil {
		return AirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime configuration must explicitly set enabled")
	}

	config := AirwallexFinancialTransactionsRuntimeConfig{
		Enabled:        *decoded.Enabled,
		APIVersion:     decoded.APIVersion,
		SchemaVersion:  decoded.SchemaVersion,
		EventVersion:   decoded.EventVersion,
		MappingVersion: decoded.MappingVersion,
		FactVersion:    decoded.FactVersion,
		Rules:          make([]AirwallexFinancialTransactionsRuntimeRule, 0, len(decoded.Rules)),
	}
	for index, rule := range decoded.Rules {
		converted, err := rule.runtimeRule()
		if err != nil {
			return AirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("decode Airwallex runtime rule %d: %w", index, err)
		}
		config.Rules = append(config.Rules, converted)
	}
	return config, nil
}

// NewAirwallexFinancialTransactionsRuntimeBundle validates a complete
// configuration and constructs the strict Financial Transactions normalizer
// plus all mapping resolvers. No heuristic source_id relationship is available
// through this public API. A disabled config produces a clear disabled bundle
// instead of a partially configured normalizer.
func NewAirwallexFinancialTransactionsRuntimeBundle(
	config AirwallexFinancialTransactionsRuntimeConfig,
	registries AirwallexRegistrySnapshotProvider,
) (*AirwallexFinancialTransactionsRuntimeBundle, error) {
	return newAirwallexFinancialTransactionsRuntimeBundle(config, registries, "")
}

// NewAirwallexFinancialTransactionsScopedRuntimeBundle adds the exact
// x-login-as scope used by the Financial Transactions REST client to the
// provider-event normalizer. It prevents a later account-registry refresh
// from allowing stale snapshots to become ledger movements after the scope is
// no longer provably one-to-one.
func NewAirwallexFinancialTransactionsScopedRuntimeBundle(
	config AirwallexFinancialTransactionsRuntimeConfig,
	registries AirwallexRegistrySnapshotProvider,
	loginAsScope string,
) (*AirwallexFinancialTransactionsRuntimeBundle, error) {
	return newAirwallexFinancialTransactionsRuntimeBundle(config, registries, loginAsScope)
}

func newAirwallexFinancialTransactionsRuntimeBundle(
	config AirwallexFinancialTransactionsRuntimeConfig,
	registries AirwallexRegistrySnapshotProvider,
	loginAsScope string,
) (*AirwallexFinancialTransactionsRuntimeBundle, error) {
	normalized, err := normalizeAirwallexFinancialTransactionsRuntimeConfig(config)
	if err != nil {
		return nil, err
	}
	if !normalized.config.Enabled {
		return &AirwallexFinancialTransactionsRuntimeBundle{}, nil
	}
	if registries == nil {
		return nil, fmt.Errorf("Airwallex runtime configuration requires an account registry snapshot provider")
	}

	strictNormalizer, err := NewAirwallexFinancialTransactionNormalizer(AirwallexFinancialTransactionNormalizerConfig{
		SchemaVersion:   normalized.config.SchemaVersion,
		EventVersion:    normalized.config.EventVersion,
		MappingVersion:  normalized.config.MappingVersion,
		FactVersion:     normalized.config.FactVersion,
		Classifications: normalized.classifications,
	})
	if err != nil {
		return nil, fmt.Errorf("build strict Airwallex Financial Transactions normalizer: %w", err)
	}

	resolvers := &AirwallexFinancialTransactionsRuntimeResolvers{
		enabled: true,
		rules:   normalized.rules,
	}
	providerEvents, err := NewAirwallexProviderEventNormalizer(AirwallexProviderEventNormalizerConfig{
		APIVersion:            normalized.config.APIVersion,
		SchemaVersion:         normalized.config.SchemaVersion,
		EventVersion:          normalized.config.EventVersion,
		LoginAsScope:          loginAsScope,
		FinancialTransactions: strictNormalizer,
		RegistrySnapshots:     registries,
		MappingResolver:       resolvers,
		RelationshipResolver:  resolvers,
		CounterpartyResolver:  resolvers,
	})
	if err != nil {
		return nil, fmt.Errorf("build Airwallex provider-event normalizer: %w", err)
	}
	return &AirwallexFinancialTransactionsRuntimeBundle{
		Enabled:               true,
		FinancialTransactions: strictNormalizer,
		ProviderEvents:        providerEvents,
		Resolvers:             resolvers,
	}, nil
}

// Enabled reports whether this resolver can return an approved mapping. It is
// useful for startup diagnostics without leaking rule data.
func (r *AirwallexFinancialTransactionsRuntimeResolvers) Enabled() bool {
	return r != nil && r.enabled
}

// RuleCount returns the number of exact runtime mappings, including explicit
// terminal classifications. The provider-event wrapper invokes resolvers before
// the strict normalizer sees an IGNORE or QUARANTINE classification, so those
// terminal rules need their own exact resolver entries as well.
func (r *AirwallexFinancialTransactionsRuntimeResolvers) RuleCount() int {
	if r == nil {
		return 0
	}
	return len(r.rules)
}

func (r *AirwallexFinancialTransactionsRuntimeResolvers) ResolveAirwallexProviderEventMapping(
	ctx context.Context,
	input AirwallexProviderEventResolutionInput,
) (AirwallexProviderEventMapping, error) {
	if err := ctx.Err(); err != nil {
		return AirwallexProviderEventMapping{}, err
	}
	rule, err := r.lookup(input)
	if err != nil {
		return AirwallexProviderEventMapping{}, err
	}
	return AirwallexProviderEventMapping{ConfiguredAccountSide: rule.configuredAccountSide}, nil
}

func (r *AirwallexFinancialTransactionsRuntimeResolvers) ResolveAirwallexProviderEventRelationship(
	ctx context.Context,
	input AirwallexProviderEventResolutionInput,
	_ AirwallexProviderEventMapping,
) (AirwallexProviderEventRelationshipResolution, error) {
	if err := ctx.Err(); err != nil {
		return AirwallexProviderEventRelationshipResolution{}, err
	}
	if _, err := r.lookup(input); err != nil {
		return AirwallexProviderEventRelationshipResolution{}, err
	}
	// A generic static config cannot safely derive a parent, reversal, or
	// conversion group key. Returning an empty relation intentionally leaves
	// linked movement kinds quarantined until a dedicated, evidence-backed
	// relation resolver exists.
	return AirwallexProviderEventRelationshipResolution{}, nil
}

func (r *AirwallexFinancialTransactionsRuntimeResolvers) ResolveAirwallexProviderEventCounterparty(
	ctx context.Context,
	input AirwallexProviderEventResolutionInput,
	_ AirwallexProviderEventMapping,
) (AirwallexProviderEventCounterpartyResolution, error) {
	if err := ctx.Err(); err != nil {
		return AirwallexProviderEventCounterpartyResolution{}, err
	}
	rule, err := r.lookup(input)
	if err != nil {
		return AirwallexProviderEventCounterpartyResolution{}, err
	}
	return AirwallexProviderEventCounterpartyResolution{
		Counterparty:              cloneAirwallexProviderEventCounterparty(rule.counterparty),
		CompanyProviderAccountKey: rule.counterpartyCompanyProviderAccountKey,
	}, nil
}

func (r *AirwallexFinancialTransactionsRuntimeResolvers) lookup(input AirwallexProviderEventResolutionInput) (airwallexFinancialTransactionsRuntimeRule, error) {
	if r == nil || !r.enabled {
		return airwallexFinancialTransactionsRuntimeRule{}, ErrAirwallexRuntimeMappingsDisabled
	}
	key, err := airwallexFinancialTransactionsRuntimeInputKey(input)
	if err != nil {
		return airwallexFinancialTransactionsRuntimeRule{}, fmt.Errorf("%w: invalid configured Financial Transactions context", ErrAirwallexRuntimeMappingNotFound)
	}
	rule, found := r.rules[key]
	if !found {
		return airwallexFinancialTransactionsRuntimeRule{}, ErrAirwallexRuntimeMappingNotFound
	}
	return cloneAirwallexFinancialTransactionsRuntimeRule(rule), nil
}

func airwallexFinancialTransactionsRuntimeInputKey(input AirwallexProviderEventResolutionInput) (airwallexFinancialTransactionsRuntimeRuleKey, error) {
	configuredKey, err := normalizeAirwallexRuntimeProviderAccountKey(input.ConfiguredAccount.ProviderAccountKey)
	if err != nil || configuredKey != input.ProviderAccountKey || input.ProviderAccountKey != strings.TrimSpace(input.ProviderAccountKey) {
		return airwallexFinancialTransactionsRuntimeRuleKey{}, fmt.Errorf("configured account key mismatch")
	}
	transactionType, err := normalizeAirwallexClassificationType("Airwallex runtime transaction type", input.Transaction.TransactionType)
	if err != nil {
		return airwallexFinancialTransactionsRuntimeRuleKey{}, err
	}
	sourceType, err := normalizeAirwallexClassificationType("Airwallex runtime source type", input.Transaction.SourceType)
	if err != nil {
		return airwallexFinancialTransactionsRuntimeRuleKey{}, err
	}
	currency, err := normalizeAirwallexRuntimeCurrency(input.Transaction.Currency)
	if err != nil {
		return airwallexFinancialTransactionsRuntimeRuleKey{}, err
	}
	status, err := normalizeAirwallexRuntimeStatus(input.Transaction.Status)
	if err != nil {
		return airwallexFinancialTransactionsRuntimeRuleKey{}, err
	}
	return airwallexFinancialTransactionsRuntimeRuleKey{
		providerAccountKey: configuredKey,
		transactionType:    transactionType,
		sourceType:         sourceType,
		currency:           currency,
		status:             status,
	}, nil
}

type normalizedAirwallexFinancialTransactionsRuntimeConfig struct {
	config          AirwallexFinancialTransactionsRuntimeConfig
	classifications []AirwallexFinancialTransactionClassification
	rules           map[airwallexFinancialTransactionsRuntimeRuleKey]airwallexFinancialTransactionsRuntimeRule
}

func normalizeAirwallexFinancialTransactionsRuntimeConfig(source AirwallexFinancialTransactionsRuntimeConfig) (normalizedAirwallexFinancialTransactionsRuntimeConfig, error) {
	if !source.Enabled {
		if source.APIVersion != "" || source.SchemaVersion != "" || source.EventVersion != "" || source.MappingVersion != "" || source.FactVersion != 0 || len(source.Rules) != 0 {
			return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("disabled Airwallex runtime configuration cannot carry versions or rules")
		}
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{config: AirwallexFinancialTransactionsRuntimeConfig{}}, nil
	}
	apiVersion, err := parseAirwallexAPIVersion(source.APIVersion)
	if err != nil {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, err
	}
	schemaVersion, err := normalizeAirwallexReconcilerVersion("Airwallex runtime schema version", source.SchemaVersion)
	if err != nil {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, err
	}
	eventVersion, err := normalizeAirwallexReconcilerVersion("Airwallex runtime event version", source.EventVersion)
	if err != nil {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, err
	}
	mappingVersion, err := normalizeAirwallexReconcilerVersion("Airwallex runtime mapping version", source.MappingVersion)
	if err != nil {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, err
	}
	if source.FactVersion <= 0 {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime provider fact version must be positive")
	}
	if len(source.Rules) == 0 || len(source.Rules) > maxAirwallexRuntimeRuleCount {
		return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime configuration requires 1 to %d rules", maxAirwallexRuntimeRuleCount)
	}

	normalized := normalizedAirwallexFinancialTransactionsRuntimeConfig{
		config: AirwallexFinancialTransactionsRuntimeConfig{
			Enabled:        true,
			APIVersion:     apiVersion,
			SchemaVersion:  schemaVersion,
			EventVersion:   eventVersion,
			MappingVersion: mappingVersion,
			FactVersion:    source.FactVersion,
			Rules:          make([]AirwallexFinancialTransactionsRuntimeRule, 0, len(source.Rules)),
		},
		rules: make(map[airwallexFinancialTransactionsRuntimeRuleKey]airwallexFinancialTransactionsRuntimeRule),
	}
	classifications := make(map[airwallexFinancialTransactionClassificationKey]AirwallexFinancialTransactionClassification)
	for index, sourceRule := range source.Rules {
		rule, classification, classificationKey, err := normalizeAirwallexFinancialTransactionsRuntimeRule(sourceRule)
		if err != nil {
			return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime rule %d: %w", index, err)
		}
		if existing, found := classifications[classificationKey]; found && !reflect.DeepEqual(existing, classification) {
			return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("Airwallex runtime rules disagree on classification for transaction type %q and source type %q", classification.TransactionType, classification.SourceType)
		}
		classifications[classificationKey] = classification
		normalized.config.Rules = append(normalized.config.Rules, rule)
		runtimeKey := airwallexFinancialTransactionsRuntimeRuleKey{
			providerAccountKey: rule.ProviderAccountKey,
			transactionType:    classification.TransactionType,
			sourceType:         classification.SourceType,
			currency:           rule.Currency,
			status:             rule.Status,
		}
		if _, found := normalized.rules[runtimeKey]; found {
			return normalizedAirwallexFinancialTransactionsRuntimeConfig{}, fmt.Errorf("duplicate Airwallex runtime mapping for one exact account/type/source/currency/status tuple")
		}
		normalized.rules[runtimeKey] = airwallexFinancialTransactionsRuntimeRule{
			configuredAccountSide:                 rule.ConfiguredAccountSide,
			counterpartyCompanyProviderAccountKey: rule.CounterpartyCompanyProviderAccountKey,
			counterparty:                          runtimeCounterpartyToProviderCounterparty(rule.Counterparty),
		}
	}

	classificationKeys := make([]airwallexFinancialTransactionClassificationKey, 0, len(classifications))
	for key := range classifications {
		classificationKeys = append(classificationKeys, key)
	}
	sort.Slice(classificationKeys, func(i, j int) bool {
		if classificationKeys[i].transactionType != classificationKeys[j].transactionType {
			return classificationKeys[i].transactionType < classificationKeys[j].transactionType
		}
		return classificationKeys[i].sourceType < classificationKeys[j].sourceType
	})
	normalized.classifications = make([]AirwallexFinancialTransactionClassification, 0, len(classificationKeys))
	for _, key := range classificationKeys {
		normalized.classifications = append(normalized.classifications, classifications[key])
	}
	return normalized, nil
}

func normalizeAirwallexFinancialTransactionsRuntimeRule(source AirwallexFinancialTransactionsRuntimeRule) (AirwallexFinancialTransactionsRuntimeRule, AirwallexFinancialTransactionClassification, airwallexFinancialTransactionClassificationKey, error) {
	evidenceReference, err := normalizeAirwallexRuntimeEvidenceReference(source.EvidenceReference)
	if err != nil {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	classification, classificationKey, err := normalizeAirwallexClassification(source.Classification)
	if err != nil {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	normalized := AirwallexFinancialTransactionsRuntimeRule{
		EvidenceReference: evidenceReference,
		Classification:    classification,
	}

	providerAccountKey, err := normalizeAirwallexRuntimeProviderAccountKey(source.ProviderAccountKey)
	if err != nil {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	currency, err := normalizeAirwallexRuntimeCurrency(source.Currency)
	if err != nil {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	status, err := normalizeAirwallexRuntimeStatus(source.Status)
	if err != nil {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	if classification.MovementKind == MovementKindFee || classification.MovementKind == MovementKindReversal || classification.MovementKind == MovementKindConversion {
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex runtime configuration refuses %s because it requires a dedicated relationship resolver", classification.MovementKind)
	}

	normalized.ProviderAccountKey = providerAccountKey
	normalized.Currency = currency
	normalized.Status = status
	if classification.Action != AirwallexFinancialTransactionActionApply {
		if source.ConfiguredAccountSide != "" || source.CounterpartyCompanyProviderAccountKey != "" || source.Counterparty != nil {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("terminal Airwallex runtime classification cannot carry relationship or counterparty mapping")
		}
		return normalized, classification, classificationKey, nil
	}
	switch classification.Direction {
	case DirectionInflow, DirectionOutflow:
		if source.ConfiguredAccountSide != "" || source.CounterpartyCompanyProviderAccountKey != "" {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("external Airwallex runtime mapping cannot declare an internal account relationship")
		}
		counterparty, err := normalizeAirwallexRuntimeManualCounterparty(source.Counterparty)
		if err != nil {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
		}
		normalized.Counterparty = counterparty
	case DirectionInternalTransfer:
		if source.Counterparty != nil {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("internal Airwallex runtime mapping cannot use a manual external counterparty")
		}
		if source.ConfiguredAccountSide != AirwallexConfiguredAccountSideFrom && source.ConfiguredAccountSide != AirwallexConfiguredAccountSideTo {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("internal Airwallex runtime mapping requires an explicit configured account side")
		}
		counterpartyCompanyKey, err := normalizeAirwallexRuntimeProviderAccountKey(source.CounterpartyCompanyProviderAccountKey)
		if err != nil || counterpartyCompanyKey == providerAccountKey {
			return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("internal Airwallex runtime mapping requires a distinct configured counterparty account key")
		}
		normalized.ConfiguredAccountSide = source.ConfiguredAccountSide
		normalized.CounterpartyCompanyProviderAccountKey = counterpartyCompanyKey
	default:
		return AirwallexFinancialTransactionsRuntimeRule{}, AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex runtime mapping has unsupported direction")
	}
	return normalized, classification, classificationKey, nil
}

func normalizeAirwallexRuntimeProviderAccountKey(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxProviderFactAccountKeyBytes {
		return "", fmt.Errorf("Airwallex runtime provider account key must be a non-blank exact bounded value")
	}
	return value, nil
}

func normalizeAirwallexRuntimeCurrency(value string) (string, error) {
	normalized, err := normalizeAirwallexNormalizationRequired("Airwallex runtime currency", value, maxProviderFactCurrencyBytes)
	if err != nil {
		return "", err
	}
	return strings.ToUpper(normalized), nil
}

func normalizeAirwallexRuntimeStatus(value string) (string, error) {
	normalized, err := normalizeAirwallexClassificationType("Airwallex runtime status", value)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func normalizeAirwallexRuntimeEvidenceReference(value string) (string, error) {
	return normalizeAirwallexNormalizationRequired("Airwallex runtime evidence reference", value, maxAirwallexRuntimeEvidenceBytes)
}

func normalizeAirwallexRuntimeManualCounterparty(source *AirwallexRuntimeManualCounterparty) (*AirwallexRuntimeManualCounterparty, error) {
	if source == nil {
		return nil, nil
	}
	normalized := *source
	var err error
	if normalized.EvidenceReference, err = normalizeAirwallexRuntimeEvidenceReference(source.EvidenceReference); err != nil {
		return nil, err
	}
	for _, field := range []*string{
		&normalized.AddressOrAccount,
		&normalized.Name,
		&normalized.CompanyEntity,
		&normalized.FundAccountName,
		&normalized.SubAccountName,
		&normalized.AccountType,
	} {
		*field, err = normalizeAirwallexRuntimeDisplayField(*field)
		if err != nil {
			return nil, err
		}
	}
	if normalized.AddressOrAccount == "" && normalized.Name == "" && normalized.CompanyEntity == "" &&
		normalized.FundAccountName == "" && normalized.SubAccountName == "" && normalized.AccountType == "" {
		return nil, fmt.Errorf("Airwallex manual counterparty cannot be empty")
	}
	return &normalized, nil
}

func normalizeAirwallexRuntimeDisplayField(value string) (string, error) {
	normalized := strings.TrimSpace(value)
	if len(normalized) > maxAirwallexRuntimeDisplayFieldLen {
		return "", fmt.Errorf("Airwallex runtime display field exceeds %d bytes", maxAirwallexRuntimeDisplayFieldLen)
	}
	return normalized, nil
}

func runtimeCounterpartyToProviderCounterparty(source *AirwallexRuntimeManualCounterparty) *AirwallexCounterparty {
	if source == nil {
		return nil
	}
	return &AirwallexCounterparty{
		AddressOrAccount: source.AddressOrAccount,
		Name:             source.Name,
		CompanyEntity:    source.CompanyEntity,
		FundAccountName:  source.FundAccountName,
		SubAccountName:   source.SubAccountName,
		AccountType:      source.AccountType,
	}
}

func cloneAirwallexFinancialTransactionsRuntimeRule(source airwallexFinancialTransactionsRuntimeRule) airwallexFinancialTransactionsRuntimeRule {
	return airwallexFinancialTransactionsRuntimeRule{
		configuredAccountSide:                 source.configuredAccountSide,
		counterpartyCompanyProviderAccountKey: source.counterpartyCompanyProviderAccountKey,
		counterparty:                          cloneAirwallexProviderEventCounterparty(source.counterparty),
	}
}

func ensureAirwallexRuntimeJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("Airwallex runtime configuration must contain one JSON value")
		}
		return fmt.Errorf("decode trailing Airwallex runtime configuration: %w", err)
	}
	return nil
}

type airwallexFinancialTransactionsRuntimeConfigJSON struct {
	Enabled        *bool                                           `json:"enabled"`
	APIVersion     string                                          `json:"api_version"`
	SchemaVersion  string                                          `json:"schema_version"`
	EventVersion   string                                          `json:"event_version"`
	MappingVersion string                                          `json:"mapping_version"`
	FactVersion    int                                             `json:"fact_version"`
	Rules          []airwallexFinancialTransactionsRuntimeRuleJSON `json:"rules"`
}

type airwallexFinancialTransactionsRuntimeRuleJSON struct {
	EvidenceReference                     string                                          `json:"evidence_reference"`
	ProviderAccountKey                    string                                          `json:"provider_account_key"`
	Currency                              string                                          `json:"currency"`
	Status                                string                                          `json:"status"`
	Classification                        airwallexFinancialTransactionClassificationJSON `json:"classification"`
	ConfiguredAccountSide                 AirwallexConfiguredAccountSide                  `json:"configured_account_side"`
	CounterpartyCompanyProviderAccountKey string                                          `json:"counterparty_company_provider_account_key"`
	Counterparty                          *airwallexRuntimeManualCounterpartyJSON         `json:"counterparty"`
}

func (source airwallexFinancialTransactionsRuntimeRuleJSON) runtimeRule() (AirwallexFinancialTransactionsRuntimeRule, error) {
	classification := source.Classification.classification()
	rule := AirwallexFinancialTransactionsRuntimeRule{
		EvidenceReference:                     source.EvidenceReference,
		ProviderAccountKey:                    source.ProviderAccountKey,
		Currency:                              source.Currency,
		Status:                                source.Status,
		Classification:                        classification,
		ConfiguredAccountSide:                 source.ConfiguredAccountSide,
		CounterpartyCompanyProviderAccountKey: source.CounterpartyCompanyProviderAccountKey,
	}
	if source.Counterparty != nil {
		rule.Counterparty = source.Counterparty.counterparty()
	}
	return rule, nil
}

type airwallexFinancialTransactionClassificationJSON struct {
	TransactionType   string                              `json:"transaction_type"`
	SourceType        string                              `json:"source_type"`
	Action            AirwallexFinancialTransactionAction `json:"action"`
	Reason            string                              `json:"reason"`
	MovementKind      MovementKind                        `json:"movement_kind"`
	Direction         Direction                           `json:"direction"`
	TransferMode      TransferMode                        `json:"transfer_mode"`
	AmountField       AirwallexFinancialAmountField       `json:"amount_field"`
	ExpectedSign      AirwallexFinancialValueSign         `json:"expected_sign"`
	OccurredAtField   AirwallexFinancialOccurredAtField   `json:"occurred_at_field"`
	IncludeFeeDisplay bool                                `json:"include_fee_display"`
	FeeDisplaySign    AirwallexFinancialValueSign         `json:"fee_display_sign"`
	ClientRateUse     AirwallexFinancialClientRateUse     `json:"client_rate_use"`
}

func (source airwallexFinancialTransactionClassificationJSON) classification() AirwallexFinancialTransactionClassification {
	return AirwallexFinancialTransactionClassification{
		TransactionType:   source.TransactionType,
		SourceType:        source.SourceType,
		Action:            source.Action,
		Reason:            source.Reason,
		MovementKind:      source.MovementKind,
		Direction:         source.Direction,
		TransferMode:      source.TransferMode,
		AmountField:       source.AmountField,
		ExpectedSign:      source.ExpectedSign,
		OccurredAtField:   source.OccurredAtField,
		IncludeFeeDisplay: source.IncludeFeeDisplay,
		FeeDisplaySign:    source.FeeDisplaySign,
		ClientRateUse:     source.ClientRateUse,
	}
}

type airwallexRuntimeManualCounterpartyJSON struct {
	EvidenceReference string `json:"evidence_reference"`
	AddressOrAccount  string `json:"address_or_account"`
	Name              string `json:"name"`
	CompanyEntity     string `json:"company_entity"`
	FundAccountName   string `json:"fund_account_name"`
	SubAccountName    string `json:"sub_account_name"`
	AccountType       string `json:"account_type"`
}

func (source airwallexRuntimeManualCounterpartyJSON) counterparty() *AirwallexRuntimeManualCounterparty {
	return &AirwallexRuntimeManualCounterparty{
		EvidenceReference: source.EvidenceReference,
		AddressOrAccount:  source.AddressOrAccount,
		Name:              source.Name,
		CompanyEntity:     source.CompanyEntity,
		FundAccountName:   source.FundAccountName,
		SubAccountName:    source.SubAccountName,
		AccountType:       source.AccountType,
	}
}
