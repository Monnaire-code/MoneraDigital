package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"monera-digital/internal/safeheron"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Mock repository for service tests
// ---------------------------------------------------------------------------

type mockRepo struct {
	insertApprovalErr error
	insertSweepErr    error
	getApprovalResult *ApprovalRecord
	getApprovalErr    error
	updateSweepErr    error

	insertedApproval *ApprovalRecord
	insertedSweep    *SweepTransaction
	updatedTxKey     string
}

func (m *mockRepo) InsertApprovalRecord(_ context.Context, rec *ApprovalRecord) error {
	m.insertedApproval = rec
	if m.insertApprovalErr != nil {
		return m.insertApprovalErr
	}
	rec.ID = 1
	rec.CreatedAt = time.Now()
	return nil
}

func (m *mockRepo) GetApprovalByID(_ context.Context, _ string) (*ApprovalRecord, error) {
	return m.getApprovalResult, m.getApprovalErr
}

func (m *mockRepo) InsertSweepTransaction(_ context.Context, st *SweepTransaction) error {
	m.insertedSweep = st
	if m.insertSweepErr != nil {
		return m.insertSweepErr
	}
	st.ID = 1
	st.CreatedAt = time.Now()
	st.UpdatedAt = time.Now()
	return nil
}

func (m *mockRepo) UpdateSweepStatus(_ context.Context, txKey, _, _, _ string, _ *time.Time) error {
	m.updatedTxKey = txKey
	return m.updateSweepErr
}

// ---------------------------------------------------------------------------
// Alert capture
// ---------------------------------------------------------------------------

type alertCapture struct {
	calls []map[string]string
}

func (a *alertCapture) fn() AlertFunc {
	return func(level, title string, fields map[string]string) {
		fields["_level"] = level
		fields["_title"] = title
		a.calls = append(a.calls, fields)
	}
}

// ---------------------------------------------------------------------------
// Service.Evaluate — TRANSACTION APPROVE path
// ---------------------------------------------------------------------------

func TestServiceEvaluate_TransactionApprove(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
	if repo.insertedApproval == nil {
		t.Fatal("approval record not inserted")
	}
	if repo.insertedApproval.ChainSymbol != "ETH" {
		t.Errorf("chainSymbol = %q, want ETH", repo.insertedApproval.ChainSymbol)
	}
	var rawBiz safeheron.CoSignerBizContentV3
	if err := json.Unmarshal(repo.insertedApproval.RawRequest, &rawBiz); err != nil {
		t.Fatalf("raw_request should be full bizContent JSON: %v", err)
	}
	if rawBiz.ApprovalId != "ap-1" {
		t.Errorf("raw_request.approvalId = %q, want ap-1", rawBiz.ApprovalId)
	}
	if rawBiz.Type != "TRANSACTION" {
		t.Errorf("raw_request.type = %q, want TRANSACTION", rawBiz.Type)
	}
	if repo.insertedSweep == nil {
		t.Fatal("sweep transaction not inserted for APPROVE")
	}
	if repo.insertedSweep.TxKey != "tx-1" {
		t.Errorf("sweep txKey = %q, want tx-1", repo.insertedSweep.TxKey)
	}
	if repo.insertedSweep.ChainSymbol != "ETH" {
		t.Errorf("sweep chainSymbol = %q, want ETH", repo.insertedSweep.ChainSymbol)
	}
	if len(alerts.calls) != 0 {
		t.Errorf("APPROVE should not trigger alert, got %d calls", len(alerts.calls))
	}
}

// ---------------------------------------------------------------------------
// TRANSACTION REJECT path
// ---------------------------------------------------------------------------

func TestServiceEvaluate_TransactionReject(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := makeBiz("AUTO_SWEEP", "EXTERNAL_ADDRESS", "acct-main")
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
	if repo.insertedSweep != nil {
		t.Error("sweep should NOT be inserted for REJECT")
	}
	if len(alerts.calls) != 1 {
		t.Fatalf("REJECT should trigger 1 alert, got %d", len(alerts.calls))
	}
	if alerts.calls[0]["approvalId"] != "ap-1" {
		t.Errorf("alert approvalId = %q", alerts.calls[0]["approvalId"])
	}
}

// ---------------------------------------------------------------------------
// CALLBACK_TEST
// ---------------------------------------------------------------------------

func TestServiceEvaluate_CallbackTest(t *testing.T) {
	repo := &mockRepo{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "test-1",
		Type:       "CALLBACK_TEST",
		Detail:     json.RawMessage(`{}`),
	}
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
	if repo.insertedApproval == nil {
		t.Fatal("CALLBACK_TEST should still insert approval record")
	}
	if repo.insertedApproval.CallbackType != "CALLBACK_TEST" {
		t.Errorf("callbackType = %q", repo.insertedApproval.CallbackType)
	}
	if repo.insertedSweep != nil {
		t.Error("CALLBACK_TEST should NOT insert sweep")
	}
}

// ---------------------------------------------------------------------------
// MPC_SIGN / WEB3_SIGN → default REJECT
// ---------------------------------------------------------------------------

func TestServiceEvaluate_MPCSign_DefaultReject(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "mpc-1",
		Type:       "MPC_SIGN",
		Detail:     json.RawMessage(`{}`),
	}
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
	if len(alerts.calls) != 1 {
		t.Errorf("should trigger REJECT alert, got %d", len(alerts.calls))
	}
}

func TestServiceEvaluate_UnknownType_DefaultReject(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "x-1",
		Type:       "BRAND_NEW_TYPE",
		Detail:     json.RawMessage(`{}`),
	}
	dec, _ := svc.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// Idempotent: duplicate approvalId
// ---------------------------------------------------------------------------

func TestServiceEvaluate_Idempotent(t *testing.T) {
	repo := &mockRepo{
		insertApprovalErr: ErrDuplicateApproval,
		getApprovalResult: &ApprovalRecord{
			ApprovalID: "ap-1",
			Action:     "APPROVE",
			Reason:     "AUTO_SWEEP approved",
		},
	}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("idempotent should return first decision, got %q", dec.Action)
	}
}

func TestServiceEvaluate_Idempotent_LookupFails(t *testing.T) {
	repo := &mockRepo{
		insertApprovalErr: ErrDuplicateApproval,
		getApprovalErr:    errors.New("db down"),
	}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	_, err := svc.Evaluate(context.Background(), biz)
	if err == nil {
		t.Fatal("expected error when idempotent lookup fails")
	}
}

// ---------------------------------------------------------------------------
// DB errors
// ---------------------------------------------------------------------------

func TestServiceEvaluate_InsertApprovalDBError(t *testing.T) {
	repo := &mockRepo{insertApprovalErr: sql.ErrConnDone}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	_, err := svc.Evaluate(context.Background(), biz)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestServiceEvaluate_InsertSweepDBError(t *testing.T) {
	repo := &mockRepo{insertSweepErr: sql.ErrConnDone}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	_, err := svc.Evaluate(context.Background(), biz)
	if err == nil {
		t.Fatal("expected error when sweep insert fails")
	}
}

func TestServiceEvaluate_InsertSweepDuplicate_Tolerated(t *testing.T) {
	repo := &mockRepo{insertSweepErr: ErrDuplicateSweepTx}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("duplicate sweep should be tolerated, got %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// Alert with nil alertFn (no panic)
// ---------------------------------------------------------------------------

func TestServiceEvaluate_NilAlertFn_NoPanic(t *testing.T) {
	repo := &mockRepo{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := makeBiz("AUTO_SWEEP", "EXTERNAL_ADDRESS", "acct-main")
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// CALLBACK_TEST with nil detail
// ---------------------------------------------------------------------------

func TestServiceEvaluate_CallbackTest_NilDetail(t *testing.T) {
	repo := &mockRepo{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "test-nil",
		Type:       "CALLBACK_TEST",
	}
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
	if repo.insertedApproval.RawRequest == nil {
		t.Error("raw_request should default to {}")
	}
}

// ---------------------------------------------------------------------------
// TRANSACTION APPROVE with non-empty TransactionStatus in detail
// ---------------------------------------------------------------------------

func TestServiceEvaluate_TransactionApprove_PreservesDetailStatus(t *testing.T) {
	repo := &mockRepo{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	detail := safeheron.TransactionApproval{
		TxKey:                  "tx-status",
		CoinKey:                "ETH",
		TxAmount:               "1.5",
		TransactionType:        "AUTO_SWEEP",
		TransactionStatus:      "SIGNING",
		DestinationAccountKey:  "acct-main",
		DestinationAccountType: "VAULT_ACCOUNT",
		SourceAddress:          "0xsrc",
	}
	data, _ := json.Marshal(detail)
	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "ap-status",
		Type:       "TRANSACTION",
		Detail:     data,
	}

	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
	if repo.insertedSweep == nil {
		t.Fatal("sweep should be inserted")
	}
	if repo.insertedSweep.TxStatus != "SIGNING" {
		t.Errorf("sweep TxStatus = %q, want SIGNING (preserved from detail)", repo.insertedSweep.TxStatus)
	}
}

// ---------------------------------------------------------------------------
// Non-TRANSACTION REJECT should have empty sourceAddress in alert
// ---------------------------------------------------------------------------

func TestServiceEvaluate_NonTransactionReject_NoSourceAddress(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "web3-1",
		Type:       "WEB3_SIGN",
		Detail:     json.RawMessage(`{}`),
	}
	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
	if len(alerts.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts.calls))
	}
	if _, has := alerts.calls[0]["sourceAddress"]; has {
		t.Error("non-TRANSACTION reject should NOT have sourceAddress in alert")
	}
	if _, has := alerts.calls[0]["txKey"]; has {
		t.Error("non-TRANSACTION reject should NOT have txKey in alert")
	}
}

// ---------------------------------------------------------------------------
// Sweep with all detail fields populated
// ---------------------------------------------------------------------------

func TestServiceEvaluate_SweepFieldsComplete(t *testing.T) {
	repo := &mockRepo{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, nil)

	detail := safeheron.TransactionApproval{
		TxKey:                  "tx-full",
		TxHash:                 "0xhash123",
		CustomerRefId:          "cust-ref-1",
		CoinKey:                "USDT_ERC20",
		FeeCoinKey:             "ETH",
		TxAmount:               "500",
		EstimateFee:            "0.005",
		TransactionType:        "AUTO_SWEEP",
		SourceAccountKey:       "src-acct",
		SourceAddress:          "0xsrc",
		DestinationAccountKey:  "acct-main",
		DestinationAccountType: "VAULT_ACCOUNT",
		DestinationAddress:     "0xdst",
	}
	data, _ := json.Marshal(detail)
	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "ap-full",
		Type:       "TRANSACTION",
		Detail:     data,
	}

	dec, err := svc.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Fatalf("action = %q, want APPROVE", dec.Action)
	}

	st := repo.insertedSweep
	if st == nil {
		t.Fatal("sweep not inserted")
	}
	if st.TxHash != "0xhash123" {
		t.Errorf("TxHash = %q", st.TxHash)
	}
	if st.CustomerRefID != "cust-ref-1" {
		t.Errorf("CustomerRefID = %q", st.CustomerRefID)
	}
	if st.FeeCoinKey != "ETH" {
		t.Errorf("FeeCoinKey = %q", st.FeeCoinKey)
	}
	if st.EstimateFee != "0.005" {
		t.Errorf("EstimateFee = %q", st.EstimateFee)
	}
	if st.SourceAccountKey != "src-acct" {
		t.Errorf("SourceAccountKey = %q", st.SourceAccountKey)
	}
	if st.SourceAddress != "0xsrc" {
		t.Errorf("SourceAddress = %q", st.SourceAddress)
	}
	if st.ApprovalID != "ap-full" {
		t.Errorf("ApprovalID = %q", st.ApprovalID)
	}
	if st.ApprovalAction != "APPROVE" {
		t.Errorf("ApprovalAction = %q", st.ApprovalAction)
	}
	if st.TxStatus != "PENDING" {
		t.Errorf("TxStatus = %q, want PENDING (default)", st.TxStatus)
	}

	rec := repo.insertedApproval
	if rec.CustomerRefID != "cust-ref-1" {
		t.Errorf("approval CustomerRefID = %q", rec.CustomerRefID)
	}
	if rec.SourceAccountKey != "src-acct" {
		t.Errorf("approval SourceAccountKey = %q", rec.SourceAccountKey)
	}
}

// ---------------------------------------------------------------------------
// Alert fields verification
// ---------------------------------------------------------------------------

func TestServiceEvaluate_RejectAlertFields(t *testing.T) {
	repo := &mockRepo{}
	alerts := &alertCapture{}
	txA := NewTransactionApprover(newTestConfig(), newTestRegistry())
	svc := NewApprovalService(repo, txA, alerts.fn())

	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "unknown-acct")
	svc.Evaluate(context.Background(), biz)

	if len(alerts.calls) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts.calls))
	}
	a := alerts.calls[0]
	if a["_level"] != "ERROR" {
		t.Errorf("level = %q, want ERROR", a["_level"])
	}
	if a["txKey"] != "tx-1" {
		t.Errorf("txKey = %q, want tx-1", a["txKey"])
	}
	if a["coinKey"] != "USDT_ERC20" {
		t.Errorf("coinKey = %q, want USDT_ERC20", a["coinKey"])
	}
	if a["txAmount"] != "100" {
		t.Errorf("txAmount = %q, want 100", a["txAmount"])
	}
	if a["txType"] != "AUTO_SWEEP" {
		t.Errorf("txType = %q, want AUTO_SWEEP", a["txType"])
	}
	if a["sourceAddress"] != "0xdef" {
		t.Errorf("sourceAddress = %q, want 0xdef", a["sourceAddress"])
	}
	if a["destinationAddress"] != "0xabc" {
		t.Errorf("destinationAddress = %q, want 0xabc", a["destinationAddress"])
	}
}
