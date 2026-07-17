package fundrouting

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"monera-digital/internal/safeheron"
)

func TestWorkerProcessOneRoutesTransactionEvent(t *testing.T) {
	snapshot := routingSnapshot()
	payload := `{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"safeheron-tx-1","coinKey":"ETHEREUM_ETH","txAmount":"1","sourceAddress":"0xSOURCE","destinationAddress":"0xDEST","transactionDirection":"INFLOW","transactionStatus":"COMPLETED","createTime":1784272800000}}`
	store := &routingStoreStub{event: &PendingEvent{ID: 4, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: strings.Repeat("a", 64), RawPayload: []byte(payload)}}
	resolver := networkResolverStub{family: "EVM"}
	worker, err := NewWorker(store, resolver)
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if store.routed.WebhookEventID != 4 || store.routed.NetworkFamily != "EVM" || store.routed.Snapshot.TxKey != snapshot.TxKey {
		t.Fatalf("routed input = %#v", store.routed)
	}
}

func TestWorkerProcessOneLeavesBacklogWhenEmpty(t *testing.T) {
	worker, err := NewWorker(&routingStoreStub{nextErr: ErrNoPendingTransactionEvent}, networkResolverStub{family: "EVM"})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
}

func TestWorkerRejectsDeterministicallyInvalidEventWithoutBlockingTheQueue(t *testing.T) {
	store := &routingStoreStub{event: &PendingEvent{ID: 9, RawPayload: []byte(`{"eventDetail":`)}}
	worker, err := NewWorker(store, networkResolverStub{family: "EVM"})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if store.rejectedID != 9 || store.rejectedCode != "ROUTING_PAYLOAD_INVALID" {
		t.Fatalf("rejection = id:%d code:%s", store.rejectedID, store.rejectedCode)
	}
}

func TestWorkerRejectsUnresolvedNetworkWithoutRouting(t *testing.T) {
	store := &routingStoreStub{event: &PendingEvent{ID: 10, RawPayload: []byte(`{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx","coinKey":"UNKNOWN","txAmount":"1","destinationAddress":"x"}}`)}}
	worker, err := NewWorker(store, networkResolverStub{err: errors.New("unknown CoinKey")})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if store.rejectedCode != "ROUTING_NETWORK_UNRESOLVED" || store.routed.WebhookEventID != 0 {
		t.Fatalf("store = %#v", store)
	}
}

func TestWorkerLeavesTransientRoutingFailurePendingForRetry(t *testing.T) {
	payload := `{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"safeheron-tx-1","coinKey":"ETHEREUM_ETH","txAmount":"1","sourceAddress":"0xSOURCE","destinationAddress":"0xDEST","transactionDirection":"INFLOW","transactionStatus":"COMPLETED","createTime":1784272800000}}`
	store := &routingStoreStub{
		event:    &PendingEvent{ID: 11, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: strings.Repeat("a", 64), RawPayload: []byte(payload)},
		routeErr: errors.New("routing state conflict"),
	}
	worker, err := NewWorker(store, networkResolverStub{family: "EVM"})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if !processed || err == nil || store.rejectedID != 0 || store.rejectedCode != "" {
		t.Fatalf("processed=%v err=%v rejection=%d/%s", processed, err, store.rejectedID, store.rejectedCode)
	}
}

func TestWorkerQuarantinesDeterministicRoutingIdentityConflict(t *testing.T) {
	payload := `{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"safeheron-tx-1","coinKey":"ETHEREUM_ETH","txAmount":"1","sourceAddress":"0xSOURCE","destinationAddress":"0xDEST","transactionDirection":"INFLOW","transactionStatus":"COMPLETED","createTime":1784272800000}}`
	store := &routingStoreStub{
		event:    &PendingEvent{ID: 12, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: strings.Repeat("a", 64), RawPayload: []byte(payload)},
		routeErr: fmt.Errorf("%w: mismatch", ErrRoutingEventConflict),
	}
	worker, err := NewWorker(store, networkResolverStub{family: "EVM"})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := worker.ProcessOne(context.Background())
	if !processed || err == nil || store.rejectedID != 12 || store.rejectedCode != "ROUTING_IDENTITY_CONFLICT" {
		t.Fatalf("processed=%v err=%v rejection=%d/%s", processed, err, store.rejectedID, store.rejectedCode)
	}
}

type routingStoreStub struct {
	event        *PendingEvent
	nextErr      error
	routed       VerifiedEventInput
	routeErr     error
	rejectedID   int64
	rejectedCode string
}

func (stub *routingStoreStub) NextPendingTransactionEvent(context.Context) (*PendingEvent, error) {
	return stub.event, stub.nextErr
}

func (stub *routingStoreStub) RouteVerifiedEvent(_ context.Context, input VerifiedEventInput) ([]RouteResult, error) {
	stub.routed = input
	return nil, stub.routeErr
}

func (stub *routingStoreStub) RejectPendingTransactionEvent(_ context.Context, id int64, code string) error {
	stub.rejectedID, stub.rejectedCode = id, code
	return nil
}

type networkResolverStub struct {
	family string
	err    error
}

func (stub networkResolverStub) ResolveNetworkFamily(context.Context, safeheron.TransactionSnapshot) (string, error) {
	return stub.family, stub.err
}
