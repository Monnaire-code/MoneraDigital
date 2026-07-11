package companyfund

import (
	"context"
	"fmt"
	"strings"
	"time"

	"monera-digital/internal/safeheron"
)

const (
	// SafeheronTransactionHistorySyncKind identifies durable historical
	// compensation windows. It is deliberately separate from webhook inbox
	// deliveries so a replayed callback cannot reuse a history checkpoint.
	SafeheronTransactionHistorySyncKind = "SAFEHERON_TRANSACTION_HISTORY"

	// SafeheronTransactionHistoryLateStatusSyncKind isolates a late-status
	// repair pass from the normal daily history checkpoint for the same account
	// and calendar window.
	SafeheronTransactionHistoryLateStatusSyncKind = "SAFEHERON_LATE_STATUS_HISTORY"

	// SafeheronTransactionHistorySnapshotEventType identifies one canonical
	// transaction-history API snapshot retained in the owned encrypted inbox.
	// It is not a Safeheron webhook event and must therefore never be parsed as
	// a webhook envelope.
	SafeheronTransactionHistorySnapshotEventType = "SAFEHERON_TRANSACTION_HISTORY_SNAPSHOT"

	maxSafeheronHistoryPageSize = int32(500)
	maxSafeheronHistoryPages    = 10_000
	maxSafeheronHistoryCursor   = 256
)

// SafeheronHistoryOwnedProviderEventIngestor is satisfied by
// OwnedProviderPayloadService. History reconciliation persists only encrypted
// canonical snapshots; the provider-event worker normalizes them later.
type SafeheronHistoryOwnedProviderEventIngestor interface {
	Ingest(ctx context.Context, input OwnedProviderPayloadInput) (ProviderEventInsertResult, error)
}

// SafeheronTransactionHistorySyncRunStore is intentionally narrow. The schema
// layer owns leases and durable checkpoint SQL, while the reconciler owns only
// page-resume state and event-ingestion counters.
type SafeheronTransactionHistorySyncRunStore interface {
	OpenSafeheronTransactionHistorySyncRun(ctx context.Context, input SafeheronTransactionHistorySyncRunInput) (SafeheronTransactionHistorySyncRun, error)
	CheckpointSafeheronTransactionHistorySyncRun(ctx context.Context, runID int64, checkpoint SafeheronTransactionHistorySyncCheckpoint) error
	CompleteSafeheronTransactionHistorySyncRun(ctx context.Context, runID int64, checkpoint SafeheronTransactionHistorySyncCheckpoint) error
}

// SafeheronTransactionHistoryReconcilerConfig contains bounded transport and
// encrypted-payload settings. It does not include a small-amount threshold:
// dust must reach the ledger risk policy rather than be filtered here.
type SafeheronTransactionHistoryReconcilerConfig struct {
	PageSize          int32
	MaxPages          int
	PayloadKeyVersion string
	PayloadRetention  time.Duration
}

// SafeheronTransactionHistoryReconcileInput is one explicit configured
// provider-account/window pair. AccountKey is deliberately supplied and
// verified independently of addresses so no wallet address is treated as an
// API account identifier.
type SafeheronTransactionHistoryReconcileInput struct {
	Account            CompanyFundAccount
	ProviderAccountKey string
	WindowStart        time.Time
	WindowEnd          time.Time
	// SyncKind and WindowKey are optional durable identity overrides. Blank
	// values retain the regular history defaults; supplied values must be exact
	// bounded identifiers so callers cannot accidentally reuse another run.
	SyncKind  string
	WindowKey string
}

// SafeheronTransactionHistorySyncRunInput is an immutable key for one
// account/window history scan. The narrow store can later map this to the
// generic sync-run schema without the reconciler assuming nullable webhook
// account fields exist.
type SafeheronTransactionHistorySyncRunInput struct {
	Channel              Channel
	SyncKind             string
	WindowKey            string
	CompanyFundAccountID int64
	ProviderAccountKey   string
	WindowStart          time.Time
	WindowEnd            time.Time
}

// SafeheronTransactionHistorySyncCheckpoint retains completed exact event IDs
// for an in-flight cursor page. A corrected raw snapshot has a different
// content-derived event ID and is intentionally not skipped on retry.
type SafeheronTransactionHistorySyncCheckpoint struct {
	NextCursor       string
	InFlightCursor   *string
	InFlightEventIDs []string
	CandidatesSeen   int
	EventsCreated    int
	EventsExisting   int
}

type SafeheronTransactionHistorySyncRun struct {
	ID           int64
	AttemptCount int
	Checkpoint   SafeheronTransactionHistorySyncCheckpoint
}

// SafeheronTransactionHistoryReconcileResult reports only durable inbox work.
// It never implies that the later provider-event worker has created a ledger
// transaction yet.
type SafeheronTransactionHistoryReconcileResult struct {
	RunID          int64
	AttemptCount   int
	PagesFetched   int
	CandidatesSeen int
	EventsCreated  int
	EventsExisting int
	Checkpoint     SafeheronTransactionHistorySyncCheckpoint
}

// SafeheronTransactionHistoryReconciler paginates the official history API
// with a configured account key and materializes each canonical snapshot as a
// separate encrypted provider event. It never calls a detail/replay endpoint.
type SafeheronTransactionHistoryReconciler struct {
	client   safeheron.TransactionHistoryClient
	ingester SafeheronHistoryOwnedProviderEventIngestor
	syncRuns SafeheronTransactionHistorySyncRunStore
	config   SafeheronTransactionHistoryReconcilerConfig
}

func NewSafeheronTransactionHistoryReconciler(
	client safeheron.TransactionHistoryClient,
	ingester SafeheronHistoryOwnedProviderEventIngestor,
	syncRuns SafeheronTransactionHistorySyncRunStore,
	config SafeheronTransactionHistoryReconcilerConfig,
) (*SafeheronTransactionHistoryReconciler, error) {
	if client == nil || ingester == nil || syncRuns == nil {
		return nil, fmt.Errorf("Safeheron transaction history reconciler dependencies are required")
	}
	normalizedConfig, err := normalizeSafeheronTransactionHistoryReconcilerConfig(config)
	if err != nil {
		return nil, err
	}
	return &SafeheronTransactionHistoryReconciler{
		client: client, ingester: ingester, syncRuns: syncRuns, config: normalizedConfig,
	}, nil
}

func normalizeSafeheronTransactionHistoryReconcilerConfig(config SafeheronTransactionHistoryReconcilerConfig) (SafeheronTransactionHistoryReconcilerConfig, error) {
	if config.PageSize < 1 || config.PageSize > maxSafeheronHistoryPageSize {
		return SafeheronTransactionHistoryReconcilerConfig{}, fmt.Errorf("Safeheron transaction history reconciliation page size is outside configured bounds")
	}
	if config.MaxPages < 1 || config.MaxPages > maxSafeheronHistoryPages {
		return SafeheronTransactionHistoryReconcilerConfig{}, fmt.Errorf("Safeheron transaction history reconciliation maximum pages is outside configured bounds")
	}
	keyVersion, err := normalizeSafeheronHistoryRequired("Safeheron history owned payload key version", config.PayloadKeyVersion, maxOwnedPayloadKeyVersionBytes)
	if err != nil {
		return SafeheronTransactionHistoryReconcilerConfig{}, err
	}
	if config.PayloadRetention <= 0 || config.PayloadRetention.Microseconds() <= 0 {
		return SafeheronTransactionHistoryReconcilerConfig{}, fmt.Errorf("Safeheron history owned payload retention must be positive")
	}
	config.PayloadKeyVersion = keyVersion
	return config, nil
}

func normalizeSafeheronHistoryRequired(label, value string, maxBytes int) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxBytes {
		return "", fmt.Errorf("%s must be an exact bounded value", label)
	}
	return value, nil
}
