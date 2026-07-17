package fundrouting

import (
	"fmt"
	"strings"
	"time"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/safeheron"
)

func BuildCandidates(snapshot safeheron.TransactionSnapshot, networkFamily string) ([]Candidate, error) {
	family := normalizeNetworkFamily(networkFamily)
	if family == "" {
		return nil, fmt.Errorf("Safeheron routing network family is required")
	}
	occurrences, err := companyfund.EnumerateSafeheronPrincipalOccurrences(snapshot, family)
	if err != nil {
		return nil, err
	}
	effectiveTime, timeSource := providerEventTime(snapshot)
	result := make([]Candidate, 0, len(occurrences))
	for _, occurrence := range occurrences {
		result = append(result, Candidate{
			RoutingIdentityKey:          occurrence.Occurrence.Key,
			IdentityAlgorithmVersion:    occurrence.Occurrence.AlgorithmVersion,
			ProviderTransactionGroupKey: strings.TrimSpace(snapshot.TxKey),
			SafeheronTxKey:              strings.TrimSpace(snapshot.TxKey),
			RawCoinKey:                  snapshot.CoinKey,
			NetworkFamily:               family,
			Direction:                   strings.ToUpper(strings.TrimSpace(snapshot.TransactionDirection)),
			Occurrence:                  occurrence,
			EffectiveEventTime:          effectiveTime,
			EventTimeSource:             timeSource,
		})
	}
	return result, nil
}

func AutomaticCustomerAdmission(eventType string, snapshot safeheron.TransactionSnapshot) Admission {
	switch strings.ToUpper(strings.TrimSpace(eventType)) {
	case "TRANSACTION_CREATED":
		return Admission{ProjectionDeferred: true, Reason: "transaction-created is not a final financial fact"}
	case "TRANSACTION_STATUS_CHANGED":
	default:
		return Admission{Reason: "event type is not a Safeheron transaction event"}
	}
	if !isTerminalRoutingStatus(snapshot.TransactionStatus) {
		return Admission{ProjectionDeferred: true, Reason: "transaction status is not terminal"}
	}
	if strings.ToUpper(strings.TrimSpace(snapshot.TransactionDirection)) != "INFLOW" {
		return Admission{Reason: "transaction direction is not INFLOW"}
	}
	return Admission{Allowed: true}
}

func providerEventTime(snapshot safeheron.TransactionSnapshot) (*time.Time, string) {
	if snapshot.CreateTime <= 0 {
		return nil, ""
	}
	value := time.UnixMilli(snapshot.CreateTime).UTC()
	return &value, EventTimeSourceProviderCreateTime
}

func Decide(input DecisionInput) DecisionResult {
	if input.CustomerAdmission.ProjectionDeferred {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonStatusNotTerminal}
	}
	customer := assignedCustomer(input.DestinationOwner)
	company := enabledCompany(input.SourceOwner, input.DestinationOwner)

	if customer != nil && company != nil {
		return decideCrossDomain(input, customer, company)
	}
	if customer != nil {
		return decideCustomer(input, customer)
	}
	if input.DestinationOwner != nil && input.DestinationOwner.Kind == OwnerKindCustomerPool {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonCustomerPoolUnassigned}
	}
	if company != nil {
		return decideCompany(input, company)
	}
	if disabledCompany(input.SourceOwner, input.DestinationOwner) {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonCompanyAccountDisabled}
	}
	return DecisionResult{Decision: DecisionOpen, Reason: ReasonOwnershipUnknown}
}

func isTerminalRoutingStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETED", "FAILED", "CANCELLED", "REJECTED":
		return true
	default:
		return false
	}
}

func decideCustomer(input DecisionInput, owner *Ownership) DecisionResult {
	if input.Candidate.Occurrence.TransferMode == companyfund.TransferModeBatch {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonCustomerBatchUnsupported}
	}
	if input.Candidate.EffectiveEventTime == nil {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonEventTimeUnverified}
	}
	if input.Candidate.EffectiveEventTime.Before(owner.EffectiveFrom) {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonBeforeCustomerAssignment}
	}
	if !input.CustomerAdmission.Allowed {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonCustomerAdmissionRejected}
	}
	return DecisionResult{
		Decision:                   DecisionCustomer,
		Reason:                     ReasonCustomerMatched,
		RequiresCustomerProjection: true,
		CustomerUserID:             owner.UserID,
	}
}

func decideCompany(input DecisionInput, owner *Ownership) DecisionResult {
	if input.Candidate.EffectiveEventTime == nil {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonEventTimeUnverified}
	}
	if input.Candidate.EffectiveEventTime.Before(owner.EffectiveFrom) {
		return DecisionResult{Decision: DecisionOpen, Reason: ReasonBeforeCompanyMonitoring}
	}
	return DecisionResult{
		Decision:                  DecisionCompany,
		Reason:                    ReasonCompanyMatched,
		RequiresCompanyProjection: true,
		CompanyFundAccountID:      owner.CompanyFundAccountID,
	}
}

func decideCrossDomain(input DecisionInput, customer, company *Ownership) DecisionResult {
	companyDecision := decideCompany(input, company)
	if companyDecision.Decision != DecisionCompany {
		return companyDecision
	}
	customerDecision := decideCustomer(input, customer)
	if customerDecision.Decision != DecisionCustomer || !input.CustomerAdmission.CrossDomainAllowed {
		companyDecision.Decision = DecisionPartial
		companyDecision.Reason = ReasonCrossDomainReviewRequired
		return companyDecision
	}
	return DecisionResult{
		Decision:                   DecisionDual,
		Reason:                     ReasonCrossDomainReviewRequired,
		RequiresCustomerProjection: true,
		RequiresCompanyProjection:  true,
		CustomerUserID:             customer.UserID,
		CompanyFundAccountID:       company.CompanyFundAccountID,
	}
}

func assignedCustomer(owner *Ownership) *Ownership {
	if owner != nil && owner.Kind == OwnerKindCustomerPool && owner.UserID != nil {
		return owner
	}
	return nil
}

func enabledCompany(owners ...*Ownership) *Ownership {
	for _, owner := range owners {
		if owner != nil && owner.Kind == OwnerKindCompanyAccount && owner.CompanyFundAccountID != nil && owner.Enabled {
			return owner
		}
	}
	return nil
}

func disabledCompany(owners ...*Ownership) bool {
	for _, owner := range owners {
		if owner != nil && owner.Kind == OwnerKindCompanyAccount && !owner.Enabled {
			return true
		}
	}
	return false
}
