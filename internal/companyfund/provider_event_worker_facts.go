package companyfund

import (
	"context"
	"fmt"
)

type preparedProviderEventFacts struct {
	facts              []ProviderEventNormalizedFact
	movements          []TransactionUpsertInput
	movementIndexByKey map[string]int
}

// persistNormalizedProviderFacts writes all pure-normalizer facts before any
// movement. It then attaches only explicitly bound returned IDs to their
// movements. Parent/group totals may be persisted without a binding, which is
// the safe representation for an unproven batch allocation.
func (worker *ProviderEventWorker) persistNormalizedProviderFacts(
	ctx context.Context,
	lease ProviderEventLease,
	normalized ProviderEventNormalizationResult,
) ([]TransactionUpsertInput, error) {
	prepared, err := prepareProviderEventFacts(lease, normalized)
	if err != nil {
		return nil, err
	}

	factIDsByReference := make(map[string]int64, len(prepared.facts))
	for _, normalizedFact := range prepared.facts {
		inserted, err := worker.repository.InsertProviderTransactionFact(ctx, normalizedFact.Input)
		if err != nil {
			return nil, fmt.Errorf("insert normalized provider fact %q: %w", normalizedFact.Reference, err)
		}
		if inserted.Fact.ID <= 0 {
			return nil, fmt.Errorf("insert normalized provider fact %q returned an invalid ID", normalizedFact.Reference)
		}
		factIDsByReference[normalizedFact.Reference] = inserted.Fact.ID
	}

	for _, binding := range normalized.FactBindings {
		movementIndex := prepared.movementIndexByKey[binding.MovementKey]
		factID := factIDsByReference[binding.FactReference]
		prepared.movements[movementIndex].ProviderTransactionFactID = &factID
	}
	return prepared.movements, nil
}

func prepareProviderEventFacts(lease ProviderEventLease, normalized ProviderEventNormalizationResult) (preparedProviderEventFacts, error) {
	prepared := preparedProviderEventFacts{
		facts:              make([]ProviderEventNormalizedFact, 0, len(normalized.Facts)),
		movements:          append([]TransactionUpsertInput(nil), normalized.Movements...),
		movementIndexByKey: make(map[string]int, len(normalized.Movements)),
	}
	for index, movement := range prepared.movements {
		if movement.ProviderTransactionFactID != nil {
			return preparedProviderEventFacts{}, NewPermanentProviderEventError(fmt.Errorf("normalized movement %q must use an explicit provider fact binding instead of a fact ID", movement.MovementKey))
		}
		prepared.movementIndexByKey[movement.MovementKey] = index
	}

	factsByReference := make(map[string]ProviderTransactionFactInput, len(normalized.Facts))
	for _, fact := range normalized.Facts {
		boundInput, err := bindProviderEventFactProvenance(lease, fact.Input)
		if err != nil {
			return preparedProviderEventFacts{}, err
		}
		fact.Input = boundInput
		prepared.facts = append(prepared.facts, fact)
		factsByReference[fact.Reference] = boundInput
	}

	bindingCounts := make(map[string]int, len(normalized.FactBindings))
	for _, binding := range normalized.FactBindings {
		fact := factsByReference[binding.FactReference]
		movement := prepared.movements[prepared.movementIndexByKey[binding.MovementKey]]
		bindingCounts[binding.FactReference]++

		if fact.ValueScope == ProviderValueScopeTransactionTotal && fact.AllocationState != ProviderFactAllocationStateProvenDerivable {
			return preparedProviderEventFacts{}, NewPermanentProviderEventError(fmt.Errorf("unproven transaction-total fact %q cannot bind to movement %q", binding.FactReference, binding.MovementKey))
		}
		if bindingCounts[binding.FactReference] > 1 && fact.AllocationState != ProviderFactAllocationStateProvenDerivable {
			return preparedProviderEventFacts{}, NewPermanentProviderEventError(fmt.Errorf("provider fact %q cannot bind to multiple movements without a proven derivation contract", binding.FactReference))
		}
		if movement.Channel != lease.Channel {
			return preparedProviderEventFacts{}, NewPermanentProviderEventError(fmt.Errorf("fact-bound movement %q channel does not match leased provider event", binding.MovementKey))
		}
	}
	return prepared, nil
}

func bindProviderEventFactProvenance(lease ProviderEventLease, input ProviderTransactionFactInput) (ProviderTransactionFactInput, error) {
	if input.Channel != lease.Channel {
		return ProviderTransactionFactInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized provider fact channel does not match leased provider event channel"))
	}
	if input.SourceProviderEventID != 0 && input.SourceProviderEventID != lease.ID {
		return ProviderTransactionFactInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized provider fact source event ID does not match leased provider event"))
	}
	if input.SourcePayloadDigest != "" && input.SourcePayloadDigest != lease.SourcePayloadDigest {
		return ProviderTransactionFactInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized provider fact source payload digest does not match leased provider event"))
	}
	input.SourceProviderEventID = lease.ID
	input.SourcePayloadDigest = lease.SourcePayloadDigest
	if _, err := input.validate(); err != nil {
		return ProviderTransactionFactInput{}, NewPermanentProviderEventError(fmt.Errorf("invalid normalized provider fact: %w", err))
	}
	return input, nil
}
