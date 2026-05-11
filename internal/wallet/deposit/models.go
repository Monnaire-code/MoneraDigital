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
)

// Reason codes recorded in deposits.failed_reason for MANUAL_REVIEW / FAILED
// branches. Surfaced in alerts and operator dashboards.
const (
	ReasonAddressUnassigned = "ADDRESS_UNASSIGNED"
	ReasonCoinUnsupported   = "COIN_UNSUPPORTED"
	ReasonBelowMinAmount    = "BELOW_MIN_AMOUNT"
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
	EventType   string            `json:"eventType"`
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
// Values follow SPEC §4.6 / §6.4. Unknown statuses get 0 so they don't poison
// a partially-credited deposit row.
func StatusRank(status string) int {
	switch status {
	case "CREATED":
		return 5
	case "SUBMITTED":
		return 10
	case "BROADCASTING":
		return 20
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

// MarshalRawPayload helper for tests / fakes.
func MarshalRawPayload(env PayloadEnvelope) ([]byte, error) {
	return json.Marshal(env)
}
