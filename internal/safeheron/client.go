package safeheron

import (
	"context"
	"encoding/json"
	"fmt"
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

type Config struct {
	BaseURL              string
	APIKey               string
	PrivateKeyPEM        string
	PlatformPublicKeyPEM string
	WebhookPublicKeyPEM  string
	WebhookPrivateKeyPEM string
	RequestTimeoutMS     int64
}

type Client struct {
	account     accountAPIClient
	compliance  complianceAPIClient
	transaction transactionAPIClient
	webhookConv webhookConverter
	tempFiles   []string
	sdkClient   sdk.Client
}

func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("safeheron: APIKey is required")
	}
	if cfg.PrivateKeyPEM == "" {
		return nil, fmt.Errorf("safeheron: PrivateKeyPEM is required")
	}
	if cfg.PlatformPublicKeyPEM == "" {
		return nil, fmt.Errorf("safeheron: PlatformPublicKeyPEM is required")
	}

	var tempFiles []string

	privPath, err := writeTempPEM("private", cfg.PrivateKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("safeheron: write private key: %w", err)
	}
	tempFiles = append(tempFiles, privPath)

	platPath, err := writeTempPEM("platform", cfg.PlatformPublicKeyPEM)
	if err != nil {
		cleanupFiles(tempFiles)
		return nil, fmt.Errorf("safeheron: write platform public key: %w", err)
	}
	tempFiles = append(tempFiles, platPath)

	baseClient := sdk.Client{Config: sdk.ApiConfig{
		BaseUrl:               cfg.BaseURL,
		ApiKey:                cfg.APIKey,
		RsaPrivateKey:         privPath,
		SafeheronRsaPublicKey: platPath,
		RequestTimeout:        cfg.RequestTimeoutMS,
	}}

	sdkAccount := &api.AccountApi{Client: baseClient}
	sdkCompliance := &api.ComplianceApi{Client: baseClient}
	sdkTransaction := &api.TransactionApi{Client: baseClient}

	c := &Client{
		account:     sdkAccount,
		compliance:  sdkCompliance,
		transaction: sdkTransaction,
		tempFiles:   tempFiles,
		sdkClient:   baseClient,
	}

	if (cfg.WebhookPublicKeyPEM == "") != (cfg.WebhookPrivateKeyPEM == "") {
		c.Close()
		return nil, fmt.Errorf("safeheron: both WebhookPublicKeyPEM and WebhookPrivateKeyPEM must be set, or neither")
	}

	if cfg.WebhookPublicKeyPEM != "" && cfg.WebhookPrivateKeyPEM != "" {
		whPubPath, err := writeTempPEM("whpub", cfg.WebhookPublicKeyPEM)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("safeheron: write webhook public key: %w", err)
		}
		c.tempFiles = append(c.tempFiles, whPubPath)

		whPrivPath, err := writeTempPEM("whpriv", cfg.WebhookPrivateKeyPEM)
		if err != nil {
			c.Close()
			return nil, fmt.Errorf("safeheron: write webhook private key: %w", err)
		}
		c.tempFiles = append(c.tempFiles, whPrivPath)

		conv := &webhook.WebhookConverter{Config: webhook.WebHookConfig{
			SafeheronWebHookRsaPublicKey: whPubPath,
			WebHookRsaPrivateKey:         whPrivPath,
		}}
		c.webhookConv = conv
	}

	return c, nil
}

func (c *Client) Close() error {
	cleanupFiles(c.tempFiles)
	c.tempFiles = nil
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
		payload, _ := json.Marshal(r.Payload)
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
		return nil, fmt.Errorf("safeheron: webhook not configured (missing WebhookPublicKeyPEM or WebhookPrivateKeyPEM)")
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
	return &evt, nil
}

func writeTempPEM(name, content string) (string, error) {
	prefix := fmt.Sprintf("safeheron-%s-%d-", name, os.Getpid())
	f, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", err
	}
	path := f.Name()

	if err := f.Chmod(0600); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	f.Close()
	return path, nil
}

func cleanupFiles(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}
