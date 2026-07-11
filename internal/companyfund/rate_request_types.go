package companyfund

import (
	"errors"
	"time"
)

const (
	maxRateProviderBytes              = 64
	maxRatePeriodKeyBytes             = 64
	maxRatePlanNameBytes              = 128
	maxRateLicenseReferenceBytes      = 256
	maxRateConfigVersionBytes         = 64
	maxRateLogicalRequestKeyBytes     = 512
	maxRateLeaseOwnerBytes            = 128
	maxRateResponseSnapshotGroupBytes = 256
	maxRateErrorCodeBytes             = 64
	maxRateErrorDetailBytes           = 4096
)

var (
	// ErrRateBudgetExhausted means no new request attempt was reserved. A
	// caller can retry after the next budget period is available; it must not
	// make an external provider call for this request.
	ErrRateBudgetExhausted = errors.New("company-fund rate budget is exhausted")

	// ErrRateBudgetConfigurationMismatch protects a period after its first
	// reservation. The frozen configuration is never silently rewritten.
	ErrRateBudgetConfigurationMismatch = errors.New("company-fund rate budget configuration does not match the existing period")

	// ErrRateRequestProviderBudgetMismatch is raised before any SQL when a
	// logical request names a different provider than its budget period.
	ErrRateRequestProviderBudgetMismatch = errors.New("company-fund rate request provider does not match its budget provider")

	// ErrRateRequestLeaseNotOwned means the request was not in a currently
	// live lease held by the caller. In particular, a DISPATCHED request cannot
	// be re-dispatched by a stale worker.
	ErrRateRequestLeaseNotOwned = errors.New("company-fund rate request lease is not owned or has expired")

	ErrRateRequestClaimLost = errors.New("company-fund rate request claim was lost")
)

// RateRequestKind distinguishes an initial provider call from an explicitly
// scheduled retry. It is persisted independently from a request's lifecycle
// state because every attempt, including a retry, consumes one budget unit.
type RateRequestKind string

const (
	RateRequestKindCurrent       RateRequestKind = "CURRENT"
	RateRequestKindHistorical    RateRequestKind = "HISTORICAL"
	RateRequestKindRetry         RateRequestKind = "RETRY"
	RateRequestKindContractCheck RateRequestKind = "CONTRACT_CHECK"
)

func (kind RateRequestKind) Valid() bool {
	switch kind {
	case RateRequestKindCurrent, RateRequestKindHistorical, RateRequestKindRetry, RateRequestKindContractCheck:
		return true
	default:
		return false
	}
}

// RateRequestState is intentionally separate from a provider response. A
// worker must persist DISPATCHED before it calls the provider, then explicitly
// complete it as a terminal state. That makes a crashed worker visible instead
// of silently spending a second provider call.
type RateRequestState string

const (
	RateRequestStatePending    RateRequestState = "PENDING"
	RateRequestStateLeased     RateRequestState = "LEASED"
	RateRequestStateRetryWait  RateRequestState = "RETRY_WAIT"
	RateRequestStateDispatched RateRequestState = "DISPATCHED"
	RateRequestStateSucceeded  RateRequestState = "SUCCEEDED"
	RateRequestStateFailed     RateRequestState = "FAILED"
	RateRequestStateUnknown    RateRequestState = "UNKNOWN"
	RateRequestStateCancelled  RateRequestState = "CANCELLED"
)

func (state RateRequestState) terminal() bool {
	switch state {
	case RateRequestStateSucceeded, RateRequestStateFailed, RateRequestStateUnknown, RateRequestStateCancelled:
		return true
	default:
		return false
	}
}

func (state RateRequestState) retryable() bool {
	return state == RateRequestStateFailed || state == RateRequestStateUnknown
}

// RateBudgetConfig describes one provider billing period. Once a request is
// reserved, the repository freezes this exact config in the database rather
// than accepting a later process's different call limit or plan details.
type RateBudgetConfig struct {
	Provider         string
	BillingAnchor    time.Time
	PeriodKey        string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	CallLimit        int
	PlanName         string
	LicenseReference string
	ConfigVersion    string
}

// RateRequestReservationInput asks the repository to return the active
// attempt for a logical request, or atomically reserve and create its next
// attempt. Provider is deliberately repeated here so a caller cannot attach a
// request to another provider's budget period by accident.
type RateRequestReservationInput struct {
	Provider              string
	Budget                RateBudgetConfig
	LogicalRequestKey     string
	RequestKind           RateRequestKind
	NormalizedBucketStart *time.Time
	NotBefore             *time.Time
}

// RateRequestAttempt is the durable work item handed to a worker only after
// its reservation transaction commits. It contains no provider response bytes
// and therefore keeps outbound HTTP outside database transactions.
type RateRequestAttempt struct {
	ID                    int64
	BudgetPeriodID        int64
	Provider              string
	LogicalRequestKey     string
	RequestKind           RateRequestKind
	NormalizedBucketStart *time.Time
	AttemptNo             int
	State                 RateRequestState
	NotBefore             *time.Time
	LeaseOwner            string
	LeaseExpiresAt        *time.Time
	ReservedAt            time.Time
	DispatchedAt          *time.Time
	ChargedAt             *time.Time
	CompletedAt           *time.Time
	ResponseSnapshotGroup string
	ErrorCode             string
	ErrorDetail           string
}

// RateRequestReservationResult reports whether a budget unit was newly
// reserved. ReusedActive is true when an active PENDING/LEASED/RETRY_WAIT/
// DISPATCHED request already owns this logical key, so no second call is
// charged.
type RateRequestReservationResult struct {
	Attempt      RateRequestAttempt
	Reserved     bool
	ReusedActive bool
}

// RateRequestLease is a claim token for a provider call. The worker must call
// MarkRateRequestDispatched before making HTTP, then finalize the dispatched
// attempt after the provider response is known.
type RateRequestLease struct {
	Attempt RateRequestAttempt
}

// RateRequestCompletion holds only bounded metadata that is safe to retain
// with the request state. Full provider payloads belong in their dedicated
// event/snapshot retention paths.
type RateRequestCompletion struct {
	State                 RateRequestState
	ResponseSnapshotGroup string
	ErrorCode             string
	ErrorDetail           string
}

type rateBudgetPeriodRecord struct {
	ID               int64
	Provider         string
	BillingAnchor    time.Time
	PeriodKey        string
	PeriodStart      time.Time
	PeriodEnd        time.Time
	CallLimit        int
	ReservedCalls    int
	UsedCalls        int
	PlanName         string
	LicenseReference string
	ConfigVersion    string
	ConfigFrozenAt   *time.Time
	FirstReservedAt  *time.Time
}
