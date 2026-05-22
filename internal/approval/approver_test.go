package approval

import (
	"context"
	"testing"

	"monera-digital/internal/safeheron"
)

func TestDefaultRejectApprover(t *testing.T) {
	approver := &DefaultRejectApprover{}

	tests := []struct {
		name    string
		bizType string
		wantSub string
	}{
		{"MPC_SIGN", "MPC_SIGN", "MPC_SIGN"},
		{"WEB3_SIGN", "WEB3_SIGN", "WEB3_SIGN"},
		{"UNKNOWN", "SOMETHING_NEW", "SOMETHING_NEW"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			biz := &safeheron.CoSignerBizContentV3{Type: tt.bizType}
			dec, err := approver.Evaluate(context.Background(), biz)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if dec.Action != "REJECT" {
				t.Errorf("action = %q, want REJECT", dec.Action)
			}
			if dec.Reason == "" {
				t.Error("reason should not be empty")
			}
		})
	}
}

func TestCallbackTestApprover(t *testing.T) {
	approver := &CallbackTestApprover{}
	biz := &safeheron.CoSignerBizContentV3{ApprovalId: "test-1", Type: "CALLBACK_TEST"}

	dec, err := approver.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
	if dec.Reason != "CALLBACK_TEST" {
		t.Errorf("reason = %q, want CALLBACK_TEST", dec.Reason)
	}
}
