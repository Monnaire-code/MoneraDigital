package companyfund

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// SafeheronLifecyclePolicy models the monotonic lifecycle required for
// out-of-order Safeheron Webhooks. Once a terminal status is stored, any later
// event retains it; field enrichment is handled independently by the merge
// lattice.
type SafeheronLifecyclePolicy struct{}

func (SafeheronLifecyclePolicy) Channel() Channel { return ChannelSafeheron }

func (SafeheronLifecyclePolicy) Transition(current, incoming LifecycleStatus) LifecycleDecision {
	current = normalizedLifecycleStatus(current)
	incoming = normalizedLifecycleStatus(incoming)
	if incoming == "" {
		return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "missing Safeheron lifecycle status"}
	}
	if _, ok := safeheronStatusRank(incoming); !ok {
		return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "unsupported Safeheron lifecycle status"}
	}
	if current == "" {
		return LifecycleDecision{Disposition: LifecycleDispositionApply, Status: incoming}
	}
	if _, ok := safeheronStatusRank(current); !ok {
		return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "stored Safeheron lifecycle status is unsupported"}
	}
	if current == incoming {
		return LifecycleDecision{Disposition: LifecycleDispositionKeep, Status: current, Reason: "same Safeheron lifecycle status"}
	}
	if isSafeheronTerminal(current) {
		return LifecycleDecision{Disposition: LifecycleDispositionKeep, Status: current, Reason: "Safeheron terminal state wins"}
	}
	currentRank, _ := safeheronStatusRank(current)
	incomingRank, _ := safeheronStatusRank(incoming)
	if incomingRank < currentRank {
		return LifecycleDecision{Disposition: LifecycleDispositionKeep, Status: current, Reason: "out-of-order Safeheron lifecycle state"}
	}
	if incomingRank == currentRank {
		return LifecycleDecision{Disposition: LifecycleDispositionKeep, Status: current, Reason: "equal-rank Safeheron lifecycle state"}
	}
	return LifecycleDecision{Disposition: LifecycleDispositionApply, Status: incoming}
}

func safeheronStatusRank(status LifecycleStatus) (int, bool) {
	switch status {
	case LifecycleStatusPending:
		return 0, true
	case LifecycleStatusSubmitted:
		return 1, true
	case LifecycleStatusSigning, LifecycleStatusProcessing:
		return 2, true
	case LifecycleStatusBroadcasting:
		return 3, true
	case LifecycleStatusConfirming:
		return 4, true
	case LifecycleStatusCompleted, LifecycleStatusFailed, LifecycleStatusCancelled, LifecycleStatusRejected:
		return 5, true
	default:
		return 0, false
	}
}

func isSafeheronTerminal(status LifecycleStatus) bool {
	switch status {
	case LifecycleStatusCompleted, LifecycleStatusFailed, LifecycleStatusCancelled, LifecycleStatusRejected:
		return true
	default:
		return false
	}
}

// AirwallexLifecyclePolicy is intentionally an explicit transition graph rather
// than a rank. Airwallex may make a documented non-monotonic lifecycle change,
// such as PAID -> FAILED, which must not be treated as a generic regression.
type AirwallexLifecyclePolicy struct{}

func (AirwallexLifecyclePolicy) Channel() Channel { return ChannelAirwallex }

func (AirwallexLifecyclePolicy) Transition(current, incoming LifecycleStatus) LifecycleDecision {
	current = normalizedLifecycleStatus(current)
	incoming = normalizedLifecycleStatus(incoming)
	if incoming == "" || !airwallexStatusKnown(incoming) {
		return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "unsupported Airwallex lifecycle status"}
	}
	if current == "" {
		return LifecycleDecision{Disposition: LifecycleDispositionApply, Status: incoming}
	}
	if !airwallexStatusKnown(current) {
		return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "stored Airwallex lifecycle status is unsupported"}
	}
	if current == incoming {
		return LifecycleDecision{Disposition: LifecycleDispositionKeep, Status: current, Reason: "same Airwallex lifecycle status"}
	}
	if airwallexTransitionAllowed(current, incoming) {
		return LifecycleDecision{Disposition: LifecycleDispositionApply, Status: incoming}
	}
	return LifecycleDecision{Disposition: LifecycleDispositionQuarantine, Status: current, Reason: "unsupported Airwallex lifecycle transition"}
}

func airwallexStatusKnown(status LifecycleStatus) bool {
	switch status {
	case LifecycleStatusPending, LifecycleStatusSubmitted, LifecycleStatusProcessing, LifecycleStatusPaid,
		LifecycleStatusFailed, LifecycleStatusCancelled, LifecycleStatusRejected, LifecycleStatusReversed:
		return true
	default:
		return false
	}
}

func airwallexTransitionAllowed(current, incoming LifecycleStatus) bool {
	allowed := map[LifecycleStatus]map[LifecycleStatus]bool{
		LifecycleStatusPending: {
			LifecycleStatusSubmitted:  true,
			LifecycleStatusProcessing: true,
			LifecycleStatusPaid:       true,
			LifecycleStatusFailed:     true,
			LifecycleStatusCancelled:  true,
		},
		LifecycleStatusSubmitted: {
			LifecycleStatusProcessing: true,
			LifecycleStatusPaid:       true,
			LifecycleStatusFailed:     true,
			LifecycleStatusCancelled:  true,
		},
		LifecycleStatusProcessing: {
			LifecycleStatusPaid:      true,
			LifecycleStatusFailed:    true,
			LifecycleStatusCancelled: true,
		},
		LifecycleStatusPaid: {
			LifecycleStatusFailed:   true,
			LifecycleStatusReversed: true,
		},
	}
	return allowed[current][incoming]
}

// LifecyclePolicyFor prevents cross-provider policy selection at the call site.
func LifecyclePolicyFor(channel Channel) (LifecyclePolicy, error) {
	switch channel {
	case ChannelSafeheron:
		return SafeheronLifecyclePolicy{}, nil
	case ChannelAirwallex:
		return AirwallexLifecyclePolicy{}, nil
	default:
		return nil, fmt.Errorf("no lifecycle policy for channel %q", channel)
	}
}

func normalizedLifecycleStatus(status LifecycleStatus) LifecycleStatus {
	return LifecycleStatus(strings.ToUpper(strings.TrimSpace(string(status))))
}

// ValidateMovementRelationship validates direction, account association and
// explicit linked-movement semantics before any persistence work happens.
func ValidateMovementRelationship(relation MovementRelation) error {
	if !relation.MovementKind.Valid() {
		return fmt.Errorf("unsupported movement kind %q", relation.MovementKind)
	}
	if !relation.TransferMode.Valid() {
		return fmt.Errorf("unsupported transfer mode %q", relation.TransferMode)
	}
	if !relation.Direction.Valid() {
		return fmt.Errorf("unsupported transaction direction %q", relation.Direction)
	}
	if !relation.HasFromAccount && !relation.HasToAccount {
		return fmt.Errorf("movement requires at least one configured company account")
	}
	switch relation.Direction {
	case DirectionInflow:
		if !relation.HasToAccount {
			return fmt.Errorf("inflow requires a configured destination account")
		}
	case DirectionOutflow:
		if !relation.HasFromAccount {
			return fmt.Errorf("outflow requires a configured source account")
		}
	case DirectionInternalTransfer:
		if !relation.HasFromAccount || !relation.HasToAccount {
			return fmt.Errorf("internal transfer requires both configured company accounts")
		}
	}

	switch relation.MovementKind {
	case MovementKindFee:
		if strings.TrimSpace(relation.ParentMovementKey) == "" {
			return fmt.Errorf("fee movement requires a parent movement")
		}
	case MovementKindReversal:
		if strings.TrimSpace(relation.ReversalOfMovementKey) == "" {
			return fmt.Errorf("reversal movement requires an original movement")
		}
	case MovementKindConversion:
		if strings.TrimSpace(relation.ConversionGroupKey) == "" {
			return fmt.Errorf("conversion movement requires a conversion group")
		}
		if !relation.ConversionLeg.Valid() {
			return fmt.Errorf("conversion movement requires a buy or sell leg")
		}
		if !relation.ConversionGroupState.Valid() {
			return fmt.Errorf("conversion movement requires an explicit group state")
		}
	}
	return nil
}

// MergeProviderFields applies the non-lifecycle provider field-level lattice.
// Manual fields and lifecycle Status are intentionally excluded: callers must
// use MergeMovementProviderFieldsForChannel to apply a provider lifecycle
// policy before any status is created, advanced, retained, or quarantined.
func MergeProviderFields(existing, incoming ProviderOwnedFields) (ProviderOwnedFields, MergeDecision) {
	comparison := compareProviderMetadata(existing.Metadata, incoming.Metadata)
	if comparison == 0 && hasEqualPriorityMoneyConflict(existing, incoming) {
		return existing, MergeDecision{Outcome: MergeOutcomeQuarantine, Reason: "equal-priority provider money conflict"}
	}

	merged := existing
	changed := false
	metadataWon := false

	merged.Amount, changed = mergeDecimalField(merged.Amount, incoming.Amount, comparison, changed)
	merged.Currency, changed = mergeStringField(merged.Currency, incoming.Currency, comparison, changed)
	merged.Asset, changed = mergeAssetField(merged.Asset, incoming.Asset, comparison, changed)
	merged.TxHash, changed = mergeStringField(merged.TxHash, incoming.TxHash, comparison, changed)
	merged.OccurredAt, changed = mergeTimeField(merged.OccurredAt, incoming.OccurredAt, comparison, changed)
	merged.CompletedAt, changed = mergeTimeField(merged.CompletedAt, incoming.CompletedAt, comparison, changed)
	if comparison > 0 {
		metadataWon = true
		changed = true
	}
	if metadataWon || isProviderMetadataEmpty(existing.Metadata) {
		merged.Metadata = incoming.Metadata
	}

	if changed {
		return merged, MergeDecision{Outcome: MergeOutcomeApplied}
	}
	return merged, MergeDecision{Outcome: MergeOutcomeUnchanged}
}

// MergeMovementProviderFields preserves every manual field byte-for-byte while
// merging only the provider-owned subset.
func MergeMovementProviderFields(existing MovementState, incoming ProviderOwnedFields) (MovementState, MergeDecision) {
	provider, decision := MergeProviderFields(existing.Provider, incoming)
	if decision.Outcome == MergeOutcomeQuarantine {
		return existing, decision
	}
	existing.Provider = provider
	return existing, decision
}

// MergeMovementProviderFieldsForChannel is the persistence-facing merge entry
// point. It applies the provider lifecycle policy before the generic field
// lattice, so a newer Safeheron fact can enrich a terminal movement without
// regressing its terminal status. New repository code must use this function
// rather than calling MergeMovementProviderFields directly.
func MergeMovementProviderFieldsForChannel(channel Channel, existing MovementState, incoming ProviderOwnedFields) (MovementState, MergeDecision) {
	policy, err := LifecyclePolicyFor(channel)
	if err != nil {
		return existing, MergeDecision{Outcome: MergeOutcomeQuarantine, Reason: err.Error()}
	}
	if incoming.Status == nil {
		return MergeMovementProviderFields(existing, incoming)
	}

	currentStatus := LifecycleStatus("")
	if existing.Provider.Status != nil {
		currentStatus = *existing.Provider.Status
	}
	lifecycle := policy.Transition(currentStatus, *incoming.Status)
	switch lifecycle.Disposition {
	case LifecycleDispositionQuarantine:
		return existing, MergeDecision{Outcome: MergeOutcomeQuarantine, Reason: lifecycle.Reason}
	}

	// The lifecycle policy owns status selection. The generic lattice receives
	// only non-status fields, so an older metadata revision cannot suppress a
	// provider-approved lifecycle advance, and a terminal KEEP cannot regress.
	incoming.Status = nil
	merged, fieldDecision := MergeMovementProviderFields(existing, incoming)
	if fieldDecision.Outcome == MergeOutcomeQuarantine {
		return existing, fieldDecision
	}
	if lifecycle.Disposition != LifecycleDispositionApply {
		return merged, fieldDecision
	}

	nextStatus := lifecycle.Status
	if merged.Provider.Status != nil && *merged.Provider.Status == nextStatus {
		return merged, fieldDecision
	}
	merged.Provider.Status = &nextStatus
	// Lifecycle status is provider-owned as well. A status-only transition can
	// carry the newest provider revision/timestamp even when it has no other
	// provider field to change, so promote that ordering metadata separately.
	// Conversely, an allowed lifecycle advance from an older fact must not
	// regress the stored provider ordering metadata.
	if compareProviderMetadata(existing.Provider.Metadata, incoming.Metadata) > 0 {
		merged.Provider.Metadata = incoming.Metadata
	}
	if fieldDecision.Outcome == MergeOutcomeUnchanged {
		fieldDecision.Outcome = MergeOutcomeApplied
	}
	return merged, fieldDecision
}

func compareProviderMetadata(existing, incoming ProviderFactMetadata) int {
	if revisionComparison, bothRevisionsPresent := comparePresentInt64(incoming.Revision, existing.Revision); bothRevisionsPresent {
		if revisionComparison != 0 {
			return revisionComparison
		}
		// Equal explicit revisions make source priority the tie-break when one
		// provider surface omitted its timestamp. A missing timestamp is not a
		// lower fact than a present timestamp for the same revision.
		if timestampComparison, bothTimestampsPresent := comparePresentTime(incoming.UpdatedAt, existing.UpdatedAt); bothTimestampsPresent && timestampComparison != 0 {
			return timestampComparison
		}
	} else {
		if comparison, comparable := comparePresentTime(incoming.UpdatedAt, existing.UpdatedAt); comparable && comparison != 0 {
			return comparison
		}
		// A timestamp remains a valid ordering signal when revisions are omitted
		// by one provider surface; it must not be ignored merely because the
		// other surface happened to expose a revision number.
		if comparison, comparable := compareOptionalTime(incoming.UpdatedAt, existing.UpdatedAt); comparable && comparison != 0 {
			return comparison
		}
		if comparison, comparable := compareOptionalInt64(incoming.Revision, existing.Revision); comparable && comparison != 0 {
			return comparison
		}
	}
	incomingPriority := providerSourcePriority(incoming.Source)
	existingPriority := providerSourcePriority(existing.Source)
	switch {
	case incomingPriority > existingPriority:
		return 1
	case incomingPriority < existingPriority:
		return -1
	default:
		return 0
	}
}

func comparePresentInt64(incoming, existing *int64) (int, bool) {
	if incoming == nil || existing == nil {
		return 0, false
	}
	switch {
	case *incoming > *existing:
		return 1, true
	case *incoming < *existing:
		return -1, true
	default:
		return 0, true
	}
}

func comparePresentTime(incoming, existing *time.Time) (int, bool) {
	if incoming == nil || existing == nil {
		return 0, false
	}
	switch {
	case incoming.After(*existing):
		return 1, true
	case incoming.Before(*existing):
		return -1, true
	default:
		return 0, true
	}
}

// compareOptionalInt64 returns comparison from incoming's point of view and
// whether this ordering dimension was available.
func compareOptionalInt64(incoming, existing *int64) (int, bool) {
	switch {
	case incoming == nil && existing == nil:
		return 0, false
	case incoming == nil:
		return -1, true
	case existing == nil:
		return 1, true
	case *incoming > *existing:
		return 1, true
	case *incoming < *existing:
		return -1, true
	default:
		return 0, false
	}
}

func compareOptionalTime(incoming, existing *time.Time) (int, bool) {
	switch {
	case incoming == nil && existing == nil:
		return 0, false
	case incoming == nil:
		return -1, true
	case existing == nil:
		return 1, true
	case incoming.After(*existing):
		return 1, true
	case incoming.Before(*existing):
		return -1, true
	default:
		return 0, false
	}
}

func providerSourcePriority(source ProviderFactSource) int {
	switch source {
	case ProviderSourceReconciliation:
		return 3
	case ProviderSourceProductDetail:
		return 2
	case ProviderSourceWebhook:
		return 1
	default:
		return 0
	}
}

func hasEqualPriorityMoneyConflict(existing, incoming ProviderOwnedFields) bool {
	if existing.Amount != nil && incoming.Amount != nil && !existing.Amount.Equal(*incoming.Amount) {
		return true
	}
	if existing.Currency != nil && incoming.Currency != nil && !strings.EqualFold(strings.TrimSpace(*existing.Currency), strings.TrimSpace(*incoming.Currency)) {
		return true
	}
	if existing.Asset != nil && incoming.Asset != nil && normalizeAssetIdentity(*existing.Asset).canonicalKey() != normalizeAssetIdentity(*incoming.Asset).canonicalKey() {
		return true
	}
	return false
}

func mergeDecimalField(existing, incoming *decimal.Decimal, comparison int, changed bool) (*decimal.Decimal, bool) {
	if incoming == nil {
		return existing, changed
	}
	if existing == nil || comparison > 0 {
		return incoming, true
	}
	return existing, changed
}

func mergeStringField(existing, incoming *string, comparison int, changed bool) (*string, bool) {
	if incoming == nil {
		return existing, changed
	}
	if existing == nil || comparison > 0 {
		return incoming, true
	}
	return existing, changed
}

func mergeAssetField(existing, incoming *AssetIdentity, comparison int, changed bool) (*AssetIdentity, bool) {
	if incoming == nil {
		return existing, changed
	}
	if existing == nil || comparison > 0 {
		return incoming, true
	}
	return existing, changed
}

func mergeTimeField(existing, incoming *time.Time, comparison int, changed bool) (*time.Time, bool) {
	if incoming == nil {
		return existing, changed
	}
	if existing == nil || comparison > 0 {
		return incoming, true
	}
	return existing, changed
}

func isProviderMetadataEmpty(metadata ProviderFactMetadata) bool {
	return metadata.Revision == nil && metadata.UpdatedAt == nil && metadata.Source == ""
}
