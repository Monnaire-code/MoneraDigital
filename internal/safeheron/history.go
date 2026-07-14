package safeheron

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
)

const (
	maxTransactionHistoryLimit = int32(500)
	maxFailedWebhookReplayAge  = 7 * 24 * time.Hour
	maxFailedWebhookReplayGap  = time.Hour
)

var (
	_ TransactionHistoryClient = (*Client)(nil)
	_ WebhookReplayClient      = (*Client)(nil)
)

// ListTransactions retrieves one cursor page from Safeheron's V2 transaction
// history API. It always uses NEXT pagination and intentionally never enables
// the provider small-amount filter, so dust remains available to finance risk
// policy rather than disappearing at the transport boundary.
func (c *Client) ListTransactions(ctx context.Context, req TransactionHistoryRequest) ([]TransactionSnapshot, error) {
	if req.Limit < 1 || req.Limit > maxTransactionHistoryLimit {
		return nil, fmt.Errorf("safeheron transaction history limit must be between 1 and %d", maxTransactionHistoryLimit)
	}
	if c == nil || c.transaction == nil {
		return nil, fmt.Errorf("safeheron transaction history API is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sdkReq := api.ListTransactionsV2Request{
		Direct:                     "NEXT",
		Limit:                      req.Limit,
		FromId:                     req.Cursor,
		SourceAccountKey:           req.SourceAccountKey,
		SourceAccountType:          req.SourceAccountType,
		DestinationAccountKey:      req.DestinationAccountKey,
		DestinationAccountType:     req.DestinationAccountType,
		AccountKey:                 req.AccountKey,
		CreateTimeMin:              req.CreateTimeMin,
		CreateTimeMax:              req.CreateTimeMax,
		CoinKey:                    req.CoinKey,
		FeeCoinKey:                 req.FeeCoinKey,
		TransactionStatus:          req.TransactionStatus,
		TransactionSubStatus:       req.TransactionSubStatus,
		CompletedTimeMin:           req.CompletedTimeMin,
		CompletedTimeMax:           req.CompletedTimeMax,
		CustomerRefId:              req.CustomerRefID,
		RealDestinationAccountType: req.RealDestinationAccountType,
		TransactionDirection:       req.TransactionDirection,
	}
	type listTransactionsResult struct {
		snapshots []TransactionSnapshot
		err       error
	}
	// The upstream SDK exposes only a blocking method. A buffered channel lets
	// this caller return promptly on runtime shutdown while the SDK request
	// finishes in its own bounded client timeout; the goroutine never logs or
	// shares the response payload after it sends its local result.
	result := make(chan listTransactionsResult, 1)
	transactionAPI := c.transaction
	go func() {
		var sdkResp api.TransactionsResponseV2
		if err := transactionAPI.ListTransactionsV2(sdkReq, &sdkResp); err != nil {
			result <- listTransactionsResult{err: fmt.Errorf("safeheron ListTransactionsV2: %w", err)}
			return
		}
		snapshots := make([]TransactionSnapshot, 0, len(sdkResp))
		for _, transaction := range sdkResp {
			snapshot, err := canonicalTransactionSnapshot(transaction)
			if err != nil {
				result <- listTransactionsResult{err: fmt.Errorf("safeheron ListTransactionsV2 canonical snapshot: %w", err)}
				return
			}
			snapshots = append(snapshots, snapshot)
		}
		result <- listTransactionsResult{snapshots: snapshots}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case completed := <-result:
		return completed.snapshots, completed.err
	}
}

// LookupTransaction gets an already-known Safeheron transaction by txKey or
// customerRefId. It cannot discover unknown chain transactions.
func (c *Client) LookupTransaction(_ context.Context, lookup TransactionLookup) (*TransactionSnapshot, error) {
	request, err := lookup.sdkRequest()
	if err != nil {
		return nil, err
	}
	if c == nil || c.transaction == nil {
		return nil, fmt.Errorf("safeheron transaction history API is not configured")
	}

	var sdkResp api.OneTransactionsResponse
	if err := c.transaction.OneTransactions(request, &sdkResp); err != nil {
		return nil, fmt.Errorf("safeheron LookupTransaction: %w", err)
	}
	snapshot, err := canonicalTransactionSnapshot(sdkResp)
	if err != nil {
		return nil, fmt.Errorf("safeheron LookupTransaction canonical snapshot: %w", err)
	}
	return &snapshot, nil
}

// ReplayTransactionWebhook asks Safeheron to redeliver the most recent event
// for one known transaction. It does not fetch or persist transaction data.
func (c *Client) ReplayTransactionWebhook(_ context.Context, txKey string) (bool, error) {
	txKey = strings.TrimSpace(txKey)
	if txKey == "" {
		return false, fmt.Errorf("safeheron replay txKey is required")
	}
	if c == nil || c.webhookReplay == nil {
		return false, fmt.Errorf("safeheron webhook replay API is not configured")
	}

	var sdkResp api.ResultResponse
	if err := c.webhookReplay.ResendWebhook(api.ResendWebhookRequest{
		Category: "TRANSACTION",
		TxKey:    txKey,
	}, &sdkResp); err != nil {
		return false, fmt.Errorf("safeheron ResendWebhook: %w", err)
	}
	return sdkResp.Result, nil
}

// ReplayFailedWebhooks asks Safeheron to redeliver failed webhook deliveries.
// Safeheron permits a maximum one-hour window from the past seven days. The
// provider's one-call-per-ten-minutes rate limit remains a durable worker
// responsibility and is intentionally not tracked in this client.
func (c *Client) ReplayFailedWebhooks(_ context.Context, window WebhookReplayWindow) (int32, error) {
	if err := validateFailedWebhookReplayWindow(window, time.Now().UnixMilli()); err != nil {
		return 0, err
	}
	if c == nil || c.webhookReplay == nil {
		return 0, fmt.Errorf("safeheron webhook replay API is not configured")
	}

	var sdkResp api.MessagesCountResponse
	if err := c.webhookReplay.ResendFailed(api.ResendFailedRequest{
		StartTime: window.StartTime,
		EndTime:   window.EndTime,
	}, &sdkResp); err != nil {
		return 0, fmt.Errorf("safeheron ResendFailed: %w", err)
	}
	return sdkResp.MessagesCount, nil
}

func (lookup TransactionLookup) sdkRequest() (api.OneTransactionsRequest, error) {
	txKey := strings.TrimSpace(lookup.TxKey)
	customerRefID := strings.TrimSpace(lookup.CustomerRefID)
	if (txKey == "") == (customerRefID == "") {
		return api.OneTransactionsRequest{}, fmt.Errorf("safeheron transaction lookup requires exactly one of txKey or customerRefId")
	}
	return api.OneTransactionsRequest{TxKey: txKey, CustomerRefId: customerRefID}, nil
}

func validateFailedWebhookReplayWindow(window WebhookReplayWindow, nowMillis int64) error {
	if window.StartTime <= 0 || window.EndTime <= 0 {
		return fmt.Errorf("safeheron failed webhook replay timestamps must be positive Unix milliseconds")
	}
	if window.EndTime <= window.StartTime {
		return fmt.Errorf("safeheron failed webhook replay end time must be after start time")
	}
	if window.EndTime-window.StartTime > maxFailedWebhookReplayGap.Milliseconds() {
		return fmt.Errorf("safeheron failed webhook replay window must not exceed one hour")
	}
	if window.StartTime < nowMillis-maxFailedWebhookReplayAge.Milliseconds() {
		return fmt.Errorf("safeheron failed webhook replay window must be within the past seven days")
	}
	if window.EndTime > nowMillis {
		return fmt.Errorf("safeheron failed webhook replay end time must not be in the future")
	}
	return nil
}

func canonicalTransactionSnapshot(source any) (TransactionSnapshot, error) {
	rawPayload, err := json.Marshal(source)
	if err != nil {
		return TransactionSnapshot{}, err
	}

	var snapshot TransactionSnapshot
	if err := json.Unmarshal(rawPayload, &snapshot); err != nil {
		return TransactionSnapshot{}, err
	}
	snapshot.RawPayload = rawPayload
	return snapshot, nil
}
