package companyfund

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	maxCompanyFundSyncKindBytes       = 32
	maxCompanyFundSyncWindowKeyBytes  = 128
	maxCompanyFundSyncLeaseOwnerBytes = 128
	maxCompanyFundSyncErrorBytes      = 4096
	maxCompanyFundSyncCheckpointBytes = 16 << 10
)

var (
	ErrCompanyFundSyncRunLeaseNotOwned = errors.New("company-fund sync-run lease is not owned or has expired")
	ErrCompanyFundSyncRunClaimLost     = errors.New("company-fund sync-run claim was lost")
)

type CompanyFundSyncRunStatus string

const (
	CompanyFundSyncRunStatusPending   CompanyFundSyncRunStatus = "PENDING"
	CompanyFundSyncRunStatusLeased    CompanyFundSyncRunStatus = "LEASED"
	CompanyFundSyncRunStatusSucceeded CompanyFundSyncRunStatus = "SUCCEEDED"
	CompanyFundSyncRunStatusFailed    CompanyFundSyncRunStatus = "FAILED"
	CompanyFundSyncRunStatusPartial   CompanyFundSyncRunStatus = "PARTIAL"
	CompanyFundSyncRunStatusSkipped   CompanyFundSyncRunStatus = "SKIPPED"
)

type CompanyFundSyncRunFinalizeOutcome string

const (
	CompanyFundSyncRunFinalizeSucceeded CompanyFundSyncRunFinalizeOutcome = "SUCCEEDED"
	CompanyFundSyncRunFinalizeSkipped   CompanyFundSyncRunFinalizeOutcome = "SKIPPED"
	CompanyFundSyncRunFinalizeRetry     CompanyFundSyncRunFinalizeOutcome = "RETRY"
	CompanyFundSyncRunFinalizePartial   CompanyFundSyncRunFinalizeOutcome = "PARTIAL"
)

// CompanyFundSyncRunInput is one immutable reconciliation window. The unique
// channel/sync-kind/window-key tuple makes every local calendar day
// independently idempotent across scheduler replicas.
type CompanyFundSyncRunInput struct {
	Channel     Channel
	SyncKind    string
	WindowKey   string
	WindowStart time.Time
	WindowEnd   time.Time
	Checkpoint  json.RawMessage
}

// CompanyFundSyncRun is the durable reconciliation state visible to a
// provider-neutral reconciler. All timestamps returned from the repository
// are database facts; callers must not derive lease expiry from local time.
type CompanyFundSyncRun struct {
	ID                   int64
	Channel              Channel
	SyncKind             string
	WindowKey            string
	WindowStart          time.Time
	WindowEnd            time.Time
	Status               CompanyFundSyncRunStatus
	Checkpoint           json.RawMessage
	CandidatesSeen       int
	EventsCreated        int
	TransactionsUpserted int
	TransactionsSkipped  int
	AttemptCount         int
	LeaseOwner           string
	LeaseExpiresAt       *time.Time
	StartedAt            *time.Time
	CompletedAt          *time.Time
	NextAttemptAt        *time.Time
	LastError            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CompanyFundSyncRunCreateResult struct {
	Run      CompanyFundSyncRun
	Inserted bool
}

// CompanyFundSyncRunClaimScope prevents a channel-specific reconciler from
// claiming another provider's window when several replicas share one queue.
type CompanyFundSyncRunClaimScope struct {
	Channel  Channel
	SyncKind string
}

func (scope CompanyFundSyncRunClaimScope) canonical() (CompanyFundSyncRunClaimScope, error) {
	if !scope.Channel.Valid() {
		return CompanyFundSyncRunClaimScope{}, fmt.Errorf("unsupported company-fund sync-run claim channel %q", scope.Channel)
	}
	scope.SyncKind = strings.TrimSpace(scope.SyncKind)
	if err := validateRequiredString("company-fund sync kind", scope.SyncKind, maxCompanyFundSyncKindBytes); err != nil {
		return CompanyFundSyncRunClaimScope{}, err
	}
	return scope, nil
}

type CompanyFundSyncRunProgressUpdate struct {
	// Checkpoint nil preserves the prior durable checkpoint. A non-nil value
	// must be one canonical JSON object and replaces it atomically with the
	// count deltas below.
	Checkpoint                json.RawMessage
	CandidatesSeenDelta       int
	EventsCreatedDelta        int
	TransactionsUpsertedDelta int
	TransactionsSkippedDelta  int
}

type CompanyFundSyncRunProgress struct {
	Checkpoint           json.RawMessage
	CandidatesSeen       int
	EventsCreated        int
	TransactionsUpserted int
	TransactionsSkipped  int
}

// CompanyFundSyncRunStore is the minimal persistence interface used by both
// Safeheron and Airwallex reconcilers. It deliberately has no provider API,
// scheduler, or HTTP dependency.
type CompanyFundSyncRunStore interface {
	CreateCompanyFundSyncRun(ctx context.Context, input CompanyFundSyncRunInput) (CompanyFundSyncRunCreateResult, error)
	ClaimNextCompanyFundSyncRun(ctx context.Context, scope CompanyFundSyncRunClaimScope, owner string, leaseDuration time.Duration) (*CompanyFundSyncRun, error)
	RenewCompanyFundSyncRunLease(ctx context.Context, runID int64, owner string, leaseDuration time.Duration) (time.Time, error)
	UpdateCompanyFundSyncRunProgress(ctx context.Context, runID int64, owner string, update CompanyFundSyncRunProgressUpdate) (CompanyFundSyncRunProgress, error)
	FinalizeCompanyFundSyncRun(ctx context.Context, runID int64, owner string, outcome CompanyFundSyncRunFinalizeOutcome, retryAt *time.Time, failureDetail string) error
}

func (input CompanyFundSyncRunInput) canonical() (CompanyFundSyncRunInput, error) {
	if !input.Channel.Valid() {
		return CompanyFundSyncRunInput{}, fmt.Errorf("unsupported company-fund sync-run channel %q", input.Channel)
	}
	input.SyncKind = strings.TrimSpace(input.SyncKind)
	input.WindowKey = strings.TrimSpace(input.WindowKey)
	if err := validateRequiredString("company-fund sync kind", input.SyncKind, maxCompanyFundSyncKindBytes); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	if err := validateRequiredString("company-fund sync window key", input.WindowKey, maxCompanyFundSyncWindowKeyBytes); err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() || !input.WindowEnd.After(input.WindowStart) {
		return CompanyFundSyncRunInput{}, fmt.Errorf("company-fund sync-run window end must be after a non-zero window start")
	}
	checkpoint, err := canonicalCompanyFundSyncCheckpoint(input.Checkpoint)
	if err != nil {
		return CompanyFundSyncRunInput{}, err
	}
	input.WindowStart = input.WindowStart.UTC()
	input.WindowEnd = input.WindowEnd.UTC()
	input.Checkpoint = checkpoint
	return input, nil
}

func (update CompanyFundSyncRunProgressUpdate) canonicalCheckpoint() (json.RawMessage, error) {
	if update.Checkpoint == nil {
		return nil, nil
	}
	return canonicalCompanyFundSyncCheckpoint(update.Checkpoint)
}

func (update CompanyFundSyncRunProgressUpdate) validate() error {
	if update.CandidatesSeenDelta < 0 || update.EventsCreatedDelta < 0 || update.TransactionsUpsertedDelta < 0 || update.TransactionsSkippedDelta < 0 {
		return fmt.Errorf("company-fund sync-run progress deltas must be non-negative")
	}
	_, err := update.canonicalCheckpoint()
	return err
}

func canonicalCompanyFundSyncCheckpoint(raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) == 0 || len(raw) > maxCompanyFundSyncCheckpointBytes {
		return nil, fmt.Errorf("company-fund sync-run checkpoint must be non-empty and at most %d bytes", maxCompanyFundSyncCheckpointBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("company-fund sync-run checkpoint must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("company-fund sync-run checkpoint must contain exactly one JSON value")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("company-fund sync-run checkpoint must be a JSON object")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize company-fund sync-run checkpoint: %w", err)
	}
	if len(canonical) > maxCompanyFundSyncCheckpointBytes {
		return nil, fmt.Errorf("company-fund sync-run checkpoint exceeds %d canonical bytes", maxCompanyFundSyncCheckpointBytes)
	}
	return json.RawMessage(canonical), nil
}

func validateCompanyFundSyncLeaseOwner(owner string) error {
	return validateRequiredString("company-fund sync-run lease owner", owner, maxCompanyFundSyncLeaseOwnerBytes)
}

func companyFundSyncLeaseDurationMicroseconds(duration time.Duration) (int64, error) {
	if duration <= 0 || duration.Microseconds() <= 0 {
		return 0, fmt.Errorf("company-fund sync-run lease duration must be at least one microsecond")
	}
	return duration.Microseconds(), nil
}

func (outcome CompanyFundSyncRunFinalizeOutcome) status() (CompanyFundSyncRunStatus, bool) {
	switch outcome {
	case CompanyFundSyncRunFinalizeSucceeded:
		return CompanyFundSyncRunStatusSucceeded, false
	case CompanyFundSyncRunFinalizeSkipped:
		return CompanyFundSyncRunStatusSkipped, false
	case CompanyFundSyncRunFinalizeRetry:
		return CompanyFundSyncRunStatusFailed, true
	case CompanyFundSyncRunFinalizePartial:
		return CompanyFundSyncRunStatusPartial, true
	default:
		return "", false
	}
}

func validateCompanyFundSyncFinalize(outcome CompanyFundSyncRunFinalizeOutcome, retryAt *time.Time, failureDetail string) (CompanyFundSyncRunStatus, error) {
	status, retries := outcome.status()
	if status == "" {
		return "", fmt.Errorf("unsupported company-fund sync-run finalize outcome %q", outcome)
	}
	if retries {
		if retryAt == nil || retryAt.IsZero() {
			return "", fmt.Errorf("retryable company-fund sync-run outcome requires a retry time")
		}
	} else if retryAt != nil {
		return "", fmt.Errorf("terminal company-fund sync-run outcome must not have a retry time")
	}
	if strings.TrimSpace(failureDetail) == "" && retries {
		return "", fmt.Errorf("retryable company-fund sync-run outcome requires a failure detail")
	}
	if len(failureDetail) > maxCompanyFundSyncErrorBytes {
		return "", fmt.Errorf("company-fund sync-run failure detail must be at most %d bytes", maxCompanyFundSyncErrorBytes)
	}
	return status, nil
}
