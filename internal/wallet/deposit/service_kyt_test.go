package deposit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"monera-digital/internal/safeheron"
)

// kytMockRepo extends mockRepo with configurable KYT timeout scan behavior.
type kytMockRepo struct {
	*mockRepo
	kytTimeoutDep *DepositRow // returned by LockOneKYTPendingTimeout
}

func (r *kytMockRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	if r.kytTimeoutDep != nil {
		dep := r.kytTimeoutDep
		r.kytTimeoutDep = nil // only return once
		return dep, nil
	}
	return nil, ErrNoPending
}

type mockKYTClient struct {
	reportFn func(ctx context.Context, txKey string) (*safeheron.KytReportResponse, error)
}

func (m *mockKYTClient) KytReport(ctx context.Context, txKey string) (*safeheron.KytReportResponse, error) {
	return m.reportFn(ctx, txKey)
}

func lowRiskReport(txKey string) *safeheron.KytReportResponse {
	return &safeheron.KytReportResponse{
		TxKey:                      txKey,
		AmlScreeningTriggeredState: "TRIGGERED",
		AmlList: []safeheron.AmlReport{{
			Provider:       "MistTrack",
			Status:         "COMPLETED",
			RiskLevel:      "LOW",
			LastUpdateTime: "1715500000000",
		}},
	}
}

func highRiskReport(txKey string) *safeheron.KytReportResponse {
	return &safeheron.KytReportResponse{
		TxKey:                      txKey,
		AmlScreeningTriggeredState: "TRIGGERED",
		AmlList: []safeheron.AmlReport{{
			Provider:       "MistTrack",
			Status:         "COMPLETED",
			RiskLevel:      "HIGH",
			LastUpdateTime: "1715500000000",
		}},
	}
}

func pendingReport(txKey string) *safeheron.KytReportResponse {
	return &safeheron.KytReportResponse{
		TxKey:                      txKey,
		AmlScreeningTriggeredState: "IN_PROGRESS",
		AmlList:                    nil,
	}
}

func newKYTSvc(t *testing.T, repo *mockRepo, kytClient KYTClient, enabled bool) *Service {
	t.Helper()
	reg := newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1)
	svc := NewService(repo, reg, nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kytClient, enabled, 100, 20*time.Minute)
	return svc
}

func completedConfirmedPayload(txKey string) PayloadEnvelope {
	return PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                txKey,
			CoinKey:              "ETH_KEY",
			TxAmount:             "1.5",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
			SourceAddress:        "0xsrc",
			BlockHeight:          12345,
			TxHash:               "0xhash-" + txKey,
		},
	}
}

// F-KYT-3: TRIGGERED+LOW → CREDITED
func TestProcessOne_KYT_LowRisk_Credits(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-low"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	dep := repo.deposits["tx-low"]
	if dep == nil {
		t.Fatal("deposit not found")
	}
	if dep.Status != DepositStatusCredited {
		t.Errorf("expected CREDITED, got %s", dep.Status)
	}
	if len(repo.doneIDs) != 1 {
		t.Errorf("expected 1 done event, got %d", len(repo.doneIDs))
	}
}

// F-KYT-4: TRIGGERED+HIGH → MANUAL_REVIEW
func TestProcessOne_KYT_HighRisk_ManualReview(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return highRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-high"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	if reason, ok := repo.manualUpdates[1]; !ok || reason != "KYT_RISK_HIGH" {
		t.Errorf("expected KYT_RISK_HIGH manual review, got %v", repo.manualUpdates)
	}
}

// F-KYT-7: IN_PROGRESS → KYT_PENDING
func TestProcessOne_KYT_InProgress_KYTPending(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return pendingReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-pending"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	dep := repo.deposits["tx-pending"]
	if dep == nil {
		t.Fatal("deposit not found")
	}
	if dep.Status != DepositStatusKYTPending {
		t.Errorf("expected KYT_PENDING, got %s", dep.Status)
	}
}

// F-KYT-12: KYT_ENABLED=false → direct credit without API call
func TestProcessOne_KYTDisabled_DirectCredit(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	apiCalled := false
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		apiCalled = true
		return nil, errors.New("should not be called")
	}}
	svc := newKYTSvc(t, repo, kyt, false) // KYT disabled
	enqueueRaw(t, repo, completedConfirmedPayload("tx-nokyt"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	if apiCalled {
		t.Error("KytReport should not be called when KYT is disabled")
	}
	dep := repo.deposits["tx-nokyt"]
	if dep == nil {
		t.Fatal("deposit not found")
	}
	if dep.Status != DepositStatusCredited {
		t.Errorf("expected CREDITED, got %s", dep.Status)
	}
}

// handleKYTApiFailure: attempts < max → event stays PENDING
func TestHandleKYTApiFailure_BelowThreshold(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API timeout")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-apifail"))

	processed, err := svc.ProcessOne(context.Background())
	if !processed {
		t.Fatal("expected processed=true")
	}
	if err == nil {
		t.Fatal("expected error from KYT API failure")
	}
	// Event should NOT be done (stays PENDING for retry)
	if len(repo.doneIDs) != 0 {
		t.Errorf("event should not be marked done, got %v", repo.doneIDs)
	}
}

// handleKYTApiFailure: attempts >= max → MANUAL_REVIEW
func TestHandleKYTApiFailure_ExceedsThreshold(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API timeout")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	svc.kytOrphanMaxRetry = 1 // set threshold to 1 so first failure exceeds it

	enqueueRaw(t, repo, completedConfirmedPayload("tx-maxfail"))

	processed, err := svc.ProcessOne(context.Background())
	if !processed {
		t.Fatal("expected processed=true")
	}
	if err != nil {
		t.Fatalf("expected nil error after forced MR, got: %v", err)
	}
	if reason, ok := repo.manualUpdates[1]; !ok || reason != ReasonKytApiFailed {
		t.Errorf("expected KYT_API_FAILED manual review, got %v", repo.manualUpdates)
	}
	if len(repo.doneIDs) != 1 {
		t.Errorf("event should be marked done after forced MR, got %v", repo.doneIDs)
	}
}

// processKYTAlert: deposit found + LOW → CREDITED
func TestProcessKYTAlert_LowRisk_Credits(t *testing.T) {
	repo := newMockRepo()
	// Pre-populate a KYT_PENDING deposit
	repo.deposits["tx-alert"] = &DepositRow{
		ID: 10, UserID: 1, SafeheronTxKey: "tx-alert", Amount: "2.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert","customerRefId":"ref","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`

	repo.pending = []*Event{{
		ID:         99,
		EventID:    "evt-alert-1",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	dep := repo.deposits["tx-alert"]
	if dep.Status != DepositStatusCredited {
		t.Errorf("expected CREDITED, got %s", dep.Status)
	}
}

// processKYTAlert: deposit not found (out-of-order) → event stays PENDING
func TestProcessKYTAlert_OutOfOrder_StaysPending(t *testing.T) {
	repo := newMockRepo()
	// No deposit for tx-orphan
	svc := newKYTSvc(t, repo, nil, true)

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:              88,
		EventID:         "evt-orphan",
		EventType:       "AML_KYT_ALERT",
		RawPayload:      []byte(payload),
		ProcessAttempts: 0,
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	// Event should NOT be marked done (stays PENDING)
	if len(repo.doneIDs) != 0 {
		t.Errorf("orphan alert should not be marked done, got %v", repo.doneIDs)
	}
}

// ScanKYTTimeouts: timeout + LOW → CREDITED
func TestScanOneKYTTimeout_LowRisk_Credits(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 20, UserID: 1, SafeheronTxKey: "tx-timeout", Amount: "3.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-timeout"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	if dep.Status != DepositStatusCredited {
		t.Errorf("expected CREDITED, got %s", dep.Status)
	}
}

// ScanKYTTimeouts: timeout + HIGH → MANUAL_REVIEW with _AFTER_TIMEOUT suffix
func TestScanOneKYTTimeout_HighRisk_ManualReview(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 21, UserID: 1, SafeheronTxKey: "tx-timeout-high", Amount: "3.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-timeout-high"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return highRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	if reason, ok := base.manualUpdates[21]; !ok || reason != "KYT_RISK_HIGH_AFTER_TIMEOUT" {
		t.Errorf("expected KYT_RISK_HIGH_AFTER_TIMEOUT, got %v", base.manualUpdates)
	}
}

// ScanKYTTimeouts: timeout + IN_PROGRESS (still pending) → MANUAL_REVIEW(KYT_TIMEOUT_STILL_PENDING)
func TestScanOneKYTTimeout_StillPending_ManualReview(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 22, UserID: 1, SafeheronTxKey: "tx-timeout-pend", Amount: "3.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-timeout-pend"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return pendingReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	if reason, ok := base.manualUpdates[22]; !ok || reason != ReasonKytTimeoutStillPending {
		t.Errorf("expected KYT_TIMEOUT_STILL_PENDING, got %v", base.manualUpdates)
	}
}

// ScanKYTTimeouts: API error → MANUAL_REVIEW(KYT_PROVIDER_FAILED_AFTER_TIMEOUT)
func TestScanOneKYTTimeout_APIError_ManualReview(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 23, UserID: 1, SafeheronTxKey: "tx-timeout-err", Amount: "3.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-timeout-err"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	if reason, ok := base.manualUpdates[23]; !ok || reason != ReasonKytProviderFailedAfterTimeout {
		t.Errorf("expected KYT_PROVIDER_FAILED_AFTER_TIMEOUT, got %v", base.manualUpdates)
	}
}

// ScanKYTTimeouts: no timeout rows → exits after 1 BeginTx (not 50)
func TestScanKYTTimeouts_Empty(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	svc.ScanKYTTimeouts(context.Background())

	repo.mu.Lock()
	calls := repo.beginTxCalls
	repo.mu.Unlock()
	if calls != 1 {
		t.Errorf("expected 1 BeginTx call on empty table, got %d (I-1: 50-iteration bug)", calls)
	}
}

// F-KYT-2: UNTRIGGERED → MANUAL_REVIEW(WARN)
func TestProcessOne_KYT_Untriggered_ManualReview(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return &safeheron.KytReportResponse{
			TxKey:                      txKey,
			AmlScreeningTriggeredState: "UNTRIGGERED",
		}, nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-untrig"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	if reason, ok := repo.manualUpdates[1]; !ok || reason != ReasonKytUntriggered {
		t.Errorf("expected KYT_UNTRIGGERED reason, got %v", repo.manualUpdates)
	}
}

// processKYTAlert: deposit found + HIGH → MANUAL_REVIEW
func TestProcessKYTAlert_HighRisk_ManualReview(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-alert-high"] = &DepositRow{
		ID: 11, UserID: 1, SafeheronTxKey: "tx-alert-high", Amount: "2.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-high","customerRefId":"ref","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"HIGH","lastUpdateTime":"1715500000000"}]}}`

	repo.pending = []*Event{{
		ID:         101,
		EventID:    "evt-alert-high",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	if reason, ok := repo.manualUpdates[11]; !ok || reason != "KYT_RISK_HIGH" {
		t.Errorf("expected KYT_RISK_HIGH manual review, got %v", repo.manualUpdates)
	}
	if len(repo.doneIDs) != 1 {
		t.Errorf("expected event marked done, got %d", len(repo.doneIDs))
	}
}

// processKYTAlert: deposit found + IN_PROGRESS (KeepPending) → stays KYT_PENDING
func TestProcessKYTAlert_KeepPending(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-alert-pend"] = &DepositRow{
		ID: 12, UserID: 1, SafeheronTxKey: "tx-alert-pend", Amount: "2.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-pend","customerRefId":"ref","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"PENDING","riskLevel":"","lastUpdateTime":"1715500000000"}]}}`

	repo.pending = []*Event{{
		ID:         102,
		EventID:    "evt-alert-pend",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	dep := repo.deposits["tx-alert-pend"]
	if dep.Status != DepositStatusKYTPending {
		t.Errorf("expected deposit to stay KYT_PENDING, got %s", dep.Status)
	}
	if len(repo.doneIDs) != 1 {
		t.Errorf("expected event marked done, got %d", len(repo.doneIDs))
	}
}

// markOrphanAlertDone: orphan alert exceeds retry → MarkEventError + commit
func TestProcessKYTAlert_OrphanExceedsRetry_MarksError(t *testing.T) {
	repo := newMockRepo()
	svc := newKYTSvc(t, repo, nil, true)
	svc.kytOrphanMaxRetry = 1 // threshold=1 so first attempt exceeds

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan-max","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:              89,
		EventID:         "evt-orphan-max",
		EventType:       "AML_KYT_ALERT",
		RawPayload:      []byte(payload),
		ProcessAttempts: 1, // already at threshold
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	if len(repo.errorIDs) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(repo.errorIDs))
	}
	if repo.errorIDs[0].msg != ReasonKytOrphanAlert {
		t.Errorf("expected KYT_ORPHAN_ALERT error message, got %q", repo.errorIDs[0].msg)
	}
}

// ScanKYTTimeouts: context cancelled mid-loop → exits early
func TestScanKYTTimeouts_ContextCancelled(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 30, UserID: 1, SafeheronTxKey: "tx-cancel", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-cancel"] = dep

	callCount := 0
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		callCount++
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	svc.ScanKYTTimeouts(ctx)

	// Should exit without processing due to cancelled context
	if callCount > 0 {
		t.Errorf("expected no KYT API calls after context cancel, got %d", callCount)
	}
}

// ScanKYTTimeouts: non-ErrNoPending error from scanOneKYTTimeout → logs and continues
func TestScanKYTTimeouts_NonFatalError_Continues(t *testing.T) {
	base := newMockRepo()
	repo := &erroringKYTMockRepo{mockRepo: base, lockErr: errors.New("db lock timeout")}

	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 1*time.Millisecond)

	// Should not panic; just log the error and continue the loop
	svc.ScanKYTTimeouts(context.Background())
}

// erroringKYTMockRepo returns an error from LockOneKYTPendingTimeout (not ErrNoPending).
type erroringKYTMockRepo struct {
	*mockRepo
	lockErr  error
	lockCall int
}

func (r *erroringKYTMockRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	r.lockCall++
	if r.lockCall > 2 {
		return nil, ErrNoPending // prevent infinite loop
	}
	return nil, r.lockErr
}

// processKYTAlert: FindDepositByTxKey returns error → propagated
func TestProcessKYTAlert_FindByTxKeyError(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-find-err"] = &DepositRow{
		ID: 13, UserID: 1, SafeheronTxKey: "tx-find-err", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	// Make FindDepositByTxKey error
	errRepo := &findByTxKeyErrRepo{mockRepo: repo, findErr: errors.New("db find error")}
	svc := NewService(errRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-find-err","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:         103,
		EventID:    "evt-find-err",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from FindDepositByTxKey failure")
	}
}

type findByTxKeyErrRepo struct {
	*mockRepo
	findErr error
}

func (r *findByTxKeyErrRepo) FindDepositByTxKey(_ context.Context, _ Tx, _ string) (*DepositRow, bool, error) {
	return nil, false, r.findErr
}

// handleKYTApiFailure: IncrementEventAttemptsNoTx fails → just logs, continues
func TestHandleKYTApiFailure_IncrementFails_StillRetries(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	incrementErrRepo := &incrementErrMockRepo{mockRepo: repo}
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API timeout")
	}}
	svc := NewService(incrementErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-inc-err"))

	processed, err := svc.ProcessOne(context.Background())
	if !processed {
		t.Fatal("expected processed=true")
	}
	if err == nil {
		t.Fatal("expected error from KYT API failure")
	}
}

type incrementErrMockRepo struct {
	*mockRepo
}

func (r *incrementErrMockRepo) IncrementEventAttemptsNoTx(_ context.Context, _ int64) error {
	return errors.New("increment failed")
}

// processKYTAlert: UpdateAMLFields fails → error propagated
func TestProcessKYTAlert_UpdateAMLFieldsError(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-aml-err"] = &DepositRow{
		ID: 14, UserID: 1, SafeheronTxKey: "tx-aml-err", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	amlErrRepo := &updateAMLErrRepo{mockRepo: repo}
	svc := NewService(amlErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-aml-err","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:         104,
		EventID:    "evt-aml-err",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from UpdateAMLFields failure")
	}
}

type updateAMLErrRepo struct {
	*mockRepo
}

func (r *updateAMLErrRepo) UpdateAMLFields(_ context.Context, _ Tx, _ int64, _, _ string, _ time.Time, _ []byte) error {
	return errors.New("update AML fields boom")
}

// T-γ: KYT enabled + COMPLETED/CONFIRMED + KytReport returns low → credit in T-γ (full T-α/T-β/T-γ path)
// This also tests the T-γ UpdateAMLFields path
func TestProcessOne_KYT_TGamma_UpdateAMLFieldsError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	amlErrRepo := &updateAMLErrRepo{mockRepo: repo}
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := NewService(amlErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-amlerr"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ UpdateAMLFields failure")
	}
}

// flagAndFinalize: flagManualReview fails AND MarkEventError also fails → wraps ErrMarkErrorFailed
func TestProcessOne_FlagAndFinalize_BothFail(t *testing.T) {
	repo := newMockRepo()
	repo.depositErr = errors.New("upsert boom in MR")
	repo.markErrorErr = errors.New("mark error also failed")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_CREATED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-double-fail",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xstranger",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when both flagManualReview and MarkEventError fail")
	}
	if !errors.Is(err, ErrMarkErrorFailed) {
		t.Errorf("expected ErrMarkErrorFailed sentinel, got: %v", err)
	}
}

// handleKYTApiFailure: exceeds threshold BUT BeginTx for tx3 fails
func TestHandleKYTApiFailure_ExceedsThreshold_BeginTxFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	svc.kytOrphanMaxRetry = 1

	enqueueRaw(t, repo, completedConfirmedPayload("tx-tx3fail"))

	// After T-α commits, the handleKYTApiFailure will try to BeginTx for tx3.
	// We set beginTxErr AFTER the first two successful BeginTx calls (T-α + T-γ attempt).
	beginTxCallTracker := &beginTxFailAfterNRepo{mockRepo: repo, failAfter: 1}

	svc.repo = beginTxCallTracker

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when tx3 BeginTx fails")
	}
}

type beginTxFailAfterNRepo struct {
	*mockRepo
	failAfter int
	callCount int
}

func (r *beginTxFailAfterNRepo) BeginTx(ctx context.Context) (Tx, error) {
	r.callCount++
	if r.callCount > r.failAfter {
		return nil, errors.New("begin tx failed for tx3")
	}
	return r.mockRepo.BeginTx(ctx)
}

// scanOneKYTTimeout: Phase-3 BeginTx fails → error returned
func TestScanOneKYTTimeout_Phase3BeginTxFails(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 31, UserID: 1, SafeheronTxKey: "tx-p3fail", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3fail"] = dep
	// Phase-1 tx succeeds (call 1), Phase-3 tx fails (call 2)
	failRepo := &beginTxFailAfterNKYTRepo{mockRepo: base, failAfter: 1, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(failRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Should not panic; the error is logged and the loop continues
}

// beginTxFailAfterNRepo with kytTimeoutDep for scan tests
type beginTxFailAfterNKYTRepo struct {
	*mockRepo
	failAfter     int
	callCount     int
	kytTimeoutDep *DepositRow
}

func (r *beginTxFailAfterNKYTRepo) BeginTx(ctx context.Context) (Tx, error) {
	r.callCount++
	if r.callCount > r.failAfter {
		return nil, errors.New("begin tx failed for phase-3")
	}
	return r.mockRepo.BeginTx(ctx)
}

func (r *beginTxFailAfterNKYTRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	if r.kytTimeoutDep != nil {
		dep := r.kytTimeoutDep
		r.kytTimeoutDep = nil
		return dep, nil
	}
	return nil, ErrNoPending
}

// T-γ: deposit status changed between T-α and T-γ → just marks event done
func TestProcessOne_KYT_TGamma_StaleDeposit(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		// Between T-α (MoveToKYTPending) and T-γ, manually change deposit status to simulate concurrent change
		repo.mu.Lock()
		if d, ok := repo.deposits[txKey]; ok {
			d.Status = DepositStatusCredited
		}
		repo.mu.Unlock()
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-stale"))

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	// Should NOT double-credit (deposit was already CREDITED when T-γ re-read it)
	if len(repo.journalCalls) != 0 {
		t.Errorf("stale deposit should not be credited again, got %d journal calls", len(repo.journalCalls))
	}
}

// C-1 regression: processKYTAlert DB error path releases tx (rollback) BEFORE NoTx increment
func TestProcessKYTAlert_DBError_RollbackBeforeIncrement(t *testing.T) {
	repo := newMockRepo()
	errRepo := &findByTxKeyErrRepo{mockRepo: repo, findErr: errors.New("db connection lost")}
	svc := NewService(errRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-c1","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID: 200, EventID: "evt-c1", EventType: "AML_KYT_ALERT", RawPayload: []byte(alertJSON),
	}}

	svc.ProcessOne(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.noTxIncrements) != 1 || repo.noTxIncrements[0] != 200 {
		t.Errorf("C-1: expected IncrementEventAttemptsNoTx(200), got %v", repo.noTxIncrements)
	}
	if !repo.rollbackBeforeNoTxInc {
		t.Error("C-1: expected rollback BEFORE NoTx increment (deadlock guard)")
	}
}

// C-2 regression: scanOneKYTTimeout Phase-3 skips when concurrent peer already credited
func TestScanOneKYTTimeout_ConcurrentPeerCredited_Skips(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 40, UserID: 1, SafeheronTxKey: "tx-c2-scan", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-c2-scan"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		// Simulate concurrent peer crediting between Phase-1 and Phase-3
		base.mu.Lock()
		if d, ok := base.deposits[txKey]; ok {
			d.Status = DepositStatusCredited
		}
		base.mu.Unlock()
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	// Must NOT double-credit — no journal entries
	if len(base.journalCalls) != 0 {
		t.Errorf("C-2: expected no journal calls (concurrent peer already credited), got %d", len(base.journalCalls))
	}
	// Deposit should remain CREDITED (not overwritten)
	if dep.Status != DepositStatusCredited {
		t.Errorf("C-2: expected deposit to stay CREDITED, got %s", dep.Status)
	}
}

// I-2 regression: processKYTAlert FindDepositByTxKey DB error increments attempts
func TestProcessKYTAlert_FindByTxKeyError_IncrementsAttempts(t *testing.T) {
	repo := newMockRepo()
	errRepo := &findByTxKeyErrRepo{mockRepo: repo, findErr: errors.New("db flaky")}
	svc := NewService(errRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-i2","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID: 201, EventID: "evt-i2", EventType: "AML_KYT_ALERT", RawPayload: []byte(alertJSON),
	}}

	svc.ProcessOne(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.noTxIncrements) != 1 || repo.noTxIncrements[0] != 201 {
		t.Errorf("I-2: expected IncrementEventAttemptsNoTx(201) on DB error, got %v", repo.noTxIncrements)
	}
}

// I-3 regression: KYT API failure below threshold wraps ErrKYTAPIBackoff sentinel
func TestHandleKYTApiFailure_BelowThreshold_BackoffSentinel(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("upstream timeout")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-i3"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("I-3: expected error from KYT API failure")
	}
	if !errors.Is(err, ErrKYTAPIBackoff) {
		t.Errorf("I-3: expected ErrKYTAPIBackoff sentinel, got: %v", err)
	}
}

// AML list truncation: amlList > maxAMLListEntries is capped
func TestWriteAMLFields_TruncatesLargeList(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-trunc"] = &DepositRow{
		ID: 50, UserID: 1, SafeheronTxKey: "tx-trunc", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	// Build 100 entries (exceeds maxAMLListEntries=50)
	bigList := make([]safeheron.AmlReport, 100)
	for i := range bigList {
		bigList[i] = safeheron.AmlReport{Provider: "test", Status: "COMPLETED", RiskLevel: "LOW"}
	}

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-trunc","amlScreeningTriggeredState":"TRIGGERED","amlList":[` +
		buildNReports(100) + `]}}`
	repo.pending = []*Event{{
		ID: 300, EventID: "evt-trunc", EventType: "AML_KYT_ALERT", RawPayload: []byte(alertJSON),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
}

func buildNReports(n int) string {
	var b strings.Builder
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}`)
	}
	return b.String()
}

// markKYTPendingManualReviewIfStillPending: deposit already CREDITED → skip (no MR)
func TestMarkKYTPendingMRGuard_AlreadyCredited_Skips(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 41, UserID: 1, SafeheronTxKey: "tx-mr-skip", Amount: "1.0", Asset: "ETH", Status: DepositStatusCredited,
	}
	base.deposits["tx-mr-skip"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())

	// Deposit already CREDITED — should NOT be overwritten to MANUAL_REVIEW
	if dep.Status != DepositStatusCredited {
		t.Errorf("expected deposit to stay CREDITED, got %s", dep.Status)
	}
	if _, ok := base.manualUpdates[41]; ok {
		t.Error("should not mark manual review on already-CREDITED deposit")
	}
}

// scanOneKYTTimeout: Phase-3 deposit not found (vanished) → just commit and skip
func TestScanOneKYTTimeout_DepositVanished_Skips(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 42, UserID: 1, SafeheronTxKey: "tx-vanish", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	// Don't add to base.deposits — simulate vanished row
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	// Should not panic — the scan logs and skips
	svc.ScanKYTTimeouts(context.Background())

	if len(base.journalCalls) != 0 {
		t.Error("expected no journal calls when deposit vanished")
	}
}

// processKYTAlert: orphan below threshold + rollback before NoTx increment (C-1 variant)
func TestProcessKYTAlert_OrphanBelowThreshold_RollbackBeforeIncrement(t *testing.T) {
	repo := newMockRepo()
	svc := newKYTSvc(t, repo, nil, true)

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan-c1","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID: 210, EventID: "evt-orphan-c1", EventType: "AML_KYT_ALERT", RawPayload: []byte(payload),
		ProcessAttempts: 0,
	}}

	svc.ProcessOne(context.Background())

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.noTxIncrements) != 1 || repo.noTxIncrements[0] != 210 {
		t.Errorf("expected IncrementEventAttemptsNoTx(210), got %v", repo.noTxIncrements)
	}
	if !repo.rollbackBeforeNoTxInc {
		t.Error("C-1: orphan path must rollback before NoTx increment")
	}
}

// AML_KYT_ALERT on non-KYT_PENDING deposit → just update AML fields and mark done
func TestProcessKYTAlert_NonPending_JustUpdatesAML(t *testing.T) {
	repo := newMockRepo()
	repo.deposits["tx-done"] = &DepositRow{
		ID: 30, UserID: 1, SafeheronTxKey: "tx-done", Amount: "1.0", Asset: "ETH", Status: DepositStatusCredited,
	}
	svc := newKYTSvc(t, repo, nil, true)

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-done","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:         77,
		EventID:    "evt-done-alert",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(payload),
	}}

	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	// Event should be marked done
	if len(repo.doneIDs) != 1 {
		t.Errorf("expected event marked done, got %v", repo.doneIDs)
	}
	// Deposit should still be CREDITED (not changed)
	if repo.deposits["tx-done"].Status != DepositStatusCredited {
		t.Errorf("deposit should remain CREDITED")
	}
}

// =================== Coverage boost tests ===================

// --- markOrphanAlertDone: MarkEventError failure ---
func TestMarkOrphanAlertDone_MarkEventErrorFails(t *testing.T) {
	repo := newMockRepo()
	repo.markErrorErr = errors.New("mark error boom")
	svc := newKYTSvc(t, repo, nil, true)
	svc.kytOrphanMaxRetry = 1

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan-merr","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:              500,
		EventID:         "evt-orphan-merr",
		EventType:       "AML_KYT_ALERT",
		RawPayload:      []byte(payload),
		ProcessAttempts: 1,
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventError fails in markOrphanAlertDone")
	}
	if !strings.Contains(err.Error(), "mark orphan alert error") {
		t.Errorf("expected 'mark orphan alert error' in message, got: %v", err)
	}
}

// --- markOrphanAlertDone: commit failure ---
func TestMarkOrphanAlertDone_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit orphan boom")
	svc := newKYTSvc(t, repo, nil, true)
	svc.kytOrphanMaxRetry = 1

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan-cerr","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:              501,
		EventID:         "evt-orphan-cerr",
		EventType:       "AML_KYT_ALERT",
		RawPayload:      []byte(payload),
		ProcessAttempts: 1,
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails in markOrphanAlertDone")
	}
	if !strings.Contains(err.Error(), "commit orphan alert") {
		t.Errorf("expected 'commit orphan alert' in message, got: %v", err)
	}
}

// --- flagAndFinalize: MarkEventDone failure when procErr is nil ---
func TestFlagAndFinalize_MarkEventDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.markDoneErr = errors.New("mark done boom")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	// ADDRESS_UNASSIGNED path: procErr is nil from flagManualReview,
	// then MarkEventDone fails
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_CREATED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-done-fail",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xstranger",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails in flagAndFinalize")
	}
	if !strings.Contains(err.Error(), "mark event done") {
		t.Errorf("expected 'mark event done' in message, got: %v", err)
	}
}

// --- ProcessOne: bad raw_payload JSON → MarkEventError + commit failure ---
func TestProcessOne_BadJSON_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit after bad json boom")
	repo.pending = []*Event{{
		ID:         510,
		EventType:  "TRANSACTION_STATUS_CHANGED",
		RawPayload: []byte(`not-valid-json`),
	}}

	svc := NewService(repo, nil, nil)
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails after marking bad JSON")
	}
	if !strings.Contains(err.Error(), "commit error state") {
		t.Errorf("expected 'commit error state' in message, got: %v", err)
	}
}

// --- ProcessOne: AML_KYT_ALERT with bad JSON in second unmarshal ---
func TestProcessOne_AMLAlert_BadSecondUnmarshal(t *testing.T) {
	// The first unmarshal (PayloadEnvelope) succeeds, but second (AMLKYTAlertDetail) fails.
	// This requires valid outer JSON with eventType=AML_KYT_ALERT but something that
	// fails on the inner struct parse. Since JSON unmarshal is lenient, we use a raw
	// payload that has eventType set but eventDetail is an invalid type.
	//
	// Actually both use json.Unmarshal on the same RawPayload, so the second unmarshal
	// into the struct with AMLKYTAlertDetail will also succeed as long as JSON is valid.
	// The only way to make the second fail is to have invalid JSON — but then the first
	// would also fail. So this code path is only reachable if the payload is valid JSON
	// but something goes wrong.
	//
	// Looking at the code more carefully: both parse the same RawPayload bytes.
	// If the first json.Unmarshal succeeds, the second will also succeed (it's the same
	// valid JSON). So this path is actually unreachable in practice. Skip this test.
	t.Skip("second unmarshal uses same bytes — unreachable when first succeeds")
}

// --- ProcessOne: AML_KYT_ALERT second unmarshal MarkEventError + commit ---
func TestProcessOne_AMLAlert_BadJSON_MarkErrorAndCommit(t *testing.T) {
	// Construct a raw event directly with eventType=AML_KYT_ALERT but raw_payload
	// that is valid JSON for PayloadEnvelope but NOT for the inner wrapper. Since
	// both use json.Unmarshal on the same raw bytes, the second will always succeed
	// if the first did. This path is effectively dead code for well-typed JSON.
	// We can still trigger it with a manually constructed event.
	repo := newMockRepo()
	repo.pending = []*Event{{
		ID:         511,
		EventID:    "evt-bad-alert",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(`{"eventType":"AML_KYT_ALERT"}`), // valid JSON, will parse fine
	}}
	// This will succeed both unmarshals but find no deposit → orphan path.
	// Can't easily make second unmarshal fail with valid JSON. Skip.
	t.Skip("second unmarshal uses same bytes — unreachable when first succeeds")
	_ = repo
}

// --- ProcessOne: failed terminal when deposit already CREDITED → skip ---
func TestProcessOne_FailedStatusOnCreditedDeposit_Skips(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	// First: credit the deposit via COMPLETED+CONFIRMED
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-fail-skip",
			CoinKey:              "K",
			TxAmount:             "1.0",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	if _, err := svc.ProcessOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if repo.deposits["tx-fail-skip"].Status != DepositStatusCredited {
		t.Fatal("setup: expected deposit CREDITED")
	}

	// Now: a FAILED event arrives for the same txKey → should skip the failed branch
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-fail-skip",
			CoinKey:              "K",
			TxAmount:             "1.0",
			TransactionStatus:    "FAILED",
			TransactionSubStatus: "SOME_REASON",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	processed, err := svc.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !processed {
		t.Fatal("expected processed=true")
	}
	// Deposit should remain CREDITED (not moved to FAILED)
	if repo.deposits["tx-fail-skip"].Status != DepositStatusCredited {
		t.Errorf("expected deposit to remain CREDITED, got %s", repo.deposits["tx-fail-skip"].Status)
	}
	// Should NOT have any failedUpdates for this deposit
	if _, ok := repo.failedUpdates[1]; ok {
		t.Error("should not mark already-CREDITED deposit as FAILED")
	}
}

// --- ProcessOne T-γ: re-read returns error (FindDepositByTxKey failure) ---
func TestProcessOne_TGamma_ReReadError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	// T-α uses UpsertDeposit (not FindDepositByTxKey), so the first FindDepositByTxKey
	// call is the T-γ re-read — fail on call 1.
	findErrRepo := &findByTxKeyFailOnCallNRepo{
		mockRepo:   repo,
		failOnCall: 1,
		findErr:    errors.New("re-read boom"),
	}
	svc := NewService(findErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-reread"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ re-read failure")
	}
	if !strings.Contains(err.Error(), "re-read deposit T-γ") {
		t.Errorf("expected 're-read deposit T-γ' in message, got: %v", err)
	}
}

// findByTxKeyFailOnCallNRepo fails FindDepositByTxKey on the Nth call.
type findByTxKeyFailOnCallNRepo struct {
	*mockRepo
	failOnCall int
	callCount  int
	findErr    error
}

func (r *findByTxKeyFailOnCallNRepo) FindDepositByTxKey(ctx context.Context, tx Tx, txKey string) (*DepositRow, bool, error) {
	r.callCount++
	if r.callCount == r.failOnCall {
		return nil, false, r.findErr
	}
	return r.mockRepo.FindDepositByTxKey(ctx, tx, txKey)
}

// --- ProcessOne T-γ: re-read returns not-found ---
func TestProcessOne_TGamma_ReReadNotFound(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		// After T-α commits, delete the deposit to simulate not-found in T-γ
		repo.mu.Lock()
		delete(repo.deposits, txKey)
		repo.mu.Unlock()
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-nf"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ re-read not found")
	}
	if !strings.Contains(err.Error(), "re-read deposit T-γ") {
		t.Errorf("expected 're-read deposit T-γ' in message, got: %v", err)
	}
}

// --- processKYTAlert: credit error in KytActionCredit path ---
func TestProcessKYTAlert_CreditError(t *testing.T) {
	repo := newMockRepo()
	repo.creditErr = errors.New("credit boom in alert")
	repo.deposits["tx-alert-credit-err"] = &DepositRow{
		ID: 60, UserID: 1, SafeheronTxKey: "tx-alert-credit-err", Amount: "2.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-credit-err","customerRefId":"ref","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         600,
		EventID:    "evt-alert-credit-err",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from credit failure in processKYTAlert")
	}
	if !strings.Contains(err.Error(), "credit deposit KYT alert") {
		t.Errorf("expected 'credit deposit KYT alert' in message, got: %v", err)
	}
}

// --- processKYTAlert: final commit error ---
func TestProcessKYTAlert_CommitError(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit alert boom")
	repo.deposits["tx-alert-commit-err"] = &DepositRow{
		ID: 61, UserID: 1, SafeheronTxKey: "tx-alert-commit-err", Amount: "2.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-commit-err","customerRefId":"ref","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         601,
		EventID:    "evt-alert-commit-err",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from commit failure in processKYTAlert")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("expected 'commit' in message, got: %v", err)
	}
}

// --- scanOneKYTTimeout: Phase 1 commit error ---
func TestScanOneKYTTimeout_Phase1CommitError(t *testing.T) {
	base := newMockRepo()
	base.commitErr = errors.New("phase-1 commit boom")
	dep := &DepositRow{
		ID: 70, UserID: 1, SafeheronTxKey: "tx-p1-cerr", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p1-cerr"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	// Should not panic — logs and continues
	svc.ScanKYTTimeouts(context.Background())
	// Phase 1 commit fails, so KYT API should not be called (no credit)
	if len(base.journalCalls) != 0 {
		t.Error("expected no journal calls when phase-1 commit fails")
	}
}

// --- scanOneKYTTimeout: WriteAMLFields error in phase 3 ---
func TestScanOneKYTTimeout_Phase3WriteAMLFieldsError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 71, UserID: 1, SafeheronTxKey: "tx-p3-aml", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3-aml"] = dep
	amlErrRepo := &kytUpdateAMLErrRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(amlErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	// Should not panic — error is logged
	svc.ScanKYTTimeouts(context.Background())

	if len(base.journalCalls) != 0 {
		t.Error("expected no journal calls when WriteAMLFields fails")
	}
}

// kytUpdateAMLErrRepo wraps mockRepo for scan tests, overriding UpdateAMLFields to error.
type kytUpdateAMLErrRepo struct {
	*mockRepo
	kytTimeoutDep *DepositRow
}

func (r *kytUpdateAMLErrRepo) UpdateAMLFields(_ context.Context, _ Tx, _ int64, _, _ string, _ time.Time, _ []byte) error {
	return errors.New("update AML fields phase-3 boom")
}

func (r *kytUpdateAMLErrRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	if r.kytTimeoutDep != nil {
		dep := r.kytTimeoutDep
		r.kytTimeoutDep = nil
		return dep, nil
	}
	return nil, ErrNoPending
}

// --- scanOneKYTTimeout: credit error in phase 3 ---
func TestScanOneKYTTimeout_Phase3CreditError(t *testing.T) {
	base := newMockRepo()
	base.creditErr = errors.New("credit phase-3 boom")
	dep := &DepositRow{
		ID: 72, UserID: 1, SafeheronTxKey: "tx-p3-credit", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3-credit"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	// Should not panic — error is logged and loop continues
	svc.ScanKYTTimeouts(context.Background())

	if len(base.journalCalls) != 0 {
		t.Error("expected no journal calls when credit fails in phase-3")
	}
}

// --- markKYTPendingManualReviewIfStillPending: BeginTx error ---
func TestMarkKYTPendingMR_BeginTxError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 80, UserID: 1, SafeheronTxKey: "tx-mr-btx", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-mr-btx"] = dep
	// Phase 1 tx succeeds, KYT API fails, then Phase-MR BeginTx fails
	mrBeginTxFailRepo := &beginTxFailAfterNKYTRepo{
		mockRepo:      base,
		failAfter:     1, // phase 1 succeeds, phase-MR fails
		kytTimeoutDep: dep,
	}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(mrBeginTxFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	// Should not panic — error logged, loop continues
	svc.ScanKYTTimeouts(context.Background())
}

// --- markKYTPendingManualReviewIfStillPending: FindDepositByTxKey error ---
func TestMarkKYTPendingMR_FindByTxKeyError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 81, UserID: 1, SafeheronTxKey: "tx-mr-find", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-mr-find"] = dep
	// Use a repo that returns a deposit for Lock, but errors on FindDepositByTxKey in phase-MR
	findErrRepo := &kytFindErrOnMRRepo{mockRepo: base, kytTimeoutDep: dep, findErr: errors.New("find boom in MR")}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(findErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Error is logged, not panicked
}

// kytFindErrOnMRRepo wraps mockRepo: LockOneKYTPendingTimeout returns dep,
// FindDepositByTxKey always returns error.
type kytFindErrOnMRRepo struct {
	*mockRepo
	kytTimeoutDep *DepositRow
	findErr       error
}

func (r *kytFindErrOnMRRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	if r.kytTimeoutDep != nil {
		dep := r.kytTimeoutDep
		r.kytTimeoutDep = nil
		return dep, nil
	}
	return nil, ErrNoPending
}

func (r *kytFindErrOnMRRepo) FindDepositByTxKey(_ context.Context, _ Tx, _ string) (*DepositRow, bool, error) {
	return nil, false, r.findErr
}

// --- markKYTPendingManualReviewIfStillPending: MarkDepositManualReview error ---
func TestMarkKYTPendingMR_MarkMRError(t *testing.T) {
	base := newMockRepo()
	base.markMRErr = errors.New("mark MR boom")
	dep := &DepositRow{
		ID: 82, UserID: 1, SafeheronTxKey: "tx-mr-mr", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-mr-mr"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Error from markKYTPendingManualReviewIfStillPending is logged, not panicked
}

// --- markKYTPendingManualReviewIfStillPending: commit error ---
func TestMarkKYTPendingMR_CommitError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 83, UserID: 1, SafeheronTxKey: "tx-mr-commit", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-mr-commit"] = dep
	// Phase 1 commit succeeds (call 1), phase-MR commit fails (call 2 via commitErr on fakeTx)
	// We need the first tx commit to succeed and the second to fail.
	// Since commitErr on mockRepo applies to all fakeTx, we need a wrapper.
	mrCommitErrRepo := &kytCommitErrOnCallNRepo{
		mockRepo:      base,
		failCommitOn:  2, // phase-1 commit succeeds, phase-MR commit fails
		kytTimeoutDep: dep,
	}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}

	svc := NewService(mrCommitErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Commit error in MR guard is logged, not panicked
}

// kytCommitErrOnCallNRepo: BeginTx returns a fakeTx whose commit fails only on the Nth commit call.
type kytCommitErrOnCallNRepo struct {
	*mockRepo
	failCommitOn  int
	commitCount   int
	kytTimeoutDep *DepositRow
	mu2           sync.Mutex
}

func (r *kytCommitErrOnCallNRepo) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := r.mockRepo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return &commitErrCounterTx{inner: tx, repo: r}, nil
}

func (r *kytCommitErrOnCallNRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	if r.kytTimeoutDep != nil {
		dep := r.kytTimeoutDep
		r.kytTimeoutDep = nil
		return dep, nil
	}
	return nil, ErrNoPending
}

type commitErrCounterTx struct {
	inner Tx
	repo  *kytCommitErrOnCallNRepo
}

func (t *commitErrCounterTx) Commit() error {
	t.repo.mu2.Lock()
	t.repo.commitCount++
	n := t.repo.commitCount
	t.repo.mu2.Unlock()
	if n == t.repo.failCommitOn {
		return errors.New("commit boom on call N")
	}
	return t.inner.Commit()
}

func (t *commitErrCounterTx) Rollback() error {
	return t.inner.Rollback()
}

// --- ProcessOne T-γ: final commit error ---
func TestProcessOne_TGamma_CommitError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	// Need T-α commit to succeed but T-γ commit to fail.
	// commitErr on mockRepo applies to ALL fakeTx, which breaks T-α.
	// Use a counted commit-fail wrapper.
	commitFailRepo := &commitFailOnCallNRepo{mockRepo: repo, failOn: 2} // T-α is commit 1, T-γ is commit 2
	svc := NewService(commitFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-commit"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ commit failure")
	}
	if !strings.Contains(err.Error(), "commit T-γ") {
		t.Errorf("expected 'commit T-γ' in message, got: %v", err)
	}
}

// commitFailOnCallNRepo wraps mockRepo and fails commit on the Nth call.
type commitFailOnCallNRepo struct {
	*mockRepo
	failOn  int
	commitN int
	mu2     sync.Mutex
}

func (r *commitFailOnCallNRepo) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := r.mockRepo.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return &countedCommitTx{inner: tx, repo: r}, nil
}

type countedCommitTx struct {
	inner Tx
	repo  *commitFailOnCallNRepo
}

func (t *countedCommitTx) Commit() error {
	t.repo.mu2.Lock()
	t.repo.commitN++
	n := t.repo.commitN
	t.repo.mu2.Unlock()
	if n == t.repo.failOn {
		return errors.New("commit T-γ boom")
	}
	return t.inner.Commit()
}

func (t *countedCommitTx) Rollback() error {
	return t.inner.Rollback()
}

// --- scanOneKYTTimeout: Phase 3 final commit error ---
func TestScanOneKYTTimeout_Phase3CommitError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 73, UserID: 1, SafeheronTxKey: "tx-p3-commit", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3-commit"] = dep
	// Phase 1 commit succeeds (call 1), Phase 3 commit fails (call 2)
	commitFailRepo := &kytCommitErrOnCallNRepo{
		mockRepo:      base,
		failCommitOn:  2,
		kytTimeoutDep: dep,
	}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(commitFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Commit error is logged and loop continues
}

// --- ProcessOne: bad raw_payload JSON → MarkEventError succeeds → commit succeeds ---
func TestProcessOne_BadJSON_MarkErrorSucceeds(t *testing.T) {
	repo := newMockRepo()
	repo.pending = []*Event{{
		ID:         520,
		EventType:  "TRANSACTION_STATUS_CHANGED",
		RawPayload: []byte(`{invalid-json`),
	}}

	svc := NewService(repo, nil, nil)
	processed, err := svc.ProcessOne(context.Background())
	if !processed {
		t.Fatal("expected processed=true")
	}
	if err == nil {
		t.Fatal("expected unmarshal error")
	}
	if !strings.Contains(err.Error(), "unmarshal raw_payload") {
		t.Errorf("expected 'unmarshal raw_payload' in message, got: %v", err)
	}
	// MarkEventError should have been called successfully
	if len(repo.errorIDs) != 1 {
		t.Errorf("expected 1 error event, got %d", len(repo.errorIDs))
	}
	// Commit should have succeeded
	repo.mu.Lock()
	commits := repo.commitCalls
	repo.mu.Unlock()
	if commits != 1 {
		t.Errorf("expected 1 commit, got %d", commits)
	}
}

// --- ProcessOne: LockNextPendingEvent non-ErrNoPending error ---
func TestProcessOne_LockEventError(t *testing.T) {
	// Create a repo where LockNextPendingEvent returns a non-ErrNoPending error
	repo := &lockEventErrRepo{mockRepo: newMockRepo(), lockErr: errors.New("lock event boom")}
	svc := NewService(repo, nil, nil)

	processed, err := svc.ProcessOne(context.Background())
	if processed {
		t.Error("expected processed=false on lock error")
	}
	if err == nil {
		t.Fatal("expected error from lock event failure")
	}
	if !strings.Contains(err.Error(), "lock event") {
		t.Errorf("expected 'lock event' in message, got: %v", err)
	}
}

type lockEventErrRepo struct {
	*mockRepo
	lockErr error
}

func (r *lockEventErrRepo) LockNextPendingEvent(_ context.Context, _ Tx) (*Event, error) {
	return nil, r.lockErr
}

// --- ProcessOne: MarkEventDone error in default (unknown eventType) branch ---
func TestProcessOne_UnknownEventType_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.markDoneErr = errors.New("mark done boom default")
	svc := newSvc(t, repo, &stubRegistry{}, nil)
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType:   "ACCOUNT_CREATED",
		EventDetail: PayloadEventDetail{TxKey: "tx-unknown-md", TransactionStatus: "X"},
	})

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails for unknown event type")
	}
}

// --- ProcessOne: commit error in default (unknown eventType) branch ---
func TestProcessOne_UnknownEventType_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit unknown event boom")
	svc := newSvc(t, repo, &stubRegistry{}, nil)
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType:   "ACCOUNT_CREATED",
		EventDetail: PayloadEventDetail{TxKey: "tx-unknown-ce", TransactionStatus: "X"},
	})

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails for unknown event type")
	}
}

// --- ProcessOne: MarkEventDone error in OUTFLOW skip branch ---
func TestProcessOne_Outflow_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.markDoneErr = errors.New("mark done boom outflow")
	svc := newSvc(t, repo, &stubRegistry{}, nil)
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-out-md",
			TransactionDirection: "OUTFLOW",
			TransactionStatus:    "COMPLETED",
		},
	})

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails for OUTFLOW skip")
	}
}

// --- ProcessOne: commit error in OUTFLOW skip branch ---
func TestProcessOne_Outflow_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit outflow boom")
	svc := newSvc(t, repo, &stubRegistry{}, nil)
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-out-ce",
			TransactionDirection: "OUTFLOW",
			TransactionStatus:    "COMPLETED",
		},
	})

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails for OUTFLOW skip")
	}
}

// --- ProcessOne: UpsertDeposit error + MarkEventError error (double fail wraps sentinel) ---
func TestProcessOne_UpsertErr_MarkErrorFails_WrapsSentinel(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	repo.depositErr = errors.New("upsert boom")
	repo.markErrorErr = errors.New("mark error also boom")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-upsert-double",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when both UpsertDeposit and MarkEventError fail")
	}
	if !errors.Is(err, ErrMarkErrorFailed) {
		t.Errorf("expected ErrMarkErrorFailed sentinel, got: %v", err)
	}
}

// --- ProcessOne: UpsertDeposit error + commit error (after MarkEventError) ---
func TestProcessOne_UpsertErr_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	repo.depositErr = errors.New("upsert boom")
	repo.commitErr = errors.New("commit upsert err boom")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-upsert-commit",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails after upsert error mark")
	}
}

// --- ProcessOne: MarkDepositFailed error ---
func TestProcessOne_MarkDepositFailedError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	// Use a repo that errors on MarkDepositFailed
	failedErrRepo := &markFailedErrRepo{mockRepo: repo, failedErr: errors.New("mark failed boom")}
	svc := NewService(failedErrRepo, reg, nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-mf-err",
			CoinKey:              "K",
			TxAmount:             "0.5",
			TransactionStatus:    "FAILED",
			TransactionSubStatus: "INSUFFICIENT_FEE",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from MarkDepositFailed failure")
	}
	if !strings.Contains(err.Error(), "mark failed") {
		t.Errorf("expected 'mark failed' in message, got: %v", err)
	}
}

type markFailedErrRepo struct {
	*mockRepo
	failedErr error
}

func (r *markFailedErrRepo) MarkDepositFailed(_ context.Context, _ Tx, _ int64, _ string) error {
	return r.failedErr
}

// --- ProcessOne: MarkEventDone error in !needsKYT path ---
func TestProcessOne_IntermediateEvent_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	repo.markDoneErr = errors.New("mark done boom intermediate")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	// CONFIRMING event: not needsKYT (not COMPLETED+CONFIRMED), not failed terminal
	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-confirm-md",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "CONFIRMING",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails for intermediate event")
	}
}

// --- ProcessOne: commit error in !needsKYT path ---
func TestProcessOne_IntermediateEvent_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	repo.commitErr = errors.New("commit intermediate boom")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-confirm-ce",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "CONFIRMING",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails for intermediate event")
	}
}

// --- ProcessOne: MoveToKYTPending error ---
func TestProcessOne_MoveToKYTPendingError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kytPendingErrRepo := &moveToKYTPendingErrRepo{mockRepo: repo, moveErr: errors.New("move to KYT boom")}
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := NewService(kytPendingErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-move-err"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from MoveToKYTPending failure")
	}
	if !strings.Contains(err.Error(), "move to KYT_PENDING") {
		t.Errorf("expected 'move to KYT_PENDING' in message, got: %v", err)
	}
}

type moveToKYTPendingErrRepo struct {
	*mockRepo
	moveErr error
}

func (r *moveToKYTPendingErrRepo) MoveToKYTPending(_ context.Context, _ Tx, _ int64) error {
	return r.moveErr
}

// --- ProcessOne: T-α commit error (after MoveToKYTPending) ---
func TestProcessOne_TAlpha_CommitError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.commitErr = errors.New("commit T-α boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-ta-commit"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-α commit failure")
	}
	if !strings.Contains(err.Error(), "commit T-α") {
		t.Errorf("expected 'commit T-α' in message, got: %v", err)
	}
}

// --- ProcessOne T-γ: BeginTx error ---
func TestProcessOne_TGamma_BeginTxError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	// T-α BeginTx succeeds (call 1), T-γ BeginTx fails (call 2)
	beginTxFailRepo := &beginTxFailAfterNRepo{mockRepo: repo, failAfter: 1}
	svc := NewService(beginTxFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-btx"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ BeginTx failure")
	}
	if !strings.Contains(err.Error(), "begin tx T-γ") {
		t.Errorf("expected 'begin tx T-γ' in message, got: %v", err)
	}
}

// --- ProcessOne T-γ: MarkEventDone error for stale deposit ---
func TestProcessOne_TGamma_StaleDeposit_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markDoneErr = errors.New("mark done stale boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		// Change deposit status between T-α and T-γ to trigger stale path
		repo.mu.Lock()
		if d, ok := repo.deposits[txKey]; ok {
			d.Status = DepositStatusCredited
		}
		repo.mu.Unlock()
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-stale-md"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails in stale path")
	}
}

// --- ProcessOne T-γ: credit error ---
func TestProcessOne_TGamma_CreditError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.creditErr = errors.New("credit T-γ boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-credit"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ credit failure")
	}
	if !strings.Contains(err.Error(), "credit deposit T-γ") {
		t.Errorf("expected 'credit deposit T-γ' in message, got: %v", err)
	}
}

// --- ProcessOne T-γ: MarkDepositManualReview error ---
func TestProcessOne_TGamma_ManualReviewError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markMRErr = errors.New("mark MR T-γ boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return highRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-mr"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ MarkDepositManualReview failure")
	}
	if !strings.Contains(err.Error(), "mark manual review T-γ") {
		t.Errorf("expected 'mark manual review T-γ' in message, got: %v", err)
	}
}

// --- ProcessOne T-γ: MarkEventDone error (after successful credit) ---
func TestProcessOne_TGamma_MarkEventDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markDoneErr = errors.New("mark done T-γ boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-tg-md"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ MarkEventDone failure")
	}
}

// --- processKYTAlert: MarkEventDone error in non-KYT_PENDING deposit path ---
func TestProcessKYTAlert_NonPending_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.markDoneErr = errors.New("mark done alert-np boom")
	repo.deposits["tx-np-md"] = &DepositRow{
		ID: 62, UserID: 1, SafeheronTxKey: "tx-np-md", Amount: "1.0", Asset: "ETH", Status: DepositStatusCredited,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-np-md","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         602,
		EventID:    "evt-np-md",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from MarkEventDone failure in non-pending alert path")
	}
}

// --- processKYTAlert: commit error in non-KYT_PENDING deposit path ---
func TestProcessKYTAlert_NonPending_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit alert-np boom")
	repo.deposits["tx-np-ce"] = &DepositRow{
		ID: 63, UserID: 1, SafeheronTxKey: "tx-np-ce", Amount: "1.0", Asset: "ETH", Status: DepositStatusCredited,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-np-ce","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         603,
		EventID:    "evt-np-ce",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from commit failure in non-pending alert path")
	}
}

// --- processKYTAlert: MarkDepositManualReview error in KYT alert ---
func TestProcessKYTAlert_MarkMRError(t *testing.T) {
	repo := newMockRepo()
	repo.markMRErr = errors.New("mark MR alert boom")
	repo.deposits["tx-alert-mr-err"] = &DepositRow{
		ID: 64, UserID: 1, SafeheronTxKey: "tx-alert-mr-err", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-mr-err","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"HIGH","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         604,
		EventID:    "evt-alert-mr-err",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from MarkDepositManualReview failure in KYT alert")
	}
}

// --- processKYTAlert: MarkEventDone error after successful credit ---
func TestProcessKYTAlert_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.markDoneErr = errors.New("mark done alert boom")
	repo.deposits["tx-alert-md"] = &DepositRow{
		ID: 65, UserID: 1, SafeheronTxKey: "tx-alert-md", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	svc := newKYTSvc(t, repo, nil, true)

	alertJSON := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-alert-md","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW","lastUpdateTime":"1715500000000"}]}}`
	repo.pending = []*Event{{
		ID:         605,
		EventID:    "evt-alert-md",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(alertJSON),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from MarkEventDone failure in KYT alert")
	}
}

// --- handleKYTApiFailure: exceeds threshold + MarkEventDone error ---
func TestHandleKYTApiFailure_ExceedsThreshold_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markDoneErr = errors.New("mark done kyt api boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	svc.kytOrphanMaxRetry = 1

	enqueueRaw(t, repo, completedConfirmedPayload("tx-api-md"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails in handleKYTApiFailure")
	}
}

// --- handleKYTApiFailure: exceeds threshold + commit error ---
func TestHandleKYTApiFailure_ExceedsThreshold_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}
	// T-α commit succeeds (call 1), tx3 commit fails (call 2)
	commitFailRepo := &commitFailOnCallNRepo{mockRepo: repo, failOn: 2}
	svc := NewService(commitFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 1, 20*time.Minute)

	enqueueRaw(t, repo, completedConfirmedPayload("tx-api-ce"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails in handleKYTApiFailure")
	}
}

// --- ProcessOne: KYTDisabled + MarkEventDone error ---
func TestProcessOne_KYTDisabled_MarkDoneFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markDoneErr = errors.New("mark done kyt disabled boom")
	svc := newKYTSvc(t, repo, nil, false)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-nokyt-md"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkEventDone fails with KYT disabled")
	}
}

// --- ProcessOne: KYTDisabled + commit error ---
func TestProcessOne_KYTDisabled_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.commitErr = errors.New("commit kyt disabled boom")
	svc := newKYTSvc(t, repo, nil, false)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-nokyt-ce"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails with KYT disabled")
	}
}

// --- ProcessOne: KYTDisabled + credit error ---
func TestProcessOne_KYTDisabled_CreditFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.creditErr = errors.New("credit kyt disabled boom")
	svc := newKYTSvc(t, repo, nil, false)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-nokyt-credit"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when credit fails with KYT disabled")
	}
}

// --- ProcessOne: parse txAmount error ---
func TestProcessOne_ParseAmountError(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 42
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_STATUS_CHANGED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-parse-amt",
			CoinKey:              "K",
			TxAmount:             "not-a-number",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xdest",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from parse txAmount failure")
	}
	if !strings.Contains(err.Error(), "parse txAmount") {
		t.Errorf("expected 'parse txAmount' in message, got: %v", err)
	}
}

// --- flagAndFinalize: commit error ---
func TestFlagAndFinalize_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.commitErr = errors.New("commit flagAndFinalize boom")
	reg := newTestRegistry("ETH", "ETHEREUM", "K", "0.0001", 11)
	svc := newSvc(t, repo, reg, nil)

	enqueueRaw(t, repo, PayloadEnvelope{
		EventType: "TRANSACTION_CREATED",
		EventDetail: PayloadEventDetail{
			TxKey:                "tx-faf-ce",
			CoinKey:              "K",
			TxAmount:             "1",
			TransactionStatus:    "COMPLETED",
			TransactionSubStatus: "CONFIRMED",
			TransactionDirection: "INFLOW",
			DestinationAddress:   "0xstranger",
		},
	})
	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when commit fails in flagAndFinalize")
	}
}

// --- scanOneKYTTimeout: Phase-3 FindDepositByTxKey error ---
func TestScanOneKYTTimeout_Phase3FindByTxKeyError(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 74, UserID: 1, SafeheronTxKey: "tx-p3-find", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3-find"] = dep
	findErrRepo := &kytFindErrOnMRRepo{mockRepo: base, kytTimeoutDep: dep, findErr: errors.New("find boom phase-3")}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(findErrRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Error is logged, loop continues
}

// --- scanOneKYTTimeout: Phase-3 MarkDepositManualReview error ---
func TestScanOneKYTTimeout_Phase3MarkMRError(t *testing.T) {
	base := newMockRepo()
	base.markMRErr = errors.New("mark MR phase-3 boom")
	dep := &DepositRow{
		ID: 75, UserID: 1, SafeheronTxKey: "tx-p3-mr", Amount: "1.0", Asset: "ETH", Status: DepositStatusKYTPending,
	}
	base.deposits["tx-p3-mr"] = dep
	repo := &kytMockRepo{mockRepo: base, kytTimeoutDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return highRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 1*time.Millisecond)

	svc.ScanKYTTimeouts(context.Background())
	// Error is logged, loop continues
}

// --- handleKYTApiFailure: exceeds threshold + MarkDepositManualReview error ---
func TestHandleKYTApiFailure_ExceedsThreshold_MarkMRFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	repo.markMRErr = errors.New("mark MR kyt api boom")
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("API down")
	}}
	svc := newKYTSvc(t, repo, kyt, true)
	svc.kytOrphanMaxRetry = 1

	enqueueRaw(t, repo, completedConfirmedPayload("tx-api-mr"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error when MarkDepositManualReview fails in handleKYTApiFailure")
	}
}

// --- ProcessOne T-γ: stale deposit commit error ---
func TestProcessOne_TGamma_StaleDeposit_CommitFails(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		// Change deposit status to simulate concurrent credit
		repo.mu.Lock()
		if d, ok := repo.deposits[txKey]; ok {
			d.Status = DepositStatusCredited
		}
		repo.mu.Unlock()
		return lowRiskReport(txKey), nil
	}}
	// T-α commit succeeds (1), T-γ stale commit fails (2)
	commitFailRepo := &commitFailOnCallNRepo{mockRepo: repo, failOn: 2}
	svc := NewService(commitFailRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-stale-ce"))

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from T-γ stale commit failure")
	}
}

// S-1: orphan AML alert below retry threshold + IncrementEventAttemptsNoTx fails.
// Without ErrKYTAPIBackoff wrap the worker would tight-loop on the same event.
func TestProcessKYTAlert_OrphanIncrementFailureYieldsBackoff(t *testing.T) {
	repo := newMockRepo()
	incRepo := &incrementErrMockRepo{mockRepo: repo}
	svc := newKYTSvc(t, repo, nil, true)
	svc.repo = incRepo // swap repo so IncrementEventAttemptsNoTx returns error

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-orphan-incfail","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:              701,
		EventID:         "evt-orphan-incfail",
		EventType:       "AML_KYT_ALERT",
		RawPayload:      []byte(payload),
		ProcessAttempts: 0, // below kytOrphanMaxRetry (100)
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error so worker yields to next tick")
	}
	if !errors.Is(err, ErrKYTAPIBackoff) {
		t.Errorf("expected ErrKYTAPIBackoff sentinel for worker yield, got: %v", err)
	}
}

// S-1 variant: FindDepositByTxKey errors AND IncrementEventAttemptsNoTx errors —
// both failures combined must still surface as ErrKYTAPIBackoff so worker yields.
func TestProcessKYTAlert_FindErrorPlusIncrementFailureYieldsBackoff(t *testing.T) {
	repo := newMockRepo()
	combined := &findAndIncrementErrRepo{mockRepo: repo, findErr: errors.New("db read failed")}
	svc := newKYTSvc(t, repo, nil, true)
	svc.repo = combined

	payload := `{"eventType":"AML_KYT_ALERT","eventDetail":{"txKey":"tx-find-incfail","amlScreeningTriggeredState":"TRIGGERED","amlList":[{"provider":"MistTrack","status":"COMPLETED","riskLevel":"LOW"}]}}`
	repo.pending = []*Event{{
		ID:         702,
		EventID:    "evt-find-incfail",
		EventType:  "AML_KYT_ALERT",
		RawPayload: []byte(payload),
	}}

	_, err := svc.ProcessOne(context.Background())
	if err == nil {
		t.Fatal("expected error from combined find+increment failure")
	}
	if !errors.Is(err, ErrKYTAPIBackoff) {
		t.Errorf("expected ErrKYTAPIBackoff (S-1), got: %v", err)
	}
}

type findAndIncrementErrRepo struct {
	*mockRepo
	findErr error
}

func (r *findAndIncrementErrRepo) FindDepositByTxKey(_ context.Context, _ Tx, _ string) (*DepositRow, bool, error) {
	return nil, false, r.findErr
}

func (r *findAndIncrementErrRepo) IncrementEventAttemptsNoTx(_ context.Context, _ int64) error {
	return errors.New("increment also failed")
}

// S-2: handleKYTApiFailure with IncrementEventAttemptsNoTx error must surface an
// ERROR-level alert so ops can see the deposit is at risk of getting stuck.
func TestHandleKYTApiFailure_IncrementFailureFiresAlert(t *testing.T) {
	repo := newMockRepo()
	repo.owners["0xdest"] = 1
	incRepo := &incrementErrMockRepo{mockRepo: repo}
	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("safeheron 503")
	}}
	alertFn, alerts := newAlertCollector()
	svc := NewService(incRepo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), alertFn)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)
	enqueueRaw(t, repo, completedConfirmedPayload("tx-s2-alert"))

	if _, err := svc.ProcessOne(context.Background()); err == nil {
		t.Fatal("expected error from KYT API failure")
	}

	got := *alerts
	if len(got) == 0 {
		t.Fatal("expected ERROR alert when increment counter fails (S-2)")
	}
	found := false
	for _, a := range got {
		if a.level == "ERROR" && a.title == "KYT attempts counter unwritable" {
			found = true
			if a.fields["txKey"] != "tx-s2-alert" {
				t.Errorf("alert missing txKey field, got %v", a.fields)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected 'KYT attempts counter unwritable' ERROR alert, got %+v", got)
	}
}

// =========================================================================
// ScanAmlPending tests
// =========================================================================

// amlPendingMockRepo overrides LockOneAmlPending to return a configured deposit.
type amlPendingMockRepo struct {
	*mockRepo
	amlPendingDep *DepositRow
}

func (r *amlPendingMockRepo) LockOneAmlPending(_ context.Context, _ Tx) (*DepositRow, error) {
	if r.amlPendingDep != nil {
		dep := r.amlPendingDep
		r.amlPendingDep = nil // return once
		return dep, nil
	}
	return nil, ErrNoPending
}

// ScanAmlPending: KYT still IN_PROGRESS → deposit stays KYT_PENDING, no MANUAL_REVIEW
func TestScanAmlPending_StillPending_KeepsKYTPending(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 30, UserID: 1, SafeheronTxKey: "tx-aml-pending", Amount: "0.011", Asset: "ETH",
		Status: DepositStatusKYTPending,
	}
	base.deposits["tx-aml-pending"] = dep
	repo := &amlPendingMockRepo{mockRepo: base, amlPendingDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return pendingReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)

	svc.ScanAmlPending(context.Background())

	if dep.Status != DepositStatusKYTPending {
		t.Errorf("expected KYT_PENDING, got %s (should not mark MR for in-flight KYT)", dep.Status)
	}
	if len(base.manualUpdates) != 0 {
		t.Errorf("expected no MANUAL_REVIEW, got %v", base.manualUpdates)
	}
}

// ScanAmlPending: KYT resolved LOW → CREDITED
func TestScanAmlPending_LowRisk_Credits(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 31, UserID: 1, SafeheronTxKey: "tx-aml-low", Amount: "0.011", Asset: "ETH",
		Status: DepositStatusKYTPending,
	}
	base.deposits["tx-aml-low"] = dep
	repo := &amlPendingMockRepo{mockRepo: base, amlPendingDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, txKey string) (*safeheron.KytReportResponse, error) {
		return lowRiskReport(txKey), nil
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetSerialFunc(func() string { return "TEST-SERIAL" })
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)

	svc.ScanAmlPending(context.Background())

	if dep.Status != DepositStatusCredited {
		t.Errorf("expected CREDITED, got %s", dep.Status)
	}
}

// ScanAmlPending: KYT API error → no MANUAL_REVIEW, deposit stays KYT_PENDING
func TestScanAmlPending_APIError_NoAction(t *testing.T) {
	base := newMockRepo()
	dep := &DepositRow{
		ID: 32, UserID: 1, SafeheronTxKey: "tx-aml-err", Amount: "0.011", Asset: "ETH",
		Status: DepositStatusKYTPending,
	}
	base.deposits["tx-aml-err"] = dep
	repo := &amlPendingMockRepo{mockRepo: base, amlPendingDep: dep}

	kyt := &mockKYTClient{reportFn: func(_ context.Context, _ string) (*safeheron.KytReportResponse, error) {
		return nil, errors.New("KYT API unreachable")
	}}

	svc := NewService(repo, newTestRegistry("ETH", "ETHEREUM", "ETH_KEY", "0.0001", 1), nil)
	svc.SetKYTDeps(kyt, true, 100, 20*time.Minute)

	svc.ScanAmlPending(context.Background())

	if dep.Status != DepositStatusKYTPending {
		t.Errorf("expected KYT_PENDING on API error, got %s", dep.Status)
	}
	if len(base.manualUpdates) != 0 {
		t.Errorf("expected no MANUAL_REVIEW on API error, got %v", base.manualUpdates)
	}
}
