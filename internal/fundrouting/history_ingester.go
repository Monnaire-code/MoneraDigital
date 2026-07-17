package fundrouting

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"monera-digital/internal/companyfund"
)

type HistoryInboxIngester struct {
	db *sql.DB
}

func NewHistoryInboxIngester(db *sql.DB) (*HistoryInboxIngester, error) {
	if db == nil {
		return nil, fmt.Errorf("Safeheron history routing inbox database is required")
	}
	return &HistoryInboxIngester{db: db}, nil
}

func (i *HistoryInboxIngester) Ingest(ctx context.Context, input companyfund.OwnedProviderPayloadInput) (companyfund.ProviderEventInsertResult, error) {
	if input.Channel != companyfund.ChannelSafeheron || input.EventType != companyfund.SafeheronTransactionHistorySnapshotEventType {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("history routing inbox accepts only canonical Safeheron history snapshots")
	}
	if strings.TrimSpace(input.ProviderEventID) == "" || len(input.Body) == 0 || !json.Valid(input.Body) {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("Safeheron history routing snapshot identity or body is invalid")
	}
	var identity struct {
		TxKey string `json:"txKey"`
	}
	if err := json.Unmarshal(input.Body, &identity); err != nil || strings.TrimSpace(identity.TxKey) == "" {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("Safeheron history routing snapshot txKey is invalid")
	}
	envelope, err := json.Marshal(struct {
		EventType   string          `json:"eventType"`
		EventDetail json.RawMessage `json:"eventDetail"`
	}{EventType: "TRANSACTION_STATUS_CHANGED", EventDetail: json.RawMessage(input.Body)})
	if err != nil {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("encode Safeheron history routing envelope: %w", err)
	}
	sum := sha256.Sum256(envelope)
	digest := hex.EncodeToString(sum[:])
	var eventID int64
	err = i.db.QueryRowContext(ctx, `INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'PENDING')
ON CONFLICT (event_id) DO NOTHING RETURNING id`, input.ProviderEventID, strings.TrimSpace(identity.TxKey), envelope, digest).Scan(&eventID)
	if err == nil {
		return companyfund.ProviderEventInsertResult{ID: eventID, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("insert Safeheron history routing event: %w", err)
	}
	var existingDigest string
	if err := i.db.QueryRowContext(ctx, `SELECT id,payload_digest FROM safeheron_webhook_events WHERE event_id=$1`, input.ProviderEventID).Scan(&eventID, &existingDigest); err != nil {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("read existing Safeheron history routing event: %w", err)
	}
	if existingDigest != digest {
		return companyfund.ProviderEventInsertResult{}, fmt.Errorf("Safeheron history routing event identity conflicts with another payload")
	}
	return companyfund.ProviderEventInsertResult{ID: eventID, Inserted: false}, nil
}

var _ companyfund.SafeheronHistoryOwnedProviderEventIngestor = (*HistoryInboxIngester)(nil)
