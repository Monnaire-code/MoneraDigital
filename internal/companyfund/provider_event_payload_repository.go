package companyfund

import (
	"context"
	"fmt"
)

const purgeExpiredOwnedProviderPayloadsSQL = `
WITH purge_candidates AS (
	SELECT id
	FROM company_fund_provider_events
	WHERE source_kind = 'OWNED_ENCRYPTED_PAYLOAD'
		AND owned_payload_ciphertext IS NOT NULL
		AND owned_payload_purged_at IS NULL
		AND owned_payload_legal_hold = false
		AND owned_payload_retention_until <= NOW()
	ORDER BY owned_payload_retention_until, id
	FOR UPDATE SKIP LOCKED
	LIMIT $1
)
UPDATE company_fund_provider_events AS provider_event
SET owned_payload_ciphertext = NULL,
	owned_payload_purged_at = NOW(),
	event_state = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN 'DEAD_LETTER'
		ELSE provider_event.event_state
	END,
	processed_at = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN NOW()
		ELSE provider_event.processed_at
	END,
	next_attempt_at = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN NULL
		ELSE provider_event.next_attempt_at
	END,
	lease_owner = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN NULL
		ELSE provider_event.lease_owner
	END,
	lease_expires_at = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN NULL
		ELSE provider_event.lease_expires_at
	END,
	last_error = CASE
		WHEN provider_event.event_state IN ('PENDING', 'FAILED', 'LEASED') THEN 'OWNED_PAYLOAD_RETENTION_EXPIRED'
		ELSE provider_event.last_error
	END,
	updated_at = NOW()
FROM purge_candidates
WHERE provider_event.id = purge_candidates.id`

// PurgeExpiredOwnedProviderPayloads removes only encrypted payload bytes that
// are no longer retained and are not under legal hold. The SHA-256 digest is
// retained; any claimable event is atomically terminalized so it cannot later
// be leased without its owned payload.
func (r *DBRepository) PurgeExpiredOwnedProviderPayloads(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("owned provider payload purge limit must be positive")
	}
	if err := r.requireDB(); err != nil {
		return 0, err
	}

	result, err := r.db.ExecContext(ctx, purgeExpiredOwnedProviderPayloadsSQL, limit)
	if err != nil {
		return 0, fmt.Errorf("purge expired owned provider payloads: %w", err)
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read expired owned provider payload purge count: %w", err)
	}
	return int(purged), nil
}
