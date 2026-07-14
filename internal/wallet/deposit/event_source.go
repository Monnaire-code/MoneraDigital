package deposit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const selectEventSourceSQL = `
SELECT id, payload_digest
FROM safeheron_webhook_events
WHERE event_id = $1`

const selectEventPayloadDigestSQL = `
SELECT payload_digest
FROM safeheron_webhook_events
WHERE event_id = $1`

// LookupEventSource returns only the immutable raw-event reference required by
// a secondary ledger bridge. It neither reads nor changes process_status, so
// the deposit worker remains the sole owner of that lifecycle.
func (r *DBRepository) LookupEventSource(ctx context.Context, eventID string) (EventSource, error) {
	if r == nil || r.db == nil {
		return EventSource{}, fmt.Errorf("safeheron event source repository is not configured")
	}
	var source EventSource
	var digest sql.NullString
	if err := r.db.QueryRowContext(ctx, selectEventSourceSQL, eventID).Scan(&source.ID, &digest); err != nil {
		if err == sql.ErrNoRows {
			return EventSource{}, fmt.Errorf("safeheron event source does not exist")
		}
		return EventSource{}, fmt.Errorf("read safeheron event source: %w", err)
	}
	if source.ID <= 0 || !digest.Valid || strings.TrimSpace(digest.String) == "" {
		return EventSource{}, fmt.Errorf("safeheron event source payload digest is unavailable")
	}
	source.PayloadDigest = digest.String
	return source, nil
}

// validateExistingEventPayloadDigest protects the raw-byte digest invariant on
// duplicate event IDs. Legacy JSONB rows without a digest cannot establish a
// SHA-256 over the original decrypted bytes, so they remain explicitly
// unverifiable rather than being retroactively stamped from a retry.
func (r *DBRepository) validateExistingEventPayloadDigest(ctx context.Context, eventID, payloadDigest string) (bool, error) {
	storedDigest, found, err := r.readEventPayloadDigest(ctx, eventID)
	if err != nil {
		return false, err
	}
	if !found || !storedDigest.Valid || strings.TrimSpace(storedDigest.String) == "" {
		return false, ErrEventPayloadDigestUnavailable
	}
	if storedDigest.String != payloadDigest {
		return false, ErrEventPayloadDigestMismatch
	}
	return false, nil
}

func (r *DBRepository) readEventPayloadDigest(ctx context.Context, eventID string) (sql.NullString, bool, error) {
	var digest sql.NullString
	err := r.db.QueryRowContext(ctx, selectEventPayloadDigestSQL, eventID).Scan(&digest)
	if err == sql.ErrNoRows {
		return sql.NullString{}, false, nil
	}
	if err != nil {
		return sql.NullString{}, false, fmt.Errorf("read safeheron event payload digest: %w", err)
	}
	return digest, true, nil
}
