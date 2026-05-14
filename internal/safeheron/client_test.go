package safeheron

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

// --- file-based PEM fixtures (v1.6) ---

// writePEMFile drops a placeholder PEM into dir with the given perm and
// returns the absolute path. NewClient only stats the file; the SDK reads it
// lazily on the first signed request, which never happens in unit tests.
func writePEMFile(t *testing.T, dir, name string, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := fmt.Sprintf("-----BEGIN TEST KEY-----\n%s\n-----END TEST KEY-----\n", name)
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// fixtureKeys writes the four PEM files NewClient validates and returns their
// paths in (priv, plat, whPub, whPriv) order.
func fixtureKeys(t *testing.T) (string, string, string, string) {
	t.Helper()
	dir := t.TempDir()
	priv := writePEMFile(t, dir, "private.pem", 0o600)
	plat := writePEMFile(t, dir, "platform.pem", 0o644)
	whPub := writePEMFile(t, dir, "webhook-pub.pem", 0o644)
	whPriv := writePEMFile(t, dir, "webhook-priv.pem", 0o600)
	return priv, plat, whPub, whPriv
}

// --- NewClient validation tests ---

func TestNewClient_MissingAPIKey(t *testing.T) {
	priv, plat, _, _ := fixtureKeys(t)
	_, err := NewClient(Config{
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "APIKey") {
		t.Fatalf("expected APIKey error, got: %v", err)
	}
}

func TestNewClient_MissingPrivateKey(t *testing.T) {
	_, plat, _, _ := fixtureKeys(t)
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "PrivateKeyPath") || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected PrivateKeyPath required error, got: %v", err)
	}
}

func TestNewClient_MissingPlatformKey(t *testing.T) {
	priv, _, _, _ := fixtureKeys(t)
	_, err := NewClient(Config{
		APIKey:         "test-key",
		PrivateKeyPath: priv,
	})
	if err == nil || !strings.Contains(err.Error(), "PlatformPublicKeyPath") || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected PlatformPublicKeyPath required error, got: %v", err)
	}
}

func TestNewClient_FilePathsHappyPath(t *testing.T) {
	priv, plat, whPub, whPriv := fixtureKeys(t)
	c, err := NewClient(Config{
		BaseURL:               "https://api.safeheron.vip",
		APIKey:                "test-api-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  whPub,
		WebhookPrivateKeyPath: whPriv,
		RequestTimeoutMS:      30000,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.account == nil || c.compliance == nil || c.transaction == nil {
		t.Fatal("expected all SDK API clients to be wired")
	}
	if c.webhookConv == nil {
		t.Fatal("expected webhook converter to be configured")
	}
}

func TestNewClient_PrivateKeyPathMissing(t *testing.T) {
	_, plat, _, _ := fixtureKeys(t)
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        "",
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path is required error, got: %v", err)
	}
}

func TestNewClient_PrivateKeyFileNotFound(t *testing.T) {
	_, plat, _, _ := fixtureKeys(t)
	bogus := filepath.Join(t.TempDir(), "does-not-exist.pem")
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        bogus,
		PlatformPublicKeyPath: plat,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent private key file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected error to wrap os.ErrNotExist, got: %v", err)
	}
}

func TestNewClient_PrivateKeyPathIsNotRegularFile(t *testing.T) {
	dir := t.TempDir()
	// Named pipe (FIFO) is a portable non-regular file under Linux/macOS;
	// exercises the IsRegular() guard in validateKeyFile that catches
	// device/socket/pipe types missed by the dir/symlink checks.
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	plat := writePEMFile(t, dir, "platform.pem", 0o644)

	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        fifo,
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected not-regular-file error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "PrivateKeyPath") {
		t.Fatalf("expected error to identify PrivateKeyPath, got: %v", err)
	}
}

func TestNewClient_PrivateKeyPathIsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := writePEMFile(t, dir, "real-private.pem", 0o600)
	link := filepath.Join(dir, "private.pem")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	plat := writePEMFile(t, dir, "platform.pem", 0o644)

	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        link,
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got: %v", err)
	}
	if !strings.Contains(err.Error(), "PrivateKeyPath") {
		t.Fatalf("expected error to identify PrivateKeyPath, got: %v", err)
	}
}

func TestNewClient_PrivateKeyPathIsDir(t *testing.T) {
	dir := t.TempDir()
	plat := writePEMFile(t, dir, "platform.pem", 0o644)
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        dir, // pointing at a directory, not a file
		PlatformPublicKeyPath: plat,
	})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected 'is a directory' error, got: %v", err)
	}
}

func TestNewClient_WithWebhookKeys(t *testing.T) {
	priv, plat, whPub, whPriv := fixtureKeys(t)
	c, err := NewClient(Config{
		BaseURL:               "https://api.safeheron.vip",
		APIKey:                "test-api-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  whPub,
		WebhookPrivateKeyPath: whPriv,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()
	if c.webhookConv == nil {
		t.Fatal("expected webhook converter to be wired when both webhook paths are set")
	}
}

func TestNewClient_WebhookKeysMustBeBothOrNeither(t *testing.T) {
	priv, plat, whPub, whPriv := fixtureKeys(t)

	cases := []struct {
		name     string
		pubPath  string
		privPath string
	}{
		{"public only", whPub, ""},
		{"private only", "", whPriv},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(Config{
				APIKey:                "test-key",
				PrivateKeyPath:        priv,
				PlatformPublicKeyPath: plat,
				WebhookPublicKeyPath:  tc.pubPath,
				WebhookPrivateKeyPath: tc.privPath,
			})
			if err == nil {
				t.Fatal("expected error for partial webhook config")
			}
			msg := err.Error()
			// Must use the "both ... must be set, or neither" wording from §10.1
			// AND name both fields so operators can grep the log for either env.
			if !strings.Contains(msg, "both") {
				t.Fatalf("expected message to contain 'both', got: %v", err)
			}
			if !strings.Contains(msg, "WebhookPublicKeyPath") || !strings.Contains(msg, "WebhookPrivateKeyPath") {
				t.Fatalf("expected message to name both webhook fields, got: %v", err)
			}
		})
	}
}

func TestNewClient_PermissionWarningEmittedButNotBlocking(t *testing.T) {
	dir := t.TempDir()
	priv := writePEMFile(t, dir, "private.pem", 0o644) // wider than recommended 0600
	plat := writePEMFile(t, dir, "platform.pem", 0o644)

	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()

	c, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
	})
	if err != nil {
		t.Fatalf("NewClient should not block on wider perms, got: %v", err)
	}
	defer c.Close()

	logged := buf.String()
	if !strings.Contains(logged, "wider than recommended") {
		t.Fatalf("expected permission warning in log, got: %q", logged)
	}
	if !strings.Contains(logged, "PrivateKeyPath") {
		t.Fatalf("expected warning to identify the field name, got: %q", logged)
	}
}

func TestNewClient_WebhookPublicKeyFileMissing(t *testing.T) {
	priv, plat, _, whPriv := fixtureKeys(t)
	bogus := filepath.Join(t.TempDir(), "missing-webhook-pub.pem")
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  bogus,
		WebhookPrivateKeyPath: whPriv,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent webhook public key file")
	}
	if !strings.Contains(err.Error(), "WebhookPublicKeyPath") {
		t.Fatalf("expected error to identify WebhookPublicKeyPath, got: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected error to wrap os.ErrNotExist, got: %v", err)
	}
}

func TestNewClient_WebhookPrivateKeyFileMissing(t *testing.T) {
	priv, plat, whPub, _ := fixtureKeys(t)
	bogus := filepath.Join(t.TempDir(), "missing-webhook-priv.pem")
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  whPub,
		WebhookPrivateKeyPath: bogus,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent webhook private key file")
	}
	if !strings.Contains(err.Error(), "WebhookPrivateKeyPath") {
		t.Fatalf("expected error to identify WebhookPrivateKeyPath, got: %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected error to wrap os.ErrNotExist, got: %v", err)
	}
}

func TestNewClient_WebhookPublicKeyPathIsDir(t *testing.T) {
	priv, plat, _, whPriv := fixtureKeys(t)
	dir := t.TempDir()
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  dir,
		WebhookPrivateKeyPath: whPriv,
	})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected webhook public key 'is a directory' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "WebhookPublicKeyPath") {
		t.Fatalf("expected error to identify WebhookPublicKeyPath, got: %v", err)
	}
}

func TestNewClient_WebhookPrivateKeyPathIsDir(t *testing.T) {
	priv, plat, whPub, _ := fixtureKeys(t)
	dir := t.TempDir()
	_, err := NewClient(Config{
		APIKey:                "test-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
		WebhookPublicKeyPath:  whPub,
		WebhookPrivateKeyPath: dir,
	})
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("expected webhook private key 'is a directory' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "WebhookPrivateKeyPath") {
		t.Fatalf("expected error to identify WebhookPrivateKeyPath, got: %v", err)
	}
}

// Locks the WARN/no-WARN matrix for validateKeyFile's permission mask. The
// rule is "warn when actual has any bit beyond recommended" — stricter perms
// (e.g. 0600 file with 0644 recommended) must NOT warn, looser perms must.
// If someone replaces actual&^recommendedPerm with actual != recommendedPerm
// or actual > recommendedPerm, this matrix catches it.
func TestValidateKeyFile_PermissionMaskMatrix(t *testing.T) {
	cases := []struct {
		name        string
		recommended os.FileMode
		actual      os.FileMode
		wantWarn    bool
	}{
		{"private exact match no warn", 0o600, 0o600, false},
		{"private stricter no warn", 0o600, 0o400, false},
		{"private group-read warns", 0o600, 0o640, true},
		{"private world-read warns", 0o600, 0o644, true},
		{"public exact match no warn", 0o644, 0o644, false},
		{"public stricter no warn", 0o644, 0o600, false},
		{"public group-write warns", 0o644, 0o664, true},
		{"public exec bit warns", 0o644, 0o755, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writePEMFile(t, t.TempDir(), "key.pem", tc.actual)
			// writePEMFile honours the mode arg, but umask can mask it; force
			// it explicitly so the assertion is deterministic.
			if err := os.Chmod(path, tc.actual); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			var buf bytes.Buffer
			prevOut := log.Writer()
			prevFlags := log.Flags()
			log.SetOutput(&buf)
			log.SetFlags(0)
			defer func() {
				log.SetOutput(prevOut)
				log.SetFlags(prevFlags)
			}()

			if err := validateKeyFile(path, "TestField", tc.recommended); err != nil {
				t.Fatalf("validateKeyFile must not return error for valid file: %v", err)
			}

			gotWarn := strings.Contains(buf.String(), "wider than recommended")
			if gotWarn != tc.wantWarn {
				t.Fatalf("warn=%v want=%v (recommended=%#o actual=%#o), log=%q",
					gotWarn, tc.wantWarn, tc.recommended, tc.actual, buf.String())
			}
		})
	}
}

// Close() is retained as a no-op for SIGTERM handlers and deferred cleanup;
// it must remain idempotent so callers can fire it from multiple paths.
func TestClient_CloseIsIdempotent(t *testing.T) {
	priv, plat, _, _ := fixtureKeys(t)
	c, err := NewClient(Config{
		APIKey:                "test-api-key",
		PrivateKeyPath:        priv,
		PlatformPublicKeyPath: plat,
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
	if detail.TxHash != "" || detail.CoinKey != "" || detail.TxAmount != "" {
		t.Fatalf("expected empty optional fields, got: %+v", detail)
	}
	if detail.TransactionStatus != "" || detail.SourceAddress != "" || detail.DestinationAddress != "" {
		t.Fatalf("expected empty status/addresses, got: %+v", detail)
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
	if string(resp.AmlList[0].Payload) != "null" {
		t.Fatalf("expected null Payload, got %s", string(resp.AmlList[0].Payload))
	}
}

// --- Integration test: real SDK withdrawal (skipped unless SAFEHERON_INTEGRATION=1) ---

func TestIntegration_CreateWithdrawal(t *testing.T) {
	if os.Getenv("SAFEHERON_INTEGRATION") != "1" {
		t.Skip("set SAFEHERON_INTEGRATION=1 and configure .env to run")
	}

	cfg := Config{
		BaseURL:               os.Getenv("SAFEHERON_API_BASE_URL"),
		APIKey:                os.Getenv("SAFEHERON_API_KEY"),
		PrivateKeyPath:        os.Getenv("SAFEHERON_PRIVATE_KEY_PATH"),
		PlatformPublicKeyPath: os.Getenv("SAFEHERON_PLATFORM_PUBLIC_KEY_PATH"),
		RequestTimeoutMS:      30000,
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
