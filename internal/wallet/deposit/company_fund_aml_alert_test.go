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
