package safeheron

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	sdk "github.com/Safeheron/safeheron-api-sdk-go/safeheron"
	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/webhook"
)

type accountAPIClient interface {
	CreateAccount(api.CreateAccountRequest, *api.CreateAccountResponse) error
	AddCoinV2(api.AddCoinV2Request, *api.AddCoinV2Response) error
	ListAccountCoin(api.ListAccountCoinRequest, *api.AccountCoinResponse) error
	GetAccountByAddress(api.OneAccountByAddressRequest, *api.AccountResponse) error
}

type complianceAPIClient interface {
	KytReport(api.KytReportRequest, *api.KytReportResponse) error
}

type transactionAPIClient interface {
	CreateTransactionsV3(api.CreateTransactionsRequest, *api.CreateTransactionV3Response) error
	OneTransactions(api.OneTransactionsRequest, *api.OneTransactionsResponse) error
}

type webhookConverter interface {
	Convert(webhook.WebHook) (string, error)
}

// Config carries paths to PEM files on disk. The SDK reads these files directly
// for RSA signing / verification. Operators are responsible for placing the
// files under secrets/ with the recommended permissions (0600 for private keys,
// 0644 for public keys); see SPEC §10.1.
type Config struct {
	BaseURL               string
	APIKey                string
	PrivateKeyPath        string
	PlatformPublicKeyPath string
	WebhookPublicKeyPath  string
	WebhookPrivateKeyPath string
	RequestTimeoutMS      int64
}

type Client struct {
	account     accountAPIClient
	compliance  complianceAPIClient
	transaction transactionAPIClient
	webhookConv webhookConverter
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("safeheron: APIKey is required")
	}
	if err := validateKeyFile(cfg.PrivateKeyPath, "PrivateKeyPath", 0o600); err != nil {
		return nil, err
	}
	if err := validateKeyFile(cfg.PlatformPublicKeyPath, "PlatformPublicKeyPath", 0o644); err != nil {
		return nil, err
	}

	baseClient := sdk.Client{Config: sdk.ApiConfig{
		BaseUrl:               cfg.BaseURL,
		ApiKey:                cfg.APIKey,
		RsaPrivateKey:         cfg.PrivateKeyPath,
		SafeheronRsaPublicKey: cfg.PlatformPublicKeyPath,
		RequestTimeout:        cfg.RequestTimeoutMS,
	}}

	c := &Client{
		account:     &api.AccountApi{Client: baseClient},
		compliance:  &api.ComplianceApi{Client: baseClient},
		transaction: &api.TransactionApi{Client: baseClient},
	}

	if (cfg.WebhookPublicKeyPath == "") != (cfg.WebhookPrivateKeyPath == "") {
		return nil, fmt.Errorf("safeheron: both WebhookPublicKeyPath and WebhookPrivateKeyPath must be set, or neither")
	}

	if cfg.WebhookPublicKeyPath != "" && cfg.WebhookPrivateKeyPath != "" {
		if err := validateKeyFile(cfg.WebhookPublicKeyPath, "WebhookPublicKeyPath", 0o644); err != nil {
			return nil, err
		}
		if err := validateKeyFile(cfg.WebhookPrivateKeyPath, "WebhookPrivateKeyPath", 0o600); err != nil {
			return nil, err
		}
		c.webhookConv = &webhook.WebhookConverter{Config: webhook.WebHookConfig{
			SafeheronWebHookRsaPublicKey: cfg.WebhookPublicKeyPath,
			WebHookRsaPrivateKey:         cfg.WebhookPrivateKeyPath,
		}}
	}

	return c, nil
}

// Close is retained as a no-op so existing call sites (signal handlers,
// container teardown, deferred cleanup) keep working after the temp-file
// machinery was removed in v1.6.
func (c *Client) Close() error {
	return nil
}

// validateKeyFile ensures path points at an existing regular file (no
// directories, no symlinks). Symlinks are rejected outright so that the
// secrets/ directory cannot be repurposed to read PEMs from arbitrary
// locations even if the directory's 0700 perms are loosened by mistake.
//
// If the permission bits are wider than recommendedPerm we log a warning but
// do not block startup — operators sometimes need looser permissions on
// shared dev boxes, and a noisy log is enough signal for production
// hardening.
func validateKeyFile(path, label string, recommendedPerm os.FileMode) error {
	if path == "" {
		return fmt.Errorf("safeheron: %s path is required", label)
	}
	// Lstat (not Stat) so symlinks are inspected directly, not followed.
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("safeheron: %s stat %q: %w", label, path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("safeheron: %s path %q is a directory", label, path)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("safeheron: %s path %q is a symlink; place the real PEM file under secrets/", label, path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("safeheron: %s path %q is not a regular file", label, path)
	}
	if actual := info.Mode().Perm(); actual&^recommendedPerm != 0 {
		log.Printf("safeheron: %s file %q permission %#o is wider than recommended %#o; tighten via chmod",
			label, path, actual, recommendedPerm)
	}
	return nil
}

func (c *Client) CreateAssetWallet(_ context.Context, customerRefID string, coinKeyList []string) (*Wallet, error) {
	if len(customerRefID) < 8 {
		return nil, fmt.Errorf("safeheron: customerRefID must be at least 8 characters, got %d", len(customerRefID))
	}
	hidden := true
	noFuel := false
	req := api.CreateAccountRequest{
		AccountName:   "DEPOSIT-" + customerRefID[:8],
		CustomerRefId: customerRefID,
		HiddenOnUI:    &hidden,
		AutoFuel:      &noFuel,
		AccountTag:    "DEPOSIT",
		CoinKeyList:   coinKeyList,
	}

	var resp api.CreateAccountResponse
	if err := c.account.CreateAccount(req, &resp); err != nil {
		return nil, fmt.Errorf("safeheron CreateAccount: %w", err)
	}

	w := &Wallet{
		AccountKey:    resp.AccountKey,
		CustomerRefID: customerRefID,
	}
	for _, ca := range resp.CoinAddressList {
		for _, addr := range ca.AddressList {
			w.CoinAddressList = append(w.CoinAddressList, CoinAddress{
				CoinKey:         ca.CoinKey,
				AddressGroupKey: ca.AddressGroupKey,
				Address:         addr.Address,
				DerivePath:      addr.DerivePath,
			})
		}
	}
	return w, nil
}

func (c *Client) AddCoin(_ context.Context, accountKey string, coinKeyList []string) (*Wallet, error) {
	req := api.AddCoinV2Request{
		AccountKey:  accountKey,
		CoinKeyList: coinKeyList,
	}
	var resp api.AddCoinV2Response
	if err := c.account.AddCoinV2(req, &resp); err != nil {
		return nil, fmt.Errorf("safeheron AddCoinV2: %w", err)
	}

	w := &Wallet{AccountKey: resp.AccountKey}
	for _, ca := range resp.CoinAddressList {
		for _, addr := range ca.AddressList {
			w.CoinAddressList = append(w.CoinAddressList, CoinAddress{
				CoinKey:         ca.CoinKey,
				AddressGroupKey: ca.AddressGroupKey,
				Address:         addr.Address,
				DerivePath:      addr.DerivePath,
			})
		}
	}
	return w, nil
}

func (c *Client) ListAccountCoin(_ context.Context, accountKey string) ([]AccountCoin, error) {
	req := api.ListAccountCoinRequest{AccountKey: accountKey}
	var resp api.AccountCoinResponse
	if err := c.account.ListAccountCoin(req, &resp); err != nil {
		return nil, fmt.Errorf("safeheron ListAccountCoin: %w", err)
	}

	coins := make([]AccountCoin, 0, len(resp))
	for _, rc := range resp {
		ac := AccountCoin{
			CoinKey: rc.CoinKey,
			Symbol:  rc.Symbol,
			Balance: rc.Balance,
		}
		for _, a := range rc.AddressList {
			ac.AddressList = append(ac.AddressList, AddressInfo{
				Address:     a.Address,
				AddressType: a.AddressType,
				DerivePath:  a.DerivePath,
				Balance:     a.AddressBalance,
			})
		}
		coins = append(coins, ac)
	}
	return coins, nil
}

func (c *Client) GetAccountByAddress(_ context.Context, address string) (*Account, error) {
	req := api.OneAccountByAddressRequest{Address: address}
	var resp api.AccountResponse
	if err := c.account.GetAccountByAddress(req, &resp); err != nil {
		return nil, fmt.Errorf("safeheron GetAccountByAddress: %w", err)
	}
	return &Account{
		AccountKey:    resp.AccountKey,
		CustomerRefID: resp.CustomerRefId,
		AccountName:   resp.AccountName,
		AccountTag:    resp.AccountTag,
		HiddenOnUI:    resp.HiddenOnUI,
		AutoFuel:      resp.AutoFuel,
	}, nil
}

func (c *Client) KytReport(_ context.Context, txKey string) (*KytReportResponse, error) {
	var sdkResp api.KytReportResponse
	if err := c.compliance.KytReport(api.KytReportRequest{TxKey: txKey}, &sdkResp); err != nil {
		return nil, fmt.Errorf("safeheron KytReport txKey=%s: %w", txKey, err)
	}
	out := &KytReportResponse{
		TxKey:                      sdkResp.TxKey,
		CustomerRefID:              sdkResp.CustomerRefId,
		AmlScreeningTriggeredState: sdkResp.AmlScreeningTriggeredState,
		AmlList:                    make([]AmlReport, 0, len(sdkResp.AmlList)),
	}
	for _, r := range sdkResp.AmlList {
		// G-2: provider Payload is interface{} — if it contains unmarshalable
		// data (channels, funcs, NaN/Inf) we'd silently store nil and lose the
		// risk evidence ops needs. Stash the error so the JSONB row carries a
		// breadcrumb instead of going dark.
		payload, err := json.Marshal(r.Payload)
		if err != nil {
			payload = fmt.Appendf(nil, `{"_marshal_error":%q}`, err.Error())
		}
		out.AmlList = append(out.AmlList, AmlReport{
			Provider:       r.Provider,
			Timestamp:      r.Timestamp,
			Status:         r.Status,
			RiskLevel:      r.RiskLevel,
			LastUpdateTime: r.LastUpdateTime,
			Payload:        payload,
		})
	}
	return out, nil
}

func (c *Client) CreateTransaction(_ context.Context, req CreateTransactionRequest) (*CreateTransactionResponse, error) {
	sdkReq := api.CreateTransactionsRequest{
		CustomerRefId:          req.CustomerRefID,
		CoinKey:                req.CoinKey,
		TxAmount:               req.TxAmount,
		TxFeeLevel:             req.TxFeeLevel,
		MaxTxFeeRate:           req.MaxTxFeeRate,
		TreatAsGrossAmount:     req.TreatAsGrossAmount,
		SourceAccountKey:       req.SourceAccountKey,
		SourceAccountType:      req.SourceAccountType,
		DestinationAccountType: req.DestinationAccountType,
		DestinationAddress:     req.DestinationAddress,
		Note:                   req.Note,
		FeeRateDto: api.FeeRateDto{
			GasLimit:       req.GasLimit,
			MaxFee:         req.MaxFee,
			MaxPriorityFee: req.MaxPriorityFee,
		},
	}
	var sdkResp api.CreateTransactionV3Response
	if err := c.transaction.CreateTransactionsV3(sdkReq, &sdkResp); err != nil {
		return nil, fmt.Errorf("safeheron CreateTransaction: %w", err)
	}
	return &CreateTransactionResponse{
		TxKey:         sdkResp.TxKey,
		CustomerRefID: sdkResp.CustomerRefId,
	}, nil
}

func (c *Client) GetTransaction(_ context.Context, txKey string) (*TransactionDetail, error) {
	var sdkResp api.OneTransactionsResponse
	if err := c.transaction.OneTransactions(api.OneTransactionsRequest{TxKey: txKey}, &sdkResp); err != nil {
		return nil, fmt.Errorf("safeheron GetTransaction txKey=%s: %w", txKey, err)
	}
	return &TransactionDetail{
		TxKey:              sdkResp.TxKey,
		TxHash:             sdkResp.TxHash,
		CoinKey:            sdkResp.CoinKey,
		TxAmount:           sdkResp.TxAmount,
		TransactionStatus:  sdkResp.TransactionStatus,
		SourceAddress:      sdkResp.SourceAddress,
		DestinationAddress: sdkResp.DestinationAddress,
	}, nil
}

func (c *Client) WebhookConvert(rawBody []byte) (*WebhookEvent, error) {
	if c.webhookConv == nil {
		return nil, fmt.Errorf("safeheron: webhook not configured (missing WebhookPublicKeyPath or WebhookPrivateKeyPath)")
	}

	var wh webhook.WebHook
	if err := json.Unmarshal(rawBody, &wh); err != nil {
		return nil, fmt.Errorf("safeheron webhook unmarshal: %w", err)
	}

	plaintext, err := c.webhookConv.Convert(wh)
	if err != nil {
		return nil, fmt.Errorf("safeheron webhook verify/decrypt: %w", err)
	}

	var evt WebhookEvent
	if err := json.Unmarshal([]byte(plaintext), &evt); err != nil {
		return nil, fmt.Errorf("safeheron webhook parse event: %w", err)
	}
	evt.RawBody = []byte(plaintext)
	return &evt, nil
}
