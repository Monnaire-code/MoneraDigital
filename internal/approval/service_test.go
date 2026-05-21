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
