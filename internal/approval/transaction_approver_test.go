package approval

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
)

type mockRegistry struct {
	data map[string]*walletconfig.CoinChain
}

func (m *mockRegistry) GetCoinChainBySafeheronKey(key string) (*walletconfig.CoinChain, bool) {
	cc, ok := m.data[key]
	return cc, ok
}

func newTestRegistry() *mockRegistry {
	return &mockRegistry{data: map[string]*walletconfig.CoinChain{
		"USDT_ERC20": {SafeheronCoinKey: "USDT_ERC20", Chain: &walletconfig.Chain{ShortName: "ETH"}},
		"ETH":        {SafeheronCoinKey: "ETH", Chain: &walletconfig.Chain{ShortName: "ETH"}},
		"TRX":        {SafeheronCoinKey: "TRX", Chain: &walletconfig.Chain{ShortName: "TRX"}},
		"BTC":        {SafeheronCoinKey: "BTC", Chain: &walletconfig.Chain{ShortName: "BTC"}},
	}}
}

func newTestConfig() ApprovalConfig {
	return ApprovalConfig{
		SweepTargetAccounts: []string{"acct-main", "acct-secondary"},
		AllowedTxTypes:      []string{"AUTO_SWEEP", "AUTO_FUEL", "UTXO_COLLECTION"},
	}
}

func makeBiz(txType, destType, destKey string) *safeheron.CoSignerBizContentV3 {
	detail := safeheron.TransactionApproval{
		TxKey:                  "tx-1",
		CoinKey:                "USDT_ERC20",
		TxAmount:               "100",
		TransactionType:        txType,
		DestinationAccountKey:  destKey,
		DestinationAccountType: destType,
		DestinationAddress:     "0xabc",
		SourceAccountKey:       "src-1",
		SourceAddress:          "0xdef",
	}
	data, _ := json.Marshal(detail)
	return &safeheron.CoSignerBizContentV3{
		ApprovalId: "ap-1",
		Type:       "TRANSACTION",
		Detail:     data,
	}
}

// ---------------------------------------------------------------------------
// AUTO_SWEEP
// ---------------------------------------------------------------------------

func TestAutoSweep_Approve(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")

	dec, err := a.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
}

func TestAutoSweep_RejectNotVault(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_SWEEP", "EXTERNAL_ADDRESS", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

func TestAutoSweep_RejectNotInWhitelist(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "unknown-acct")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

func TestAutoSweep_RejectEmptyWhitelist(t *testing.T) {
	cfg := newTestConfig()
	cfg.SweepTargetAccounts = nil
	a := NewTransactionApprover(cfg, newTestRegistry())
	biz := makeBiz("AUTO_SWEEP", "VAULT_ACCOUNT", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for empty whitelist", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// AUTO_FUEL
// ---------------------------------------------------------------------------

func TestAutoFuel_Approve(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_FUEL", "VAULT_ACCOUNT", "acct-main")

	dec, err := a.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
}

func TestAutoFuel_RejectNotInWhitelist(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_FUEL", "VAULT_ACCOUNT", "unknown-acct")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for non-whitelisted account", dec.Action)
	}
}

func TestAutoFuel_RejectEmptyWhitelist(t *testing.T) {
	cfg := newTestConfig()
	cfg.SweepTargetAccounts = nil
	a := NewTransactionApprover(cfg, newTestRegistry())
	biz := makeBiz("AUTO_FUEL", "VAULT_ACCOUNT", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for empty whitelist", dec.Action)
	}
}

func TestAutoFuel_RejectNotVault(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("AUTO_FUEL", "EXTERNAL_ADDRESS", "any-acct")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// UTXO_COLLECTION
// ---------------------------------------------------------------------------

func TestUTXOCollection_Approve(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("UTXO_COLLECTION", "VAULT_ACCOUNT", "acct-main")

	dec, err := a.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "APPROVE" {
		t.Errorf("action = %q, want APPROVE", dec.Action)
	}
}

func TestUTXOCollection_RejectNotInWhitelist(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("UTXO_COLLECTION", "VAULT_ACCOUNT", "unknown-acct")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for non-whitelisted account", dec.Action)
	}
}

func TestUTXOCollection_RejectEmptyWhitelist(t *testing.T) {
	cfg := newTestConfig()
	cfg.SweepTargetAccounts = nil
	a := NewTransactionApprover(cfg, newTestRegistry())
	biz := makeBiz("UTXO_COLLECTION", "VAULT_ACCOUNT", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for empty whitelist", dec.Action)
	}
}

func TestUTXOCollection_RejectNotVault(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("UTXO_COLLECTION", "EXTERNAL_ADDRESS", "any-acct")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// NORMAL (reserved, always REJECT)
// ---------------------------------------------------------------------------

func TestNormal_AlwaysReject(t *testing.T) {
	cfg := newTestConfig()
	cfg.AllowedTxTypes = append(cfg.AllowedTxTypes, "NORMAL")
	a := NewTransactionApprover(cfg, newTestRegistry())
	biz := makeBiz("NORMAL", "VAULT_ACCOUNT", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// tx_type not in allowed list
// ---------------------------------------------------------------------------

func TestTxTypeNotAllowed(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := makeBiz("UNKNOWN_TYPE", "VAULT_ACCOUNT", "acct-main")

	dec, _ := a.Evaluate(context.Background(), biz)
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// tx_type in allowed list but not a named case → default branch
// ---------------------------------------------------------------------------

func TestUnknownTypeInAllowedList_HitsDefault(t *testing.T) {
	cfg := newTestConfig()
	cfg.AllowedTxTypes = append(cfg.AllowedTxTypes, "FUTURE_TYPE")
	a := NewTransactionApprover(cfg, newTestRegistry())
	biz := makeBiz("FUTURE_TYPE", "VAULT_ACCOUNT", "acct-main")

	dec, err := a.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for unknown-but-allowed type", dec.Action)
	}
	if !strings.Contains(dec.Reason, "unknown transaction type") {
		t.Errorf("reason should mention unknown type, got: %q", dec.Reason)
	}
}

// ---------------------------------------------------------------------------
// Invalid detail JSON
// ---------------------------------------------------------------------------

func TestInvalidDetailJSON(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	biz := &safeheron.CoSignerBizContentV3{
		ApprovalId: "ap-1",
		Type:       "TRANSACTION",
		Detail:     json.RawMessage(`not-json`),
	}

	dec, err := a.Evaluate(context.Background(), biz)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != "REJECT" {
		t.Errorf("action = %q, want REJECT for invalid JSON", dec.Action)
	}
}

// ---------------------------------------------------------------------------
// ResolveChainSymbol
// ---------------------------------------------------------------------------

func TestResolveChainSymbol_Found(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())

	tests := []struct {
		coinKey string
		want    string
	}{
		{"USDT_ERC20", "ETH"},
		{"TRX", "TRX"},
		{"BTC", "BTC"},
	}
	for _, tt := range tests {
		t.Run(tt.coinKey, func(t *testing.T) {
			got := a.ResolveChainSymbol(tt.coinKey)
			if got != tt.want {
				t.Errorf("ResolveChainSymbol(%q) = %q, want %q", tt.coinKey, got, tt.want)
			}
		})
	}
}

func TestResolveChainSymbol_NotFound(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	got := a.ResolveChainSymbol("UNKNOWN_COIN")
	if got != "UNKNOWN" {
		t.Errorf("ResolveChainSymbol(UNKNOWN_COIN) = %q, want UNKNOWN", got)
	}
}

func TestResolveChainSymbol_Empty(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), newTestRegistry())
	got := a.ResolveChainSymbol("")
	if got != "UNKNOWN" {
		t.Errorf("ResolveChainSymbol(\"\") = %q, want UNKNOWN", got)
	}
}

func TestResolveChainSymbol_NilRegistry(t *testing.T) {
	a := NewTransactionApprover(newTestConfig(), nil)
	got := a.ResolveChainSymbol("USDT_ERC20")
	if got != "UNKNOWN" {
		t.Errorf("ResolveChainSymbol with nil registry = %q, want UNKNOWN", got)
	}
}

func TestResolveChainSymbol_NilChain(t *testing.T) {
	reg := &mockRegistry{data: map[string]*walletconfig.CoinChain{
		"BAD": {SafeheronCoinKey: "BAD", Chain: nil},
	}}
	a := NewTransactionApprover(newTestConfig(), reg)
	got := a.ResolveChainSymbol("BAD")
	if got != "UNKNOWN" {
		t.Errorf("ResolveChainSymbol(BAD) = %q, want UNKNOWN", got)
	}
}
