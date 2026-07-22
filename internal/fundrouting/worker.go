package fundrouting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"monera-digital/internal/adaptiveschedule"
	"monera-digital/internal/safeheron"
)

var ErrNoPendingTransactionEvent = errors.New("no pending Safeheron transaction routing event")
var ErrRoutingEventConflict = errors.New("Safeheron routing event conflicts with stored identity")

type RoutingStore interface {
	NextPendingTransactionEvent(context.Context) (*PendingEvent, error)
	RouteVerifiedEvent(context.Context, VerifiedEventInput) ([]RouteResult, error)
	RejectPendingTransactionEvent(context.Context, int64, string) error
}

type NetworkFamilyResolver interface {
	ResolveNetworkFamily(context.Context, safeheron.TransactionSnapshot) (string, error)
}

type Worker struct {
	store    RoutingStore
	resolver NetworkFamilyResolver
	runner   *adaptiveRunner
}

func NewWorker(store RoutingStore, resolver NetworkFamilyResolver) (*Worker, error) {
	if store == nil || resolver == nil {
		return nil, fmt.Errorf("Safeheron routing store and network resolver are required")
	}
	worker := &Worker{store: store, resolver: resolver}
	worker.runner = newAdaptiveRunner("Safeheron routing worker", time.Second, adaptiveschedule.DefaultMaxIdle, worker.ProcessOne)
	return worker, nil
}

// SetOnWorked registers a callback after a cycle that processed routing work
// (for example waking the projection worker). Nil clears the callback.
func (worker *Worker) SetOnWorked(fn func()) {
	if worker == nil || worker.runner == nil {
		return
	}
	worker.runner.onWorked = fn
}

// Notify wakes routing after a durable transaction webhook is available.
// Safe before Run.
func (worker *Worker) Notify() bool {
	if worker == nil || worker.runner == nil {
		return false
	}
	return worker.runner.Notify()
}

func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	event, err := worker.store.NextPendingTransactionEvent(ctx)
	if errors.Is(err, ErrNoPendingTransactionEvent) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var envelope struct {
		EventType   string                        `json:"eventType"`
		EventDetail safeheron.TransactionSnapshot `json:"eventDetail"`
	}
	if err := json.Unmarshal(event.RawPayload, &envelope); err != nil {
		return true, worker.reject(ctx, event.ID, "ROUTING_PAYLOAD_INVALID")
	}
	family, err := worker.resolver.ResolveNetworkFamily(ctx, envelope.EventDetail)
	if err != nil {
		return true, worker.reject(ctx, event.ID, "ROUTING_NETWORK_UNRESOLVED")
	}
	if _, err := BuildCandidates(envelope.EventDetail, family); err != nil {
		return true, worker.reject(ctx, event.ID, "ROUTING_OCCURRENCE_INVALID")
	}
	eventType := event.EventType
	if envelope.EventType != "" {
		eventType = envelope.EventType
	}
	_, err = worker.store.RouteVerifiedEvent(ctx, VerifiedEventInput{
		WebhookEventID: event.ID,
		EventType:      eventType,
		PayloadDigest:  event.PayloadDigest,
		NetworkFamily:  family,
		Snapshot:       envelope.EventDetail,
	})
	if err != nil {
		if !errors.Is(err, ErrRoutingEventConflict) {
			return true, fmt.Errorf("route Safeheron event %d failed transiently: %w", event.ID, err)
		}
		if rejectErr := worker.reject(ctx, event.ID, "ROUTING_IDENTITY_CONFLICT"); rejectErr != nil {
			return true, errors.Join(err, rejectErr)
		}
		return true, fmt.Errorf("route Safeheron event %d conflicted and was quarantined: %w", event.ID, err)
	}
	return true, nil
}

func (worker *Worker) reject(ctx context.Context, eventID int64, code string) error {
	if err := worker.store.RejectPendingTransactionEvent(ctx, eventID, code); err != nil {
		return fmt.Errorf("reject Safeheron routing event %d: %w", eventID, err)
	}
	return nil
}

func (worker *Worker) Run(ctx context.Context) {
	if worker == nil || worker.runner == nil {
		return
	}
	worker.runner.Run(ctx)
}
