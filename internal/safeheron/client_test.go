package safeheron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/webhook"
)

// --- mock SDK interfaces ---

type mockAccountAPI struct {
	createAccountFn      func(api.CreateAccountRequest, *api.CreateAccountResponse) error
	addCoinV2Fn          func(api.AddCoinV2Request, *api.AddCoinV2Response) error
	listAccountCoinFn    func(api.ListAccountCoinRequest, *api.AccountCoinResponse) error
	getAccountByAddrFn   func(api.OneAccountByAddressRequest, *api.AccountResponse) error
}

func (m *mockAccountAPI) CreateAccount(req api.CreateAccountRequest, resp *api.CreateAccountResponse) error {
	return m.createAccountFn(req, resp)
}
func (m *mockAccountAPI) AddCoinV2(req api.AddCoinV2Request, resp *api.AddCoinV2Response) error {
	return m.addCoinV2Fn(req, resp)
}
func (m *mockAccountAPI) ListAccountCoin(req api.ListAccountCoinRequest, resp *api.AccountCoinResponse) error {
	return m.listAccountCoinFn(req, resp)
}
func (m *mockAccountAPI) GetAccountByAddress(req api.OneAccountByAddressRequest, resp *api.AccountResponse) error {
	return m.getAccountByAddrFn(req, resp)
}

type mockWebhookConv struct {
	convertFn func(webhook.WebHook) (string, error)
}

func (m *mockWebhookConv) Convert(wh webhook.WebHook) (string, error) {
	return m.convertFn(wh)
}

func newTestClient(acct accountAPIClient, wh webhookConverter) *Client {
	return &Client{account: acct, webhookConv: wh}
}

// --- NewClient validation tests ---

func TestNewClient_MissingAPIKey(t *testing.T) {
	_, err := NewClient(Config{
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	})
	if err == nil || !strings.Contains(err.Error(), "APIKey") {
		t.Fatalf("expected APIKey error, got: %v", err)
	}
}

func TestNewClient_MissingPrivateKey(t *testing.T) {
	_, err := NewClient(Config{
		APIKey:               "test-key",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	})
	if err == nil || !strings.Contains(err.Error(), "PrivateKeyPEM") {
		t.Fatalf("expected PrivateKeyPEM error, got: %v", err)
	}
}

func TestNewClient_MissingPlatformKey(t *testing.T) {
	_, err := NewClient(Config{
		APIKey:        "test-key",
		PrivateKeyPEM: "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
	})
	if err == nil || !strings.Contains(err.Error(), "PlatformPublicKeyPEM") {
		t.Fatalf("expected PlatformPublicKeyPEM error, got: %v", err)
	}
}

func TestNewClient_TempFilesCreatedAndCleaned(t *testing.T) {
	c, err := NewClient(Config{
		BaseURL:              "https://api.safeheron.vip",
		APIKey:               "test-api-key",
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\nMIIEvgI=\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nMIIBIjA=\n-----END PUBLIC KEY-----",
		RequestTimeoutMS:     30000,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if len(c.tempFiles) != 2 {
		t.Fatalf("expected 2 temp files, got %d", len(c.tempFiles))
	}
	for _, f := range c.tempFiles {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatalf("temp file %s missing: %v", f, err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("wrong permissions on %s: %v", f, info.Mode().Perm())
		}
		if !strings.HasPrefix(filepath.Base(f), "safeheron-") {
			t.Fatalf("unexpected filename: %s", f)
		}
	}

	saved := make([]string, len(c.tempFiles))
	copy(saved, c.tempFiles)
	c.Close()
	for _, f := range saved {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("temp file %s not cleaned up", f)
		}
	}
	if c.tempFiles != nil {
		t.Fatal("tempFiles should be nil after Close")
	}
}

func TestNewClient_WithWebhookKeys(t *testing.T) {
	c, err := NewClient(Config{
		BaseURL:              "https://api.safeheron.vip",
		APIKey:               "test-api-key",
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\nMIIEvgI=\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nMIIBIjA=\n-----END PUBLIC KEY-----",
		WebhookPublicKeyPEM:  "-----BEGIN PUBLIC KEY-----\nWHPUB=\n-----END PUBLIC KEY-----",
		WebhookPrivateKeyPEM: "-----BEGIN PRIVATE KEY-----\nWHPRIV=\n-----END PRIVATE KEY-----",
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer c.Close()
	if len(c.tempFiles) != 4 {
		t.Fatalf("expected 4 temp files, got %d", len(c.tempFiles))
	}
}

func TestNewClient_TempFileWriteFailure(t *testing.T) {
	orig := os.Getenv("TMPDIR")
	os.Setenv("TMPDIR", "/nonexistent-path-safeheron-test")
	defer os.Setenv("TMPDIR", orig)

	_, err := NewClient(Config{
		APIKey:               "test-key",
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	})
	if err == nil {
		t.Fatal("expected error when temp dir is invalid")
	}
}

// --- CreateAssetWallet tests ---

func TestCreateAssetWallet_Success(t *testing.T) {
	mock := &mockAccountAPI{
		createAccountFn: func(req api.CreateAccountRequest, resp *api.CreateAccountResponse) error {
			if req.CustomerRefId != "test-ref-1234" {
				t.Fatalf("unexpected customerRefId: %s", req.CustomerRefId)
			}
			if !*req.HiddenOnUI || *req.AutoFuel {
				t.Fatal("expected hiddenOnUI=true, autoFuel=false")
			}
			if req.AccountTag != "DEPOSIT" {
				t.Fatalf("expected DEPOSIT tag, got: %s", req.AccountTag)
			}
			resp.AccountKey = "ak-001"
			resp.CoinAddressList = []struct {
				CoinKey          string `json:"coinKey"`
				AddressGroupKey  string `json:"addressGroupKey"`
				AddressGroupName string `json:"addressGroupName"`
				AddressList      []struct {
					Address     string `json:"address"`
					AddressType string `json:"addressType"`
					DerivePath  string `json:"derivePath"`
				} `json:"addressList"`
			}{
				{
					CoinKey:         "ETH",
					AddressGroupKey: "agk-1",
					AddressList: []struct {
						Address     string `json:"address"`
						AddressType string `json:"addressType"`
						DerivePath  string `json:"derivePath"`
					}{
						{Address: "0xabc123", DerivePath: "m/44'/60'/0'/0/0"},
					},
				},
			}
			return nil
		},
	}

	c := newTestClient(mock, nil)
	w, err := c.CreateAssetWallet(context.Background(), "test-ref-1234", []string{"ETH"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.AccountKey != "ak-001" {
		t.Fatalf("expected ak-001, got %s", w.AccountKey)
	}
	if len(w.CoinAddressList) != 1 || w.CoinAddressList[0].Address != "0xabc123" {
		t.Fatalf("unexpected coin addresses: %+v", w.CoinAddressList)
	}
}

func TestCreateAssetWallet_SDKError(t *testing.T) {
	mock := &mockAccountAPI{
		createAccountFn: func(_ api.CreateAccountRequest, _ *api.CreateAccountResponse) error {
			return errors.New("sdk: connection refused")
		},
	}
	c := newTestClient(mock, nil)
	_, err := c.CreateAssetWallet(context.Background(), "test-ref-1234", []string{"ETH"})
	if err == nil || !strings.Contains(err.Error(), "CreateAccount") {
		t.Fatalf("expected CreateAccount error, got: %v", err)
	}
}

// --- AddCoin tests ---

func TestAddCoin_Success(t *testing.T) {
	mock := &mockAccountAPI{
		addCoinV2Fn: func(req api.AddCoinV2Request, resp *api.AddCoinV2Response) error {
			if req.AccountKey != "ak-001" {
				t.Fatalf("unexpected accountKey: %s", req.AccountKey)
			}
			resp.AccountKey = "ak-001"
			resp.CoinAddressList = []struct {
				CoinKey          string `json:"coinKey"`
				AddressGroupKey  string `json:"addressGroupKey"`
				AddressGroupName string `json:"addressGroupName"`
				AddressList      []struct {
					Address     string `json:"address"`
					AddressType string `json:"addressType"`
					DerivePath  string `json:"derivePath"`
				} `json:"addressList"`
			}{
				{
					CoinKey: "USDT_ERC20",
					AddressList: []struct {
						Address     string `json:"address"`
						AddressType string `json:"addressType"`
						DerivePath  string `json:"derivePath"`
					}{
						{Address: "0xabc123"},
					},
				},
			}
			return nil
		},
	}

	c := newTestClient(mock, nil)
	w, err := c.AddCoin(context.Background(), "ak-001", []string{"USDT_ERC20"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.AccountKey != "ak-001" {
		t.Fatalf("expected ak-001, got %s", w.AccountKey)
	}
	if len(w.CoinAddressList) != 1 || w.CoinAddressList[0].CoinKey != "USDT_ERC20" {
		t.Fatalf("unexpected result: %+v", w.CoinAddressList)
	}
}

func TestAddCoin_SDKError(t *testing.T) {
	mock := &mockAccountAPI{
		addCoinV2Fn: func(_ api.AddCoinV2Request, _ *api.AddCoinV2Response) error {
			return errors.New("sdk: rate limited")
		},
	}
	c := newTestClient(mock, nil)
	_, err := c.AddCoin(context.Background(), "ak-001", []string{"ETH"})
	if err == nil || !strings.Contains(err.Error(), "AddCoinV2") {
		t.Fatalf("expected AddCoinV2 error, got: %v", err)
	}
}

// --- ListAccountCoin tests ---

func TestListAccountCoin_Success(t *testing.T) {
	mock := &mockAccountAPI{
		listAccountCoinFn: func(req api.ListAccountCoinRequest, resp *api.AccountCoinResponse) error {
			if req.AccountKey != "ak-001" {
				t.Fatalf("unexpected accountKey: %s", req.AccountKey)
			}
			*resp = api.AccountCoinResponse{
				{
					CoinKey: "ETH",
					Symbol:  "ETH",
					Balance: "1.5",
					AddressList: []struct {
						Address        string `json:"address"`
						AddressType    string `json:"addressType"`
						DerivePath     string `json:"derivePath"`
						AddressBalance string `json:"addressBalance"`
					}{
						{Address: "0xabc", AddressBalance: "1.5"},
					},
				},
			}
			return nil
		},
	}

	c := newTestClient(mock, nil)
	coins, err := c.ListAccountCoin(context.Background(), "ak-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(coins) != 1 || coins[0].CoinKey != "ETH" || coins[0].Balance != "1.5" {
		t.Fatalf("unexpected result: %+v", coins)
	}
	if len(coins[0].AddressList) != 1 || coins[0].AddressList[0].Balance != "1.5" {
		t.Fatalf("unexpected address list: %+v", coins[0].AddressList)
	}
}

func TestListAccountCoin_SDKError(t *testing.T) {
	mock := &mockAccountAPI{
		listAccountCoinFn: func(_ api.ListAccountCoinRequest, _ *api.AccountCoinResponse) error {
			return errors.New("sdk: timeout")
		},
	}
	c := newTestClient(mock, nil)
	_, err := c.ListAccountCoin(context.Background(), "ak-001")
	if err == nil || !strings.Contains(err.Error(), "ListAccountCoin") {
		t.Fatalf("expected ListAccountCoin error, got: %v", err)
	}
}

// --- GetAccountByAddress tests ---

func TestGetAccountByAddress_Success(t *testing.T) {
	mock := &mockAccountAPI{
		getAccountByAddrFn: func(req api.OneAccountByAddressRequest, resp *api.AccountResponse) error {
			if req.Address != "0xtest" {
				t.Fatalf("unexpected address: %s", req.Address)
			}
			resp.AccountKey = "ak-001"
			resp.CustomerRefId = "ref-123"
			resp.AccountName = "DEPOSIT-ref-123"
			resp.AccountTag = "DEPOSIT"
			resp.HiddenOnUI = true
			resp.AutoFuel = false
			return nil
		},
	}

	c := newTestClient(mock, nil)
	acct, err := c.GetAccountByAddress(context.Background(), "0xtest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if acct.AccountKey != "ak-001" || acct.CustomerRefID != "ref-123" {
		t.Fatalf("unexpected result: %+v", acct)
	}
	if !acct.HiddenOnUI || acct.AutoFuel {
		t.Fatal("expected hiddenOnUI=true, autoFuel=false")
	}
}

func TestGetAccountByAddress_SDKError(t *testing.T) {
	mock := &mockAccountAPI{
		getAccountByAddrFn: func(_ api.OneAccountByAddressRequest, _ *api.AccountResponse) error {
			return errors.New("sdk: not found")
		},
	}
	c := newTestClient(mock, nil)
	_, err := c.GetAccountByAddress(context.Background(), "0xtest")
	if err == nil || !strings.Contains(err.Error(), "GetAccountByAddress") {
		t.Fatalf("expected GetAccountByAddress error, got: %v", err)
	}
}

func TestCreateAssetWallet_ShortCustomerRefID(t *testing.T) {
	c := newTestClient(&mockAccountAPI{}, nil)
	_, err := c.CreateAssetWallet(context.Background(), "short", []string{"ETH"})
	if err == nil || !strings.Contains(err.Error(), "at least 8") {
		t.Fatalf("expected short customerRefID error, got: %v", err)
	}
}

func TestCreateAssetWallet_EmptyCoinAddressList(t *testing.T) {
	mock := &mockAccountAPI{
		createAccountFn: func(_ api.CreateAccountRequest, resp *api.CreateAccountResponse) error {
			resp.AccountKey = "ak-empty"
			return nil
		},
	}
	c := newTestClient(mock, nil)
	w, err := c.CreateAssetWallet(context.Background(), "test-ref-1234", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(w.CoinAddressList) != 0 {
		t.Fatalf("expected empty coin address list, got %d", len(w.CoinAddressList))
	}
}

func TestAddCoin_EmptyResponse(t *testing.T) {
	mock := &mockAccountAPI{
		addCoinV2Fn: func(_ api.AddCoinV2Request, resp *api.AddCoinV2Response) error {
			resp.AccountKey = "ak-001"
			return nil
		},
	}
	c := newTestClient(mock, nil)
	w, err := c.AddCoin(context.Background(), "ak-001", []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.AccountKey != "ak-001" || len(w.CoinAddressList) != 0 {
		t.Fatalf("unexpected result: %+v", w)
	}
}

func TestListAccountCoin_EmptyResponse(t *testing.T) {
	mock := &mockAccountAPI{
		listAccountCoinFn: func(_ api.ListAccountCoinRequest, _ *api.AccountCoinResponse) error {
			return nil
		},
	}
	c := newTestClient(mock, nil)
	coins, err := c.ListAccountCoin(context.Background(), "ak-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(coins) != 0 {
		t.Fatalf("expected empty coins, got %d", len(coins))
	}
}

func TestNewClient_PartialWebhookKeyRejectsConfig(t *testing.T) {
	privPEM := "-----BEGIN PRIVATE KEY-----\nMIIEvgI=\n-----END PRIVATE KEY-----"
	pubPEM := "-----BEGIN PUBLIC KEY-----\nMIIBIjA=\n-----END PUBLIC KEY-----"

	_, err := NewClient(Config{
		APIKey:               "test-key",
		PrivateKeyPEM:        privPEM,
		PlatformPublicKeyPEM: pubPEM,
		WebhookPublicKeyPEM:  pubPEM,
	})
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("expected partial webhook key error, got: %v", err)
	}

	_, err = NewClient(Config{
		APIKey:               "test-key",
		PrivateKeyPEM:        privPEM,
		PlatformPublicKeyPEM: pubPEM,
		WebhookPrivateKeyPEM: privPEM,
	})
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("expected partial webhook key error, got: %v", err)
	}
}

func TestNewClient_WebhookPubKeyWriteFailure(t *testing.T) {
	privPEM := "-----BEGIN PRIVATE KEY-----\nMIIEvgI=\n-----END PRIVATE KEY-----"
	pubPEM := "-----BEGIN PUBLIC KEY-----\nMIIBIjA=\n-----END PUBLIC KEY-----"

	orig := os.Getenv("TMPDIR")

	c, err := NewClient(Config{
		APIKey:               "test-key",
		PrivateKeyPEM:        privPEM,
		PlatformPublicKeyPEM: pubPEM,
	})
	if err != nil {
		t.Fatalf("base NewClient failed: %v", err)
	}
	c.Close()

	os.Setenv("TMPDIR", "/nonexistent-webhook-test")
	defer os.Setenv("TMPDIR", orig)

	_, err = NewClient(Config{
		APIKey:               "test-key",
		PrivateKeyPEM:        privPEM,
		PlatformPublicKeyPEM: pubPEM,
		WebhookPublicKeyPEM:  pubPEM,
		WebhookPrivateKeyPEM: privPEM,
	})
	if err == nil {
		t.Fatal("expected error when webhook PEM temp dir is invalid")
	}
	if !strings.Contains(err.Error(), "webhook") {
		t.Fatalf("expected webhook error, got: %v", err)
	}
}

// --- WebhookConvert tests ---

func TestWebhookConvert_NotConfigured(t *testing.T) {
	c := newTestClient(nil, nil)
	_, err := c.WebhookConvert([]byte(`{"timestamp":"1"}`))
	if err == nil || !strings.Contains(err.Error(), "webhook not configured") {
		t.Fatalf("expected webhook not configured error, got: %v", err)
	}
}

func TestWebhookConvert_Success(t *testing.T) {
	mock := &mockWebhookConv{
		convertFn: func(wh webhook.WebHook) (string, error) {
			if wh.Timestamp != "1234567890" {
				t.Fatalf("unexpected timestamp: %s", wh.Timestamp)
			}
			return `{"eventType":"TRANSACTION_STATUS_CHANGED","eventDetail":{"txKey":"tx-001","coinKey":"ETH","txAmount":"1.5","transactionStatus":"COMPLETED","transactionSubStatus":"CONFIRMED","destinationAddress":"0xabc","transactionDirection":"INFLOW"}}`, nil
		},
	}

	c := newTestClient(nil, mock)
	body := `{"timestamp":"1234567890","sig":"test-sig","key":"test-key","bizContent":"test","rsaType":"ECB_OAEP","aesType":"GCM"}`
	evt, err := c.WebhookConvert([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.EventType != "TRANSACTION_STATUS_CHANGED" {
		t.Fatalf("unexpected eventType: %s", evt.EventType)
	}
	if evt.EventDetail.TxKey != "tx-001" || evt.EventDetail.TxAmount != "1.5" {
		t.Fatalf("unexpected event detail: %+v", evt.EventDetail)
	}
	if evt.EventDetail.TransactionDirection != "INFLOW" {
		t.Fatalf("expected INFLOW, got %s", evt.EventDetail.TransactionDirection)
	}
}

func TestWebhookConvert_InvalidJSON(t *testing.T) {
	mock := &mockWebhookConv{convertFn: func(_ webhook.WebHook) (string, error) { return "", nil }}
	c := newTestClient(nil, mock)
	_, err := c.WebhookConvert([]byte("not-json"))
	if err == nil || !strings.Contains(err.Error(), "unmarshal") {
		t.Fatalf("expected unmarshal error, got: %v", err)
	}
}

func TestWebhookConvert_VerifyFailed(t *testing.T) {
	mock := &mockWebhookConv{
		convertFn: func(_ webhook.WebHook) (string, error) {
			return "", errors.New("webhook signature verification failed")
		},
	}

	c := newTestClient(nil, mock)
	body := `{"timestamp":"1234567890","sig":"bad","key":"k","bizContent":"b","rsaType":"ECB_OAEP","aesType":"GCM"}`
	_, err := c.WebhookConvert([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "verify") {
		t.Fatalf("expected verify error, got: %v", err)
	}
}

func TestWebhookConvert_InvalidEventJSON(t *testing.T) {
	mock := &mockWebhookConv{
		convertFn: func(_ webhook.WebHook) (string, error) {
			return "not-valid-json", nil
		},
	}

	c := newTestClient(nil, mock)
	body := `{"timestamp":"1234567890","sig":"ok","key":"k","bizContent":"b","rsaType":"ECB_OAEP","aesType":"GCM"}`
	_, err := c.WebhookConvert([]byte(body))
	if err == nil || !strings.Contains(err.Error(), "parse event") {
		t.Fatalf("expected parse event error, got: %v", err)
	}
}

// --- Helper function tests ---

func TestWriteTempPEM(t *testing.T) {
	content := "-----BEGIN TEST-----\ndata\n-----END TEST-----"
	path, err := writeTempPEM("test", content)
	if err != nil {
		t.Fatalf("writeTempPEM failed: %v", err)
	}
	defer os.Remove(path)

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Fatalf("content mismatch: got %q", string(got))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("wrong permissions: %v", info.Mode().Perm())
	}
}

func TestCleanupFiles(t *testing.T) {
	f1, _ := writeTempPEM("cleanup1", "test1")
	f2, _ := writeTempPEM("cleanup2", "test2")
	cleanupFiles([]string{f1, f2})
	for _, f := range []string{f1, f2} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("file %s should have been removed", f)
		}
	}
}

func TestCleanupFiles_NonexistentPath(t *testing.T) {
	cleanupFiles([]string{"/nonexistent/file.pem"})
}

func TestClose_Idempotent(t *testing.T) {
	c, _ := NewClient(Config{
		BaseURL:              "https://api.safeheron.vip",
		APIKey:               "test-api-key",
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\nMIIEvgI=\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nMIIBIjA=\n-----END PUBLIC KEY-----",
	})
	c.Close()
	if err := c.Close(); err != nil {
		t.Fatalf("second Close should be safe: %v", err)
	}
}
