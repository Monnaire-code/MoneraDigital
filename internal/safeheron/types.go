package safeheron

import "encoding/json"

// Coin is the project-local mirror of immutable metadata returned by
// Safeheron's /v1/coin/list endpoint.
type Coin struct {
	CoinKey         string
	CoinName        string
	Symbol          string
	CoinDecimal     int32
	FeeCoinKey      string
	BlockChain      string
	BlockchainType  string
	Network         string
	TokenIdentifier string
}

// KytReportResponse mirrors SDK api.KytReportResponse — 项目内类型，
// 业务层只 import internal/safeheron，不直接依赖 SDK。
type KytReportResponse struct {
	TxKey                      string      `json:"txKey"`
	CustomerRefID              string      `json:"customerRefId"`
	AmlScreeningTriggeredState string      `json:"amlScreeningTriggeredState"` // IN_PROGRESS / TRIGGERED / UNTRIGGERED
	AmlList                    []AmlReport `json:"amlList"`
}

// AmlReport 是单个 KYT provider 的筛查结果。Phase 1 只接 MistTrack 一家。
type AmlReport struct {
	Provider       string          `json:"provider"`       // MistTrack / Chainalysis / Elliptic
	Timestamp      string          `json:"timestamp"`      // UNIX 毫秒
	Status         string          `json:"status"`         // PENDING / COMPLETED / SKIPPED / FAILED
	RiskLevel      string          `json:"riskLevel"`      // LOW / MEDIUM / HIGH / SEVERE / UNKNOWN
	LastUpdateTime string          `json:"lastUpdateTime"` // UNIX 毫秒
	Payload        json.RawMessage `json:"payload"`        // 各 provider 详细数据，业务层仅存档到 deposits.aml_list JSONB
}

type Wallet struct {
	AccountKey      string
	CustomerRefID   string
	CoinAddressList []CoinAddress
}

type CoinAddress struct {
	CoinKey         string
	AddressGroupKey string
	Address         string
	DerivePath      string
}

type AccountCoin struct {
	CoinKey     string
	Symbol      string
	Balance     string
	AddressList []AddressInfo
}

type AddressInfo struct {
	Address     string
	AddressType string
	DerivePath  string
	Balance     string
}

type Account struct {
	AccountKey    string
	CustomerRefID string
	AccountName   string
	AccountTag    string
	HiddenOnUI    bool
	AutoFuel      bool
}

type CreateTransactionRequest struct {
	CustomerRefID          string
	CoinKey                string
	TxAmount               string
	TxFeeLevel             string // LOW / MIDDLE / HIGH（留空走默认）
	MaxTxFeeRate           string // gas 费上限（Gwei），可选
	TreatAsGrossAmount     bool   // true: fee 从 amount 里扣，false: fee 额外扣
	GasLimit               string // EIP-1559: gas limit
	MaxFee                 string // EIP-1559: maxFeePerGas (Gwei)
	MaxPriorityFee         string // EIP-1559: maxPriorityFeePerGas (Gwei)
	SourceAccountKey       string
	SourceAccountType      string // VAULT_ACCOUNT
	DestinationAccountType string // ONE_TIME_ADDRESS
	DestinationAddress     string
	Note                   string
}

type CreateTransactionResponse struct {
	TxKey         string
	CustomerRefID string
}

type TransactionDetail struct {
	TxKey              string
	TxHash             string
	CoinKey            string
	TxAmount           string
	TransactionStatus  string
	SourceAddress      string
	DestinationAddress string
}

type WebhookEvent struct {
	EventType   string      `json:"eventType"`
	EventDetail EventDetail `json:"eventDetail"`
	// RawBody holds the full decrypted Safeheron plaintext. Not serialized to
	// JSON — populated by WebhookConvert so callers can store the lossless
	// payload (AML_KYT_ALERT eventDetail fields are not in EventDetail).
	RawBody []byte `json:"-"`
}

type EventDetail struct {
	TxKey                string `json:"txKey"`
	CoinKey              string `json:"coinKey"`
	TxAmount             string `json:"txAmount"`
	TransactionStatus    string `json:"transactionStatus"`
	TransactionSubStatus string `json:"transactionSubStatus"`
	SourceAddress        string `json:"sourceAddress"`
	DestinationAddress   string `json:"destinationAddress"`
	CustomerRefID        string `json:"customerRefId"`
	BlockHeight          int64  `json:"blockHeight"`
	BlockHash            string `json:"blockHash"`
	TxHash               string `json:"txHash"`
	TransactionDirection string `json:"transactionDirection"`
}
