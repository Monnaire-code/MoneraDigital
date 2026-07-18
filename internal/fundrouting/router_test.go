package fundrouting

import (
	"testing"
	"time"

	"monera-digital/internal/companyfund"
	"monera-digital/internal/safeheron"
)

func TestBuildCandidatesPreservesConfigurationIndependentOccurrences(t *testing.T) {
	snapshot := routingSnapshot()
	snapshot.CoinKey = "UNKNOWN_ERC20"
	snapshot.DestinationAddress = ""
	snapshot.DestinationAddressList = []safeheron.TransactionDestinationAddress{
		{Address: "0xB", Amount: "1"},
		{Address: "0xA", Amount: "1"},
		{Address: "0xA", Amount: "1"},
	}

	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("candidate count = %d, want 3", len(candidates))
	}
	if candidates[0].Occurrence.DuplicateOrdinal != 0 || candidates[1].Occurrence.DuplicateOrdinal != 1 {
		t.Fatalf("duplicate ordinals = %d / %d", candidates[0].Occurrence.DuplicateOrdinal, candidates[1].Occurrence.DuplicateOrdinal)
	}
	if candidates[0].EffectiveEventTime == nil || candidates[0].EventTimeSource != EventTimeSourceProviderCreateTime {
		t.Fatalf("event time = %#v / %q", candidates[0].EffectiveEventTime, candidates[0].EventTimeSource)
	}
	if candidates[0].RoutingIdentityKey != candidates[0].Occurrence.Occurrence.Key {
		t.Fatalf("routing identity = %q, occurrence = %q", candidates[0].RoutingIdentityKey, candidates[0].Occurrence.Occurrence.Key)
	}
}

func TestBuildCandidatesLeavesMissingProviderBusinessTimeUnverified(t *testing.T) {
	snapshot := routingSnapshot()
	snapshot.CreateTime = 0
	snapshot.CompletedTime = 0

	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil {
		t.Fatalf("BuildCandidates: %v", err)
	}
	if candidates[0].EffectiveEventTime != nil || candidates[0].EventTimeSource != "" {
		t.Fatalf("unverified event time = %#v / %q", candidates[0].EffectiveEventTime, candidates[0].EventTimeSource)
	}
}

func TestDecideFailsClosedForOwnershipAndTimeBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	base := Candidate{Occurrence: companyfund.SafeheronPrincipalOccurrence{TransferMode: companyfund.TransferModeSingle}}
	base.EffectiveEventTime = &now
	base.Direction = "INFLOW"

	tests := []struct {
		name     string
		input    DecisionInput
		decision Decision
		reason   ReasonCode
		customer bool
		company  bool
	}{
		{
			name:     "unassigned customer pool",
			input:    DecisionInput{Candidate: base, DestinationOwner: &Ownership{Kind: OwnerKindCustomerPool}},
			decision: DecisionOpen, reason: ReasonCustomerPoolUnassigned,
		},
		{
			name:     "assigned customer after event",
			input:    DecisionInput{Candidate: base, DestinationOwner: &Ownership{Kind: OwnerKindCustomerPool, UserID: intPointer(42), EffectiveFrom: now.Add(time.Minute)}, CustomerAdmission: Admission{Allowed: true}},
			decision: DecisionOpen, reason: ReasonBeforeCustomerAssignment,
		},
		{
			name:     "assigned customer accepted",
			input:    DecisionInput{Candidate: base, DestinationOwner: &Ownership{Kind: OwnerKindCustomerPool, UserID: intPointer(42), EffectiveFrom: now.Add(-time.Minute)}, CustomerAdmission: Admission{Allowed: true}},
			decision: DecisionCustomer, reason: ReasonCustomerMatched, customer: true,
		},
		{
			name:     "company accepted",
			input:    DecisionInput{Candidate: base, DestinationOwner: &Ownership{Kind: OwnerKindCompanyAccount, CompanyFundAccountID: int64Pointer(7), Enabled: true, EffectiveFrom: now.Add(-time.Minute)}},
			decision: DecisionCompany, reason: ReasonCompanyMatched, company: true,
		},
		{
			name:     "disabled company remains open",
			input:    DecisionInput{Candidate: base, DestinationOwner: &Ownership{Kind: OwnerKindCompanyAccount, CompanyFundAccountID: int64Pointer(7), EffectiveFrom: now.Add(-time.Minute)}},
			decision: DecisionOpen, reason: ReasonCompanyAccountDisabled,
		},
		{
			name:     "unknown ownership",
			input:    DecisionInput{Candidate: base},
			decision: DecisionOpen, reason: ReasonOwnershipUnknown,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Decide(test.input)
			if result.Decision != test.decision || result.Reason != test.reason || result.RequiresCustomerProjection != test.customer || result.RequiresCompanyProjection != test.company {
				t.Fatalf("decision = %#v", result)
			}
		})
	}
}

func TestDecideRejectsCustomerBatchAndUnverifiedBoundary(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	batch := Candidate{Occurrence: companyfund.SafeheronPrincipalOccurrence{TransferMode: companyfund.TransferModeBatch}, Direction: "INFLOW", EffectiveEventTime: &now}
	owner := &Ownership{Kind: OwnerKindCustomerPool, UserID: intPointer(42), EffectiveFrom: now.Add(-time.Hour)}
	result := Decide(DecisionInput{Candidate: batch, DestinationOwner: owner, CustomerAdmission: Admission{Allowed: true}})
	if result.Decision != DecisionOpen || result.Reason != ReasonCustomerBatchUnsupported {
		t.Fatalf("batch decision = %#v", result)
	}

	unverified := batch
	unverified.Occurrence.TransferMode = companyfund.TransferModeSingle
	unverified.EffectiveEventTime = nil
	result = Decide(DecisionInput{Candidate: unverified, DestinationOwner: owner, CustomerAdmission: Admission{Allowed: true}})
	if result.Decision != DecisionOpen || result.Reason != ReasonEventTimeUnverified {
		t.Fatalf("unverified decision = %#v", result)
	}
}

func TestDecideCrossDomainPreservesCompanyProjectionWhenCustomerCannotApply(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	input := DecisionInput{
		Candidate: Candidate{
			Occurrence:         companyfund.SafeheronPrincipalOccurrence{TransferMode: companyfund.TransferModeSingle},
			Direction:          "OUTFLOW",
			EffectiveEventTime: &now,
		},
		SourceOwner:       &Ownership{Kind: OwnerKindCompanyAccount, CompanyFundAccountID: int64Pointer(7), Enabled: true, EffectiveFrom: now.Add(-time.Hour)},
		DestinationOwner:  &Ownership{Kind: OwnerKindCustomerPool, UserID: intPointer(42), EffectiveFrom: now.Add(-time.Hour)},
		CustomerAdmission: Admission{Allowed: false, Reason: "direction is not INFLOW"},
	}

	result := Decide(input)
	if result.Decision != DecisionPartial || result.Reason != ReasonCrossDomainReviewRequired ||
		result.RequiresCustomerProjection || !result.RequiresCompanyProjection || result.CompanyFundAccountID == nil {
		t.Fatalf("cross-domain decision = %#v", result)
	}
}

func TestDecideCrossDomainDefaultsToCompanyOnlyUntilDirectionSemanticsAreProven(t *testing.T) {
	now := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	input := DecisionInput{
		Candidate: Candidate{
			Occurrence:         companyfund.SafeheronPrincipalOccurrence{TransferMode: companyfund.TransferModeSingle},
			Direction:          "INFLOW",
			EffectiveEventTime: &now,
		},
		SourceOwner:       &Ownership{Kind: OwnerKindCompanyAccount, CompanyFundAccountID: int64Pointer(7), Enabled: true, EffectiveFrom: now.Add(-time.Hour)},
		DestinationOwner:  &Ownership{Kind: OwnerKindCustomerPool, UserID: intPointer(42), EffectiveFrom: now.Add(-time.Hour)},
		CustomerAdmission: Admission{Allowed: true},
	}

	result := Decide(input)
	if result.Decision != DecisionPartial || result.RequiresCustomerProjection || !result.RequiresCompanyProjection {
		t.Fatalf("unproven cross-domain decision = %#v", result)
	}

	input.CustomerAdmission.CrossDomainAllowed = true
	result = Decide(input)
	if result.Decision != DecisionDual || !result.RequiresCustomerProjection || !result.RequiresCompanyProjection {
		t.Fatalf("proven cross-domain decision = %#v", result)
	}
}

func TestAutomaticCustomerAdmissionRequiresTransactionInflow(t *testing.T) {
	if admission := AutomaticCustomerAdmission("TRANSACTION_STATUS_CHANGED", routingSnapshot()); !admission.Allowed {
		t.Fatalf("valid admission rejected: %#v", admission)
	}
	snapshot := routingSnapshot()
	snapshot.TransactionDirection = "OUTFLOW"
	if admission := AutomaticCustomerAdmission("TRANSACTION_STATUS_CHANGED", snapshot); admission.Allowed {
		t.Fatalf("outflow admission = %#v", admission)
	}
	if admission := AutomaticCustomerAdmission("AML_KYT_ALERT", routingSnapshot()); admission.Allowed {
		t.Fatalf("non-transaction admission = %#v", admission)
	}
}

func TestAutomaticRoutingDefersEveryProjectionUntilTerminalStatus(t *testing.T) {
	snapshot := routingSnapshot()
	snapshot.TransactionStatus = "CONFIRMING"
	admission := AutomaticCustomerAdmission("TRANSACTION_STATUS_CHANGED", snapshot)
	if !admission.ProjectionDeferred {
		t.Fatalf("nonterminal admission = %#v", admission)
	}
	now := time.UnixMilli(snapshot.CreateTime)
	result := Decide(DecisionInput{
		Candidate:         Candidate{EffectiveEventTime: &now},
		DestinationOwner:  &Ownership{Kind: OwnerKindCompanyAccount, CompanyFundAccountID: int64Pointer(7), Enabled: true, EffectiveFrom: now.Add(-time.Hour)},
		CustomerAdmission: admission,
	})
	if result.Decision != DecisionOpen || result.Reason != ReasonStatusNotTerminal || result.RequiresCompanyProjection {
		t.Fatalf("nonterminal company routing decision = %#v", result)
	}
	if created := AutomaticCustomerAdmission("TRANSACTION_CREATED", routingSnapshot()); !created.ProjectionDeferred {
		t.Fatalf("created event admission = %#v", created)
	}
}

func routingSnapshot() safeheron.TransactionSnapshot {
	return safeheron.TransactionSnapshot{
		TxKey:                "safeheron-tx-1",
		TxHash:               "0xhash",
		CoinKey:              "ETHEREUM_ETH",
		TxAmount:             "1",
		SourceAddress:        "0xSOURCE",
		DestinationAddress:   "0xDEST",
		TransactionDirection: "INFLOW",
		TransactionStatus:    "COMPLETED",
		CreateTime:           time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC).UnixMilli(),
	}
}

func intPointer(value int) *int { return &value }

func int64Pointer(value int64) *int64 { return &value }
