package safeheron

import (
	"encoding/json"
	"fmt"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/cosigner"
)

// ---------------------------------------------------------------------------
// Business type definitions — SDK does not provide these
// ---------------------------------------------------------------------------

type CoSignerBizContentV3 struct {
	ApprovalId string          `json:"approvalId"`
	Type       string          `json:"type"`
	Detail     json.RawMessage `json:"detail"`
}

type TransactionApproval struct {
	TxKey                      string                    `json:"txKey"`
	TxHash                     string                    `json:"txHash"`
	CoinKey                    string                    `json:"coinKey"`
	TxAmount                   string                    `json:"txAmount"`
	SourceAccountKey           string                    `json:"sourceAccountKey"`
	SourceAccountType          string                    `json:"sourceAccountType"`
	SourceAddress              string                    `json:"sourceAddress"`
	SourceAddressList          []AddressEntry            `json:"sourceAddressList"`
	DestinationAccountKey      string                    `json:"destinationAccountKey"`
	DestinationAccountType     string                    `json:"destinationAccountType"`
	DestinationAddress         string                    `json:"destinationAddress"`
	Memo                       string                    `json:"memo"`
	DestinationAddressList     []DestinationAddressEntry `json:"destinationAddressList"`
	DestinationProfile         *DestinationProfile       `json:"destinationProfile"`
	TransactionType            string                    `json:"transactionType"`
	TransactionStatus          string                    `json:"transactionStatus"`
	TransactionSubStatus       string                    `json:"transactionSubStatus"`
	CreateTime                 int64                     `json:"createTime"`
	Note                       string                    `json:"note"`
	AuditUserKey               string                    `json:"auditUserKey"`
	CreatedByUserKey           string                    `json:"createdByUserKey"`
	EstimateFee                string                    `json:"estimateFee"`
	FeeCoinKey                 string                    `json:"feeCoinKey"`
	ReplaceTxHash              string                    `json:"replaceTxHash"`
	CustomerRefId              string                    `json:"customerRefId"`
	ReplacedTxKey              string                    `json:"replacedTxKey"`
	ReplacedCustomerRefId      string                    `json:"replacedCustomerRefId"`
	CustomerExt1               string                    `json:"customerExt1"`
	CustomerExt2               string                    `json:"customerExt2"`
	TransactionDirection       string                    `json:"transactionDirection"`
	AmlScreeningTriggeredState string                    `json:"amlScreeningTriggeredState"`
	AmlList                    json.RawMessage           `json:"amlList"`
}

type AddressEntry struct {
	Address string `json:"address"`
}

type DestinationAddressEntry struct {
	Address string `json:"address"`
	Memo    string `json:"memo"`
	Amount  string `json:"amount"`
}

type DestinationProfile struct {
	ConnectId string `json:"connectId"`
	Name      string `json:"name"`
}

type MPCSignApproval struct {
	TxKey                string          `json:"txKey"`
	TransactionStatus    string          `json:"transactionStatus"`
	TransactionSubStatus string          `json:"transactionSubStatus"`
	CreateTime           int64           `json:"createTime"`
	SourceAccountKey     string          `json:"sourceAccountKey"`
	CreatedByUserKey     string          `json:"createdByUserKey"`
	CustomerRefId        string          `json:"customerRefId"`
	CustomerExt1         string          `json:"customerExt1"`
	CustomerExt2         string          `json:"customerExt2"`
	SignAlg              string          `json:"signAlg"`
	DataList             json.RawMessage `json:"dataList"`
}

type Web3Approval struct {
	TxKey                string          `json:"txKey"`
	SubjectType          string          `json:"subjectType"`
	AccountKey           string          `json:"accountKey"`
	SourceAddress        string          `json:"sourceAddress"`
	TransactionStatus    string          `json:"transactionStatus"`
	TransactionSubStatus string          `json:"transactionSubStatus"`
	CreatedByUserKey     string          `json:"createdByUserKey"`
	CreateTime           int64           `json:"createTime"`
	AuditUserKey         string          `json:"auditUserKey"`
	CustomerRefId        string          `json:"customerRefId"`
	CustomerExt1         string          `json:"customerExt1"`
	CustomerExt2         string          `json:"customerExt2"`
	Note                 string          `json:"note"`
	Transaction          json.RawMessage `json:"transaction"`
	Message              json.RawMessage `json:"message"`
	MessageHash          json.RawMessage `json:"messageHash"`
}

// ---------------------------------------------------------------------------
// CosignerClient — wraps SDK CoSignerConverter
// ---------------------------------------------------------------------------

type CosignerConfig struct {
	CoSignerPubKeyPath     string
	CallbackPrivateKeyPath string
}

type cosignerConverter interface {
	RequestV3Convert(cosigner.CoSignerCallBackV3) (string, error)
	ResponseV3Converter(any) (map[string]string, error)
}

type CosignerClient struct {
	converter cosignerConverter
}

func NewCosignerClient(cfg CosignerConfig) (*CosignerClient, error) {
	if err := validateKeyFile(cfg.CoSignerPubKeyPath, "CoSignerPubKey", 0o644); err != nil {
		return nil, err
	}
	if err := validateKeyFile(cfg.CallbackPrivateKeyPath, "CallbackPrivateKey", 0o600); err != nil {
		return nil, err
	}
	conv := &cosigner.CoSignerConverter{Config: cosigner.CoSignerConfig{
		CoSignerPubKey:                    cfg.CoSignerPubKeyPath,
		ApprovalCallbackServicePrivateKey: cfg.CallbackPrivateKeyPath,
	}}
	return &CosignerClient{converter: conv}, nil
}

func (c *CosignerClient) ParseRequest(req cosigner.CoSignerCallBackV3) (*CoSignerBizContentV3, error) {
	plaintext, err := c.converter.RequestV3Convert(req)
	if err != nil {
		return nil, fmt.Errorf("cosigner verify/decode: %w", err)
	}
	var biz CoSignerBizContentV3
	if err := json.Unmarshal([]byte(plaintext), &biz); err != nil {
		return nil, fmt.Errorf("cosigner unmarshal bizContent: %w", err)
	}
	return &biz, nil
}

func (c *CosignerClient) BuildResponse(approvalId, action string) (map[string]string, error) {
	resp := cosigner.CoSignerResponseV3{
		ApprovalId: approvalId,
		Action:     action,
	}
	result, err := c.converter.ResponseV3Converter(resp)
	if err != nil {
		return nil, fmt.Errorf("cosigner sign response: %w", err)
	}
	return result, nil
}
