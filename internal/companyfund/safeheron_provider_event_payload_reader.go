package companyfund

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrSafeheronWebhookPayloadUnavailable means an immutable raw-event reference
// no longer has a readable JSONB source. It is a permanent provenance failure,
// not a reason to touch the deposit-owned process_status lifecycle.
var ErrSafeheronWebhookPayloadUnavailable = errors.New("Safeheron webhook payload is unavailable")

type safeheronWebhookPayloadQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// PostgresSafeheronWebhookPayloadReader reads only the verified JSONB payload
// retained by Safeheron's existing receiver. PostgreSQL may normalize JSONB
// text, so callers must use the delivery's stored payload digest as provenance
// and must never recompute it from these parsed source bytes.
type PostgresSafeheronWebhookPayloadReader struct {
	db safeheronWebhookPayloadQueryer
}

func NewPostgresSafeheronWebhookPayloadReader(db safeheronWebhookPayloadQueryer) *PostgresSafeheronWebhookPayloadReader {
	return &PostgresSafeheronWebhookPayloadReader{db: db}
}

const selectSafeheronWebhookPayloadSQL = `
SELECT raw_payload::text
FROM safeheron_webhook_events
WHERE id = $1`

func (reader *PostgresSafeheronWebhookPayloadReader) ReadSafeheronWebhookPayload(ctx context.Context, safeheronWebhookEventID int) ([]byte, error) {
	if safeheronWebhookEventID <= 0 {
		return nil, NewPermanentProviderEventError(fmt.Errorf("invalid Safeheron raw event reference"))
	}
	if reader == nil || reader.db == nil {
		return nil, fmt.Errorf("Safeheron webhook payload reader is not configured")
	}
	var payload []byte
	err := reader.db.QueryRowContext(ctx, selectSafeheronWebhookPayloadSQL, safeheronWebhookEventID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NewPermanentProviderEventError(ErrSafeheronWebhookPayloadUnavailable)
	}
	if err != nil {
		return nil, fmt.Errorf("read Safeheron webhook JSONB payload: %w", err)
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return nil, NewPermanentProviderEventError(fmt.Errorf("Safeheron webhook JSONB payload is invalid"))
	}
	return append([]byte(nil), payload...), nil
}

var _ SafeheronWebhookPayloadReader = (*PostgresSafeheronWebhookPayloadReader)(nil)
