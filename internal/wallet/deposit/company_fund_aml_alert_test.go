package deposit

import (
	"context"
	"testing"
)

type companyFundAMLAlertHandlerStub struct {
	result CompanyFundAMLAlertResult
	err    error
	calls  []CompanyFundAMLAlertInput
}

func (stub *companyFundAMLAlertHandlerStub) HandleCompanyFundAMLAlert(_ context.Context, input CompanyFundAMLAlertInput) (CompanyFundAMLAlertResult, error) {
	stub.calls = append(stub.calls, input)
	return stub.result, stub.err
}

func TestProcessKYTAlert_CompanyFundAlertIsAppliedWithoutCustomerOrphan(t *testing.T) {
	repo := newMockRepo()
	handler := &companyFundAMLAlertHandlerStub{result: CompanyFundAMLAlertApplied}
	svc := newKYTSvc(t, repo, nil, true)
	svc.SetCompanyFundAMLAlertHandler(handler)

	repo.pending = []*Event{{
		ID:             701,
		EventID:        "company-aml-event",
		EventType:      "AML_KYT_ALERT",
		RawPayload:     []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"company-tx","amlList":[{"provider":"MistTrack","riskLevel":"LOW"}]}}`),
		SafeheronTxKey: "company-tx",
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne() = %v, %v", processed, err)
	}
	if len(handler.calls) != 1 {
		t.Fatalf("company AML handler calls = %d, want 1", len(handler.calls))
	}
	if got := handler.calls[0]; got.TransactionKey != "company-tx" || got.ScreeningState != "TRIGGERED" || got.RiskLevel != KytLow {
		t.Fatalf("company AML handler input = %#v", got)
	}
	if len(repo.doneIDs) != 1 || len(repo.errorIDs) != 0 || len(repo.noTxIncrements) != 0 {
		t.Fatalf("company AML event must be finalized without orphan handling: done=%v errors=%v increments=%v", repo.doneIDs, repo.errorIDs, repo.noTxIncrements)
	}
}

func TestProcessKYTAlert_CompanyFundAlertDefersWithoutCustomerOrphanRetry(t *testing.T) {
	repo := newMockRepo()
	handler := &companyFundAMLAlertHandlerStub{result: CompanyFundAMLAlertDeferred}
	svc := newKYTSvc(t, repo, nil, true)
	svc.SetCompanyFundAMLAlertHandler(handler)

	repo.pending = []*Event{{
		ID:             702,
		EventID:        "company-aml-before-projection",
		EventType:      "AML_KYT_ALERT",
		RawPayload:     []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"company-before-projection","amlList":[{"provider":"MistTrack","riskLevel":"LOW"}]}}`),
		SafeheronTxKey: "company-before-projection",
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil || processed {
		t.Fatalf("ProcessOne() = %v, %v; want deferred without error", processed, err)
	}
	if len(handler.calls) != 1 || len(repo.doneIDs) != 0 || len(repo.errorIDs) != 0 || len(repo.noTxIncrements) != 0 {
		t.Fatalf("deferred company AML event must avoid customer orphan handling: calls=%d done=%v errors=%v increments=%v", len(handler.calls), repo.doneIDs, repo.errorIDs, repo.noTxIncrements)
	}
}

func TestProcessKYTAlert_CustomerDepositStillCreditsWhenCompanyHandlerIsInstalled(t *testing.T) {
	repo := newMockRepo()
	handler := &companyFundAMLAlertHandlerStub{result: CompanyFundAMLAlertNotCompany}
	svc := newKYTSvc(t, repo, nil, true)
	svc.SetCompanyFundAMLAlertHandler(handler)
	repo.deposits["customer-tx"] = &DepositRow{
		ID: 41, UserID: 7, SafeheronTxKey: "customer-tx", Amount: "1.25", Asset: "USDT", Status: DepositStatusKYTPending,
	}
	repo.pending = []*Event{{
		ID:         703,
		EventID:    "customer-aml-event",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"customer-tx","amlList":[{"provider":"MistTrack","riskLevel":"LOW"}]}}`),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne() = %v, %v", processed, err)
	}
	if repo.deposits["customer-tx"].Status != DepositStatusCredited {
		t.Fatalf("customer deposit status = %s, want CREDITED", repo.deposits["customer-tx"].Status)
	}
	if len(handler.calls) != 1 || len(repo.doneIDs) != 1 {
		t.Fatalf("customer AML event must preserve user processing: calls=%d done=%v", len(handler.calls), repo.doneIDs)
	}
}

func TestProcessKYTAlert_CustomerHighRiskStillRequiresManualReviewWhenCompanyHandlerIsInstalled(t *testing.T) {
	repo := newMockRepo()
	handler := &companyFundAMLAlertHandlerStub{result: CompanyFundAMLAlertNotCompany}
	svc := newKYTSvc(t, repo, nil, true)
	svc.SetCompanyFundAMLAlertHandler(handler)
	repo.deposits["customer-high-risk-tx"] = &DepositRow{
		ID: 43, UserID: 9, SafeheronTxKey: "customer-high-risk-tx", Amount: "1.25", Asset: "USDT", Status: DepositStatusKYTPending,
	}
	repo.pending = []*Event{{
		ID:         705,
		EventID:    "customer-high-risk-aml-event",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"customer-high-risk-tx","amlList":[{"provider":"MistTrack","riskLevel":"HIGH"}]}}`),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne() = %v, %v", processed, err)
	}
	if repo.deposits["customer-high-risk-tx"].Status != DepositStatusManualReview {
		t.Fatalf("high-risk customer deposit status = %s, want MANUAL_REVIEW", repo.deposits["customer-high-risk-tx"].Status)
	}
	if len(handler.calls) != 1 || len(repo.doneIDs) != 1 {
		t.Fatalf("customer AML event must preserve user manual-review handling: calls=%d done=%v", len(handler.calls), repo.doneIDs)
	}
}

func TestProcessKYTAlert_DualRoutingWaitsForBothCustomerAndCompanyProjection(t *testing.T) {
	repo := newMockRepo()
	handler := &companyFundAMLAlertHandlerStub{result: CompanyFundAMLAlertDeferred}
	svc := newKYTSvc(t, repo, nil, true)
	svc.SetCompanyFundAMLAlertHandler(handler)
	repo.deposits["dual-tx"] = &DepositRow{
		ID: 42, UserID: 8, SafeheronTxKey: "dual-tx", Amount: "2.5", Asset: "USDT", Status: DepositStatusKYTPending,
	}
	repo.pending = []*Event{{
		ID:         704,
		EventID:    "dual-aml-event",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(`{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"dual-tx","amlList":[{"provider":"MistTrack","riskLevel":"LOW"}]}}`),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil || processed {
		t.Fatalf("ProcessOne() = %v, %v; want deferred without error", processed, err)
	}
	if repo.deposits["dual-tx"].Status != DepositStatusKYTPending {
		t.Fatalf("dual customer deposit must wait for complete routing, got %s", repo.deposits["dual-tx"].Status)
	}
	if len(handler.calls) != 1 || len(repo.doneIDs) != 0 || len(repo.errorIDs) != 0 || len(repo.noTxIncrements) != 0 {
		t.Fatalf("dual AML event must remain pending: calls=%d done=%v errors=%v increments=%v", len(handler.calls), repo.doneIDs, repo.errorIDs, repo.noTxIncrements)
	}
}
