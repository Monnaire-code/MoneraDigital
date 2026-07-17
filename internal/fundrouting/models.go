package fundrouting

import (
	"strings"
	"time"

	"monera-digital/internal/companyfund"
)

type Decision string

const (
	DecisionOpen        Decision = "OPEN"
	DecisionPartial     Decision = "PARTIAL"
	DecisionCustomer    Decision = "CUSTOMER"
	DecisionCompany     Decision = "COMPANY"
	DecisionDual        Decision = "DUAL"
	DecisionNotRelevant Decision = "NOT_RELEVANT"
	DecisionDismissed   Decision = "DISMISSED"
)

type ReasonCode string

const (
	ReasonOwnershipUnknown          ReasonCode = "OWNERSHIP_UNKNOWN"
	ReasonCustomerPoolUnassigned    ReasonCode = "CUSTOMER_POOL_UNASSIGNED"
	ReasonCustomerBatchUnsupported  ReasonCode = "CUSTOMER_BATCH_UNSUPPORTED"
	ReasonEventTimeUnverified       ReasonCode = "EVENT_TIME_UNVERIFIED"
	ReasonBeforeCustomerAssignment  ReasonCode = "BEFORE_CUSTOMER_ASSIGNMENT"
	ReasonBeforeCompanyMonitoring   ReasonCode = "BEFORE_COMPANY_MONITORING"
	ReasonCompanyAccountDisabled    ReasonCode = "COMPANY_ACCOUNT_DISABLED"
	ReasonCustomerAdmissionRejected ReasonCode = "CUSTOMER_ADMISSION_REJECTED"
	ReasonCustomerMatched           ReasonCode = "CUSTOMER_MATCHED"
	ReasonCompanyMatched            ReasonCode = "COMPANY_MATCHED"
	ReasonCrossDomainReviewRequired ReasonCode = "CROSS_DOMAIN_REVIEW_REQUIRED"
	ReasonStatusNotTerminal         ReasonCode = "STATUS_NOT_TERMINAL"
)

type OwnerKind string

const (
	OwnerKindCustomerPool   OwnerKind = "CUSTOMER_POOL"
	OwnerKindCompanyAccount OwnerKind = "COMPANY_ACCOUNT"
)

const EventTimeSourceProviderCreateTime = "PROVIDER_CREATE_TIME"

type Candidate struct {
	RoutingIdentityKey          string
	IdentityAlgorithmVersion    string
	ProviderTransactionGroupKey string
	SafeheronTxKey              string
	RawCoinKey                  string
	NetworkFamily               string
	Direction                   string
	Occurrence                  companyfund.SafeheronPrincipalOccurrence
	EffectiveEventTime          *time.Time
	EventTimeSource             string
}

type Ownership struct {
	Kind                 OwnerKind
	UserID               *int
	CompanyFundAccountID *int64
	EffectiveFrom        time.Time
	Enabled              bool
}

type Admission struct {
	Allowed            bool
	CrossDomainAllowed bool
	ProjectionDeferred bool
	Reason             string
}

type DecisionInput struct {
	Candidate         Candidate
	SourceOwner       *Ownership
	DestinationOwner  *Ownership
	CustomerAdmission Admission
}

type DecisionResult struct {
	Decision                   Decision
	Reason                     ReasonCode
	RequiresCustomerProjection bool
	RequiresCompanyProjection  bool
	CustomerUserID             *int
	CompanyFundAccountID       *int64
}

func normalizeNetworkFamily(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
