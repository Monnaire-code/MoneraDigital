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
	byFamily      map[string][]*walletconfig.CoinChain
	byChain       map[string][]*walletconfig.CoinChain
	chains        []*walletconfig.Chain
	allCoinChains []*walletconfig.CoinChain
}

func (m *mockChainsRegistry) ListEnabledCoinChainsByFamily(family string) []*walletconfig.CoinChain {
	return m.byFamily[family]
}
func (m *mockChainsRegistry) ListEnabledCoinChainsByChain(chain string) []*walletconfig.CoinChain {
	return m.byChain[chain]
}
func (m *mockChainsRegistry) AllChains() []*walletconfig.Chain { return m.chains }
func (m *mockChainsRegistry) AllEnabledCoinChains() []*walletconfig.CoinChain {
	return m.allCoinChains
}

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

// --- GetDepositCoins tests ---

func newDepositCoinsCtx(userID int) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/wallet/deposit-coins", nil)
	if userID > 0 {
		c.Set("userID", userID)
	}
	return c, w
}

func TestGetDepositCoins_Unauthorized(t *testing.T) {
	h := newDepositTestHandler(&mockPoolManager{}, &mockChainsRegistry{})
	c, w := newDepositCoinsCtx(0)
	h.GetDepositCoins(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestGetDepositCoins_RegistryUnavailable(t *testing.T) {
	h := &Handler{}
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGetDepositCoins_GroupingAndShape(t *testing.T) {
	ethChain := &walletconfig.Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ExplorerURL: "https://etherscan.io", ShortName: "ETH"}
	bscChain := &walletconfig.Chain{Code: "BSC", Name: "BNB Smart Chain", NetworkFamily: "EVM", ExplorerURL: "https://bscscan.com", ShortName: "BSC"}

	ethCoin := &walletconfig.Coin{ID: 1, Symbol: "ETH", Name: "Ether", IsStable: false, Enabled: true, DisplayOrder: 10}
	usdcCoin := &walletconfig.Coin{ID: 5, Symbol: "USDC", Name: "USD Coin", IsStable: true, Enabled: true, DisplayOrder: 50}

	reg := &mockChainsRegistry{
		allCoinChains: []*walletconfig.CoinChain{
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: ethCoin, IsNative: true, TokenContract: "", Decimals: 18, MinDepositAmount: "0.001", TokenStandard: "Native", EstimatedArrivalMinutes: 2},
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: usdcCoin, IsNative: false, TokenContract: "0xA0b869", Decimals: 6, MinDepositAmount: "1", TokenStandard: "ERC20", EstimatedArrivalMinutes: 2},
			{ChainCode: "BSC", Chain: bscChain, Coin: usdcCoin, IsNative: false, TokenContract: "0x8AC76a", Decimals: 18, MinDepositAmount: "1", TokenStandard: "BEP20", EstimatedArrivalMinutes: 1},
		},
	}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body depositCoinsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Coins) != 2 {
		t.Fatalf("expected 2 coins, got %d", len(body.Coins))
	}

	// ETH (displayOrder=10) should come first
	if body.Coins[0].Symbol != "ETH" {
		t.Errorf("expected ETH first, got %s", body.Coins[0].Symbol)
	}
	if len(body.Coins[0].Networks) != 1 {
		t.Fatalf("ETH should have 1 network, got %d", len(body.Coins[0].Networks))
	}
	// Native coin: tokenContract must be null
	if body.Coins[0].Networks[0].TokenContract != nil {
		t.Errorf("native ETH tokenContract should be null, got %v", body.Coins[0].Networks[0].TokenContract)
	}
	if body.Coins[0].Networks[0].ShortName != "ETH" {
		t.Errorf("expected shortName ETH, got %s", body.Coins[0].Networks[0].ShortName)
	}

	// USDC should have 2 networks
	if body.Coins[1].Symbol != "USDC" {
		t.Errorf("expected USDC second, got %s", body.Coins[1].Symbol)
	}
	if len(body.Coins[1].Networks) != 2 {
		t.Fatalf("USDC should have 2 networks, got %d", len(body.Coins[1].Networks))
	}
	// Non-native: tokenContract must be a string
	for _, net := range body.Coins[1].Networks {
		if net.TokenContract == nil {
			t.Errorf("non-native USDC on %s should have tokenContract, got nil", net.ChainCode)
		}
	}
}

func TestGetDepositCoins_SkipsNilCoinOrChain(t *testing.T) {
	reg := &mockChainsRegistry{
		allCoinChains: []*walletconfig.CoinChain{
			{ChainCode: "ETHEREUM", Chain: nil, Coin: &walletconfig.Coin{Symbol: "ETH", DisplayOrder: 10}},
			{ChainCode: "ETHEREUM", Chain: &walletconfig.Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ShortName: "ETH"}, Coin: nil},
			{ChainCode: "ETHEREUM", Chain: &walletconfig.Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ShortName: "ETH"}, Coin: &walletconfig.Coin{Symbol: "ETH", Name: "Ether", DisplayOrder: 10}, IsNative: true, Decimals: 18, MinDepositAmount: "0.001"},
		},
	}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var body depositCoinsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Coins) != 1 {
		t.Fatalf("expected 1 coin (nil-ref entries skipped), got %d", len(body.Coins))
	}
	if body.Coins[0].Symbol != "ETH" {
		t.Errorf("expected ETH, got %s", body.Coins[0].Symbol)
	}
}

func TestGetDepositCoins_ShortNameFallback(t *testing.T) {
	chain := &walletconfig.Chain{Code: "MYCHAIN", Name: "My Chain", NetworkFamily: "EVM", ShortName: ""}
	coin := &walletconfig.Coin{ID: 1, Symbol: "ABC", Name: "ABC Coin", DisplayOrder: 10}
	reg := &mockChainsRegistry{
		allCoinChains: []*walletconfig.CoinChain{
			{ChainCode: "MYCHAIN", Chain: chain, Coin: coin, IsNative: true, Decimals: 18, MinDepositAmount: "0.01"},
		},
	}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body depositCoinsResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Coins) != 1 || len(body.Coins[0].Networks) != 1 {
		t.Fatalf("expected 1 coin with 1 network, got %+v", body.Coins)
	}
	if body.Coins[0].Networks[0].ShortName != "MYCHAIN" {
		t.Errorf("expected shortName fallback to chain code MYCHAIN, got %s", body.Coins[0].Networks[0].ShortName)
	}
}

func TestGetDepositCoins_NetworksSortedByDisplayOrder(t *testing.T) {
	ethChain := &walletconfig.Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ShortName: "ETH"}
	bscChain := &walletconfig.Chain{Code: "BSC", Name: "BNB Smart Chain", NetworkFamily: "EVM", ShortName: "BSC"}
	tronChain := &walletconfig.Chain{Code: "TRON", Name: "Tron", NetworkFamily: "TRON", ShortName: "TRX"}

	usdtCoin := &walletconfig.Coin{ID: 4, Symbol: "USDT", Name: "Tether", IsStable: true, Enabled: true, DisplayOrder: 40}

	reg := &mockChainsRegistry{
		allCoinChains: []*walletconfig.CoinChain{
			{ChainCode: "TRON", Chain: tronChain, Coin: usdtCoin, DisplayOrder: 70, IsNative: false, TokenContract: "TR7N", Decimals: 6, MinDepositAmount: "1", TokenStandard: "TRC20"},
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: usdtCoin, DisplayOrder: 20, IsNative: false, TokenContract: "0xdAC1", Decimals: 6, MinDepositAmount: "1", TokenStandard: "ERC20"},
			{ChainCode: "BSC", Chain: bscChain, Coin: usdtCoin, DisplayOrder: 50, IsNative: false, TokenContract: "0x55d3", Decimals: 18, MinDepositAmount: "1", TokenStandard: "BEP20"},
		},
	}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body depositCoinsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Coins) != 1 {
		t.Fatalf("expected 1 coin (USDT), got %d", len(body.Coins))
	}
	nets := body.Coins[0].Networks
	if len(nets) != 3 {
		t.Fatalf("expected 3 networks, got %d", len(nets))
	}
	if nets[0].ChainCode != "ETHEREUM" {
		t.Errorf("expected ETHEREUM first (displayOrder=20), got %s", nets[0].ChainCode)
	}
	if nets[1].ChainCode != "BSC" {
		t.Errorf("expected BSC second (displayOrder=50), got %s", nets[1].ChainCode)
	}
	if nets[2].ChainCode != "TRON" {
		t.Errorf("expected TRON third (displayOrder=70), got %s", nets[2].ChainCode)
	}
}

func TestGetDepositCoins_CoinsSortedByDisplayOrder(t *testing.T) {
	ethChain := &walletconfig.Chain{Code: "ETHEREUM", Name: "Ethereum", NetworkFamily: "EVM", ShortName: "ETH"}

	// Intentionally out of order: TRX(60) → ETH(10) → USDC(50)
	trxCoin := &walletconfig.Coin{ID: 3, Symbol: "TRX", Name: "TRON", IsStable: false, Enabled: true, DisplayOrder: 60}
	ethCoin := &walletconfig.Coin{ID: 1, Symbol: "ETH", Name: "Ether", IsStable: false, Enabled: true, DisplayOrder: 10}
	usdcCoin := &walletconfig.Coin{ID: 5, Symbol: "USDC", Name: "USD Coin", IsStable: true, Enabled: true, DisplayOrder: 50}

	reg := &mockChainsRegistry{
		allCoinChains: []*walletconfig.CoinChain{
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: trxCoin, IsNative: false, Decimals: 6, MinDepositAmount: "0.1", DisplayOrder: 60},
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: ethCoin, IsNative: true, Decimals: 18, MinDepositAmount: "0.001", DisplayOrder: 10},
			{ChainCode: "ETHEREUM", Chain: ethChain, Coin: usdcCoin, IsNative: false, TokenContract: "0xA0b8", Decimals: 6, MinDepositAmount: "1", DisplayOrder: 50},
		},
	}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body depositCoinsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Coins) != 3 {
		t.Fatalf("expected 3 coins, got %d", len(body.Coins))
	}
	// Must be sorted by DisplayOrder: ETH(10) → USDC(50) → TRX(60)
	if body.Coins[0].Symbol != "ETH" {
		t.Errorf("expected ETH first (displayOrder=10), got %s", body.Coins[0].Symbol)
	}
	if body.Coins[1].Symbol != "USDC" {
		t.Errorf("expected USDC second (displayOrder=50), got %s", body.Coins[1].Symbol)
	}
	if body.Coins[2].Symbol != "TRX" {
		t.Errorf("expected TRX third (displayOrder=60), got %s", body.Coins[2].Symbol)
	}
}

func TestGetDepositCoins_EmptyRegistry(t *testing.T) {
	reg := &mockChainsRegistry{allCoinChains: nil}
	h := newDepositTestHandler(nil, reg)
	c, w := newDepositCoinsCtx(1)
	h.GetDepositCoins(c)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"coins":[]`) {
		t.Errorf(`expected "coins":[] for empty registry, got: %s`, body)
	}
	if strings.Contains(body, `"coins":null`) {
		t.Errorf("coins must be [] not null, got: %s", body)
	}
}
