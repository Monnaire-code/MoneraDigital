package safeheron

import "encoding/json"

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

type WebhookEvent struct {
	EventType   string      `json:"eventType"`
	EventDetail EventDetail `json:"eventDetail"`
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
