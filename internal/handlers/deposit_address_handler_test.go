package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"

	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"
)

type mockPoolManager struct {
	mu       sync.Mutex
	assigned map[int]*pool.Address
	addrErr  error
	calls    int32
	// returnFn lets tests inject per-user behaviour (e.g. concurrency tests).
	returnFn func(userID int, family string) (*pool.Address, error)
}

func (m *mockPoolManager) GetOrAssign(_ context.Context, userID int, family string) (*pool.Address, error) {
	atomic.AddInt32(&m.calls, 1)
	if m.returnFn != nil {
		return m.returnFn(userID, family)
	}
	if m.addrErr != nil {
		return nil, m.addrErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.assigned[userID]; ok {
		return a, nil
	}
	return nil, errors.New("not configured")
}

type mockChainsRegistry struct {
	byFamily map[string][]*walletconfig.CoinChain
	byChain  map[string][]*walletconfig.CoinChain
	chains   []*walletconfig.Chain
}

func (m *mockChainsRegistry) ListEnabledCoinChainsByFamily(family string) []*walletconfig.CoinChain {
	return m.byFamily[family]
}
func (m *mockChainsRegistry) ListEnabledCoinChainsByChain(chain string) []*walletconfig.CoinChain {
	return m.byChain[chain]
}
func (m *mockChainsRegistry) AllChains() []*walletconfig.Chain { return m.chains }

func newDepositTestHandler(pm DepositPoolManager, reg ChainsRegistry) *Handler {
	h := &Handler{}
	h.SetSafeheronDeps(pm, reg)
	return h
}

func newAuthedCtx(userID int, query string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	url := "/api/wallet/deposit-address"
	if query != "" {
		url += "?" + query
	}
	c.Request = httptest.NewRequest(http.MethodGet, url, nil)
	if userID > 0 {
		c.Set("userID", userID)
	}
	return c, w
}

func TestGetDepositAddress_Unauthorized(t *testing.T) {
	h := newDepositTestHandler(&mockPoolManager{}, &mockChainsRegistry{})
	c, w := newAuthedCtx(0, "networkFamily=EVM")
	h.GetDepositAddress(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestGetDepositAddress_QueryParam_IsCamelCase verifies the handler reads
// `networkFamily` (camelCase, per CLAUDE.md JSON naming convention).
// Regression: R2-C-1 — handler used to read `network_family` (snake_case),
// breaking the contract with the frontend which sends camelCase. Production
// traffic would have always 400'd with INVALID_NETWORK_FAMILY.
func TestGetDepositAddress_QueryParam_IsCamelCase(t *testing.T) {
	pm := &mockPoolManager{
		assigned: map[int]*pool.Address{
			1: {ID: 1, NetworkFamily: "EVM", Address: "0xcamel"},
		},
	}
	reg := &mockChainsRegistry{byFamily: map[string][]*walletconfig.CoinChain{"EVM": nil}}
	h := newDepositTestHandler(pm, reg)
	c, w := newAuthedCtx(1, "networkFamily=EVM")
	h.GetDepositAddress(c)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with camelCase param, got %d: %s", w.Code, w.Body.String())
	}
	// And snake_case must now miss — confirms we read camelCase, not "either".
	c2, w2 := newAuthedCtx(1, "network_family=EVM")
	h.GetDepositAddress(c2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 with snake_case param (handler should only honour camelCase per CLAUDE.md), got %d", w2.Code)
	}
}

func TestGetDepositAddress_InvalidFamily(t *testing.T) {
	h := newDepositTestHandler(&mockPoolManager{}, &mockChainsRegistry{})
	c, w := newAuthedCtx(1, "networkFamily=BTC")
	h.GetDepositAddress(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "INVALID_NETWORK_FAMILY" {
		t.Errorf("expected INVALID_NETWORK_FAMILY error, got %v", body)
	}
}

func TestGetDepositAddress_MissingFamily(t *testing.T) {
	h := newDepositTestHandler(&mockPoolManager{}, &mockChainsRegistry{})
	c, w := newAuthedCtx(1, "")
	h.GetDepositAddress(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetDepositAddress_NotInitialised(t *testing.T) {
	h := &Handler{} // no SetSafeheronDeps
	c, w := newAuthedCtx(1, "networkFamily=EVM")
	h.GetDepositAddress(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetDepositAddress_AssignSuccess(t *testing.T) {
	pm := &mockPoolManager{
		assigned: map[int]*pool.Address{
			42: {ID: 1, NetworkFamily: "EVM", Address: "0xabc"},
		},
	}
	reg := &mockChainsRegistry{
		byFamily: map[string][]*walletconfig.CoinChain{
			"EVM": {
				{
					ChainCode:        "ETHEREUM",
					Coin:             &walletconfig.Coin{Symbol: "ETH"},
					SafeheronCoinKey: "ETH",
					Decimals:         18,
					MinDepositAmount: "0.001",
				},
				{
					ChainCode:        "BSC",
					Coin:             &walletconfig.Coin{Symbol: "USDT"},
					SafeheronCoinKey: "USDT_BSC",
					Decimals:         18,
					MinDepositAmount: "1",
				},
			},
		},
	}
	h := newDepositTestHandler(pm, reg)

	c, w := newAuthedCtx(42, "networkFamily=EVM")
	h.GetDepositAddress(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body depositAddressResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Address != "0xabc" || body.NetworkFamily != "EVM" {
		t.Errorf("unexpected body: %+v", body)
	}
	if len(body.SupportedCoins) != 2 {
		t.Fatalf("expected 2 supported coins, got %d", len(body.SupportedCoins))
	}
	if body.SupportedCoins[0].Symbol != "ETH" || body.SupportedCoins[0].MinDeposit != "0.001" {
		t.Errorf("coin[0] mismatch: %+v", body.SupportedCoins[0])
	}
	// Pre-ship code-review Important: frontend uses `${chainCode}-${coinKey}`
	// as React row key; without coinKey the key would be "ETHEREUM-undefined"
	// for every coin on the same chain → duplicate-key warning + reconciliation
	// glitches. Also exposes `decimals` so the UI can format amounts.
	if body.SupportedCoins[0].CoinKey != "ETH" || body.SupportedCoins[0].Decimals != 18 {
		t.Errorf("coin[0] must expose coinKey+decimals, got: %+v", body.SupportedCoins[0])
	}
	if body.SupportedCoins[1].CoinKey != "USDT_BSC" {
		t.Errorf("coin[1] coinKey must come from registry SafeheronCoinKey, got: %+v", body.SupportedCoins[1])
	}
}

func TestGetDepositAddress_PoolEmpty(t *testing.T) {
	pm := &mockPoolManager{addrErr: pool.ErrPoolEmpty}
	reg := &mockChainsRegistry{}
	h := newDepositTestHandler(pm, reg)
	c, w := newAuthedCtx(1, "networkFamily=TRON")
	h.GetDepositAddress(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "POOL_EMPTY" {
		t.Errorf("expected POOL_EMPTY error, got %v", body)
	}
}

func TestGetDepositAddress_AssignError(t *testing.T) {
	pm := &mockPoolManager{addrErr: errors.New("db down")}
	reg := &mockChainsRegistry{}
	h := newDepositTestHandler(pm, reg)
	c, w := newAuthedCtx(1, "networkFamily=EVM")
	h.GetDepositAddress(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

// TestGetDepositAddress_AssignError_BodyNoLeak verifies that internal error
// details (e.g. raw DB errors, SQL fragments) never reach the client body.
// Regression: T6-I-3 — previously the handler echoed err.Error() in `message`.
func TestGetDepositAddress_AssignError_BodyNoLeak(t *testing.T) {
	pm := &mockPoolManager{addrErr: errors.New("pq: relation \"address_pool\" does not exist (sql: SELECT * FROM ...)")}
	reg := &mockChainsRegistry{}
	h := newDepositTestHandler(pm, reg)
	c, w := newAuthedCtx(1, "networkFamily=EVM")
	h.GetDepositAddress(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	body := w.Body.String()
	for _, leak := range []string{"pq:", "relation", "SELECT", "sql:"} {
		if strings.Contains(body, leak) {
			t.Errorf("error body must not leak internal detail %q, got: %s", leak, body)
		}
	}
	// But the error code must still be present so the frontend can react.
	if !strings.Contains(body, "ASSIGN_FAILED") {
		t.Errorf("error code ASSIGN_FAILED must remain in body, got: %s", body)
	}
}

func TestGetDepositAddress_Concurrent10Users(t *testing.T) {
	// Simulates 10 different users hitting the endpoint concurrently — each
	// must come back with a distinct address.
	addresses := make(map[int]*pool.Address, 10)
	for i := 1; i <= 10; i++ {
		addresses[i] = &pool.Address{
			ID:            i,
			NetworkFamily: "EVM",
			Address:       randAddress(i),
		}
	}
	pm := &mockPoolManager{
		returnFn: func(uid int, _ string) (*pool.Address, error) {
			return addresses[uid], nil
		},
	}
	reg := &mockChainsRegistry{byFamily: map[string][]*walletconfig.CoinChain{"EVM": nil}}
	h := newDepositTestHandler(pm, reg)

	var wg sync.WaitGroup
	resultMu := sync.Mutex{}
	results := make(map[string]int)
	for uid := 1; uid <= 10; uid++ {
		wg.Add(1)
		go func(uid int) {
			defer wg.Done()
			c, w := newAuthedCtx(uid, "networkFamily=EVM")
			h.GetDepositAddress(c)
			if w.Code != http.StatusOK {
				t.Errorf("user %d: expected 200, got %d", uid, w.Code)
				return
			}
			var body depositAddressResponse
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			resultMu.Lock()
			results[body.Address]++
			resultMu.Unlock()
		}(uid)
	}
	wg.Wait()

	if len(results) != 10 {
		t.Errorf("expected 10 distinct addresses, got %d: %v", len(results), results)
	}
}

func TestGetDepositAddress_SameUserReturnsSameAddress(t *testing.T) {
	addr := &pool.Address{ID: 99, NetworkFamily: "EVM", Address: "0xstable"}
	pm := &mockPoolManager{
		returnFn: func(_ int, _ string) (*pool.Address, error) {
			return addr, nil
		},
	}
	reg := &mockChainsRegistry{}
	h := newDepositTestHandler(pm, reg)

	for i := 0; i < 3; i++ {
		c, w := newAuthedCtx(7, "networkFamily=EVM")
		h.GetDepositAddress(c)
		if w.Code != http.StatusOK {
			t.Fatalf("call %d: expected 200, got %d", i, w.Code)
		}
		var body depositAddressResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.Address != "0xstable" {
			t.Errorf("call %d: expected 0xstable, got %s", i, body.Address)
		}
	}
}

func TestGetSupportedChains_Unauthorized(t *testing.T) {
	h := newDepositTestHandler(&mockPoolManager{}, &mockChainsRegistry{})
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/supported-chains", nil)
	h.GetSupportedChains(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

// TestGetSupportedChains_EmptyChains verifies that when the registry has zero
// enabled chains, the endpoint returns 200 + an empty JSON array (NOT null and
// NOT missing the field). The frontend deserialises chains[] regardless and
// breaks if it gets null. Regression: T6-S-3.
func TestGetSupportedChains_EmptyChains(t *testing.T) {
	reg := &mockChainsRegistry{
		chains:  nil,
		byChain: map[string][]*walletconfig.CoinChain{},
	}
	h := newDepositTestHandler(nil, reg)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("userID", 1)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/supported-chains", nil)
	h.GetSupportedChains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with empty list, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"chains":[]`) {
		t.Errorf(`expected "chains":[] for empty registry, got: %s`, body)
	}
	if strings.Contains(body, `"chains":null`) {
		t.Errorf("chains must be [] not null, got: %s", body)
	}
}

func TestGetSupportedChains_NoRegistry(t *testing.T) {
	h := &Handler{}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("userID", 1)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/supported-chains", nil)
	h.GetSupportedChains(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetSupportedChains_Success(t *testing.T) {
	reg := &mockChainsRegistry{
		chains: []*walletconfig.Chain{
			{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", NativeSymbol: "ETH", ExplorerURL: "https://etherscan.io"},
			{Code: "TRON", Name: "Tron", NetworkFamily: "TRON", NativeSymbol: "TRX"},
		},
		byChain: map[string][]*walletconfig.CoinChain{
			"ETHEREUM": {
				{
					ChainCode:        "ETHEREUM",
					Coin:             &walletconfig.Coin{Symbol: "ETH"},
					IsNative:         true,
					Decimals:         18,
					MinDepositAmount: "0.001",
				},
			},
			"TRON": {
				{
					ChainCode:        "TRON",
					Coin:             &walletconfig.Coin{Symbol: "TRX"},
					IsNative:         true,
					Decimals:         6,
					MinDepositAmount: "0.1",
				},
			},
		},
	}
	h := newDepositTestHandler(nil, reg)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("userID", 1)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/supported-chains", nil)
	h.GetSupportedChains(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body supportedChainsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(body.Chains))
	}
	chainsByCode := map[string]supportedChain{}
	for _, ch := range body.Chains {
		chainsByCode[ch.Code] = ch
	}
	if eth, ok := chainsByCode["ETHEREUM"]; !ok || len(eth.Coins) != 1 || eth.Coins[0].Symbol != "ETH" || eth.Coins[0].Decimals != 18 {
		t.Errorf("ETHEREUM chain incorrect: %+v", eth)
	}
	if tr, ok := chainsByCode["TRON"]; !ok || tr.NativeSymbol != "TRX" {
		t.Errorf("TRON chain incorrect: %+v", tr)
	}
}

func TestDeprecatedWalletEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/wallet/create", nil)
	DeprecatedWalletEndpoint(c)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d", w.Code)
	}
	var body map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "DEPRECATED" {
		t.Errorf("expected DEPRECATED, got %v", body)
	}
}

func TestSetSafeheronDeps_NilSafe(t *testing.T) {
	h := &Handler{}
	// nil interfaces should be a no-op, leaving fallback path intact.
	h.SetSafeheronDeps(nil, nil)
	if h.poolManager != nil || h.walletRegistry != nil {
		t.Error("nil SetSafeheronDeps must not assign fields")
	}
}

func randAddress(seed int) string {
	const hex = "0123456789abcdef"
	out := []byte("0x")
	x := uint64(seed) + 1
	for i := 0; i < 40; i++ {
		out = append(out, hex[x%16])
		x = x*31 + 7
	}
	return string(out)
}
