package companyfund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrProviderEventPermanent = errors.New("permanent company-fund provider event processing failure")

// ProviderEventPermanentError classifies a provider format, schema-version,
// or structural error that cannot succeed on retry. Adapters must wrap only
// safe summaries here; raw provider payload bytes must never be included in
// the error text.
type ProviderEventPermanentError struct {
	cause error
}

func (e *ProviderEventPermanentError) Error() string {
	if e == nil || e.cause == nil {
		return ErrProviderEventPermanent.Error()
	}
	return ErrProviderEventPermanent.Error() + ": " + e.cause.Error()
}

func (e *ProviderEventPermanentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *ProviderEventPermanentError) Is(target error) bool {
	return target == ErrProviderEventPermanent
}

// NewPermanentProviderEventError marks a safe parsing/normalization failure
// as terminal. Transient provider, database, and network errors must be
// returned without this wrapper so the worker schedules a retry instead.
func NewPermanentProviderEventError(cause error) error {
	if cause == nil {
		cause = ErrProviderEventPermanent
	}
	return &ProviderEventPermanentError{cause: cause}
}

// ProviderEventNormalizationResult is the bounded output of a channel parser
// and normalizer. A supported non-transaction event must set Ignored; an
// empty result without that explicit marker is a permanent parser contract
// violation rather than a silently dropped delivery.
type ProviderEventNormalizationResult struct {
	Facts        []ProviderEventNormalizedFact
	Movements    []TransactionUpsertInput
	FactBindings []ProviderEventMovementFactBinding
	Ignored      bool
}

func (result ProviderEventNormalizationResult) validate() error {
	if result.Ignored && (len(result.Facts) != 0 || len(result.Movements) != 0 || len(result.FactBindings) != 0) {
		return NewPermanentProviderEventError(fmt.Errorf("ignored provider event cannot contain facts, movements, or fact bindings"))
	}
	if !result.Ignored && len(result.Facts) == 0 && len(result.Movements) == 0 {
		return NewPermanentProviderEventError(fmt.Errorf("provider event normalizer returned no facts or movements without marking the event ignored"))
	}

	factsByReference := make(map[string]struct{}, len(result.Facts))
	for _, fact := range result.Facts {
		if err := validateRequiredString("provider event normalized fact reference", fact.Reference, maxProviderEventNormalizedFactReferenceBytes); err != nil {
			return NewPermanentProviderEventError(err)
		}
		if _, exists := factsByReference[fact.Reference]; exists {
			return NewPermanentProviderEventError(fmt.Errorf("duplicate provider event normalized fact reference %q", fact.Reference))
		}
		factsByReference[fact.Reference] = struct{}{}
	}

	movementsByKey := make(map[string]struct{}, len(result.Movements))
	for _, movement := range result.Movements {
		if strings.TrimSpace(movement.MovementKey) == "" {
			return NewPermanentProviderEventError(fmt.Errorf("provider event normalized movement key is required"))
		}
		if _, exists := movementsByKey[movement.MovementKey]; exists {
			return NewPermanentProviderEventError(fmt.Errorf("duplicate provider event normalized movement key %q", movement.MovementKey))
		}
		movementsByKey[movement.MovementKey] = struct{}{}
	}

	boundMovements := make(map[string]struct{}, len(result.FactBindings))
	for _, binding := range result.FactBindings {
		if err := validateRequiredString("provider event fact binding movement key", binding.MovementKey, 256); err != nil {
			return NewPermanentProviderEventError(err)
		}
		if err := validateRequiredString("provider event fact binding reference", binding.FactReference, maxProviderEventNormalizedFactReferenceBytes); err != nil {
			return NewPermanentProviderEventError(err)
		}
		if _, exists := movementsByKey[binding.MovementKey]; !exists {
			return NewPermanentProviderEventError(fmt.Errorf("provider event fact binding references unknown movement %q", binding.MovementKey))
		}
		if _, exists := factsByReference[binding.FactReference]; !exists {
			return NewPermanentProviderEventError(fmt.Errorf("provider event fact binding references unknown fact %q", binding.FactReference))
		}
		if _, exists := boundMovements[binding.MovementKey]; exists {
			return NewPermanentProviderEventError(fmt.Errorf("provider event movement %q has more than one fact binding", binding.MovementKey))
		}
		boundMovements[binding.MovementKey] = struct{}{}
	}
	return nil
}

const maxProviderEventNormalizedFactReferenceBytes = 128

// ProviderEventNormalizedFact is an immutable provider-level fact emitted by
// a pure normalizer. Reference is local to one event result; it is how the
// worker links the returned durable fact ID to a movement without allowing the
// normalizer to perform database I/O.
type ProviderEventNormalizedFact struct {
	Reference string
	Input     ProviderTransactionFactInput
}

// ProviderEventMovementFactBinding explicitly attaches one normalized fact to
// one movement key. Parent totals can therefore be retained as audit facts
// without being copied onto batch child movements.
type ProviderEventMovementFactBinding struct {
	MovementKey   string
	FactReference string
}

// ProviderEventNormalizer is injected per channel. It receives only source
// bytes for the leased delivery and must return provider-owned movement data;
// it must not make a database transaction span provider I/O or parsing.
type ProviderEventNormalizer interface {
	NormalizeProviderEvent(ctx context.Context, lease ProviderEventLease, sourceBytes []byte) (ProviderEventNormalizationResult, error)
}

// ProviderEventPayloadReader is the source-byte boundary. Its concrete
// implementation dispatches Safeheron raw-event references separately from
// company-owned encrypted payloads.
type ProviderEventPayloadReader interface {
	ReadProviderEventPayload(ctx context.Context, lease ProviderEventLease) ([]byte, error)
}

// ProviderEventWorkerRepository is deliberately narrow so worker behavior can
// be TDD'd using fakes and so a claim transaction is committed before any
// source read, decrypt, parser, or normalizer work starts.
type ProviderEventWorkerRepository interface {
	ClaimNextProviderEvent(ctx context.Context, owner string, leaseDuration time.Duration) (*ProviderEventLease, error)
	RenewProviderEventLease(ctx context.Context, eventID int64, owner string, leaseDuration time.Duration) (time.Time, error)
	FinalizeProviderEvent(ctx context.Context, eventID int64, owner string, outcome ProviderEventFinalizeOutcome, retryAt *time.Time, failureDetail string) error
	InsertProviderTransactionFact(ctx context.Context, input ProviderTransactionFactInput) (ProviderTransactionFactInsertResult, error)
	UpsertCompanyFundTransaction(ctx context.Context, input TransactionUpsertInput) (TransactionUpsertResult, error)
}

// ProviderEventRetryPolicy is a bounded exponential backoff policy. Attempt
// one uses InitialDelay; later attempts double until MaxDelay.
type ProviderEventRetryPolicy struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

func (policy ProviderEventRetryPolicy) validate() error {
	if policy.InitialDelay <= 0 || policy.InitialDelay.Microseconds() <= 0 {
		return fmt.Errorf("provider event retry initial delay must be at least one microsecond")
	}
	if policy.MaxDelay <= 0 || policy.MaxDelay < policy.InitialDelay {
		return fmt.Errorf("provider event retry max delay must be positive and no smaller than the initial delay")
	}
	return nil
}

func (policy ProviderEventRetryPolicy) Delay(attempt int) (time.Duration, error) {
	if err := policy.validate(); err != nil {
		return 0, err
	}
	if attempt <= 0 {
		return 0, fmt.Errorf("provider event retry attempt must be positive")
	}

	delay := policy.InitialDelay
	for retry := 1; retry < attempt && delay < policy.MaxDelay; retry++ {
		if delay > policy.MaxDelay/2 {
			return policy.MaxDelay, nil
		}
		delay *= 2
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay, nil
	}
	return delay, nil
}

// ProviderEventWorkerConfig contains no provider credentials or routing. Both
// the lease and retry scheduling are explicit so callers can configure jobs
// without hard-coded timing behavior.
type ProviderEventWorkerConfig struct {
	Owner         string
	LeaseDuration time.Duration
	RenewInterval time.Duration
	RetryPolicy   ProviderEventRetryPolicy
	Now           func() time.Time
	// TransactionValuator is optional best-effort post-commit enrichment. Its
	// result is deliberately never fed into provider-event finalization: the
	// ledger upsert is already durable and valuation repair owns later retries.
	TransactionValuator CompanyFundTransactionValuator
}

func (config ProviderEventWorkerConfig) validate() error {
	if err := validateLeaseOwner(config.Owner); err != nil {
		return err
	}
	if _, err := providerEventLeaseDurationMicroseconds(config.LeaseDuration); err != nil {
		return err
	}
	if config.RenewInterval <= 0 || config.RenewInterval >= config.LeaseDuration {
		return fmt.Errorf("provider event lease renew interval must be positive and shorter than the lease duration")
	}
	if err := config.RetryPolicy.validate(); err != nil {
		return err
	}
	if config.Now == nil {
		return fmt.Errorf("provider event worker clock is required")
	}
	return nil
}

// ProviderEventWorkerResult distinguishes an empty queue from a claimed event
// that was terminalized or scheduled for retry by this invocation.
type ProviderEventWorkerResult struct {
	Claimed       bool
	EventID       int64
	FactCount     int
	MovementCount int
	Outcome       ProviderEventFinalizeOutcome
}
