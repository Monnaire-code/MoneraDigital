package deposit

import (
	"encoding/json"
	"errors"
)

// Process statuses for safeheron_webhook_events.process_status.
const (
	ProcessPending = "PENDING"
	ProcessDone    = "DONE"
	ProcessError   = "ERROR"
)

// deposits.status values constrained by ck_deposits_status (migration 020).
const (
	DepositStatusPending        = "PENDING"
	DepositStatusChainVerifying = "CHAIN_VERIFYING"
	DepositStatusChainVerified  = "CHAIN_VERIFIED"
	DepositStatusCredited       = "CREDITED"
	DepositStatusFailed         = "FAILED"
	DepositStatusManualReview   = "MANUAL_REVIEW"
	DepositStatusKYTPending     = "KYT_PENDING" // SPEC §7.1: 链上 COMPLETED 但 KYT 评估未结束
)

// Reason codes recorded in deposits.failed_reason for MANUAL_REVIEW / FAILED
// branches. Surfaced in alerts and operator dashboards.
const (
	ReasonAddressUnassigned = "ADDRESS_UNASSIGNED"
	ReasonCoinUnsupported   = "COIN_UNSUPPORTED"
	ReasonBelowMinAmount    = "BELOW_MIN_AMOUNT"
	// ReasonInvalidCoinConfig surfaces when coin_chains.min_deposit_amount fails
	// to parse — typically an operator typo. Routing such events to MANUAL_REVIEW
	// prevents silently accepting deposits against a broken config. T7-S-4.
	ReasonInvalidCoinConfig = "INVALID_COIN_CONFIG"
)

// JournalBizTypeDeposit identifies deposit ledger rows in account_journal.
const JournalBizTypeDeposit = 10

// Event mirrors a safeheron_webhook_events row.
type Event struct {
	ID              int64
	EventID         string
	EventType       string
	SafeheronTxKey  string
	CustomerRefID   string
	RawPayload      []byte
	ProcessStatus   string
	ProcessAttempts int
	ErrorMessage    string
}

// PayloadEnvelope is the decrypted webhook business envelope (eventType +
// eventDetail). The Safeheron SDK returns this layer to us after verifying the
// signature and AES-GCM decrypting the outer envelope.
type PayloadEnvelope struct {
	EventType   string             `json:"eventType"`
	EventDetail PayloadEventDetail `json:"eventDetail"`
}

// PayloadEventDetail captures the subset of eventDetail fields Phase 1 uses.
// Additional Safeheron fields (replaceTxHash, destinationAddressList, ...) are
// preserved in the raw_payload column for forensic replay.
type PayloadEventDetail struct {
	TxKey                string `json:"txKey"`
	TxHash               string `json:"txHash"`
	CoinKey              string `json:"coinKey"`
	TxAmount             string `json:"txAmount"`
	TransactionStatus    string `json:"transactionStatus"`
	TransactionSubStatus string `json:"transactionSubStatus"`
	SourceAddress        string `json:"sourceAddress"`
	DestinationAddress   string `json:"destinationAddress"`
	CustomerRefID        string `json:"customerRefId"`
	BlockHeight          int64  `json:"blockHeight"`
	BlockHash            string `json:"blockHash"`
	TransactionDirection string `json:"transactionDirection"`
}

// StatusRank gives Safeheron transactionStatus a monotonic ordering used to
// guard against out-of-order webhooks (e.g. COMPLETED arriving before
// CONFIRMING). Higher = later in the lifecycle.
//
// Values locked by plan §3.1 / SPEC §4.6:
//
//	SUBMITTED=10, SIGNING=20, BROADCASTING=30, CONFIRMING=50,
//	FAILED/CANCELLED/REJECTED=90, COMPLETED=100
//
// Unknown statuses get 0 so they don't poison a partially-credited row.
func StatusRank(status string) int {
	switch status {
	case "SUBMITTED":
		return 10
	case "SIGNING":
		return 20
	case "BROADCASTING":
		return 30
	case "CONFIRMING":
		return 50
	case "FAILED", "CANCELLED", "REJECTED":
		return 90
	case "COMPLETED":
		return 100
	default:
		return 0
	}
}

// ErrNoPending signals that the worker found no PENDING event to process — the
// happy-path "sleep until next tick" outcome.
var ErrNoPending = errors.New("no pending event")

// ErrMarkErrorFailed signals that processing failed AND the subsequent
// MarkEventError call also failed, so the event remains PENDING. The worker
// detects this with errors.Is and yields to its ticker interval to avoid
// hot-looping on the same un-markable row. T7-I-5.
var ErrMarkErrorFailed = errors.New("mark event error failed; event remains pending")

// ErrKYTAPIBackoff signals that a KYT API call failed but the event is below
// the orphan retry threshold (event stays PENDING for the next cycle). The
// worker yields to its ticker so we don't burn Safeheron quota in a tight loop
// during an upstream outage. T10-I-3.
var ErrKYTAPIBackoff = errors.New("KYT API failed; backing off until next tick")

// MarshalRawPayload helper for tests / fakes.
func MarshalRawPayload(env PayloadEnvelope) ([]byte, error) {
	return json.Marshal(env)
}

// AMLKYTAlertDetail 是 AML_KYT_ALERT webhook eventDetail 的独立 struct。
// 字段集与 PayloadEventDetail 不同（无 transactionStatus，多 amlList），需二次 unmarshal。
type AMLKYTAlertDetail struct {
	TxKey                      string              `json:"txKey"`
	CustomerRefID              string              `json:"customerRefId"`
	AmlScreeningTriggeredState string              `json:"amlScreeningTriggeredState"`
	AmlList                    []AMLKYTAlertReport `json:"amlList"`
}

// AMLKYTAlertReport 是 AML_KYT_ALERT 内嵌的单条 provider 报告。
// 字段与 safeheron.AmlReport 对齐，但从 webhook payload 解析而非 API 返回。
type AMLKYTAlertReport struct {
	Provider       string          `json:"provider"`
	Timestamp      string          `json:"timestamp"`
	Status         string          `json:"status"`
	RiskLevel      string          `json:"riskLevel"`
	LastUpdateTime string          `json:"lastUpdateTime"`
	Payload        json.RawMessage `json:"payload"`
}
