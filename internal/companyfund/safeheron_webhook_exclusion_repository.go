package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrSafeheronWebhookExclusionIdentityConflict = errors.New("Safeheron webhook exclusion identity conflict")

const insertSafeheronWebhookRawEventExclusionSQL = `
INSERT INTO company_fund_safeheron_raw_event_exclusions (
	safeheron_webhook_event_id, source_payload_digest, exclusion_reason, configuration_fingerprint
) VALUES ($1, $2, $3, $4)
ON CONFLICT (safeheron_webhook_event_id) DO UPDATE
SET exclusion_reason = EXCLUDED.exclusion_reason,
	configuration_fingerprint = EXCLUDED.configuration_fingerprint
WHERE company_fund_safeheron_raw_event_exclusions.source_payload_digest = EXCLUDED.source_payload_digest
	AND (
		company_fund_safeheron_raw_event_exclusions.exclusion_reason IS DISTINCT FROM EXCLUDED.exclusion_reason
		OR company_fund_safeheron_raw_event_exclusions.configuration_fingerprint IS DISTINCT FROM EXCLUDED.configuration_fingerprint
	)
RETURNING safeheron_webhook_event_id`

const selectSafeheronWebhookRawEventExclusionDigestSQL = `
SELECT source_payload_digest
FROM company_fund_safeheron_raw_event_exclusions
WHERE safeheron_webhook_event_id = $1`

// RecordSafeheronWebhookRawEventExclusion persists a negative marker after
// independently proving the raw source row still has the supplied immutable
// digest. Permanent reasons remain idempotent; configuration-dependent reasons
// update their fingerprint after a settings change so the collector can defer
// the next decision to the current registry. It never touches deposit state.
func (r *DBRepository) RecordSafeheronWebhookRawEventExclusion(
	ctx context.Context,
	input SafeheronWebhookRawEventExclusionInput,
) error {
	if err := input.validate(); err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}
	if err := r.verifySafeheronWebhookExclusionSource(ctx, input); err != nil {
		return err
	}
	var configurationFingerprint any
	if input.ConfigurationFingerprint != "" {
		configurationFingerprint = input.ConfigurationFingerprint
	}
	var rawEventID int
	err := r.db.QueryRowContext(ctx, insertSafeheronWebhookRawEventExclusionSQL,
		input.SafeheronWebhookEventID,
		input.PayloadDigest,
		input.Reason,
		configurationFingerprint,
	).Scan(&rawEventID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("insert Safeheron webhook exclusion: %w", err)
	}
	var existingDigest string
	if err := r.db.QueryRowContext(ctx, selectSafeheronWebhookRawEventExclusionDigestSQL, input.SafeheronWebhookEventID).Scan(&existingDigest); err != nil {
		return fmt.Errorf("read duplicate Safeheron webhook exclusion: %w", err)
	}
	if existingDigest != input.PayloadDigest {
		return ErrSafeheronWebhookExclusionIdentityConflict
	}
	return nil
}

func (r *DBRepository) verifySafeheronWebhookExclusionSource(ctx context.Context, input SafeheronWebhookRawEventExclusionInput) error {
	var payloadDigest sql.NullString
	if err := r.db.QueryRowContext(ctx, selectSafeheronWebhookPayloadDigestSQL, input.SafeheronWebhookEventID).Scan(&payloadDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("referenced Safeheron webhook event does not exist")
		}
		return fmt.Errorf("read referenced Safeheron webhook payload digest: %w", err)
	}
	if !payloadDigest.Valid || payloadDigest.String != input.PayloadDigest {
		return fmt.Errorf("referenced Safeheron webhook payload digest does not match exclusion source digest")
	}
	return nil
}

var _ SafeheronWebhookRawEventExclusionStore = (*DBRepository)(nil)
