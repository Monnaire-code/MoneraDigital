package companyfund

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// ProviderEventWorker claims one durable provider delivery at a time. It
// deliberately has no HTTP client, handler, container, or route dependency:
// concrete channel parsers and source readers are injected at construction.
type ProviderEventWorker struct {
	repository  ProviderEventWorkerRepository
	payloads    ProviderEventPayloadReader
	normalizers map[Channel]ProviderEventNormalizer
	config      ProviderEventWorkerConfig
}

type providerEventDueRepository interface {
	NextProviderEventDue(context.Context) (time.Time, error)
}

func NewProviderEventWorker(
	repository ProviderEventWorkerRepository,
	payloads ProviderEventPayloadReader,
	normalizers map[Channel]ProviderEventNormalizer,
	config ProviderEventWorkerConfig,
) (*ProviderEventWorker, error) {
	if repository == nil {
		return nil, fmt.Errorf("provider event worker repository is required")
	}
	if payloads == nil {
		return nil, fmt.Errorf("provider event worker payload reader is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if len(normalizers) == 0 {
		return nil, fmt.Errorf("at least one provider event normalizer is required")
	}

	registered := make(map[Channel]ProviderEventNormalizer, len(normalizers))
	for channel, normalizer := range normalizers {
		if !channel.Valid() {
			return nil, fmt.Errorf("unsupported provider event normalizer channel %q", channel)
		}
		if normalizer == nil {
			return nil, fmt.Errorf("provider event normalizer for channel %q is required", channel)
		}
		registered[channel] = normalizer
	}
	return &ProviderEventWorker{repository: repository, payloads: payloads, normalizers: registered, config: config}, nil
}

// NextProviderEventDue exposes the repository's durable retry deadline to the
// runtime without making it part of the worker's required storage contract.
func (worker *ProviderEventWorker) NextProviderEventDue(ctx context.Context) (time.Time, error) {
	if worker == nil {
		return time.Time{}, nil
	}
	repository, ok := worker.repository.(providerEventDueRepository)
	if !ok {
		return time.Time{}, nil
	}
	return repository.NextProviderEventDue(ctx)
}

// ProcessNext claims and processes at most one event. Claiming is delegated to
// the repository first, so all source reads, decrypts, parsing, normalization,
// and movement upserts happen outside its short claim transaction.
func (worker *ProviderEventWorker) ProcessNext(ctx context.Context) (result ProviderEventWorkerResult, err error) {
	var (
		lease         *ProviderEventLease
		stopHeartbeat func() error
		valuationWake bool
	)
	defer func() {
		if recover() == nil {
			return
		}
		if lease == nil {
			result = ProviderEventWorkerResult{}
			err = fmt.Errorf("provider event processing panicked before a delivery was claimed")
			return
		}
		if stopHeartbeat != nil {
			if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
				result = ProviderEventWorkerResult{Claimed: true, EventID: lease.ID}
				err = heartbeatErr
				return
			}
		}

		// Do not retain or format the recovered value: a parser/source panic can
		// carry provider bytes. A panic is operationally transient, so let the
		// durable retry path own scheduling when this worker still has its lease.
		result, err = worker.finalizeProcessingFailure(ctx, lease, result, errors.New("provider event processing panicked"))
	}()

	if worker == nil || worker.repository == nil || worker.payloads == nil {
		return ProviderEventWorkerResult{}, fmt.Errorf("provider event worker is not configured")
	}

	lease, err = worker.repository.ClaimNextProviderEvent(ctx, worker.config.Owner, worker.config.LeaseDuration)
	if err != nil {
		return ProviderEventWorkerResult{}, fmt.Errorf("claim next provider event: %w", err)
	}
	if lease == nil {
		return ProviderEventWorkerResult{}, nil
	}
	result = ProviderEventWorkerResult{Claimed: true, EventID: lease.ID}

	// Renew once before external work begins. This both verifies our still-live
	// lease and gives source I/O the complete configured lease window.
	if _, err := worker.repository.RenewProviderEventLease(ctx, lease.ID, worker.config.Owner, worker.config.LeaseDuration); err != nil {
		logProviderEventWorkerDeferral("initial_lease_renewal")
		return result, fmt.Errorf("renew provider event lease before processing: %w", err)
	}

	normalizer, ok := worker.normalizers[lease.Channel]
	if !ok {
		return worker.finalizeProcessingFailure(ctx, lease, result, fmt.Errorf("provider event normalizer is not configured for channel %q", lease.Channel))
	}

	processingContext, stopHeartbeat := worker.startLeaseHeartbeat(ctx, lease.ID)
	payload, processingErr := worker.payloads.ReadProviderEventPayload(processingContext, *lease)
	if processingErr == nil {
		var normalized ProviderEventNormalizationResult
		normalized, processingErr = normalizer.NormalizeProviderEvent(processingContext, *lease, payload)
		if processingErr == nil {
			processingErr = normalized.validate()
		}
		if processingErr == nil && normalized.Ignored {
			if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
				logProviderEventWorkerDeferral("lease_heartbeat")
				return result, heartbeatErr
			}
			if err := worker.repository.FinalizeProviderEvent(ctx, lease.ID, worker.config.Owner, ProviderEventFinalizeIgnored, nil, ""); err != nil {
				logProviderEventWorkerDeferral("finalize_ignored")
				return result, fmt.Errorf("finalize ignored provider event: %w", err)
			}
			result.Outcome = ProviderEventFinalizeIgnored
			return result, nil
		}
		if processingErr == nil {
			var movements []TransactionUpsertInput
			movements, processingErr = worker.persistNormalizedProviderFacts(processingContext, *lease, normalized)
			if processingErr == nil {
				result.FactCount = len(normalized.Facts)
			}
			for _, movement := range movements {
				if processingErr != nil {
					break
				}
				movement, processingErr = bindProviderEventMovementProvenance(*lease, movement)
				if processingErr != nil {
					break
				}
				var upserted TransactionUpsertResult
				if upserted, processingErr = worker.repository.UpsertCompanyFundTransaction(processingContext, movement); processingErr != nil {
					processingErr = fmt.Errorf("upsert provider event movement %q: %w", movement.MovementKey, processingErr)
					break
				}
				valuation := worker.valueSuccessfulLedgerTransactionBestEffort(processingContext, upserted.ID)
				if !valuationWake && valuationRepairNeeded(valuation) {
					worker.notifyValuationRepairNeeded()
					valuationWake = true
				}
				result.MovementCount++
			}
		}
	}

	if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
		logProviderEventWorkerDeferral("lease_heartbeat")
		return result, heartbeatErr
	}
	if processingErr != nil {
		return worker.finalizeProcessingFailure(ctx, lease, result, processingErr)
	}
	if err := worker.repository.FinalizeProviderEvent(ctx, lease.ID, worker.config.Owner, ProviderEventFinalizeProcessed, nil, ""); err != nil {
		logProviderEventWorkerDeferral("finalize_processed")
		return result, fmt.Errorf("finalize processed provider event: %w", err)
	}
	result.Outcome = ProviderEventFinalizeProcessed
	return result, nil
}

func (worker *ProviderEventWorker) notifyValuationRepairNeeded() {
	if worker == nil || worker.config.OnValuationRepairNeeded == nil {
		return
	}
	defer func() { _ = recover() }()
	worker.config.OnValuationRepairNeeded()
}

func valuationRepairNeeded(result CompanyFundValuationProcessResult) bool {
	return result.Err != nil || result.Result.Reason == USDValuationReasonRateMissing || result.Result.Reason == USDValuationReasonCacheStale
}

// valueSuccessfulLedgerTransactionBestEffort keeps optional USD enrichment
// outside the provider-event success/retry decision. The upsert has already
// committed; the valuator's own repair sweep handles temporary failures. A
// defensive panic recovery also prevents a faulty optional integration from
// turning a completed financial movement into a duplicate provider retry.
func (worker *ProviderEventWorker) valueSuccessfulLedgerTransactionBestEffort(ctx context.Context, transactionID int64) (result CompanyFundValuationProcessResult) {
	if worker == nil || worker.config.TransactionValuator == nil || transactionID <= 0 {
		return CompanyFundValuationProcessResult{TransactionID: transactionID, Skipped: true}
	}
	defer func() {
		if recover() != nil {
			result = CompanyFundValuationProcessResult{TransactionID: transactionID, Err: errors.New("company-fund immediate valuation panicked")}
		}
	}()
	return worker.config.TransactionValuator.ValueTransaction(ctx, transactionID)
}

func (worker *ProviderEventWorker) finalizeProcessingFailure(ctx context.Context, lease *ProviderEventLease, result ProviderEventWorkerResult, processingErr error) (ProviderEventWorkerResult, error) {
	failureDetail := providerEventFailureDetail(processingErr)
	if isPermanentProviderEventFailure(processingErr) {
		if err := worker.repository.FinalizeProviderEvent(ctx, lease.ID, worker.config.Owner, ProviderEventFinalizeFailed, nil, failureDetail); err != nil {
			logProviderEventWorkerDeferral("finalize_dead_letter")
			return result, fmt.Errorf("dead-letter provider event: %w", err)
		}
		result.Outcome = ProviderEventFinalizeFailed
		return result, nil
	}

	delay, err := worker.config.RetryPolicy.Delay(lease.AttemptCount)
	if err != nil {
		return result, fmt.Errorf("calculate provider event retry backoff: %w", err)
	}
	now := worker.config.Now().UTC()
	if now.IsZero() {
		return result, fmt.Errorf("provider event worker clock returned zero time")
	}
	retryAt := now.Add(delay)
	if err := worker.repository.FinalizeProviderEvent(ctx, lease.ID, worker.config.Owner, ProviderEventFinalizeRetry, &retryAt, failureDetail); err != nil {
		logProviderEventWorkerDeferral("finalize_retry")
		return result, fmt.Errorf("schedule provider event retry: %w", err)
	}
	result.Outcome = ProviderEventFinalizeRetry
	return result, nil
}

func logProviderEventWorkerDeferral(stage string) {
	// Do not include the wrapped error: adapters can carry provider-originated
	// data. The stage alone is enough to distinguish retry mechanics from a
	// parser or persistence failure in operational logs.
	log.Printf("company-fund provider event worker deferred: stage=%s", stage)
}

func bindProviderEventMovementProvenance(lease ProviderEventLease, movement TransactionUpsertInput) (TransactionUpsertInput, error) {
	if movement.Channel != lease.Channel {
		return TransactionUpsertInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized movement channel does not match leased provider event channel"))
	}
	if movement.ProviderEventID != "" && movement.ProviderEventID != lease.ProviderEventID {
		return TransactionUpsertInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized movement provider event ID does not match leased provider event"))
	}
	if movement.LatestProviderEventID != nil && *movement.LatestProviderEventID != lease.ID {
		return TransactionUpsertInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized movement latest provider event ID does not match leased provider event"))
	}
	if movement.RawSnapshotDigest != "" && movement.RawSnapshotDigest != lease.SourcePayloadDigest {
		return TransactionUpsertInput{}, NewPermanentProviderEventError(fmt.Errorf("normalized movement raw snapshot digest does not match leased provider event"))
	}

	eventID := lease.ID
	movement.ProviderEventID = lease.ProviderEventID
	movement.LatestProviderEventID = &eventID
	movement.RawSnapshotDigest = lease.SourcePayloadDigest
	movement.AuthorizingRoutingActionID = lease.AuthorizingRoutingActionID
	return movement, nil
}

func isPermanentProviderEventFailure(err error) bool {
	return errors.Is(err, ErrProviderEventPermanent) ||
		errors.Is(err, ErrOwnedProviderPayloadUnavailable) ||
		errors.Is(err, ErrOwnedProviderPayloadIntegrity)
}

func providerEventFailureDetail(err error) string {
	if err == nil {
		return "provider event processing failed"
	}
	return truncateProviderEventError(err.Error())
}

func (worker *ProviderEventWorker) startLeaseHeartbeat(parent context.Context, eventID int64) (context.Context, func() error) {
	processingContext, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var heartbeatErr error

	go func() {
		defer func() {
			if recover() != nil {
				heartbeatErr = errors.New("provider event lease heartbeat panicked")
				cancel()
			}
			close(done)
		}()
		ticker := time.NewTicker(worker.config.RenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-processingContext.Done():
				return
			case <-ticker.C:
				if _, err := worker.repository.RenewProviderEventLease(processingContext, eventID, worker.config.Owner, worker.config.LeaseDuration); err != nil {
					heartbeatErr = fmt.Errorf("renew provider event lease while processing: %w", err)
					cancel()
					return
				}
			}
		}
	}()

	return processingContext, func() error {
		cancel()
		<-done
		return heartbeatErr
	}
}
