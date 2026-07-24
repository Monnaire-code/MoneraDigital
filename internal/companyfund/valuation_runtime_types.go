package companyfund

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const (
	defaultCompanyFundCurrentValuationPolicyVersion = "current-usd-v1"
	companyFundCurrentValuationTrigger              = "CURRENT_RATE_INGESTION"
	defaultCompanyFundValuationRepairLimit          = 100
	maxCompanyFundValuationRepairLimit              = 1000
)

// CompanyFundTransactionValuator is intentionally best-effort at the ledger
// integration boundary. A provider event has already committed its durable
// movement before this is called, so a rate/cache/database enrichment failure
// is exposed in the returned result and must never force that provider event
// back into its ingestion retry queue. Sweep provides durable repair after a
// process crash or temporary enrichment outage.
type CompanyFundTransactionValuator interface {
	ValueTransaction(context.Context, int64) CompanyFundValuationProcessResult
	Sweep(context.Context, int) CompanyFundValuationSweepResult
}

// CompanyFundValuationCandidateStore separates durable candidate discovery
// and history append from valuation calculation. DBRepository implements it;
// fakes can exercise the safety boundary without a database.
type CompanyFundValuationCandidateStore interface {
	GetCompanyFundTransactionValuationCandidate(context.Context, int64) (*CompanyFundTransactionValuationCandidate, error)
	ListCompanyFundValuationRepairCandidates(context.Context, int) ([]CompanyFundTransactionValuationCandidate, error)
	ListCompanyFundValuationRepairCandidatesAfter(context.Context, int64, int) ([]CompanyFundTransactionValuationCandidate, error)
	ApplyCompanyFundValuation(context.Context, CompanyFundValuationApplyInput) (CompanyFundValuationApplyResult, error)
}

// CompanyFundTransactionValuationCandidate contains only immutable/provider
// transaction facts and the current valuation guard needed for a safe append.
// Finance-owned fields are deliberately absent.
type CompanyFundTransactionValuationCandidate struct {
	ID                        int64
	Channel                   TransactionSource
	MovementKind              MovementKind
	Direction                 Direction
	Currency                  string
	Amount                    decimal.Decimal
	Asset                     AssetIdentity
	IsUnrecognizedAsset       bool
	FromCompanyFundAccountID  *int64
	ToCompanyFundAccountID    *int64
	OccurredAt                *time.Time
	CompletedAt               *time.Time
	FirstSeenAt               time.Time
	ProviderTransactionFactID *int64
	ProviderReportedUSD       *decimal.Decimal
	ProviderValueScope        ProviderValueScope
	ProviderAllocationState   ProviderFactAllocationState
	AirwallexConversionFrom   string
	AirwallexConversionTo     string

	CurrentValuationHistoryID             *int64
	CurrentValuationDependencyFingerprint string
	CurrentValuationStatus                USDValuationStatus
	CurrentValuationSource                USDValuationSource
}

// CompanyFundCurrentValuatorConfig pins the calculation/policy contract that
// participates in every durable valuation dependency fingerprint.
type CompanyFundCurrentValuatorConfig struct {
	PolicyVersion   string
	DefaultMappings CoinGeckoDefaultRateMappings
}

// CompanyFundValuationProcessResult carries an error instead of returning one
// so post-ledger callers cannot accidentally retry provider ingestion. Err is
// limited to local repository/cache/configuration failures and never contains
// provider payload bytes.
type CompanyFundValuationProcessResult struct {
	TransactionID int64
	Skipped       bool
	SkippedManual bool
	Applied       bool
	Converged     bool
	Superseded    bool
	Result        USDValuationResult
	Err           error
}

type CompanyFundValuationSweepResult struct {
	CandidateCount int
	Attempted      int
	Applied        int
	Converged      int
	Superseded     int
	SkippedManual  int
	Failed         int
	Err            error
}

// companyFundValuationSweepCursor is process-local fairness state only. It is
// never a source of truth: a restart begins again from ID zero, and the query
// still selects every missing/UNPRICED/STALE row from PostgreSQL.
type companyFundValuationSweepCursor struct {
	mu      sync.Mutex
	afterID int64
}

func normalizeCompanyFundValuationRepairLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultCompanyFundValuationRepairLimit, nil
	}
	if limit < 1 || limit > maxCompanyFundValuationRepairLimit {
		return 0, fmt.Errorf("company-fund valuation repair limit must be between 1 and %d", maxCompanyFundValuationRepairLimit)
	}
	return limit, nil
}
