package companyfund

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"monera-digital/internal/safeheron"
)

// SafeheronWebhookExclusionReason records why a verified Safeheron raw event
// is outside the company-fund ledger. Permanent reasons stay excluded forever;
// configuration-dependent reasons are tied to a stable settings fingerprint.
// Neither form modifies deposit processing state.
type SafeheronWebhookExclusionReason string

const (
	SafeheronWebhookExclusionNonTransactionStatus SafeheronWebhookExclusionReason = "NON_TRANSACTION_STATUS"
	SafeheronWebhookExclusionInvalidPayload       SafeheronWebhookExclusionReason = "INVALID_PAYLOAD"
	SafeheronWebhookExclusionEventTypeMismatch    SafeheronWebhookExclusionReason = "EVENT_TYPE_MISMATCH"
	SafeheronWebhookExclusionUnmappedAsset        SafeheronWebhookExclusionReason = "UNMAPPED_ASSET"
	SafeheronWebhookExclusionNoConfiguredAddress  SafeheronWebhookExclusionReason = "NO_CONFIGURED_ADDRESS"
)

const (
	maxSafeheronWebhookExclusionReasonBytes       = 64
	safeheronWebhookEligibilityFingerprintVersion = "safeheron-webhook-eligibility-config:v1"
)

// SafeheronWebhookEligibilityInput is created only after the Safeheron
// signature verifier and raw-event recorder have completed. RawPayload stays
// in memory for classification and must never be logged by implementations.
type SafeheronWebhookEligibilityInput struct {
	SafeheronWebhookEventID int
	EventType               string
	PayloadDigest           string
	RawPayload              []byte
}

func (input SafeheronWebhookEligibilityInput) validate() error {
	if input.SafeheronWebhookEventID <= 0 || strings.TrimSpace(input.EventType) == "" ||
		input.EventType != strings.TrimSpace(input.EventType) || !isLowerSHA256Hex(input.PayloadDigest) {
		return fmt.Errorf("invalid Safeheron webhook eligibility input")
	}
	return nil
}

// SafeheronWebhookEligibilityDecision says whether one verified raw event is
// eligible to create a company-fund provider event. A false decision is
// persisted as a negative marker before it is returned.
type SafeheronWebhookEligibilityDecision struct {
	Candidate bool
}

// SafeheronWebhookEligibility is shared by the synchronous webhook bridge and
// the bounded compensator. Implementations must fail closed: only a true
// decision may create a company-fund provider-event reference.
type SafeheronWebhookEligibility interface {
	AssessAndRecord(ctx context.Context, input SafeheronWebhookEligibilityInput) (SafeheronWebhookEligibilityDecision, error)
}

// SafeheronWebhookCandidateEvaluation is the pure eligibility result before a
// durable negative marker is written.
type SafeheronWebhookCandidateEvaluation struct {
	Candidate                bool
	ExclusionReason          SafeheronWebhookExclusionReason
	ConfigurationFingerprint string
}

func (evaluation SafeheronWebhookCandidateEvaluation) validate() error {
	if evaluation.Candidate {
		if evaluation.ExclusionReason != "" || evaluation.ConfigurationFingerprint != "" {
			return fmt.Errorf("eligible Safeheron webhook candidate must not have exclusion metadata")
		}
		return nil
	}
	if err := validateSafeheronWebhookExclusionReason(evaluation.ExclusionReason); err != nil {
		return err
	}
	return validateSafeheronWebhookExclusionConfigurationFingerprint(
		evaluation.ExclusionReason,
		evaluation.ConfigurationFingerprint,
	)
}

// SafeheronWebhookCandidateEvaluator decides whether a verified raw payload
// belongs to a configured company Safeheron wallet. It has no database write
// and must never infer a chain from a ticker or address string.
type SafeheronWebhookCandidateEvaluator interface {
	EvaluateSafeheronWebhookCandidate(ctx context.Context, eventType string, rawPayload []byte) (SafeheronWebhookCandidateEvaluation, error)
}

// SafeheronWebhookRawEventExclusionInput identifies a durable negative marker
// by both the deposit-owned raw-event ID and its immutable SHA-256 digest.
type SafeheronWebhookRawEventExclusionInput struct {
	SafeheronWebhookEventID  int
	PayloadDigest            string
	Reason                   SafeheronWebhookExclusionReason
	ConfigurationFingerprint string
}

func (input SafeheronWebhookRawEventExclusionInput) validate() error {
	if input.SafeheronWebhookEventID <= 0 || !isLowerSHA256Hex(input.PayloadDigest) {
		return fmt.Errorf("invalid Safeheron webhook exclusion input")
	}
	if err := validateSafeheronWebhookExclusionReason(input.Reason); err != nil {
		return err
	}
	return validateSafeheronWebhookExclusionConfigurationFingerprint(input.Reason, input.ConfigurationFingerprint)
}

func validateSafeheronWebhookExclusionReason(reason SafeheronWebhookExclusionReason) error {
	if reason == "" || len(reason) > maxSafeheronWebhookExclusionReasonBytes || string(reason) != strings.TrimSpace(string(reason)) {
		return fmt.Errorf("invalid Safeheron webhook exclusion reason")
	}
	switch reason {
	case SafeheronWebhookExclusionNonTransactionStatus,
		SafeheronWebhookExclusionInvalidPayload,
		SafeheronWebhookExclusionEventTypeMismatch,
		SafeheronWebhookExclusionUnmappedAsset,
		SafeheronWebhookExclusionNoConfiguredAddress:
		return nil
	default:
		return fmt.Errorf("unsupported Safeheron webhook exclusion reason")
	}
}

func safeheronWebhookExclusionReasonUsesConfigurationFingerprint(reason SafeheronWebhookExclusionReason) bool {
	switch reason {
	case SafeheronWebhookExclusionUnmappedAsset, SafeheronWebhookExclusionNoConfiguredAddress:
		return true
	default:
		return false
	}
}

func validateSafeheronWebhookExclusionConfigurationFingerprint(
	reason SafeheronWebhookExclusionReason,
	fingerprint string,
) error {
	if safeheronWebhookExclusionReasonUsesConfigurationFingerprint(reason) {
		if !isLowerSHA256Hex(fingerprint) {
			return fmt.Errorf("configuration-dependent Safeheron webhook exclusion requires a SHA-256 configuration fingerprint")
		}
		return nil
	}
	if fingerprint != "" {
		return fmt.Errorf("permanent Safeheron webhook exclusion must not have a configuration fingerprint")
	}
	return nil
}

// SafeheronWebhookEligibilityFingerprintProvider resolves the current stable
// configuration content fingerprint on every collector query. It intentionally
// does not expose snapshot LoadedAt: ordinary minute refreshes with identical
// settings must keep excluding already-classified customer events.
type SafeheronWebhookEligibilityFingerprintProvider interface {
	CurrentSafeheronWebhookEligibilityFingerprint() (string, error)
}

// CurrentSafeheronWebhookEligibilityFingerprint makes AccountRegistry the
// dynamic fingerprint source for the Safeheron raw-event compensator.
func (registry *AccountRegistry) CurrentSafeheronWebhookEligibilityFingerprint() (string, error) {
	if registry == nil {
		return "", fmt.Errorf("Safeheron webhook eligibility account registry is not configured")
	}
	return safeheronWebhookEligibilityConfigurationFingerprint(registry.Snapshot())
}

// safeheronWebhookEligibilityConfigurationFingerprint hashes only the enabled
// Safeheron configuration that can influence raw-event eligibility: normalized
// address/network/provider-account identities and their enabled asset mapping
// policies. The canonical content is sorted and excludes LoadedAt, so a cache
// refresh with the same settings produces the same value.
func safeheronWebhookEligibilityConfigurationFingerprint(snapshot *AccountRegistrySnapshot) (string, error) {
	if snapshot == nil {
		return "", fmt.Errorf("Safeheron webhook eligibility account registry snapshot is unavailable")
	}

	accountKeys := make(map[int64]string)
	records := make([]string, 0)
	for _, account := range snapshot.Accounts() {
		if account.Channel != ChannelSafeheron || !account.Enabled {
			continue
		}
		address := account.NormalizedAddress
		if strings.TrimSpace(address) == "" {
			address = account.WalletAddress
		}
		addressKey := safeheronAddressKey(account.NetworkFamily, address)
		providerAccountKey := strings.TrimSpace(account.ProviderAccountKey)
		if addressKey == "" || providerAccountKey != account.ProviderAccountKey {
			return "", fmt.Errorf("invalid enabled Safeheron account in webhook eligibility fingerprint")
		}
		accountKey := lengthDelimitedTuple([]string{addressKey, providerAccountKey})
		accountKeys[account.ID] = accountKey
		records = append(records, lengthDelimitedTuple([]string{"account", accountKey}))
	}
	for _, policy := range snapshot.AssetPolicies() {
		accountKey, found := accountKeys[policy.AccountID]
		if !found || !policy.Enabled {
			continue
		}
		asset := normalizeAssetIdentity(policy.Asset)
		asset.ContractAddress = normalizeAssetContract(asset.ContractAddress)
		records = append(records, lengthDelimitedTuple([]string{
			"policy",
			accountKey,
			asset.canonicalKey(),
		}))
	}
	sort.Strings(records)

	canonical := lengthDelimitedTuple(append([]string{safeheronWebhookEligibilityFingerprintVersion}, records...))
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

// SafeheronWebhookRawEventExclusionStore persists a negative eligibility
// marker. The implementation must verify the referenced raw-event digest so a
// marker can never hide a differently addressed source row.
type SafeheronWebhookRawEventExclusionStore interface {
	RecordSafeheronWebhookRawEventExclusion(ctx context.Context, input SafeheronWebhookRawEventExclusionInput) error
}

// SafeheronWebhookEligibilityService coordinates fail-closed classification
// with durable negative markers. Positive candidates intentionally do not get
// a marker: a bridge failure remains visible to the anti-join collector.
type SafeheronWebhookEligibilityService struct {
	evaluator  SafeheronWebhookCandidateEvaluator
	exclusions SafeheronWebhookRawEventExclusionStore
}

func NewSafeheronWebhookEligibilityService(
	evaluator SafeheronWebhookCandidateEvaluator,
	exclusions SafeheronWebhookRawEventExclusionStore,
) (*SafeheronWebhookEligibilityService, error) {
	if evaluator == nil {
		return nil, fmt.Errorf("Safeheron webhook candidate evaluator is required")
	}
	if exclusions == nil {
		return nil, fmt.Errorf("Safeheron webhook exclusion store is required")
	}
	return &SafeheronWebhookEligibilityService{evaluator: evaluator, exclusions: exclusions}, nil
}

func (service *SafeheronWebhookEligibilityService) AssessAndRecord(
	ctx context.Context,
	input SafeheronWebhookEligibilityInput,
) (SafeheronWebhookEligibilityDecision, error) {
	if service == nil || service.evaluator == nil || service.exclusions == nil {
		return SafeheronWebhookEligibilityDecision{}, fmt.Errorf("Safeheron webhook eligibility service is not configured")
	}
	if err := input.validate(); err != nil {
		return SafeheronWebhookEligibilityDecision{}, err
	}
	if err := ctx.Err(); err != nil {
		return SafeheronWebhookEligibilityDecision{}, err
	}
	evaluation, err := service.evaluator.EvaluateSafeheronWebhookCandidate(ctx, input.EventType, input.RawPayload)
	if err != nil {
		return SafeheronWebhookEligibilityDecision{}, err
	}
	if err := evaluation.validate(); err != nil {
		return SafeheronWebhookEligibilityDecision{}, err
	}
	if evaluation.Candidate {
		return SafeheronWebhookEligibilityDecision{Candidate: true}, nil
	}
	if err := service.exclusions.RecordSafeheronWebhookRawEventExclusion(ctx, SafeheronWebhookRawEventExclusionInput{
		SafeheronWebhookEventID:  input.SafeheronWebhookEventID,
		PayloadDigest:            input.PayloadDigest,
		Reason:                   evaluation.ExclusionReason,
		ConfigurationFingerprint: evaluation.ConfigurationFingerprint,
	}); err != nil {
		return SafeheronWebhookEligibilityDecision{}, err
	}
	return SafeheronWebhookEligibilityDecision{}, nil
}

// RegistrySafeheronWebhookCandidateEvaluator checks a transaction-status
// delivery against one current immutable account-registry snapshot. It first
// resolves the Safeheron coin key through explicit configured asset policies,
// then looks up source and every destination address under that exact network.
type RegistrySafeheronWebhookCandidateEvaluator struct {
	registries SafeheronRegistrySnapshotProvider
}

func NewRegistrySafeheronWebhookCandidateEvaluator(
	registries SafeheronRegistrySnapshotProvider,
) (*RegistrySafeheronWebhookCandidateEvaluator, error) {
	if registries == nil {
		return nil, fmt.Errorf("Safeheron webhook account registry snapshot provider is required")
	}
	return &RegistrySafeheronWebhookCandidateEvaluator{registries: registries}, nil
}

func (evaluator *RegistrySafeheronWebhookCandidateEvaluator) EvaluateSafeheronWebhookCandidate(
	ctx context.Context,
	eventType string,
	rawPayload []byte,
) (SafeheronWebhookCandidateEvaluation, error) {
	if evaluator == nil || evaluator.registries == nil {
		return SafeheronWebhookCandidateEvaluation{}, fmt.Errorf("Safeheron webhook candidate evaluator is not configured")
	}
	if err := ctx.Err(); err != nil {
		return SafeheronWebhookCandidateEvaluation{}, err
	}
	eventType = strings.TrimSpace(eventType)
	if eventType != safeheronTransactionStatusChangedEventType {
		return SafeheronWebhookCandidateEvaluation{ExclusionReason: SafeheronWebhookExclusionNonTransactionStatus}, nil
	}
	envelope, err := parseSafeheronProviderEventEnvelope(rawPayload)
	if err != nil {
		return SafeheronWebhookCandidateEvaluation{ExclusionReason: SafeheronWebhookExclusionInvalidPayload}, nil
	}
	if envelope.EventType != eventType {
		return SafeheronWebhookCandidateEvaluation{ExclusionReason: SafeheronWebhookExclusionEventTypeMismatch}, nil
	}
	var snapshot safeheron.TransactionSnapshot
	if json.Unmarshal(envelope.EventDetail, &snapshot) != nil || strings.TrimSpace(snapshot.TxKey) == "" || strings.TrimSpace(snapshot.CoinKey) == "" {
		return SafeheronWebhookCandidateEvaluation{ExclusionReason: SafeheronWebhookExclusionInvalidPayload}, nil
	}
	registry := evaluator.registries.Snapshot()
	if registry == nil {
		return SafeheronWebhookCandidateEvaluation{}, fmt.Errorf("Safeheron webhook account registry snapshot is unavailable")
	}
	configurationFingerprint, err := safeheronWebhookEligibilityConfigurationFingerprint(registry)
	if err != nil {
		return SafeheronWebhookCandidateEvaluation{}, err
	}
	mapping, err := safeheronWebhookTransactionMapping(registry, snapshot)
	if err != nil {
		return SafeheronWebhookCandidateEvaluation{
			ExclusionReason:          SafeheronWebhookExclusionUnmappedAsset,
			ConfigurationFingerprint: configurationFingerprint,
		}, nil
	}
	for _, address := range safeheronWebhookTransactionAddresses(snapshot) {
		if _, found := registry.LookupSafeheron(mapping.NetworkFamily, address); found {
			return SafeheronWebhookCandidateEvaluation{Candidate: true}, nil
		}
	}
	return SafeheronWebhookCandidateEvaluation{
		ExclusionReason:          SafeheronWebhookExclusionNoConfiguredAddress,
		ConfigurationFingerprint: configurationFingerprint,
	}, nil
}

func safeheronWebhookTransactionMapping(
	registry *AccountRegistrySnapshot,
	snapshot safeheron.TransactionSnapshot,
) (SafeheronTransactionMapping, error) {
	networkFamily, principal, err := registrySafeheronAssetMapping(registry, snapshot.CoinKey)
	if err != nil {
		return SafeheronTransactionMapping{}, err
	}
	mapping := SafeheronTransactionMapping{NetworkFamily: networkFamily, PrincipalAsset: principal}
	if strings.TrimSpace(snapshot.FeeCoinKey) == "" {
		return mapping, nil
	}
	feeNetwork, fee, err := registrySafeheronAssetMapping(registry, snapshot.FeeCoinKey)
	if err != nil {
		return SafeheronTransactionMapping{}, err
	}
	if feeNetwork != networkFamily {
		return SafeheronTransactionMapping{}, fmt.Errorf("Safeheron fee asset network family conflicts with principal asset")
	}
	mapping.FeeAsset = &fee
	return mapping, nil
}

func safeheronWebhookTransactionAddresses(snapshot safeheron.TransactionSnapshot) []string {
	addresses := make([]string, 0, 2+len(snapshot.SourceAddressList)+len(snapshot.DestinationAddressList))
	appendAddress := func(address string) {
		if address = strings.TrimSpace(address); address != "" {
			addresses = append(addresses, address)
		}
	}
	appendAddress(snapshot.SourceAddress)
	for _, source := range snapshot.SourceAddressList {
		appendAddress(source.Address)
	}
	appendAddress(snapshot.DestinationAddress)
	for _, destination := range snapshot.DestinationAddressList {
		appendAddress(destination.Address)
	}
	return addresses
}

var _ SafeheronWebhookEligibility = (*SafeheronWebhookEligibilityService)(nil)
var _ SafeheronWebhookCandidateEvaluator = (*RegistrySafeheronWebhookCandidateEvaluator)(nil)
