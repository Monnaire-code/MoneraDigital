// Package companyfund contains provider-neutral rules for company-fund
// ingestion. It deliberately has no database, HTTP, handler, or container
// dependency so provider adapters and repositories share one deterministic
// money-domain contract.
package companyfund

import (
	"time"

	"github.com/shopspring/decimal"
)

const (
	// DecimalCalculationScale is the only scale used when the domain must divide
	// exact amounts (for example, deriving an USD unit price). Presentation
	// rounding belongs outside this package.
	DecimalCalculationScale int32 = 18

	// MovementIdentityAlgorithmVersion is persisted with every generated
	// fallback movement identity so future algorithm changes do not rewrite old
	// identities.
	MovementIdentityAlgorithmVersion = "v1"
)

// Channel identifies the custody or banking provider that supplied a fact.
type Channel string

const (
	ChannelSafeheron Channel = "SAFEHERON"
	ChannelAirwallex Channel = "AIRWALLEX"
)

func (c Channel) Valid() bool {
	return c == ChannelSafeheron || c == ChannelAirwallex
}

// TransferMode describes the provider shape, separate from the accounting
// movement kind. One batch can produce many principal movements.
type TransferMode string

const (
	TransferModeSingle TransferMode = "SINGLE"
	TransferModeBatch  TransferMode = "BATCH"
)

func (m TransferMode) Valid() bool {
	return m == TransferModeSingle || m == TransferModeBatch
}

// MovementKind describes the ledger semantics of one observed balance change.
type MovementKind string

const (
	MovementKindPrincipal  MovementKind = "PRINCIPAL"
	MovementKindFee        MovementKind = "FEE"
	MovementKindReversal   MovementKind = "REVERSAL"
	MovementKindAdjustment MovementKind = "ADJUSTMENT"
	MovementKindConversion MovementKind = "CONVERSION"
)

func (k MovementKind) Valid() bool {
	switch k {
	case MovementKindPrincipal, MovementKindFee, MovementKindReversal, MovementKindAdjustment, MovementKindConversion:
		return true
	default:
		return false
	}
}

// Direction carries the economic direction; amounts are always non-negative
// magnitudes.
type Direction string

const (
	DirectionInflow           Direction = "INFLOW"
	DirectionOutflow          Direction = "OUTFLOW"
	DirectionInternalTransfer Direction = "INTERNAL_TRANSFER"
)

func (d Direction) Valid() bool {
	return d == DirectionInflow || d == DirectionOutflow || d == DirectionInternalTransfer
}

// ConversionLeg identifies an observed side of a provider conversion. It is
// never synthesized from enrichment-only data.
type ConversionLeg string

const (
	ConversionLegBuy  ConversionLeg = "BUY"
	ConversionLegSell ConversionLeg = "SELL"
)

func (l ConversionLeg) Valid() bool {
	return l == ConversionLegBuy || l == ConversionLegSell
}

// ConversionGroupState makes a single observed conversion leg explicitly
// incomplete instead of fabricating a missing counterpart.
type ConversionGroupState string

const (
	ConversionGroupComplete   ConversionGroupState = "COMPLETE"
	ConversionGroupIncomplete ConversionGroupState = "INCOMPLETE"
)

func (s ConversionGroupState) Valid() bool {
	return s == ConversionGroupComplete || s == ConversionGroupIncomplete
}

// AssetIdentity uses provider/chain/contract identity rather than a display
// symbol alone. Empty optional components remain explicit in a canonical key.
type AssetIdentity struct {
	Currency         string
	ChainCode        string
	ProviderAssetKey string
	ContractAddress  string
}

// MovementIdentityInput is the deterministic fallback identity tuple. The
// provider's own stable movement/line ID is preferred by adapters; this tuple
// handles normal and batch records that do not have one.
type MovementIdentityInput struct {
	Channel          Channel
	ProviderParentID string
	MovementKind     MovementKind
	Asset            AssetIdentity
	NormalizedFrom   string
	NormalizedTo     string
	Amount           decimal.Decimal
	Occurrence       int
}

// MovementIdentity is the persisted versioned movement key and its audit
// components. Digest is a fixed SHA-256 hex string; Key includes the algorithm
// version to make future algorithm migration explicit.
type MovementIdentity struct {
	AlgorithmVersion string
	Digest           string
	Key              string
	Occurrence       int
	Input            MovementIdentityInput
}

// MovementRelation captures only structural linkage. It is kept separate from
// provider parsing so invalid fee/reversal/conversion structures can be
// quarantined before storage.
type MovementRelation struct {
	MovementKind          MovementKind
	TransferMode          TransferMode
	Direction             Direction
	HasFromAccount        bool
	HasToAccount          bool
	ParentMovementKey     string
	ReversalOfMovementKey string
	ConversionGroupKey    string
	ConversionLeg         ConversionLeg
	ConversionGroupState  ConversionGroupState
}

// LifecycleStatus is provider-owned status text normalized to uppercase.
type LifecycleStatus string

const (
	LifecycleStatusPending      LifecycleStatus = "PENDING"
	LifecycleStatusSubmitted    LifecycleStatus = "SUBMITTED"
	LifecycleStatusSigning      LifecycleStatus = "SIGNING"
	LifecycleStatusProcessing   LifecycleStatus = "PROCESSING"
	LifecycleStatusBroadcasting LifecycleStatus = "BROADCASTING"
	LifecycleStatusConfirming   LifecycleStatus = "CONFIRMING"
	LifecycleStatusCompleted    LifecycleStatus = "COMPLETED"
	LifecycleStatusPaid         LifecycleStatus = "PAID"
	LifecycleStatusFailed       LifecycleStatus = "FAILED"
	LifecycleStatusCancelled    LifecycleStatus = "CANCELLED"
	LifecycleStatusRejected     LifecycleStatus = "REJECTED"
	LifecycleStatusReversed     LifecycleStatus = "REVERSED"
)

// LifecycleDisposition tells callers whether a status update changes the row,
// is intentionally ignored, or must be quarantined for review.
type LifecycleDisposition string

const (
	LifecycleDispositionApply      LifecycleDisposition = "APPLY"
	LifecycleDispositionKeep       LifecycleDisposition = "KEEP"
	LifecycleDispositionQuarantine LifecycleDisposition = "QUARANTINE"
)

// LifecycleDecision separates terminal/out-of-order behavior from provider
// field enrichment. A KEEP decision may still permit a later field merge.
type LifecycleDecision struct {
	Disposition LifecycleDisposition
	Status      LifecycleStatus
	Reason      string
}

// LifecyclePolicy is deliberately channel-specific. Callers must obtain it by
// Channel through LifecyclePolicyFor rather than applying one provider's rules
// to the other provider's statuses.
type LifecyclePolicy interface {
	Channel() Channel
	Transition(current, incoming LifecycleStatus) LifecycleDecision
}

// ProviderFactSource sets a deterministic precedence for equal provider
// revisions: reconciliation facts outrank product-detail facts, which outrank
// Webhook facts.
type ProviderFactSource string

const (
	ProviderSourceWebhook        ProviderFactSource = "WEBHOOK"
	ProviderSourceProductDetail  ProviderFactSource = "PRODUCT_DETAIL"
	ProviderSourceReconciliation ProviderFactSource = "RECONCILIATION"
)

// ProviderFactMetadata is attached to a coherent set of provider-owned fields.
// Nil revision/time means that provider did not supply that ordering dimension.
type ProviderFactMetadata struct {
	Revision  *int64
	UpdatedAt *time.Time
	Source    ProviderFactSource
}

// ProviderOwnedFields are the fields a provider adapter may propose. Every
// optional field uses a pointer so missing data cannot erase an existing fact.
type ProviderOwnedFields struct {
	Metadata    ProviderFactMetadata
	Amount      *decimal.Decimal
	Currency    *string
	Asset       *AssetIdentity
	TxHash      *string
	Status      *LifecycleStatus
	OccurredAt  *time.Time
	CompletedAt *time.Time
}

// ManualFields are finance/risk-review fields. They are intentionally absent
// from ProviderOwnedFields and must survive all automatic provider merges.
type ManualFields struct {
	FinanceCategoryLevel1ID  int64
	FinanceCategoryLevel2ID  int64
	IsOperatingCashflow      *bool
	Applicant                string
	BusinessDescription      string
	ClassificationRemark     string
	ClassifiedBy             string
	ClassifiedAt             *time.Time
	SummaryInclusionOverride *bool
	RiskOverrideReason       string
	RiskOverrideBy           string
	RiskOverrideAt           *time.Time
	RiskReviewCompletedAt    *time.Time
}

// MovementState joins provider and manual fields to make ownership boundaries
// explicit in a merge operation.
type MovementState struct {
	Provider ProviderOwnedFields
	Manual   ManualFields
}

// CompanyFundMovement is the exact-decimal canonical movement used by
// normalizers before it is persisted. Direction carries any economic sign;
// Amount and all USD fields remain non-negative magnitudes.
type CompanyFundMovement struct {
	Identity              MovementIdentity
	Channel               Channel
	MovementKind          MovementKind
	TransferMode          TransferMode
	Direction             Direction
	Amount                decimal.Decimal
	Asset                 AssetIdentity
	FromAccountID         *int64
	ToAccountID           *int64
	ParentMovementKey     string
	ReversalOfMovementKey string
	ConversionGroupKey    string
	ConversionLeg         ConversionLeg
	ConversionGroupState  ConversionGroupState
	ProviderReportedUSD   *decimal.Decimal
	USDValuation          USDValuationResult
	Provider              ProviderOwnedFields
	Manual                ManualFields
}

// AccountAssetPolicy carries the applied provider/chain/contract mapping and
// risk settings. It can be snapshotted onto a CompanyFundMovement without
// consulting current account configuration later.
type AccountAssetPolicy struct {
	ID                         int64
	AccountID                  int64
	Asset                      AssetIdentity
	Dust                       DustPolicy
	AutoExcludeDustFromSummary bool
	// CoinGeckoID is either an explicit CoinGecko coin ID or the reserved
	// explicit fiat mapping `fiat:<ISO-like code>` (for example `fiat:JPY`).
	// The fiat code must equal Asset.Currency and is priced only from a
	// provider-declared fiat exchange-rate entry, never from ticker inference.
	CoinGeckoID              string
	CoinGeckoPlatformID      string
	CoinGeckoContractAddress string
	Enabled                  bool
}

type MergeOutcome string

const (
	MergeOutcomeUnchanged  MergeOutcome = "UNCHANGED"
	MergeOutcomeApplied    MergeOutcome = "APPLIED"
	MergeOutcomeQuarantine MergeOutcome = "QUARANTINE"
)

// MergeDecision reports why a provider fact was accepted, retained, or sent to
// review. Quarantine never mutates the existing movement state.
type MergeDecision struct {
	Outcome MergeOutcome
	Reason  string
}

// DustPolicy is an account/asset policy snapshot used only for automatic risk
// classification. A nil Threshold or disabled policy does not classify dust.
type DustPolicy struct {
	ID        int64
	Enabled   bool
	Threshold *decimal.Decimal
}

type AMLRiskLevel string

const (
	AMLRiskLevelUnknown  AMLRiskLevel = "UNKNOWN"
	AMLRiskLevelLow      AMLRiskLevel = "LOW"
	AMLRiskLevelMedium   AMLRiskLevel = "MEDIUM"
	AMLRiskLevelHigh     AMLRiskLevel = "HIGH"
	AMLRiskLevelCritical AMLRiskLevel = "CRITICAL"
)

type RiskFlag string

const (
	RiskFlagDust                RiskFlag = "DUST"
	RiskFlagSourcePhishing      RiskFlag = "SOURCE_PHISHING"
	RiskFlagDestinationPhishing RiskFlag = "DESTINATION_PHISHING"
	RiskFlagUnrecognizedAsset   RiskFlag = "UNRECOGNIZED_ASSET"
	RiskFlagZeroAmount          RiskFlag = "ZERO_AMOUNT"
	RiskFlagAMLLock             RiskFlag = "AML_LOCK"
	RiskFlagAMLHigh             RiskFlag = "AML_HIGH_RISK"
	RiskFlagAMLCritical         RiskFlag = "AML_CRITICAL"
)

// RiskInput contains provider results as pointers because absent/unknown and
// false must remain distinguishable in the ledger.
type RiskInput struct {
	Channel             Channel
	Direction           Direction
	Amount              decimal.Decimal
	Asset               AssetIdentity
	Policy              DustPolicy
	SourcePhishing      *bool
	DestinationPhishing *bool
	AMLLock             *bool
	AMLRiskLevel        AMLRiskLevel
	UnrecognizedAsset   bool
	SummaryOverride     *bool
	ConfiguredFromID    *int64
	ConfiguredToID      *int64
}

// RiskAssessment is a provider-neutral result. It does not change
// IsOperatingCashflow or other manual fields.
type RiskAssessment struct {
	PolicySubjectAccountID   *int64
	IsDust                   bool
	ReviewRequired           bool
	AutomaticExclusion       bool
	EffectiveSummaryIncluded bool
	ImmediateAlert           bool
	AlertAggregationKey      string
	Flags                    []RiskFlag
}

// ProviderValueScope distinguishes an observed movement value from a parent or
// group total. Parent/group totals are durable audit facts but are not copied to
// a child without a proven allocation contract.
type ProviderValueScope string

const (
	ProviderValueScopeTransactionTotal  ProviderValueScope = "TRANSACTION_TOTAL"
	ProviderValueScopeDirectItem        ProviderValueScope = "DIRECT_ITEM"
	ProviderValueScopeConversionGroup   ProviderValueScope = "CONVERSION_GROUP"
	ProviderValueScopeDerivedFromParent ProviderValueScope = "DERIVED_FROM_PARENT"
)

// ProviderFactAllocationState records whether a provider-level total can be
// allocated to a movement. A transaction/group fact is retained even when an
// allocation has not been proven; callers must not turn UNPROVEN totals into
// child USD values.
type ProviderFactAllocationState string

const (
	ProviderFactAllocationStateNotApplicable   ProviderFactAllocationState = "NOT_APPLICABLE"
	ProviderFactAllocationStateUnproven        ProviderFactAllocationState = "UNPROVEN"
	ProviderFactAllocationStateProvenDerivable ProviderFactAllocationState = "PROVEN_DERIVABLE"
)

type USDValuationSource string

const (
	USDValuationSourceUSDPar    USDValuationSource = "USD_PAR"
	USDValuationSourceSafeheron USDValuationSource = "SAFEHERON"
	USDValuationSourceAirwallex USDValuationSource = "AIRWALLEX"
	USDValuationSourceCoinGecko USDValuationSource = "COINGECKO"
)

type USDValuationMethod string

const (
	USDValuationMethodUSDPar              USDValuationMethod = "USD_PAR"
	USDValuationMethodProviderTransaction USDValuationMethod = "PROVIDER_TRANSACTION_TIME"
	USDValuationMethodProviderConversion  USDValuationMethod = "PROVIDER_CONVERSION"
	USDValuationMethodCoinGeckoDirect     USDValuationMethod = "COINGECKO_DIRECT"
	USDValuationMethodCoinGeckoBTCCross   USDValuationMethod = "COINGECKO_BTC_CROSS"
)

type USDValuationStatus string

const (
	USDValuationStatusFinal       USDValuationStatus = "FINAL"
	USDValuationStatusProvisional USDValuationStatus = "PROVISIONAL"
	USDValuationStatusUnpriced    USDValuationStatus = "UNPRICED"
	USDValuationStatusStale       USDValuationStatus = "STALE"
)

// USDValuationBasis records whether the selected value is tied to the
// provider/chain transaction time or is only a current-cache ingestion-time
// approximation.
type USDValuationBasis string

const (
	USDValuationBasisTransactionTime USDValuationBasis = "TRANSACTION_TIME"
	USDValuationBasisIngestionTime   USDValuationBasis = "INGESTION_TIME"
)

// MarketPriceKind distinguishes a historical observation from a current
// response/cache snapshot. Their eligibility and BTC-cross group rules differ.
type MarketPriceKind string

const (
	MarketPriceKindCurrent    MarketPriceKind = "CURRENT"
	MarketPriceKindHistorical MarketPriceKind = "HISTORICAL"
)

func (k MarketPriceKind) Valid() bool {
	return k == MarketPriceKindCurrent || k == MarketPriceKindHistorical
}

type USDValuationReason string

const (
	USDValuationReasonNone                  USDValuationReason = ""
	USDValuationReasonMappingMissing        USDValuationReason = "MAPPING_MISSING"
	USDValuationReasonRateMissing           USDValuationReason = "RATE_MISSING"
	USDValuationReasonHistoricalGap         USDValuationReason = "HISTORICAL_GAP"
	USDValuationReasonCacheStale            USDValuationReason = "CACHE_STALE"
	USDValuationReasonQuotaExhausted        USDValuationReason = "QUOTA_EXHAUSTED"
	USDValuationReasonLicenseDisabled       USDValuationReason = "LICENSE_DISABLED"
	USDValuationReasonProviderError         USDValuationReason = "PROVIDER_ERROR"
	USDValuationReasonUnprovenProviderScope USDValuationReason = "UNPROVEN_PROVIDER_SCOPE"
	USDValuationReasonNonUSDCross           USDValuationReason = "NON_USD_CROSS"
	USDValuationReasonZeroAmount            USDValuationReason = "ZERO_AMOUNT"
	USDValuationReasonMissingValuationTime  USDValuationReason = "MISSING_VALUATION_TIME"
	USDValuationReasonRevaluationPending    USDValuationReason = "REVALUATION_PENDING"
)

// USDValuationInput separates provider reported value from market pricing. All
// timestamps are caller-provided; the domain never invents a wall-clock time because
// the selected valuation must be reproducible in audit and revaluation jobs.
type USDValuationInput struct {
	Channel                 Channel
	MovementKind            MovementKind
	Currency                string
	Amount                  decimal.Decimal
	ProviderReportedUSD     *decimal.Decimal
	ProviderValueScope      ProviderValueScope
	ProviderScopeProven     bool
	AirwallexConversionFrom string
	AirwallexConversionTo   string
	CoinGeckoUnitPrice      *decimal.Decimal
	CoinGeckoPriceKind      MarketPriceKind
	CoinGeckoPriceAt        *time.Time
	CoinGeckoEffectiveAt    *time.Time
	CoinGeckoAvailableAt    *time.Time
	CoinGeckoGranularity    string
	ValuationTargetAt       *time.Time
	AvailableAtCutoffAt     *time.Time
	HistoricalMaxGap        time.Duration
	IngestionAt             *time.Time
}

type USDValuationResult struct {
	Value               *decimal.Decimal
	UnitPrice           decimal.Decimal
	ProviderReportedUSD *decimal.Decimal
	Source              USDValuationSource
	Method              USDValuationMethod
	Status              USDValuationStatus
	Reason              USDValuationReason
	Basis               USDValuationBasis
	ValuationTargetAt   *time.Time
	PriceAt             *time.Time
	EffectiveAt         *time.Time
	AvailableAt         *time.Time
	Granularity         string
}

// HistoricalPricePoint is a provider observation. A point after the target is
// never eligible, even if it is closer than a valid prior point.
type HistoricalPricePoint struct {
	Price       decimal.Decimal
	PriceAt     time.Time
	AvailableAt time.Time
}

// BTCCrossLeg is one eligible Bitcoin quote snapshot used to derive a
// fiat-to-USD rate. Current inputs must share a response group; historical
// inputs may come from independent requests in the same normalized series.
type BTCCrossLeg struct {
	Price           decimal.Decimal
	PriceAt         time.Time
	AvailableAt     time.Time
	Provider        string
	AssetID         string
	Quote           string
	PolicyVersion   string
	Granularity     string
	BucketStart     time.Time
	IsEligibleLeaf  bool
	IsFinal         bool
	PriceKind       MarketPriceKind
	SnapshotGroupID string
}

type BTCCrossResult struct {
	UnitPrice       decimal.Decimal
	PriceAt         time.Time
	AvailableAt     time.Time
	IsFinal         bool
	PriceKind       MarketPriceKind
	SnapshotGroupID string
}

// The following record models are intentionally transport-neutral. Repository
// and adapter packages can add persistence tags later without making domain
// rules depend on SQL or HTTP concerns.

type CompanyFundAccount struct {
	ID                 int64
	Channel            Channel
	ProviderAccountKey string
	WalletAddress      string
	NormalizedAddress  string
	NetworkFamily      string
	CompanyEntity      string
	FundAccountName    string
	SubAccountName     string
	AccountType        string
	AccountName        string
	AccountRole        string
	Enabled            bool
}

type AccountSnapshot struct {
	AccountID       int64
	CompanyEntity   string
	FundAccountName string
	SubAccountName  string
	AccountType     string
}

type CompanyFundProviderEvent struct {
	ID                      int64
	Channel                 Channel
	ProviderEventID         string
	EventType               string
	ProviderAccountKey      string
	PayloadDigest           string
	SafeheronWebhookEventID *int
	ReceivedAt              time.Time
}

type ProviderTransactionFact struct {
	ID                        int64
	Channel                   Channel
	ProviderAccountKey        string
	ProviderTransactionID     string
	ProviderGroupID           string
	FactIdentityKey           string
	FactVersion               int
	SourceEventID             int64
	SourcePayloadDigest       string
	ProviderOccurredAt        *time.Time
	ProviderAmount            *decimal.Decimal
	ProviderCurrency          string
	ProviderReportedUSD       *decimal.Decimal
	ConversionFromCurrency    string
	ConversionToCurrency      string
	ConversionRate            *decimal.Decimal
	ConversionBuyAmount       *decimal.Decimal
	ConversionSellAmount      *decimal.Decimal
	ValueScope                ProviderValueScope
	AllocationState           ProviderFactAllocationState
	DerivationContractVersion string
	ProviderExtrasJSON        []byte
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type SyncRun struct {
	ID          int64
	Channel     Channel
	WindowStart time.Time
	WindowEnd   time.Time
	Status      string
	Attempt     int
	Cursor      string
}

type RateSnapshot struct {
	ID                    int64
	Provider              string
	AssetIdentityKey      string
	QuoteCurrency         string
	Rate                  decimal.Decimal
	EffectiveAt           time.Time
	AvailableAt           time.Time
	BucketStart           time.Time
	PolicyVersion         string
	IsEligibleLeaf        bool
	NumeratorSnapshotID   *int64
	DenominatorSnapshotID *int64
}

type RateRequest struct {
	ID                int64
	Provider          string
	LogicalRequestKey string
	AttemptNo         int
	BudgetPeriodID    int64
	State             string
	LeaseOwner        string
	LeaseExpiresAt    *time.Time
}

type RateBudgetPeriod struct {
	ID            int64
	Provider      string
	BillingAnchor time.Time
	PeriodKey     string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	CallLimit     int
	ReservedCalls int
	UsedCalls     int
}

type TransactionValuationHistory struct {
	ID                    int64
	TransactionID         int64
	ValuationVersion      int64
	Value                 *decimal.Decimal
	Status                USDValuationStatus
	DependencyFingerprint string
	AppliedAt             time.Time
}

type ValuationJob struct {
	ID                                   int64
	TransactionID                        int64
	TargetDependencyFingerprint          string
	PolicyVersion                        string
	ExpectedCurrentState                 ValuationCurrentStateExpectation
	ExpectedCurrentHistoryID             *int64
	ExpectedCurrentDependencyFingerprint string
	State                                string
	AttemptCount                         int
	LeaseOwner                           string
	LeaseExpiresAt                       *time.Time
}
