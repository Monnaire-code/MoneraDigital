package deposit

import (
	"context"
	"errors"
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
