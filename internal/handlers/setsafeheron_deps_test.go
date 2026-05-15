package handlers

import (
	"context"
	"reflect"
	"testing"

	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"
)

// TestSetSafeheronDeps_RejectsTypedNilPoolManager verifies that passing a typed
// nil concrete pointer wrapped in the interface does NOT pollute Handler fields,
// preserving the 503-fallback path. Regression: T6-I-1.
func TestSetSafeheronDeps_RejectsTypedNilPoolManager(t *testing.T) {
	h := &Handler{}

	var pm *pool.Manager // typed nil — `pm == nil` (concrete) is true but interface assigned to it is non-nil
	h.SetSafeheronDeps(DepositPoolManager(pm), nil)

	if h.poolManager != nil {
		v := reflect.ValueOf(h.poolManager)
		if v.Kind() == reflect.Ptr && v.IsNil() {
			t.Errorf("SetSafeheronDeps must reject typed-nil; PoolManager still holds *pool.Manager(nil)")
		}
	}
}

func TestSetSafeheronDeps_RejectsTypedNilRegistry(t *testing.T) {
	h := &Handler{}

	var reg *walletconfig.Registry
	h.SetSafeheronDeps(nil, ChainsRegistry(reg))

	if h.walletRegistry != nil {
		v := reflect.ValueOf(h.walletRegistry)
		if v.Kind() == reflect.Ptr && v.IsNil() {
			t.Errorf("SetSafeheronDeps must reject typed-nil; WalletRegistry still holds *walletconfig.Registry(nil)")
		}
	}
}

func TestSetSafeheronDeps_AcceptsRealValue(t *testing.T) {
	h := &Handler{}
	pm := &mockPoolManager{}
	reg := &mockChainsRegistry{}
	h.SetSafeheronDeps(pm, reg)

	if h.poolManager == nil {
		t.Errorf("PoolManager should be set when real value provided")
	}
	if h.walletRegistry == nil {
		t.Errorf("WalletRegistry should be set when real value provided")
	}
}

// Keep `context` imported even if other helpers move away
var _ = context.Background
