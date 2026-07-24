package companyfund

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestSafeheronLifecycle_TerminalStateNeverRegresses(t *testing.T) {
	policy := SafeheronLifecyclePolicy{}
	decision := policy.Transition(LifecycleStatusCompleted, LifecycleStatusConfirming)
	if decision.Disposition != LifecycleDispositionKeep || decision.Status != LifecycleStatusCompleted {
		t.Fatalf("COMPLETED -> CONFIRMING = %#v, want terminal state retained", decision)
	}

	decision = policy.Transition(LifecycleStatusFailed, LifecycleStatusPending)
	if decision.Disposition != LifecycleDispositionKeep || decision.Status != LifecycleStatusFailed {
		t.Fatalf("FAILED -> PENDING = %#v, want terminal state retained", decision)
	}

	decision = policy.Transition(LifecycleStatusPending, LifecycleStatusConfirming)
	if decision.Disposition != LifecycleDispositionApply || decision.Status != LifecycleStatusConfirming {
		t.Fatalf("PENDING -> CONFIRMING = %#v, want apply", decision)
	}
	decision = policy.Transition(LifecycleStatusSigning, LifecycleStatusBroadcasting)
	if decision.Disposition != LifecycleDispositionApply || decision.Status != LifecycleStatusBroadcasting {
		t.Fatalf("SIGNING -> BROADCASTING = %#v, want apply", decision)
	}
	decision = policy.Transition(LifecycleStatusSubmitted, LifecycleStatusSigning)
	if decision.Disposition != LifecycleDispositionApply || decision.Status != LifecycleStatusSigning {
		t.Fatalf("SUBMITTED -> SIGNING = %#v, want apply", decision)
	}
}

func TestAirwallexLifecycle_UsesExplicitTransitions(t *testing.T) {
	policy := AirwallexLifecyclePolicy{}
	if decision := policy.Transition(LifecycleStatusPaid, LifecycleStatusFailed); decision.Disposition != LifecycleDispositionApply {
		t.Fatalf("PAID -> FAILED must be explicitly allowed, got %#v", decision)
	}
	if decision := policy.Transition(LifecycleStatusFailed, LifecycleStatusPaid); decision.Disposition != LifecycleDispositionQuarantine {
		t.Fatalf("FAILED -> PAID must be quarantined, got %#v", decision)
	}
	if _, err := LifecyclePolicyFor(ChannelSafeheron); err != nil {
		t.Fatalf("LifecyclePolicyFor(SAFEHERON): %v", err)
	}
	if _, err := LifecyclePolicyFor(TransactionSource("OTHER")); err == nil {
		t.Fatal("unknown channel must not select a lifecycle policy")
	}
}

func TestValidateMovementRelationship(t *testing.T) {
	validPrincipal := MovementRelation{
		MovementKind: MovementKindPrincipal,
		TransferMode: TransferModeSingle,
		Direction:    DirectionInflow,
		HasToAccount: true,
	}
	if err := ValidateMovementRelationship(validPrincipal); err != nil {
		t.Fatalf("valid principal: %v", err)
	}

	if err := ValidateMovementRelationship(MovementRelation{
		MovementKind:   MovementKindFee,
		TransferMode:   TransferModeSingle,
		Direction:      DirectionOutflow,
		HasFromAccount: true,
	}); err == nil {
		t.Fatal("fee without parent must be rejected")
	}

	if err := ValidateMovementRelationship(MovementRelation{
		MovementKind:          MovementKindReversal,
		TransferMode:          TransferModeSingle,
		Direction:             DirectionInflow,
		HasToAccount:          true,
		ReversalOfMovementKey: "original-movement",
	}); err != nil {
		t.Fatalf("linked reversal: %v", err)
	}

	if err := ValidateMovementRelationship(MovementRelation{
		MovementKind:         MovementKindConversion,
		TransferMode:         TransferModeSingle,
		Direction:            DirectionOutflow,
		HasFromAccount:       true,
		ConversionGroupKey:   "group-1",
		ConversionLeg:        ConversionLegSell,
		ConversionGroupState: ConversionGroupIncomplete,
	}); err != nil {
		t.Fatalf("observed incomplete conversion leg: %v", err)
	}
}

func TestMergeProviderFields_PreservesManualFieldsAndQuarantinesEqualPriorityMoneyConflict(t *testing.T) {
	revision := int64(7)
	updatedAt := time.Date(2026, time.July, 10, 3, 0, 0, 0, time.UTC)
	amount := decimal.RequireFromString("10")
	existing := MovementState{
		Manual: ManualFields{
			FinanceCategoryLevel1ID:  101,
			SummaryInclusionOverride: boolPointer(true),
			RiskOverrideReason:       "finance-reviewed",
		},
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &revision, UpdatedAt: &updatedAt, Source: ProviderSourceWebhook},
			Amount:   &amount,
			Currency: stringPointer("USDT"),
			TxHash:   stringPointer("0xknown"),
		},
	}

	incomingNil := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &revision, UpdatedAt: &updatedAt, Source: ProviderSourceProductDetail},
		Amount:   nil,
		Currency: nil,
		TxHash:   nil,
	}
	merged, decision := MergeMovementProviderFields(existing, incomingNil)
	if decision.Outcome == MergeOutcomeQuarantine || merged.Provider.Amount == nil || !merged.Provider.Amount.Equal(amount) {
		t.Fatalf("null provider fields must not erase known money: %#v %#v", merged, decision)
	}
	if merged.Manual.FinanceCategoryLevel1ID != 101 || merged.Manual.SummaryInclusionOverride == nil || !*merged.Manual.SummaryInclusionOverride {
		t.Fatalf("provider merge overwrote manual fields: %#v", merged.Manual)
	}

	conflicting := incomingNil
	conflicting.Metadata.Source = ProviderSourceWebhook
	conflicting.Amount = decimalPointer("11")
	_, decision = MergeMovementProviderFields(existing, conflicting)
	if decision.Outcome != MergeOutcomeQuarantine {
		t.Fatalf("equal revision/priority conflicting amount must quarantine, got %#v", decision)
	}

	detail := conflicting
	detail.Metadata.Source = ProviderSourceProductDetail
	merged, decision = MergeMovementProviderFields(existing, detail)
	if decision.Outcome != MergeOutcomeApplied || merged.Provider.Amount == nil || !merged.Provider.Amount.Equal(decimal.RequireFromString("11")) {
		t.Fatalf("product detail must outrank equal-revision webhook, got %#v %#v", merged, decision)
	}

	newRevision := int64(8)
	newer := conflicting
	newer.Metadata.Revision = &newRevision
	merged, decision = MergeMovementProviderFields(existing, newer)
	if decision.Outcome != MergeOutcomeApplied || merged.Provider.Amount == nil || !merged.Provider.Amount.Equal(decimal.RequireFromString("11")) {
		t.Fatalf("newer revision must win, got %#v %#v", merged, decision)
	}
}

func TestMergeMovementProviderFieldsForChannel_SafeheronTerminalStatusWinsNewerRevision(t *testing.T) {
	oldRevision := int64(1)
	newRevision := int64(2)
	oldTime := time.Date(2026, time.July, 10, 1, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Minute)
	existing := MovementState{
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &oldRevision, UpdatedAt: &oldTime, Source: ProviderSourceWebhook},
			Status:   lifecycleStatusPointer(LifecycleStatusCompleted),
		},
	}
	incoming := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &newRevision, UpdatedAt: &newTime, Source: ProviderSourceReconciliation},
		Status:   lifecycleStatusPointer(LifecycleStatusConfirming),
		TxHash:   stringPointer("0xlate-enrichment"),
	}

	merged, decision := MergeMovementProviderFieldsForChannel(ChannelSafeheron, existing, incoming)
	if decision.Outcome != MergeOutcomeApplied {
		t.Fatalf("non-status provider enrichment should still apply: %#v", decision)
	}
	if merged.Provider.Status == nil || *merged.Provider.Status != LifecycleStatusCompleted {
		t.Fatalf("Safeheron terminal status regressed: %#v", merged.Provider.Status)
	}
	if merged.Provider.TxHash == nil || *merged.Provider.TxHash != "0xlate-enrichment" {
		t.Fatalf("non-status field did not merge: %#v", merged.Provider)
	}
}

func TestMergeMovementProviderFieldsForChannel_RejectsUnknownSafeheronStatusBeforeGenericMerge(t *testing.T) {
	unknown := LifecycleStatus("UNKNOWN")
	merged, decision := MergeMovementProviderFieldsForChannel(ChannelSafeheron, MovementState{}, ProviderOwnedFields{
		Status: &unknown,
		TxHash: stringPointer("0xmust-not-merge"),
	})
	if decision.Outcome != MergeOutcomeQuarantine || merged.Provider.TxHash != nil {
		t.Fatalf("unknown Safeheron lifecycle status must not bypass policy: %#v %#v", merged, decision)
	}
}

func TestMergeMovementProviderFieldsForChannel_LifecycleAdvanceDoesNotDependOnMetadataRank(t *testing.T) {
	higherRevision := int64(7)
	lowerRevision := int64(6)
	existingAmount := decimal.RequireFromString("10")
	incomingAmount := decimal.RequireFromString("11")
	existing := MovementState{
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &higherRevision, Source: ProviderSourceReconciliation},
			Status:   lifecycleStatusPointer(LifecycleStatusPending),
			Amount:   &existingAmount,
		},
	}
	incoming := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &lowerRevision, Source: ProviderSourceWebhook},
		Status:   lifecycleStatusPointer(LifecycleStatusConfirming),
		Amount:   &incomingAmount,
	}

	merged, decision := MergeMovementProviderFieldsForChannel(ChannelSafeheron, existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || merged.Provider.Status == nil || *merged.Provider.Status != LifecycleStatusConfirming {
		t.Fatalf("provider-approved lifecycle advance must apply despite lower metadata revision: %#v %#v", merged, decision)
	}
	if merged.Provider.Amount == nil || !merged.Provider.Amount.Equal(existingAmount) {
		t.Fatalf("lower-priority non-status provider money must not overwrite: %#v", merged.Provider)
	}
}

func TestMergeMovementProviderFieldsForChannel_StatusOnlyAdvancePromotesNewerMetadata(t *testing.T) {
	oldRevision := int64(1)
	newRevision := int64(2)
	oldTime := time.Date(2026, time.July, 10, 8, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Minute)
	existing := MovementState{
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &oldRevision, UpdatedAt: &oldTime, Source: ProviderSourceWebhook},
			Status:   lifecycleStatusPointer(LifecycleStatusPending),
		},
	}
	incoming := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &newRevision, UpdatedAt: &newTime, Source: ProviderSourceReconciliation},
		Status:   lifecycleStatusPointer(LifecycleStatusConfirming),
	}

	merged, decision := MergeMovementProviderFieldsForChannel(ChannelSafeheron, existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || merged.Provider.Status == nil || *merged.Provider.Status != LifecycleStatusConfirming {
		t.Fatalf("status-only lifecycle advance = %#v %#v", merged, decision)
	}
	if merged.Provider.Metadata.Revision == nil || *merged.Provider.Metadata.Revision != newRevision ||
		merged.Provider.Metadata.UpdatedAt == nil || !merged.Provider.Metadata.UpdatedAt.Equal(newTime) ||
		merged.Provider.Metadata.Source != ProviderSourceReconciliation {
		t.Fatalf("status-only lifecycle advance must promote newer provider metadata: %#v", merged.Provider.Metadata)
	}
}

func TestMergeMovementProviderFieldsForChannel_LifecycleAdvanceRetainsHigherExistingMetadata(t *testing.T) {
	higherRevision := int64(7)
	lowerRevision := int64(6)
	existingTime := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	incomingTime := existingTime.Add(-time.Minute)
	existing := MovementState{
		Provider: ProviderOwnedFields{
			Metadata: ProviderFactMetadata{Revision: &higherRevision, UpdatedAt: &existingTime, Source: ProviderSourceReconciliation},
			Status:   lifecycleStatusPointer(LifecycleStatusPending),
		},
	}
	incoming := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &lowerRevision, UpdatedAt: &incomingTime, Source: ProviderSourceWebhook},
		Status:   lifecycleStatusPointer(LifecycleStatusConfirming),
	}

	merged, decision := MergeMovementProviderFieldsForChannel(ChannelSafeheron, existing, incoming)
	if decision.Outcome != MergeOutcomeApplied || merged.Provider.Status == nil || *merged.Provider.Status != LifecycleStatusConfirming {
		t.Fatalf("lifecycle advance = %#v %#v", merged, decision)
	}
	if merged.Provider.Metadata.Revision == nil || *merged.Provider.Metadata.Revision != higherRevision ||
		merged.Provider.Metadata.UpdatedAt == nil || !merged.Provider.Metadata.UpdatedAt.Equal(existingTime) ||
		merged.Provider.Metadata.Source != ProviderSourceReconciliation {
		t.Fatalf("lower-revision lifecycle advance must not regress provider metadata: %#v", merged.Provider.Metadata)
	}
}

func TestGenericProviderMergeNeverChangesLifecycleStatus(t *testing.T) {
	oldRevision := int64(1)
	newRevision := int64(2)
	completed := LifecycleStatusCompleted
	confirming := LifecycleStatusConfirming
	existing := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &oldRevision, Source: ProviderSourceWebhook},
		Status:   &completed,
	}
	incoming := ProviderOwnedFields{
		Metadata: ProviderFactMetadata{Revision: &newRevision, Source: ProviderSourceReconciliation},
		Status:   &confirming,
		TxHash:   stringPointer("0xallowed-non-status-enrichment"),
	}

	merged, decision := MergeProviderFields(existing, incoming)
	if merged.Status == nil || *merged.Status != LifecycleStatusCompleted {
		t.Fatalf("generic provider merge must never change lifecycle status: %#v", merged)
	}
	if decision.Outcome != MergeOutcomeApplied || merged.TxHash == nil || *merged.TxHash != "0xallowed-non-status-enrichment" {
		t.Fatalf("generic merge must retain non-status lattice behavior: %#v %#v", merged, decision)
	}

	movement, decision := MergeMovementProviderFields(MovementState{Provider: existing}, incoming)
	if movement.Provider.Status == nil || *movement.Provider.Status != LifecycleStatusCompleted || decision.Outcome != MergeOutcomeApplied {
		t.Fatalf("generic movement merge must also preserve lifecycle status: %#v %#v", movement, decision)
	}
}

func stringPointer(value string) *string { return &value }

func boolPointer(value bool) *bool { return &value }

func decimalPointer(value string) *decimal.Decimal {
	parsed := decimal.RequireFromString(value)
	return &parsed
}
