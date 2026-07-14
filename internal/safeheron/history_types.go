package safeheron

import (
	"context"
	"encoding/json"
)

// TransactionHistoryClient provides the provider-history operations needed by
// reconciliation without exposing Safeheron's SDK types to callers.
type TransactionHistoryClient interface {
	ListTransactions(ctx context.Context, req TransactionHistoryRequest) ([]TransactionSnapshot, error)
	LookupTransaction(ctx context.Context, lookup TransactionLookup) (*TransactionSnapshot, error)
}

// WebhookReplayClient asks Safeheron to redeliver Webhook events. It only
// triggers delivery; callers must let normal webhook ingestion handle the data.
type WebhookReplayClient interface {
	ReplayTransactionWebhook(ctx context.Context, txKey string) (bool, error)
	ReplayFailedWebhooks(ctx context.Context, window WebhookReplayWindow) (int32, error)
}

// TransactionHistoryRequest is the provider-neutral subset of a transaction
// history query. Cursor is opaque to callers; Safeheron uses the last txKey.
// Small-amount and amount-range filters are intentionally absent so a treasury
// reconciliation cannot hide dust or precision-sensitive movements.
type TransactionHistoryRequest struct {
	Limit                      int32
	Cursor                     string
	AccountKey                 string
	SourceAccountKey           string
	SourceAccountType          string
	DestinationAccountKey      string
	DestinationAccountType     string
	CreateTimeMin              int64
	CreateTimeMax              int64
	CompletedTimeMin           int64
	CompletedTimeMax           int64
	CoinKey                    string
	FeeCoinKey                 string
	TransactionStatus          string
	TransactionSubStatus       string
	CustomerRefID              string
	RealDestinationAccountType string
	TransactionDirection       string
}

// TransactionLookup identifies one already-known provider transaction. Exactly
// one identifier must be present; it is not a tx-hash lookup API.
type TransactionLookup struct {
	TxKey         string
	CustomerRefID string
}

// WebhookReplayWindow uses Unix milliseconds, matching Safeheron's request
// contract. The caller owns the provider's ten-minute replay rate limit.
type WebhookReplayWindow struct {
	StartTime int64
	EndTime   int64
}

// TransactionSnapshot is a provider-neutral representation of a transaction
// history record. Monetary values intentionally remain strings. RawPayload is
// deterministic JSON reconstructed from the pinned SDK response for encrypted
// provider-owned retention; it is not serialized with this value.
type TransactionSnapshot struct {
	TxKey                      string                          `json:"txKey"`
	TxHash                     string                          `json:"txHash"`
	CoinKey                    string                          `json:"coinKey"`
	TxAmount                   string                          `json:"txAmount"`
	SourceAccountKey           string                          `json:"sourceAccountKey"`
	SourceAccountType          string                          `json:"sourceAccountType"`
	SourceAddress              string                          `json:"sourceAddress"`
	IsSourcePhishing           bool                            `json:"isSourcePhishing"`
	SourceAddressList          []TransactionSourceAddress      `json:"sourceAddressList"`
	DestinationAccountKey      string                          `json:"destinationAccountKey"`
	DestinationAccountType     string                          `json:"destinationAccountType"`
	DestinationAddress         string                          `json:"destinationAddress"`
	IsDestinationPhishing      bool                            `json:"isDestinationPhishing"`
	DestinationAddressList     []TransactionDestinationAddress `json:"destinationAddressList"`
	Memo                       string                          `json:"memo"`
	DestinationTag             string                          `json:"destinationTag"`
	TransactionType            string                          `json:"transactionType"`
	TransactionDirection       string                          `json:"transactionDirection"`
	TransactionStatus          string                          `json:"transactionStatus"`
	TransactionSubStatus       string                          `json:"transactionSubStatus"`
	CreateTime                 int64                           `json:"createTime"`
	CompletedTime              int64                           `json:"completedTime"`
	TxFee                      string                          `json:"txFee"`
	FeeCoinKey                 string                          `json:"feeCoinKey"`
	GasFee                     []TransactionGasFee             `json:"gasFee"`
	BlockHeight                int64                           `json:"blockHeight"`
	TxAmountToUSD              string                          `json:"txAmountToUsd"`
	CustomerRefID              string                          `json:"customerRefId"`
	CustomerExt1               string                          `json:"customerExt1"`
	CustomerExt2               string                          `json:"customerExt2"`
	AmlLock                    string                          `json:"amlLock"`
	AMLScreeningTriggeredState string                          `json:"amlScreeningTriggeredState"`
	AMLList                    []TransactionAMLRecord          `json:"amlList"`
	RawPayload                 json.RawMessage                 `json:"-"`
}

type TransactionSourceAddress struct {
	Address          string `json:"address"`
	IsSourcePhishing bool   `json:"isSourcePhishing"`
	AddressGroupKey  string `json:"addressGroupKey"`
}

type TransactionDestinationAddress struct {
	Address               string `json:"address"`
	IsDestinationPhishing bool   `json:"isDestinationPhishing"`
	Memo                  string `json:"memo"`
	Amount                string `json:"amount"`
	AddressGroupKey       string `json:"addressGroupKey"`
}

type TransactionGasFee struct {
	Symbol string `json:"symbol"`
	Amount string `json:"amount"`
}

type TransactionAMLRecord struct {
	Provider       string `json:"provider"`
	Timestamp      string `json:"timestamp"`
	Status         string `json:"status"`
	RiskLevel      string `json:"riskLevel"`
	LastUpdateTime string `json:"lastUpdateTime"`
}
