package companyfund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	maxAirwallexNormalizationVersionBytes = 64
	maxAirwallexNormalizationTypeBytes    = 128
	maxAirwallexNormalizationIDBytes      = 256
	maxAirwallexNormalizationNumericBytes = 256
)

// AirwallexFinancialTransactionAction is the pinned disposition for one exact
// transaction_type/source_type pair. There is deliberately no fallback rule:
// a new provider type must be reviewed and added as a new mapping version.
type AirwallexFinancialTransactionAction string

const (
	AirwallexFinancialTransactionActionApply      AirwallexFinancialTransactionAction = "APPLY"
	AirwallexFinancialTransactionActionIgnore     AirwallexFinancialTransactionAction = "IGNORE"
	AirwallexFinancialTransactionActionQuarantine AirwallexFinancialTransactionAction = "QUARANTINE"
)

// AirwallexFinancialAmountField identifies the one provider number that was
// sandbox-proven to represent this balance-changing line. It is never selected
// dynamically from a numeric sign.
type AirwallexFinancialAmountField string

const (
	AirwallexFinancialAmountFieldAmount AirwallexFinancialAmountField = "AMOUNT"
	AirwallexFinancialAmountFieldFee    AirwallexFinancialAmountField = "FEE"
	AirwallexFinancialAmountFieldNet    AirwallexFinancialAmountField = "NET"
)

// AirwallexFinancialValueSign is part of the approved mapping. Direction is
// not inferred from the sign: this only converts the approved signed provider
// field to the non-negative domain magnitude exactly once.
type AirwallexFinancialValueSign string

const (
	AirwallexFinancialValueSignPositive AirwallexFinancialValueSign = "POSITIVE"
	AirwallexFinancialValueSignNegative AirwallexFinancialValueSign = "NEGATIVE"
)

// AirwallexFinancialOccurredAtField selects a provider timestamp only after
// the mapping has explicitly declared which documented field means the balance
// movement time for this line type.
type AirwallexFinancialOccurredAtField string

const (
	AirwallexFinancialOccurredAtCreated AirwallexFinancialOccurredAtField = "CREATED_AT"
	AirwallexFinancialOccurredAtSettled AirwallexFinancialOccurredAtField = "SETTLED_AT"
)

// AirwallexFinancialClientRateUse remains opt-in because the public API field
// alone does not prove a conversion-leg relationship. A Sandbox-approved
// mapping may enable it only with explicit conversion currencies in the input.
type AirwallexFinancialClientRateUse string

const (
	AirwallexFinancialClientRateUseNone           AirwallexFinancialClientRateUse = ""
	AirwallexFinancialClientRateUseConversionRate AirwallexFinancialClientRateUse = "CONVERSION_RATE"
)

// AirwallexConfiguredAccountSide is required for an internal movement so the
// normalizer never guesses which company account supplied the balance leg.
type AirwallexConfiguredAccountSide string

const (
	AirwallexConfiguredAccountSideFrom AirwallexConfiguredAccountSide = "FROM"
	AirwallexConfiguredAccountSideTo   AirwallexConfiguredAccountSide = "TO"
)

// AirwallexFinancialTransactionDisposition says whether a provider item is
// safe to persist as a reportable movement. Ignore and quarantine intentionally
// return no movement/fact/upsert input.
type AirwallexFinancialTransactionDisposition string

const (
	AirwallexFinancialTransactionDispositionApply      AirwallexFinancialTransactionDisposition = "APPLY"
	AirwallexFinancialTransactionDispositionIgnore     AirwallexFinancialTransactionDisposition = "IGNORE"
	AirwallexFinancialTransactionDispositionQuarantine AirwallexFinancialTransactionDisposition = "QUARANTINE"
)

// AirwallexFinancialTransactionClassification is one exact, versioned mapping
// from provider transaction/source types to ledger semantics. Values here are
// configuration, not a claim about any current Airwallex Sandbox shape.
type AirwallexFinancialTransactionClassification struct {
	TransactionType   string
	SourceType        string
	Action            AirwallexFinancialTransactionAction
	Reason            string
	MovementKind      MovementKind
	Direction         Direction
	TransferMode      TransferMode
	AmountField       AirwallexFinancialAmountField
	ExpectedSign      AirwallexFinancialValueSign
	OccurredAtField   AirwallexFinancialOccurredAtField
	IncludeFeeDisplay bool
	FeeDisplaySign    AirwallexFinancialValueSign
	ClientRateUse     AirwallexFinancialClientRateUse
}

// AirwallexFinancialTransactionNormalizerConfig pins the API/event/mapping
// versions used by one normalizer instance. Callers must carry the same schema
// and event versions on every input; nullable webhook account_id/org_id are
// deliberately absent from this contract and cannot select a company account.
type AirwallexFinancialTransactionNormalizerConfig struct {
	SchemaVersion   string
	EventVersion    string
	MappingVersion  string
	FactVersion     int
	Classifications []AirwallexFinancialTransactionClassification
}

type airwallexFinancialTransactionClassificationKey struct {
	transactionType string
	sourceType      string
}

// AirwallexFinancialTransactionNormalizer is an immutable, pure adapter. It
// has no database, HTTP, registry, or wall-clock dependency.
type AirwallexFinancialTransactionNormalizer struct {
	schemaVersion   string
	eventVersion    string
	mappingVersion  string
	factVersion     int
	classifications map[airwallexFinancialTransactionClassificationKey]AirwallexFinancialTransactionClassification
}

// AirwallexFinancialTransactionSourceMetadata identifies the already-stored
// raw provider snapshot that produced a normalized fact. The worker supplies
// it explicitly after account resolution; this normalizer never derives it
// from a webhook envelope's nullable account_id or org_id.
type AirwallexFinancialTransactionSourceMetadata struct {
	ProviderEventID       string
	ProviderEventRecordID int64
	PayloadDigest         string
	FactSource            ProviderFactSource
	SeenSource            TransactionSeenSource
}

// AirwallexCounterparty holds only already-available, explicit display data.
// It is never populated from undocumented Financial Transactions fields.
type AirwallexCounterparty struct {
	AddressOrAccount string
	Name             string
	CompanyEntity    string
	FundAccountName  string
	SubAccountName   string
	AccountType      string
}

// AirwallexMovementRelationship carries resolver-proven links. The
// normalizer refuses fee/reversal/conversion movements without the matching
// explicit linkage instead of attempting to derive links from source_id.
type AirwallexMovementRelationship struct {
	ParentMovementKey     string
	ReversalOfMovementKey string
	ConversionGroupKey    string
	ConversionLeg         ConversionLeg
	ConversionGroupState  ConversionGroupState
}

// AirwallexConversionDetails is needed only by a mapping explicitly approved
// to store client_rate as a conversion rate. The currencies are supplied by a
// sandbox-proven resolver rather than guessed from currency_pair.
type AirwallexConversionDetails struct {
	FromCurrency string
	ToCurrency   string
}

// AirwallexFinancialTransactionNormalizationInput contains all identities
// required to make a safe decision. ConfiguredAccount is an immutable snapshot
// selected by the caller; it is validated against ProviderAccountKey but is
// never looked up from provider-provided account/org fields here.
type AirwallexFinancialTransactionNormalizationInput struct {
	SchemaVersion              string
	EventVersion               string
	ProviderAccountKey         string
	ConfiguredAccount          CompanyFundAccount
	Counterparty               *AirwallexCounterparty
	CounterpartyCompanyAccount *CompanyFundAccount
	ConfiguredAccountSide      AirwallexConfiguredAccountSide
	AssetPolicy                *AccountAssetPolicy
	Source                     AirwallexFinancialTransactionSourceMetadata
	Relationship               AirwallexMovementRelationship
	Conversion                 AirwallexConversionDetails
	FinancialTransaction       AirwallexFinancialTransaction
}

// AirwallexFinancialTransactionNormalizationResult has a complete pure
// persistence proposal only for APPLY. Provider raw bytes remain owned by the
// encrypted provider-event boundary; ProviderFact keeps only allowlisted facts.
type AirwallexFinancialTransactionNormalizationResult struct {
	Disposition            AirwallexFinancialTransactionDisposition
	Reason                 string
	MappingVersion         string
	FinancialTransactionID string
	SourceID               string
	Movement               *CompanyFundMovement
	ProviderFact           *ProviderTransactionFactInput
	Transaction            *TransactionUpsertInput
	Risk                   *RiskAssessment
}

// ProviderEventNormalization converts an applied pure Airwallex result into
// the worker's facts-first contract. The fact reference is local to this one
// result; the worker persists the fact, then injects its durable ID into the
// explicitly bound movement. No unproven transaction/group total is emitted
// or bound as a child value here.
func (result AirwallexFinancialTransactionNormalizationResult) ProviderEventNormalization() (ProviderEventNormalizationResult, error) {
	switch result.Disposition {
	case AirwallexFinancialTransactionDispositionIgnore:
		return ProviderEventNormalizationResult{Ignored: true}, nil
	case AirwallexFinancialTransactionDispositionQuarantine:
		return ProviderEventNormalizationResult{}, NewPermanentProviderEventError(fmt.Errorf("Airwallex financial transaction normalization quarantined: %s", strings.TrimSpace(result.Reason)))
	case AirwallexFinancialTransactionDispositionApply:
		if result.ProviderFact == nil || result.Transaction == nil || strings.TrimSpace(result.Transaction.MovementKey) == "" {
			return ProviderEventNormalizationResult{}, NewPermanentProviderEventError(fmt.Errorf("applied Airwallex financial transaction normalization is incomplete"))
		}
		factReference := "airwallex-fact:" + payloadSHA256Hex([]byte(result.ProviderFact.FactIdentityKey))
		return ProviderEventNormalizationResult{
			Facts: []ProviderEventNormalizedFact{{
				Reference: factReference,
				Input:     *result.ProviderFact,
			}},
			Movements: []TransactionUpsertInput{*result.Transaction},
			FactBindings: []ProviderEventMovementFactBinding{{
				MovementKey:   result.Transaction.MovementKey,
				FactReference: factReference,
			}},
		}, nil
	default:
		return ProviderEventNormalizationResult{}, NewPermanentProviderEventError(fmt.Errorf("unsupported Airwallex financial transaction normalization disposition %q", result.Disposition))
	}
}

// NewAirwallexFinancialTransactionNormalizer validates an immutable, exact
// allowlist. It intentionally does not include production defaults: the
// required source-type, sign, source-ID, and conversion contracts belong to
// gated Sandbox evidence and must be configured deliberately.
func NewAirwallexFinancialTransactionNormalizer(config AirwallexFinancialTransactionNormalizerConfig) (*AirwallexFinancialTransactionNormalizer, error) {
	schemaVersion, err := normalizeAirwallexNormalizationRequired("Airwallex schema version", config.SchemaVersion, maxAirwallexNormalizationVersionBytes)
	if err != nil {
		return nil, err
	}
	eventVersion, err := normalizeAirwallexNormalizationRequired("Airwallex event version", config.EventVersion, maxAirwallexNormalizationVersionBytes)
	if err != nil {
		return nil, err
	}
	mappingVersion, err := normalizeAirwallexNormalizationRequired("Airwallex mapping version", config.MappingVersion, maxAirwallexNormalizationVersionBytes)
	if err != nil {
		return nil, err
	}
	if config.FactVersion <= 0 {
		return nil, fmt.Errorf("Airwallex provider fact version must be positive")
	}
	if len(config.Classifications) == 0 {
		return nil, fmt.Errorf("Airwallex normalizer requires at least one explicit classification")
	}

	classifications := make(map[airwallexFinancialTransactionClassificationKey]AirwallexFinancialTransactionClassification, len(config.Classifications))
	for _, source := range config.Classifications {
		classification, key, err := normalizeAirwallexClassification(source)
		if err != nil {
			return nil, err
		}
		if _, exists := classifications[key]; exists {
			return nil, fmt.Errorf("duplicate Airwallex classification for transaction type %q and source type %q", key.transactionType, key.sourceType)
		}
		classifications[key] = classification
	}

	return &AirwallexFinancialTransactionNormalizer{
		schemaVersion:   schemaVersion,
		eventVersion:    eventVersion,
		mappingVersion:  mappingVersion,
		factVersion:     config.FactVersion,
		classifications: classifications,
	}, nil
}

// Normalize converts one Financial Transactions API item only when an exact
// pinned mapping and all provenance/account inputs are available. Every input
// failure becomes a quarantine disposition with no partial movement output.
func (n *AirwallexFinancialTransactionNormalizer) Normalize(input AirwallexFinancialTransactionNormalizationInput) AirwallexFinancialTransactionNormalizationResult {
	result := AirwallexFinancialTransactionNormalizationResult{
		Disposition:            AirwallexFinancialTransactionDispositionQuarantine,
		FinancialTransactionID: strings.TrimSpace(input.FinancialTransaction.ProviderID),
		SourceID:               strings.TrimSpace(input.FinancialTransaction.SourceID),
	}
	if n == nil {
		result.Reason = "AIRWALLEX_NORMALIZER_NOT_CONFIGURED"
		return result
	}
	result.MappingVersion = n.mappingVersion
	if schemaVersion := strings.TrimSpace(input.SchemaVersion); schemaVersion != n.schemaVersion {
		result.Reason = "AIRWALLEX_SCHEMA_VERSION_MISMATCH"
		return result
	}
	if eventVersion := strings.TrimSpace(input.EventVersion); eventVersion != n.eventVersion {
		result.Reason = "AIRWALLEX_EVENT_VERSION_MISMATCH"
		return result
	}
	if err := input.Source.validate(); err != nil {
		result.Reason = "AIRWALLEX_SOURCE_METADATA_INVALID"
		return result
	}
	sourceProviderEventID := strings.TrimSpace(input.Source.ProviderEventID)

	configuredAccount, providerAccountKey, err := validateAirwallexConfiguredAccount(input.ConfiguredAccount, input.ProviderAccountKey)
	if err != nil {
		result.Reason = "AIRWALLEX_CONFIGURED_ACCOUNT_INVALID"
		return result
	}
	transaction, err := normalizeAirwallexFinancialTransaction(input.FinancialTransaction)
	if err != nil {
		result.Reason = "AIRWALLEX_FINANCIAL_TRANSACTION_INVALID"
		return result
	}
	result.FinancialTransactionID = transaction.providerID
	result.SourceID = transaction.sourceID
	numbers, err := parseAirwallexFinancialTransactionNumbers(input.FinancialTransaction)
	if err != nil {
		result.Reason = "AIRWALLEX_FINANCIAL_TRANSACTION_NUMERIC_INVALID"
		return result
	}

	classification, exists := n.classifications[airwallexFinancialTransactionClassificationKey{
		transactionType: transaction.transactionType,
		sourceType:      transaction.sourceType,
	}]
	if !exists {
		result.Reason = "AIRWALLEX_UNMAPPED_TRANSACTION_SOURCE_TYPE"
		return result
	}
	switch classification.Action {
	case AirwallexFinancialTransactionActionIgnore:
		result.Disposition = AirwallexFinancialTransactionDispositionIgnore
		result.Reason = classification.Reason
		return result
	case AirwallexFinancialTransactionActionQuarantine:
		result.Reason = classification.Reason
		return result
	case AirwallexFinancialTransactionActionApply:
		// Continue below.
	default:
		result.Reason = "AIRWALLEX_CLASSIFICATION_ACTION_INVALID"
		return result
	}

	amount, err := normalizedAirwallexMovementMagnitude(numbers, classification.AmountField, classification.ExpectedSign)
	if err != nil || !amount.IsPositive() || validateProviderFactDecimal("Airwallex normalized movement amount", &amount, false) != nil {
		result.Reason = "AIRWALLEX_MOVEMENT_AMOUNT_INVALID"
		return result
	}
	occurredAt, err := airwallexOccurrenceTime(transaction, classification.OccurredAtField)
	if err != nil {
		result.Reason = "AIRWALLEX_OCCURRENCE_TIME_INVALID"
		return result
	}

	resolvedAccounts, err := resolveAirwallexMovementAccounts(classification.Direction, configuredAccount, input.CounterpartyCompanyAccount, input.ConfiguredAccountSide)
	if err != nil {
		result.Reason = "AIRWALLEX_MOVEMENT_ACCOUNTS_INVALID"
		return result
	}
	relation := normalizeAirwallexRelationship(input.Relationship)
	if err := ValidateMovementRelationship(MovementRelation{
		MovementKind:          classification.MovementKind,
		TransferMode:          classification.TransferMode,
		Direction:             classification.Direction,
		HasFromAccount:        resolvedAccounts.fromAccountID != nil,
		HasToAccount:          resolvedAccounts.toAccountID != nil,
		ParentMovementKey:     relation.ParentMovementKey,
		ReversalOfMovementKey: relation.ReversalOfMovementKey,
		ConversionGroupKey:    relation.ConversionGroupKey,
		ConversionLeg:         relation.ConversionLeg,
		ConversionGroupState:  relation.ConversionGroupState,
	}); err != nil {
		result.Reason = "AIRWALLEX_MOVEMENT_RELATIONSHIP_INVALID"
		return result
	}

	asset := AssetIdentity{Currency: transaction.currency}
	policy, err := validatedAirwallexAssetPolicy(input.AssetPolicy, configuredAccount.ID, asset)
	if err != nil {
		result.Reason = "AIRWALLEX_ASSET_POLICY_INVALID"
		return result
	}
	risk, err := EvaluateRisk(RiskInput{
		Channel:          ChannelAirwallex,
		Direction:        classification.Direction,
		Amount:           amount,
		Asset:            asset,
		Policy:           airwallexDustPolicy(policy),
		ConfiguredFromID: resolvedAccounts.fromAccountID,
		ConfiguredToID:   resolvedAccounts.toAccountID,
	})
	if err != nil {
		result.Reason = "AIRWALLEX_RISK_EVALUATION_INVALID"
		return result
	}

	identity, err := BuildMovementIdentity(MovementIdentityInput{
		Channel:          ChannelAirwallex,
		ProviderParentID: transaction.providerID,
		MovementKind:     classification.MovementKind,
		Asset:            asset,
		NormalizedFrom:   resolvedAccounts.identityFrom,
		NormalizedTo:     resolvedAccounts.identityTo,
		Amount:           amount,
		Occurrence:       1,
	})
	if err != nil {
		result.Reason = "AIRWALLEX_MOVEMENT_IDENTITY_INVALID"
		return result
	}

	display, err := buildAirwallexTransactionDisplay(classification, numbers, transaction.currency, configuredAccount, input.Counterparty, resolvedAccounts)
	if err != nil {
		result.Reason = "AIRWALLEX_DISPLAY_METADATA_INVALID"
		return result
	}
	conversion, err := buildAirwallexConversionFact(classification, numbers, input.Conversion)
	if err != nil {
		result.Reason = "AIRWALLEX_CONVERSION_FACT_INVALID"
		return result
	}
	extras, err := buildAirwallexFinancialTransactionFactExtras(transaction, input.FinancialTransaction, n.schemaVersion, n.eventVersion, n.mappingVersion)
	if err != nil {
		result.Reason = "AIRWALLEX_PROVIDER_FACT_EXTRAS_INVALID"
		return result
	}
	factIdentityKey := airwallexFinancialTransactionFactIdentity(n.schemaVersion, n.eventVersion, n.mappingVersion, sourceProviderEventID, transaction.providerID)
	if err := validateRequiredString("Airwallex provider fact identity", factIdentityKey, maxProviderFactIdentityKeyBytes); err != nil {
		result.Reason = "AIRWALLEX_PROVIDER_FACT_IDENTITY_INVALID"
		return result
	}

	amountCopy := amount
	currencyCopy := transaction.currency
	assetCopy := asset
	occurredAtCopy := occurredAt
	provider := ProviderOwnedFields{
		Metadata:   ProviderFactMetadata{Source: input.Source.FactSource},
		Amount:     &amountCopy,
		Currency:   &currencyCopy,
		Asset:      &assetCopy,
		OccurredAt: &occurredAtCopy,
	}
	providerFact := ProviderTransactionFactInput{
		Channel:                ChannelAirwallex,
		ProviderAccountKey:     providerAccountKey,
		ProviderTransactionID:  transaction.providerID,
		ProviderGroupID:        transaction.batchID,
		FactIdentityKey:        factIdentityKey,
		FactVersion:            n.factVersion,
		SourceProviderEventID:  input.Source.ProviderEventRecordID,
		SourcePayloadDigest:    input.Source.PayloadDigest,
		ProviderOccurredAt:     &occurredAtCopy,
		ProviderAmount:         &amountCopy,
		ProviderCurrency:       transaction.currency,
		ConversionFromCurrency: conversion.fromCurrency,
		ConversionToCurrency:   conversion.toCurrency,
		ConversionRate:         conversion.rate,
		ValueScope:             ProviderValueScopeDirectItem,
		AllocationState:        ProviderFactAllocationStateNotApplicable,
		ProviderExtrasJSON:     extras,
	}
	movement := CompanyFundMovement{
		Identity:              identity,
		Channel:               ChannelAirwallex,
		MovementKind:          classification.MovementKind,
		TransferMode:          classification.TransferMode,
		Direction:             classification.Direction,
		Amount:                amount,
		Asset:                 asset,
		FromAccountID:         copyAirwallexInt64(resolvedAccounts.fromAccountID),
		ToAccountID:           copyAirwallexInt64(resolvedAccounts.toAccountID),
		ParentMovementKey:     relation.ParentMovementKey,
		ReversalOfMovementKey: relation.ReversalOfMovementKey,
		ConversionGroupKey:    relation.ConversionGroupKey,
		ConversionLeg:         relation.ConversionLeg,
		ConversionGroupState:  relation.ConversionGroupState,
		Provider:              provider,
	}
	automaticRisk := airwallexAutomaticRiskInput(policy, risk)
	latestProviderEventID := input.Source.ProviderEventRecordID
	transactionUpsert := TransactionUpsertInput{
		MovementKey:              identity.Key,
		Channel:                  ChannelAirwallex,
		IdentityAlgorithmVersion: identity.AlgorithmVersion,
		ProviderAccountKey:       providerAccountKey,
		ProviderTransactionID:    transaction.providerID,
		ProviderEventID:          sourceProviderEventID,
		// The Financial Transactions item ID is the stable movement line
		// identity. source_id remains an allowlisted provider fact only until a
		// Sandbox fixture proves a cross-surface identity contract.
		ProviderMovementID:       transaction.providerID,
		MovementIndex:            0,
		MovementKind:             classification.MovementKind,
		TransferMode:             classification.TransferMode,
		Direction:                classification.Direction,
		ParentMovementKey:        relation.ParentMovementKey,
		ReversalOfMovementKey:    relation.ReversalOfMovementKey,
		ConversionGroupKey:       relation.ConversionGroupKey,
		ConversionLeg:            relation.ConversionLeg,
		ConversionGroupState:     relation.ConversionGroupState,
		FromCompanyFundAccountID: copyAirwallexInt64(resolvedAccounts.fromAccountID),
		ToCompanyFundAccountID:   copyAirwallexInt64(resolvedAccounts.toAccountID),
		Currency:                 transaction.currency,
		Asset:                    asset,
		Amount:                   amount,
		OccurredAt:               &occurredAtCopy,
		LatestProviderEventID:    &latestProviderEventID,
		RawSnapshotDigest:        input.Source.PayloadDigest,
		FirstSeenSource:          input.Source.SeenSource,
		Provider:                 provider,
		ProviderStatusRank:       0,
		ProviderDisplay:          display,
		AutomaticRisk:            automaticRisk,
	}
	if _, err := providerFact.validate(); err != nil {
		result.Reason = "AIRWALLEX_PROVIDER_FACT_INVALID"
		return result
	}
	if err := transactionUpsert.validate(); err != nil {
		result.Reason = "AIRWALLEX_TRANSACTION_UPSERT_INVALID"
		return result
	}

	result.Disposition = AirwallexFinancialTransactionDispositionApply
	result.Movement = &movement
	result.ProviderFact = &providerFact
	result.Transaction = &transactionUpsert
	riskCopy := risk
	result.Risk = &riskCopy
	return result
}

func normalizeAirwallexClassification(source AirwallexFinancialTransactionClassification) (AirwallexFinancialTransactionClassification, airwallexFinancialTransactionClassificationKey, error) {
	transactionType, err := normalizeAirwallexClassificationType("Airwallex transaction type", source.TransactionType)
	if err != nil {
		return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	sourceType, err := normalizeAirwallexClassificationType("Airwallex source type", source.SourceType)
	if err != nil {
		return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
	}
	classification := source
	classification.TransactionType = transactionType
	classification.SourceType = sourceType
	classification.Reason = strings.TrimSpace(source.Reason)

	switch classification.Action {
	case AirwallexFinancialTransactionActionApply:
		if classification.Reason != "" {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("applied Airwallex classification cannot carry a terminal disposition reason")
		}
		if !classification.MovementKind.Valid() || !classification.Direction.Valid() || !classification.TransferMode.Valid() ||
			!classification.AmountField.valid() || !classification.ExpectedSign.valid() || !classification.OccurredAtField.valid() {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("applied Airwallex classification requires valid movement, direction, amount, sign, and occurrence rules")
		}
		if classification.AmountField == AirwallexFinancialAmountFieldFee && classification.MovementKind != MovementKindFee {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex fee amount field is only valid for a fee movement")
		}
		if classification.IncludeFeeDisplay {
			if classification.MovementKind == MovementKindFee || classification.AmountField == AirwallexFinancialAmountFieldFee || !classification.FeeDisplaySign.valid() {
				return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex fee display requires an explicit non-fee movement and fee sign rule")
			}
		} else if classification.FeeDisplaySign != "" {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex fee display sign requires fee display opt-in")
		}
		switch classification.ClientRateUse {
		case AirwallexFinancialClientRateUseNone:
		case AirwallexFinancialClientRateUseConversionRate:
			if classification.MovementKind != MovementKindConversion {
				return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("Airwallex client rate conversion mapping requires a conversion movement")
			}
		default:
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("unsupported Airwallex client rate use %q", classification.ClientRateUse)
		}
	case AirwallexFinancialTransactionActionIgnore, AirwallexFinancialTransactionActionQuarantine:
		terminalReason, err := normalizeAirwallexNormalizationRequired("terminal Airwallex classification reason", classification.Reason, maxAirwallexNormalizationIDBytes)
		if err != nil {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, err
		}
		classification.Reason = terminalReason
		if classification.MovementKind != "" || classification.Direction != "" || classification.TransferMode != "" ||
			classification.AmountField != "" || classification.ExpectedSign != "" || classification.OccurredAtField != "" ||
			classification.IncludeFeeDisplay || classification.FeeDisplaySign != "" || classification.ClientRateUse != "" {
			return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("terminal Airwallex classification cannot include movement mapping fields")
		}
	default:
		return AirwallexFinancialTransactionClassification{}, airwallexFinancialTransactionClassificationKey{}, fmt.Errorf("unsupported Airwallex classification action %q", classification.Action)
	}
	return classification, airwallexFinancialTransactionClassificationKey{transactionType: transactionType, sourceType: sourceType}, nil
}

func normalizeAirwallexClassificationType(label, value string) (string, error) {
	normalized, err := normalizeAirwallexNormalizationRequired(label, value, maxAirwallexNormalizationTypeBytes)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(normalized, "*?") {
		return "", fmt.Errorf("%s cannot use wildcard matching", label)
	}
	return strings.ToUpper(normalized), nil
}

func normalizeAirwallexNormalizationRequired(label, value string, maxBytes int) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" || len(normalized) > maxBytes || !utf8.ValidString(normalized) {
		return "", fmt.Errorf("%s must be non-blank valid UTF-8 within %d bytes", label, maxBytes)
	}
	return normalized, nil
}

func (source AirwallexFinancialTransactionSourceMetadata) validate() error {
	if _, err := normalizeAirwallexNormalizationRequired("Airwallex source provider event ID", source.ProviderEventID, maxAirwallexNormalizationIDBytes); err != nil {
		return err
	}
	if source.ProviderEventRecordID <= 0 || !isLowerSHA256Hex(source.PayloadDigest) || !source.FactSource.valid() || !source.SeenSource.valid() {
		return fmt.Errorf("Airwallex source metadata is incomplete or invalid")
	}
	return nil
}

func validateAirwallexConfiguredAccount(source CompanyFundAccount, providerAccountKey string) (CompanyFundAccount, string, error) {
	if source.ID <= 0 || source.Channel != ChannelAirwallex || !source.Enabled {
		return CompanyFundAccount{}, "", fmt.Errorf("configured Airwallex account must be enabled with a positive ID")
	}
	configuredKey, err := normalizeAirwallexNormalizationRequired("configured Airwallex provider account key", source.ProviderAccountKey, maxProviderFactAccountKeyBytes)
	if err != nil {
		return CompanyFundAccount{}, "", err
	}
	providedKey, err := normalizeAirwallexNormalizationRequired("Airwallex provider account key", providerAccountKey, maxProviderFactAccountKeyBytes)
	if err != nil {
		return CompanyFundAccount{}, "", err
	}
	if configuredKey != providedKey {
		return CompanyFundAccount{}, "", fmt.Errorf("explicit Airwallex provider account key does not match configured account")
	}
	return source, configuredKey, nil
}

type normalizedAirwallexFinancialTransaction struct {
	providerID      string
	batchID         string
	createdAt       string
	settledAt       string
	currency        string
	currencyPair    string
	sourceID        string
	sourceType      string
	status          string
	transactionType string
	fundingSourceID string
}

func normalizeAirwallexFinancialTransaction(source AirwallexFinancialTransaction) (normalizedAirwallexFinancialTransaction, error) {
	providerID, err := normalizeAirwallexNormalizationRequired("Airwallex financial transaction ID", source.ProviderID, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	currency, err := normalizeAirwallexNormalizationRequired("Airwallex financial transaction currency", source.Currency, maxProviderFactCurrencyBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	transactionType, err := normalizeAirwallexClassificationType("Airwallex financial transaction type", source.TransactionType)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	sourceType, err := normalizeAirwallexClassificationType("Airwallex financial transaction source type", source.SourceType)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	createdAt, err := normalizeAirwallexNormalizationRequired("Airwallex financial transaction created time", source.CreatedAt, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	batchID, err := normalizeAirwallexOptional("Airwallex financial transaction batch ID", source.BatchID, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	settledAt, err := normalizeAirwallexOptional("Airwallex financial transaction settled time", source.SettledAt, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	currencyPair, err := normalizeAirwallexOptional("Airwallex financial transaction currency pair", source.CurrencyPair, maxAirwallexNormalizationTypeBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	sourceID, err := normalizeAirwallexOptional("Airwallex financial transaction source ID", source.SourceID, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	status, err := normalizeAirwallexOptional("Airwallex financial transaction status", source.Status, maxAirwallexNormalizationTypeBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	fundingSourceID, err := normalizeAirwallexOptional("Airwallex financial transaction funding source ID", source.FundingSourceID, maxAirwallexNormalizationIDBytes)
	if err != nil {
		return normalizedAirwallexFinancialTransaction{}, err
	}
	return normalizedAirwallexFinancialTransaction{
		providerID:      providerID,
		batchID:         batchID,
		createdAt:       createdAt,
		settledAt:       settledAt,
		currency:        strings.ToUpper(currency),
		currencyPair:    currencyPair,
		sourceID:        sourceID,
		sourceType:      sourceType,
		status:          status,
		transactionType: transactionType,
		fundingSourceID: fundingSourceID,
	}, nil
}

func normalizeAirwallexOptional(label, value string, maxBytes int) (string, error) {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return "", nil
	}
	if len(normalized) > maxBytes || !utf8.ValidString(normalized) {
		return "", fmt.Errorf("%s must be valid UTF-8 within %d bytes", label, maxBytes)
	}
	return normalized, nil
}

type parsedAirwallexFinancialTransactionNumbers struct {
	amount     decimal.Decimal
	fee        *decimal.Decimal
	net        *decimal.Decimal
	clientRate *decimal.Decimal
}

func parseAirwallexFinancialTransactionNumbers(source AirwallexFinancialTransaction) (parsedAirwallexFinancialTransactionNumbers, error) {
	amount, err := parseRequiredAirwallexJSONDecimal("amount", source.Amount)
	if err != nil {
		return parsedAirwallexFinancialTransactionNumbers{}, err
	}
	fee, err := parseOptionalAirwallexJSONDecimal("fee", source.Fee)
	if err != nil {
		return parsedAirwallexFinancialTransactionNumbers{}, err
	}
	net, err := parseOptionalAirwallexJSONDecimal("net", source.Net)
	if err != nil {
		return parsedAirwallexFinancialTransactionNumbers{}, err
	}
	clientRate, err := parseOptionalAirwallexJSONDecimal("client_rate", source.ClientRate)
	if err != nil {
		return parsedAirwallexFinancialTransactionNumbers{}, err
	}
	return parsedAirwallexFinancialTransactionNumbers{amount: amount, fee: fee, net: net, clientRate: clientRate}, nil
}

func parseRequiredAirwallexJSONDecimal(label string, raw json.RawMessage) (decimal.Decimal, error) {
	value, present, err := parseAirwallexJSONDecimal(label, raw)
	if err != nil || !present {
		return decimal.Zero, fmt.Errorf("Airwallex %s must be a JSON number", label)
	}
	return *value, nil
}

func parseOptionalAirwallexJSONDecimal(label string, raw json.RawMessage) (*decimal.Decimal, error) {
	value, _, err := parseAirwallexJSONDecimal(label, raw)
	return value, err
}

func parseAirwallexJSONDecimal(label string, raw json.RawMessage) (*decimal.Decimal, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if len(trimmed) > maxAirwallexNormalizationNumericBytes {
		return nil, false, fmt.Errorf("Airwallex %s numeric value exceeds bounds", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false, fmt.Errorf("decode Airwallex %s numeric value: %w", label, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false, fmt.Errorf("Airwallex %s must contain one JSON value", label)
	}
	number, ok := decoded.(json.Number)
	if !ok {
		return nil, false, fmt.Errorf("Airwallex %s must be a JSON number", label)
	}
	value, err := decimal.NewFromString(number.String())
	if err != nil {
		return nil, false, fmt.Errorf("parse Airwallex %s decimal: %w", label, err)
	}
	return &value, true, nil
}

func normalizedAirwallexMovementMagnitude(numbers parsedAirwallexFinancialTransactionNumbers, field AirwallexFinancialAmountField, expectedSign AirwallexFinancialValueSign) (decimal.Decimal, error) {
	value := &numbers.amount
	switch field {
	case AirwallexFinancialAmountFieldAmount:
	case AirwallexFinancialAmountFieldFee:
		value = numbers.fee
	case AirwallexFinancialAmountFieldNet:
		value = numbers.net
	default:
		return decimal.Zero, fmt.Errorf("unsupported Airwallex movement amount field")
	}
	if value == nil {
		return decimal.Zero, fmt.Errorf("mapped Airwallex movement amount field is absent")
	}
	// Direction was supplied by the allowlist. This is the only sign conversion:
	// it rejects an unexpected sign and returns the corresponding magnitude without
	// using sign to infer an inflow/outflow direction.
	switch expectedSign {
	case AirwallexFinancialValueSignPositive:
		if !value.IsPositive() {
			return decimal.Zero, fmt.Errorf("Airwallex movement amount must be positive")
		}
		return *value, nil
	case AirwallexFinancialValueSignNegative:
		if !value.IsNegative() {
			return decimal.Zero, fmt.Errorf("Airwallex movement amount must be negative")
		}
		return value.Neg(), nil
	default:
		return decimal.Zero, fmt.Errorf("unsupported Airwallex movement sign rule")
	}
}

func airwallexOccurrenceTime(source normalizedAirwallexFinancialTransaction, field AirwallexFinancialOccurredAtField) (time.Time, error) {
	value := source.createdAt
	if field == AirwallexFinancialOccurredAtSettled {
		value = source.settledAt
	}
	if value == "" {
		return time.Time{}, fmt.Errorf("Airwallex mapped occurrence time is absent")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse Airwallex occurrence time: %w", err)
	}
	return parsed.UTC(), nil
}

type resolvedAirwallexMovementAccounts struct {
	fromAccountID *int64
	toAccountID   *int64
	identityFrom  string
	identityTo    string
	fromAccount   CompanyFundAccount
	toAccount     CompanyFundAccount
}

func resolveAirwallexMovementAccounts(direction Direction, configured CompanyFundAccount, counterparty *CompanyFundAccount, configuredSide AirwallexConfiguredAccountSide) (resolvedAirwallexMovementAccounts, error) {
	configuredKey := strings.TrimSpace(configured.ProviderAccountKey)
	switch direction {
	case DirectionInflow:
		if counterparty != nil || configuredSide != "" {
			return resolvedAirwallexMovementAccounts{}, fmt.Errorf("external inflow cannot carry an internal account assignment")
		}
		return resolvedAirwallexMovementAccounts{
			toAccountID: airwallexInt64Pointer(configured.ID),
			identityTo:  configuredKey,
			toAccount:   configured,
		}, nil
	case DirectionOutflow:
		if counterparty != nil || configuredSide != "" {
			return resolvedAirwallexMovementAccounts{}, fmt.Errorf("external outflow cannot carry an internal account assignment")
		}
		return resolvedAirwallexMovementAccounts{
			fromAccountID: airwallexInt64Pointer(configured.ID),
			identityFrom:  configuredKey,
			fromAccount:   configured,
		}, nil
	case DirectionInternalTransfer:
		if counterparty == nil || (configuredSide != AirwallexConfiguredAccountSideFrom && configuredSide != AirwallexConfiguredAccountSideTo) {
			return resolvedAirwallexMovementAccounts{}, fmt.Errorf("internal Airwallex movement requires an explicit other account and configured side")
		}
		other, otherKey, err := validateAirwallexConfiguredAccount(*counterparty, counterparty.ProviderAccountKey)
		if err != nil || other.ID == configured.ID {
			return resolvedAirwallexMovementAccounts{}, fmt.Errorf("internal Airwallex counterparty account is invalid")
		}
		if configuredSide == AirwallexConfiguredAccountSideFrom {
			return resolvedAirwallexMovementAccounts{
				fromAccountID: airwallexInt64Pointer(configured.ID),
				toAccountID:   airwallexInt64Pointer(other.ID),
				identityFrom:  configuredKey,
				identityTo:    otherKey,
				fromAccount:   configured,
				toAccount:     other,
			}, nil
		}
		return resolvedAirwallexMovementAccounts{
			fromAccountID: airwallexInt64Pointer(other.ID),
			toAccountID:   airwallexInt64Pointer(configured.ID),
			identityFrom:  otherKey,
			identityTo:    configuredKey,
			fromAccount:   other,
			toAccount:     configured,
		}, nil
	default:
		return resolvedAirwallexMovementAccounts{}, fmt.Errorf("unsupported Airwallex direction")
	}
}

func normalizeAirwallexRelationship(source AirwallexMovementRelationship) AirwallexMovementRelationship {
	return AirwallexMovementRelationship{
		ParentMovementKey:     strings.TrimSpace(source.ParentMovementKey),
		ReversalOfMovementKey: strings.TrimSpace(source.ReversalOfMovementKey),
		ConversionGroupKey:    strings.TrimSpace(source.ConversionGroupKey),
		ConversionLeg:         source.ConversionLeg,
		ConversionGroupState:  source.ConversionGroupState,
	}
}

func validatedAirwallexAssetPolicy(source *AccountAssetPolicy, accountID int64, asset AssetIdentity) (*AccountAssetPolicy, error) {
	if source == nil {
		return nil, nil
	}
	if !source.Enabled || source.ID <= 0 || source.AccountID != accountID || normalizeAssetIdentity(source.Asset).canonicalKey() != normalizeAssetIdentity(asset).canonicalKey() {
		return nil, fmt.Errorf("Airwallex account asset policy does not match the configured fiat account and asset")
	}
	copy := *source
	copy.Asset = normalizeAssetIdentity(source.Asset)
	copy.Dust = DustPolicy{ID: source.Dust.ID, Enabled: source.Dust.Enabled, Threshold: copyAirwallexDecimal(source.Dust.Threshold)}
	if copy.Dust.Enabled && (copy.Dust.ID <= 0 || copy.Dust.Threshold == nil) {
		return nil, fmt.Errorf("enabled Airwallex dust policy requires an ID and threshold")
	}
	return &copy, nil
}

func airwallexDustPolicy(policy *AccountAssetPolicy) DustPolicy {
	if policy == nil {
		return DustPolicy{}
	}
	return policy.Dust
}

func buildAirwallexTransactionDisplay(classification AirwallexFinancialTransactionClassification, numbers parsedAirwallexFinancialTransactionNumbers, currency string, configured CompanyFundAccount, counterparty *AirwallexCounterparty, resolved resolvedAirwallexMovementAccounts) (ProviderTransactionDisplayInput, error) {
	configuredParty := airwallexCompanyAccountDisplay(configured)
	counterpartyParty, counterpartyName := airwallexCounterpartyDisplay(counterparty)
	display := ProviderTransactionDisplayInput{}
	switch classification.Direction {
	case DirectionInflow:
		display.From = counterpartyParty
		display.To = configuredParty
		display.PayerName = counterpartyName
	case DirectionOutflow:
		display.From = configuredParty
		display.To = counterpartyParty
		display.PayeeName = counterpartyName
	case DirectionInternalTransfer:
		display.From = airwallexCompanyAccountDisplay(resolved.fromAccount)
		display.To = airwallexCompanyAccountDisplay(resolved.toAccount)
	default:
		return ProviderTransactionDisplayInput{}, fmt.Errorf("unsupported Airwallex display direction")
	}
	if classification.IncludeFeeDisplay {
		if numbers.fee == nil || numbers.fee.IsZero() {
			return normalizedAirwallexDisplay(display)
		}
		fee, err := normalizedAirwallexFeeDisplayAmount(numbers.fee, classification.FeeDisplaySign)
		if err != nil {
			return ProviderTransactionDisplayInput{}, err
		}
		currencyCopy := currency
		display.Fee = ProviderTransactionFeeInput{Amount: &fee, Currency: &currencyCopy}
	}
	return normalizedAirwallexDisplay(display)
}

func normalizedAirwallexDisplay(display ProviderTransactionDisplayInput) (ProviderTransactionDisplayInput, error) {
	normalized, err := normalizeProviderTransactionDisplay(display)
	if err != nil {
		return ProviderTransactionDisplayInput{}, err
	}
	return ProviderTransactionDisplayInput{
		From: ProviderTransactionPartyDisplayInput{
			AddressOrAccount: normalized.From.AddressOrAccount,
			CompanyEntity:    normalized.From.CompanyEntity,
			FundAccountName:  normalized.From.FundAccountName,
			SubAccountName:   normalized.From.SubAccountName,
			AccountType:      normalized.From.AccountType,
		},
		To: ProviderTransactionPartyDisplayInput{
			AddressOrAccount: normalized.To.AddressOrAccount,
			CompanyEntity:    normalized.To.CompanyEntity,
			FundAccountName:  normalized.To.FundAccountName,
			SubAccountName:   normalized.To.SubAccountName,
			AccountType:      normalized.To.AccountType,
		},
		PayerName: normalized.PayerName,
		PayeeName: normalized.PayeeName,
		Fee: ProviderTransactionFeeInput{
			Amount:   normalized.FeeAmount,
			Currency: normalized.FeeCurrency,
		},
	}, nil
}

func airwallexCompanyAccountDisplay(account CompanyFundAccount) ProviderTransactionPartyDisplayInput {
	return ProviderTransactionPartyDisplayInput{
		AddressOrAccount: airwallexOptionalStringPointer(account.ProviderAccountKey),
		CompanyEntity:    airwallexOptionalStringPointer(account.CompanyEntity),
		FundAccountName:  airwallexOptionalStringPointer(account.FundAccountName),
		SubAccountName:   airwallexOptionalStringPointer(account.SubAccountName),
		AccountType:      airwallexOptionalStringPointer(account.AccountType),
	}
}

func airwallexCounterpartyDisplay(counterparty *AirwallexCounterparty) (ProviderTransactionPartyDisplayInput, *string) {
	if counterparty == nil {
		return ProviderTransactionPartyDisplayInput{}, nil
	}
	return ProviderTransactionPartyDisplayInput{
		AddressOrAccount: airwallexOptionalStringPointer(counterparty.AddressOrAccount),
		CompanyEntity:    airwallexOptionalStringPointer(counterparty.CompanyEntity),
		FundAccountName:  airwallexOptionalStringPointer(counterparty.FundAccountName),
		SubAccountName:   airwallexOptionalStringPointer(counterparty.SubAccountName),
		AccountType:      airwallexOptionalStringPointer(counterparty.AccountType),
	}, airwallexOptionalStringPointer(counterparty.Name)
}

func normalizedAirwallexFeeDisplayAmount(value *decimal.Decimal, sign AirwallexFinancialValueSign) (decimal.Decimal, error) {
	if value == nil {
		return decimal.Zero, fmt.Errorf("configured Airwallex fee display requires a fee value")
	}
	return normalizedAirwallexMovementMagnitude(parsedAirwallexFinancialTransactionNumbers{fee: value}, AirwallexFinancialAmountFieldFee, sign)
}

type airwallexConversionFact struct {
	fromCurrency string
	toCurrency   string
	rate         *decimal.Decimal
}

func buildAirwallexConversionFact(classification AirwallexFinancialTransactionClassification, numbers parsedAirwallexFinancialTransactionNumbers, source AirwallexConversionDetails) (airwallexConversionFact, error) {
	if classification.ClientRateUse == AirwallexFinancialClientRateUseNone {
		return airwallexConversionFact{}, nil
	}
	if classification.ClientRateUse != AirwallexFinancialClientRateUseConversionRate || numbers.clientRate == nil || !numbers.clientRate.IsPositive() {
		return airwallexConversionFact{}, fmt.Errorf("Airwallex conversion rate mapping requires a positive client_rate")
	}
	fromCurrency, err := normalizeAirwallexNormalizationRequired("Airwallex conversion from currency", source.FromCurrency, maxProviderFactCurrencyBytes)
	if err != nil {
		return airwallexConversionFact{}, err
	}
	toCurrency, err := normalizeAirwallexNormalizationRequired("Airwallex conversion to currency", source.ToCurrency, maxProviderFactCurrencyBytes)
	if err != nil {
		return airwallexConversionFact{}, err
	}
	rate := *numbers.clientRate
	if err := validateProviderFactDecimal("Airwallex conversion rate", &rate, true); err != nil {
		return airwallexConversionFact{}, err
	}
	return airwallexConversionFact{fromCurrency: strings.ToUpper(fromCurrency), toCurrency: strings.ToUpper(toCurrency), rate: &rate}, nil
}

func buildAirwallexFinancialTransactionFactExtras(
	transaction normalizedAirwallexFinancialTransaction,
	source AirwallexFinancialTransaction,
	schemaVersion string,
	eventVersion string,
	mappingVersion string,
) ([]byte, error) {
	numericValues := map[string]json.RawMessage{
		"amount": append(json.RawMessage(nil), source.Amount...),
	}
	if len(bytes.TrimSpace(source.Fee)) > 0 {
		numericValues["fee"] = append(json.RawMessage(nil), source.Fee...)
	}
	if len(bytes.TrimSpace(source.Net)) > 0 {
		numericValues["net"] = append(json.RawMessage(nil), source.Net...)
	}
	if len(bytes.TrimSpace(source.ClientRate)) > 0 {
		numericValues["client_rate"] = append(json.RawMessage(nil), source.ClientRate...)
	}
	value := struct {
		FinancialTransactionID string                     `json:"financial_transaction_id"`
		SchemaVersion          string                     `json:"normalization_schema_version"`
		EventVersion           string                     `json:"normalization_event_version"`
		MappingVersion         string                     `json:"normalization_mapping_version"`
		SourceID               string                     `json:"source_id,omitempty"`
		BatchID                string                     `json:"batch_id,omitempty"`
		FundingSourceID        string                     `json:"funding_source_id,omitempty"`
		SourceType             string                     `json:"source_type"`
		TransactionType        string                     `json:"transaction_type"`
		Status                 string                     `json:"status,omitempty"`
		CurrencyPair           string                     `json:"currency_pair,omitempty"`
		RawNumericValues       map[string]json.RawMessage `json:"raw_numeric_values"`
	}{
		FinancialTransactionID: transaction.providerID,
		SchemaVersion:          schemaVersion,
		EventVersion:           eventVersion,
		MappingVersion:         mappingVersion,
		SourceID:               transaction.sourceID,
		BatchID:                transaction.batchID,
		FundingSourceID:        transaction.fundingSourceID,
		SourceType:             transaction.sourceType,
		TransactionType:        transaction.transactionType,
		Status:                 transaction.status,
		CurrencyPair:           transaction.currencyPair,
		RawNumericValues:       numericValues,
	}
	extras, err := json.Marshal(value)
	if err != nil || len(extras) > maxProviderFactExtrasBytes {
		return nil, fmt.Errorf("marshal bounded Airwallex provider fact extras")
	}
	return extras, nil
}

func airwallexFinancialTransactionFactIdentity(schemaVersion, eventVersion, mappingVersion, providerEventID, financialTransactionID string) string {
	canonical := lengthDelimitedTuple([]string{
		"airwallex-financial-transaction",
		"v1",
		strings.TrimSpace(schemaVersion),
		strings.TrimSpace(eventVersion),
		strings.TrimSpace(mappingVersion),
		strings.TrimSpace(providerEventID),
		strings.TrimSpace(financialTransactionID),
	})
	return "airwallex-financial-transaction:v1:" + payloadSHA256Hex([]byte(canonical))
}

func airwallexAutomaticRiskInput(policy *AccountAssetPolicy, risk RiskAssessment) ProviderAutomaticRiskInput {
	autoExcluded := risk.AutomaticExclusion
	input := ProviderAutomaticRiskInput{AutoExcludedFromSummary: &autoExcluded}
	if policy != nil && policy.Dust.Enabled && policy.Dust.ID > 0 && policy.Dust.Threshold != nil {
		isDust := risk.IsDust
		policyID := policy.Dust.ID
		input.IsDust = &isDust
		input.DustPolicyID = &policyID
		input.DustThreshold = copyAirwallexDecimal(policy.Dust.Threshold)
	}
	if len(risk.Flags) > 0 {
		flags := append([]RiskFlag(nil), risk.Flags...)
		input.RiskFlags = &flags
	}
	return input
}

func (value AirwallexFinancialAmountField) valid() bool {
	return value == AirwallexFinancialAmountFieldAmount || value == AirwallexFinancialAmountFieldFee || value == AirwallexFinancialAmountFieldNet
}

func (value AirwallexFinancialValueSign) valid() bool {
	return value == AirwallexFinancialValueSignPositive || value == AirwallexFinancialValueSignNegative
}

func (value AirwallexFinancialOccurredAtField) valid() bool {
	return value == AirwallexFinancialOccurredAtCreated || value == AirwallexFinancialOccurredAtSettled
}

func airwallexOptionalStringPointer(value string) *string {
	normalized := strings.TrimSpace(value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func airwallexInt64Pointer(value int64) *int64 {
	copy := value
	return &copy
}

func copyAirwallexInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyAirwallexDecimal(value *decimal.Decimal) *decimal.Decimal {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
