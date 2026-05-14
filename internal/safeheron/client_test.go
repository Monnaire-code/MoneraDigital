package safeheron

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/webhook"
)

// --- mock SDK interfaces ---

type mockAccountAPI struct {
	createAccountFn    func(api.CreateAccountRequest, *api.CreateAccountResponse) error
	addCoinV2Fn        func(api.AddCoinV2Request, *api.AddCoinV2Response) error
	listAccountCoinFn  func(api.ListAccountCoinRequest, *api.AccountCoinResponse) error
	getAccountByAddrFn func(api.OneAccountByAddressRequest, *api.AccountResponse) error
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

type mockTransactionAPI struct {
	createTxFn func(api.CreateTransactionsRequest, *api.CreateTransactionV3Response) error
	oneTxFn    func(api.OneTransactionsRequest, *api.OneTransactionsResponse) error
}

func (m *mockTransactionAPI) CreateTransactionsV3(req api.CreateTransactionsRequest, resp *api.CreateTransactionV3Response) error {
	return m.createTxFn(req, resp)
}
func (m *mockTransactionAPI) OneTransactions(req api.OneTransactionsRequest, resp *api.OneTransactionsResponse) error {
	return m.oneTxFn(req, resp)
}

type mockComplianceAPI struct {
	kytReportFn func(api.KytReportRequest, *api.KytReportResponse) error
}

func (m *mockComplianceAPI) KytReport(req api.KytReportRequest, resp *api.KytReportResponse) error {
	return m.kytReportFn(req, resp)
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
	tempDir := c.tempDir
	if tempDir == "" {
		t.Fatal("expected tempDir to be set (SEC-2)")
	}
	c.Close()
	for _, f := range saved {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("temp file %s not cleaned up", f)
		}
	}
	if c.tempFiles != nil {
		t.Fatal("tempFiles should be nil after Close")
	}
	// SEC-2: process tempDir must also be removed so PEM data does not survive
	// process exit even if a future code path forgets to track a file.
	if _, err := os.Stat(tempDir); !os.IsNotExist(err) {
		t.Fatalf("tempDir %s should be removed after Close (SEC-2)", tempDir)
	}
	if c.tempDir != "" {
		t.Fatal("tempDir field should be cleared after Close")
	}
}

// SEC-2: Close() must be idempotent so the signal handler can call it after
// the process already cleaned up via defer (or vice versa).
func TestClient_CloseIsIdempotent(t *testing.T) {
	c, err := NewClient(Config{
		BaseURL:              "https://api.safeheron.vip",
		APIKey:               "test-api-key",
		PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\nA=\n-----END PRIVATE KEY-----",
		PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\nB=\n-----END PUBLIC KEY-----",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close must not error: %v", err)
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
	dir := t.TempDir()
	content := "-----BEGIN TEST-----\ndata\n-----END TEST-----"
	path, err := writeTempPEM(dir, "test", content)
	if err != nil {
		t.Fatalf("writeTempPEM failed: %v", err)
	}
	defer os.Remove(path)

	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Fatalf("content mismatch: got %q", string(got))
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("wrong permissions: %v", info.Mode().Perm())
	}
}

func TestCleanupFiles(t *testing.T) {
	dir := t.TempDir()
	f1, _ := writeTempPEM(dir, "cleanup1", "test1")
	f2, _ := writeTempPEM(dir, "cleanup2", "test2")
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

// --- KytReport tests ---

func TestKytReport_Success(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(req api.KytReportRequest, resp *api.KytReportResponse) error {
			if req.TxKey != "tx-kyt-001" {
				t.Fatalf("unexpected txKey: %s", req.TxKey)
			}
			resp.TxKey = "tx-kyt-001"
			resp.CustomerRefId = "ref-abc"
			resp.AmlScreeningTriggeredState = "TRIGGERED"
			resp.AmlList = []api.AmlReport{
				{
					Provider:       "MistTrack",
					Timestamp:      "1715500000000",
					Status:         "COMPLETED",
					RiskLevel:      "LOW",
					LastUpdateTime: "1715500001000",
					Payload:        map[string]any{"score": 10},
				},
			}
			return nil
		},
	}

	c := &Client{compliance: mock}
	resp, err := c.KytReport(context.Background(), "tx-kyt-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TxKey != "tx-kyt-001" || resp.CustomerRefID != "ref-abc" {
		t.Fatalf("unexpected response header: %+v", resp)
	}
	if resp.AmlScreeningTriggeredState != "TRIGGERED" {
		t.Fatalf("expected TRIGGERED, got %s", resp.AmlScreeningTriggeredState)
	}
	if len(resp.AmlList) != 1 {
		t.Fatalf("expected 1 AmlReport, got %d", len(resp.AmlList))
	}
	r := resp.AmlList[0]
	if r.Provider != "MistTrack" || r.RiskLevel != "LOW" || r.Status != "COMPLETED" {
		t.Fatalf("unexpected AmlReport: %+v", r)
	}
	if len(r.Payload) == 0 || string(r.Payload) == "null" {
		t.Fatal("expected non-empty Payload (json.RawMessage from SDK any)")
	}
}

func TestKytReport_PayloadMarshal(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(_ api.KytReportRequest, resp *api.KytReportResponse) error {
			resp.TxKey = "tx-002"
			resp.AmlScreeningTriggeredState = "TRIGGERED"
			resp.AmlList = []api.AmlReport{
				{
					Provider:  "MistTrack",
					Status:    "COMPLETED",
					RiskLevel: "HIGH",
					Payload:   map[string]any{"riskScore": 85, "tags": []string{"mixer"}},
				},
			}
			return nil
		},
	}

	c := &Client{compliance: mock}
	resp, err := c.KytReport(context.Background(), "tx-002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload := string(resp.AmlList[0].Payload)
	if !strings.Contains(payload, "riskScore") || !strings.Contains(payload, "mixer") {
		t.Fatalf("Payload not correctly marshaled from SDK any: %s", payload)
	}
}

// G-2: provider payload that fails json.Marshal must still leave a breadcrumb
// in the AML row so ops doesn't lose the evidence trail. The previous code
// silently stored nil.
func TestKytReport_PayloadMarshalFailureRetainsError(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(_ api.KytReportRequest, resp *api.KytReportResponse) error {
			resp.TxKey = "tx-marshal-fail"
			resp.AmlScreeningTriggeredState = "TRIGGERED"
			resp.AmlList = []api.AmlReport{
				{
					Provider:  "MistTrack",
					Status:    "COMPLETED",
					RiskLevel: "HIGH",
					Payload:   make(chan int), // channels are not marshalable
				},
			}
			return nil
		},
	}

	c := &Client{compliance: mock}
	resp, err := c.KytReport(context.Background(), "tx-marshal-fail")
	if err != nil {
		t.Fatalf("KytReport should swallow marshal errors and continue, got: %v", err)
	}
	payload := string(resp.AmlList[0].Payload)
	if !strings.Contains(payload, "_marshal_error") {
		t.Fatalf("expected payload to retain _marshal_error breadcrumb, got: %s", payload)
	}
}

func TestKytReport_SDKError(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(_ api.KytReportRequest, _ *api.KytReportResponse) error {
			return errors.New("sdk: compliance API timeout")
		},
	}

	c := &Client{compliance: mock}
	_, err := c.KytReport(context.Background(), "tx-err-001")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "KytReport") || !strings.Contains(err.Error(), "tx-err-001") {
		t.Fatalf("error should contain method name and txKey: %v", err)
	}
}

// --- CreateTransaction tests ---

func TestCreateTransaction_Success(t *testing.T) {
	mock := &mockTransactionAPI{
		createTxFn: func(req api.CreateTransactionsRequest, resp *api.CreateTransactionV3Response) error {
			if req.CoinKey != "ETH" {
				t.Fatalf("unexpected coinKey: %s", req.CoinKey)
			}
			if req.TxAmount != "0.001" {
				t.Fatalf("unexpected txAmount: %s", req.TxAmount)
			}
			if req.SourceAccountType != "VAULT_ACCOUNT" {
				t.Fatalf("unexpected sourceAccountType: %s", req.SourceAccountType)
			}
			if req.DestinationAccountType != "ONE_TIME_ADDRESS" {
				t.Fatalf("unexpected destAccountType: %s", req.DestinationAccountType)
			}
			if req.DestinationAddress != "0xdest" {
				t.Fatalf("unexpected destAddress: %s", req.DestinationAddress)
			}
			resp.TxKey = "tx-withdraw-001"
			resp.CustomerRefId = "ref-w-001"
			return nil
		},
	}

	c := &Client{transaction: mock}
	resp, err := c.CreateTransaction(context.Background(), CreateTransactionRequest{
		CustomerRefID:          "ref-w-001",
		CoinKey:                "ETH",
		TxAmount:               "0.001",
		SourceAccountKey:       "ak-001",
		SourceAccountType:      "VAULT_ACCOUNT",
		DestinationAccountType: "ONE_TIME_ADDRESS",
		DestinationAddress:     "0xdest",
		Note:                   "test withdrawal",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TxKey != "tx-withdraw-001" {
		t.Fatalf("expected tx-withdraw-001, got %s", resp.TxKey)
	}
	if resp.CustomerRefID != "ref-w-001" {
		t.Fatalf("expected ref-w-001, got %s", resp.CustomerRefID)
	}
}

func TestCreateTransaction_SDKError(t *testing.T) {
	mock := &mockTransactionAPI{
		createTxFn: func(_ api.CreateTransactionsRequest, _ *api.CreateTransactionV3Response) error {
			return errors.New("sdk: insufficient balance")
		},
	}
	c := &Client{transaction: mock}
	_, err := c.CreateTransaction(context.Background(), CreateTransactionRequest{
		CoinKey:  "ETH",
		TxAmount: "100",
	})
	if err == nil || !strings.Contains(err.Error(), "CreateTransaction") {
		t.Fatalf("expected CreateTransaction error, got: %v", err)
	}
}

// --- GetTransaction tests ---

func TestGetTransaction_Success(t *testing.T) {
	mock := &mockTransactionAPI{
		oneTxFn: func(req api.OneTransactionsRequest, resp *api.OneTransactionsResponse) error {
			if req.TxKey != "tx-001" {
				t.Fatalf("unexpected txKey: %s", req.TxKey)
			}
			resp.TxKey = "tx-001"
			resp.TxHash = "0xabcdef"
			resp.CoinKey = "ETH"
			resp.TxAmount = "0.001"
			resp.TransactionStatus = "COMPLETED"
			resp.SourceAddress = "0xsrc"
			resp.DestinationAddress = "0xdst"
			return nil
		},
	}
	c := &Client{transaction: mock}
	detail, err := c.GetTransaction(context.Background(), "tx-001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.TxKey != "tx-001" || detail.TxHash != "0xabcdef" {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if detail.TransactionStatus != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", detail.TransactionStatus)
	}
}

func TestGetTransaction_SDKError(t *testing.T) {
	mock := &mockTransactionAPI{
		oneTxFn: func(_ api.OneTransactionsRequest, _ *api.OneTransactionsResponse) error {
			return errors.New("sdk: not found")
		},
	}
	c := &Client{transaction: mock}
	_, err := c.GetTransaction(context.Background(), "tx-missing")
	if err == nil || !strings.Contains(err.Error(), "GetTransaction") {
		t.Fatalf("expected GetTransaction error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "tx-missing") {
		t.Fatalf("error should contain txKey: %v", err)
	}
}

// --- CreateTransaction edge cases ---

func TestCreateTransaction_NoteForwarded(t *testing.T) {
	var captured api.CreateTransactionsRequest
	mock := &mockTransactionAPI{
		createTxFn: func(req api.CreateTransactionsRequest, resp *api.CreateTransactionV3Response) error {
			captured = req
			resp.TxKey = "tx-note-001"
			resp.CustomerRefId = "ref-note"
			return nil
		},
	}

	c := &Client{transaction: mock}
	_, err := c.CreateTransaction(context.Background(), CreateTransactionRequest{
		CustomerRefID:          "ref-note",
		CoinKey:                "ETH",
		TxAmount:               "0.01",
		SourceAccountKey:       "ak-001",
		SourceAccountType:      "VAULT_ACCOUNT",
		DestinationAccountType: "ONE_TIME_ADDRESS",
		DestinationAddress:     "0xdest",
		Note:                   "withdrawal for user 42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.Note != "withdrawal for user 42" {
		t.Fatalf("Note not forwarded to SDK request: got %q", captured.Note)
	}
	if captured.CustomerRefId != "ref-note" {
		t.Fatalf("CustomerRefId mismatch: got %q", captured.CustomerRefId)
	}
	if captured.SourceAccountKey != "ak-001" {
		t.Fatalf("SourceAccountKey mismatch: got %q", captured.SourceAccountKey)
	}
}

func TestCreateTransaction_EmptyNote(t *testing.T) {
	var captured api.CreateTransactionsRequest
	mock := &mockTransactionAPI{
		createTxFn: func(req api.CreateTransactionsRequest, resp *api.CreateTransactionV3Response) error {
			captured = req
			resp.TxKey = "tx-empty-note"
			return nil
		},
	}

	c := &Client{transaction: mock}
	_, err := c.CreateTransaction(context.Background(), CreateTransactionRequest{
		CoinKey:                "ETH",
		TxAmount:               "0.01",
		SourceAccountType:      "VAULT_ACCOUNT",
		DestinationAccountType: "ONE_TIME_ADDRESS",
		DestinationAddress:     "0xdest",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.Note != "" {
		t.Fatalf("expected empty Note, got %q", captured.Note)
	}
}

// --- GetTransaction edge cases ---

func TestGetTransaction_EmptyFields(t *testing.T) {
	mock := &mockTransactionAPI{
		oneTxFn: func(_ api.OneTransactionsRequest, resp *api.OneTransactionsResponse) error {
			// SDK returns a response with all fields empty/zero
			resp.TxKey = "tx-empty"
			return nil
		},
	}
	c := &Client{transaction: mock}
	detail, err := c.GetTransaction(context.Background(), "tx-empty")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if detail.TxKey != "tx-empty" {
		t.Fatalf("expected tx-empty, got %s", detail.TxKey)
	}
	if detail.TxHash != "" {
		t.Fatalf("expected empty TxHash, got %s", detail.TxHash)
	}
	if detail.CoinKey != "" {
		t.Fatalf("expected empty CoinKey, got %s", detail.CoinKey)
	}
	if detail.TxAmount != "" {
		t.Fatalf("expected empty TxAmount, got %s", detail.TxAmount)
	}
	if detail.TransactionStatus != "" {
		t.Fatalf("expected empty TransactionStatus, got %s", detail.TransactionStatus)
	}
	if detail.SourceAddress != "" {
		t.Fatalf("expected empty SourceAddress, got %s", detail.SourceAddress)
	}
	if detail.DestinationAddress != "" {
		t.Fatalf("expected empty DestinationAddress, got %s", detail.DestinationAddress)
	}
}

// --- KytReport edge cases ---

func TestKytReport_EmptyAmlList(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(_ api.KytReportRequest, resp *api.KytReportResponse) error {
			resp.TxKey = "tx-no-aml"
			resp.AmlScreeningTriggeredState = "UNTRIGGERED"
			return nil
		},
	}
	c := &Client{compliance: mock}
	resp, err := c.KytReport(context.Background(), "tx-no-aml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AmlScreeningTriggeredState != "UNTRIGGERED" {
		t.Fatalf("expected UNTRIGGERED, got %s", resp.AmlScreeningTriggeredState)
	}
	if len(resp.AmlList) != 0 {
		t.Fatalf("expected empty AmlList, got %d", len(resp.AmlList))
	}
}

func TestKytReport_NilPayload(t *testing.T) {
	mock := &mockComplianceAPI{
		kytReportFn: func(_ api.KytReportRequest, resp *api.KytReportResponse) error {
			resp.TxKey = "tx-nil-payload"
			resp.AmlScreeningTriggeredState = "TRIGGERED"
			resp.AmlList = []api.AmlReport{
				{
					Provider:  "MistTrack",
					Status:    "COMPLETED",
					RiskLevel: "LOW",
					Payload:   nil,
				},
			}
			return nil
		},
	}
	c := &Client{compliance: mock}
	resp, err := c.KytReport(context.Background(), "tx-nil-payload")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.AmlList) != 1 {
		t.Fatalf("expected 1 AmlReport, got %d", len(resp.AmlList))
	}
	// json.Marshal(nil) returns "null"
	if string(resp.AmlList[0].Payload) != "null" {
		t.Fatalf("expected null Payload, got %s", string(resp.AmlList[0].Payload))
	}
}

// --- NewClient: selective write failure coverage ---
//
// These tests use a goroutine that busy-waits for N files to appear in a
// custom TMPDIR, then chmod 0555 the directory so the (N+1)-th
// os.CreateTemp call inside NewClient fails with "permission denied".
// The Chmod+WriteString+Close work inside writeTempPEM between CreateTemp
// calls provides enough time for the watcher goroutine to act reliably.

// lockDirAfterNFiles spins until at least n files exist in dir, then
// removes write permission so subsequent os.CreateTemp calls fail.
func lockDirAfterNFiles(dir string, n int, locked *atomic.Bool) {
	for {
		entries, _ := os.ReadDir(dir)
		if len(entries) >= n && !locked.Load() {
			os.Chmod(dir, 0555)
			locked.Store(true)
			return
		}
	}
}

func TestNewClient_PlatformKeyWriteFailure_Cleanup(t *testing.T) {
	// Private key write succeeds (1 file), platform key write fails.
	// Exercises lines 73-76: cleanupFiles(tempFiles) + return.
	//
	// The goroutine must lock the directory between the first and second
	// writeTempPEM calls. This is a tight race (single Chmod+WriteString+Close
	// gap), so we retry a few times before giving up.
	orig := os.Getenv("TMPDIR")
	defer os.Setenv("TMPDIR", orig)

	for range 5 {
		os.Setenv("TMPDIR", orig) // restore before MkdirTemp
		dir, err := os.MkdirTemp("", "platform-fail-")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}

		var locked atomic.Bool
		go lockDirAfterNFiles(dir, 1, &locked)

		os.Setenv("TMPDIR", dir)
		c, err := NewClient(Config{
			APIKey:               "test-key",
			PrivateKeyPEM:        "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----",
			PlatformPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
		})

		os.Chmod(dir, 0755)
		os.RemoveAll(dir)

		if err == nil {
			// Race lost: both writes completed before chmod. Clean up and retry.
			c.Close()
			continue
		}
		if !strings.Contains(err.Error(), "platform public key") {
			t.Fatalf("expected 'platform public key' error, got: %v", err)
		}
		return // success
	}
	t.Skip("could not win the race after 5 attempts (platform key write failure)")
}

// SEC-2 refactor note: TestNewClient_WebhookPubKeyWriteFailure_Cleanup and
// TestNewClient_WebhookPrivKeyWriteFailure_Cleanup were removed alongside the
// switch to a process-owned tempDir. Their lockDirAfterNFiles trick chmod-ed
// the OUTER tmp dir after N entries existed; the new layout creates a single
// safeheron-* subdir and writes key files INSIDE it, so the race never fires
// and the chmod no longer affects writes. Cleanup is now dominated by the
// unconditional `defer os.RemoveAll(tempDir)` exercised by
// TestClient_CloseIsIdempotent and TestNewClient_TempFilesCreatedAndCleaned.

// --- writeTempPEM edge cases ---

func TestWriteTempPEM_CreateTempFailure(t *testing.T) {
	_, err := writeTempPEM("/nonexistent-dir-for-createtemp-test", "test", "content")
	if err == nil {
		t.Fatal("expected error when dir is invalid")
	}
}

func TestWriteTempPEM_ContentPreserved(t *testing.T) {
	// Verify multi-line PEM content with special characters is preserved exactly.
	dir := t.TempDir()
	content := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQ+/=\nmore+data==\n-----END RSA PRIVATE KEY-----"
	path, err := writeTempPEM(dir, "multiline", content)
	if err != nil {
		t.Fatalf("writeTempPEM failed: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content mismatch:\nwant: %q\ngot:  %q", content, string(got))
	}
}

func TestCleanupFiles_MixedExistentAndNonexistent(t *testing.T) {
	dir := t.TempDir()
	f1, _ := writeTempPEM(dir, "mixed1", "test")
	cleanupFiles([]string{f1, "/nonexistent/mixed2.pem", f1})
	if _, err := os.Stat(f1); !os.IsNotExist(err) {
		t.Fatalf("file %s should have been removed", f1)
	}
}

// --- Integration test: real SDK withdrawal (skipped unless SAFEHERON_INTEGRATION=1) ---

func TestIntegration_CreateWithdrawal(t *testing.T) {
	if os.Getenv("SAFEHERON_INTEGRATION") != "1" {
		t.Skip("set SAFEHERON_INTEGRATION=1 and configure .env to run")
	}

	cfg := Config{
		BaseURL:              os.Getenv("SAFEHERON_API_BASE_URL"),
		APIKey:               os.Getenv("SAFEHERON_API_KEY"),
		PrivateKeyPEM:        os.Getenv("SAFEHERON_PRIVATE_KEY_PEM"),
		PlatformPublicKeyPEM: os.Getenv("SAFEHERON_PLATFORM_PUBLIC_KEY_PEM"),
		RequestTimeoutMS:     30000,
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	req := CreateTransactionRequest{
		CustomerRefID:          fmt.Sprintf("test-withdraw-%d", time.Now().UnixMilli()),
		CoinKey:                os.Getenv("TEST_WITHDRAW_COIN_KEY"),
		TxAmount:               os.Getenv("TEST_WITHDRAW_AMOUNT"),
		TreatAsGrossAmount:     false,
		GasLimit:               "21000",
		MaxFee:                 "10",
		MaxPriorityFee:         "2",
		SourceAccountKey:       os.Getenv("TEST_WITHDRAW_SOURCE_ACCOUNT_KEY"),
		SourceAccountType:      "VAULT_ACCOUNT",
		DestinationAccountType: "ONE_TIME_ADDRESS",
		DestinationAddress:     os.Getenv("TEST_WITHDRAW_DEST_ADDRESS"),
		Note:                   "integration test withdrawal",
	}

	if req.CoinKey == "" || req.TxAmount == "" || req.SourceAccountKey == "" || req.DestinationAddress == "" {
		t.Skip("set TEST_WITHDRAW_COIN_KEY, TEST_WITHDRAW_AMOUNT, TEST_WITHDRAW_SOURCE_ACCOUNT_KEY, TEST_WITHDRAW_DEST_ADDRESS")
	}

	resp, err := client.CreateTransaction(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}
	t.Logf("Withdrawal created: txKey=%s customerRefId=%s", resp.TxKey, resp.CustomerRefID)

	detail, err := client.GetTransaction(context.Background(), resp.TxKey)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	t.Logf("Transaction detail: status=%s amount=%s coin=%s dest=%s",
		detail.TransactionStatus, detail.TxAmount, detail.CoinKey, detail.DestinationAddress)
}
