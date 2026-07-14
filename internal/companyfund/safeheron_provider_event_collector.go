package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const maxSafeheronProviderEventCollectorBatchSize = 500

// SafeheronRawEventCandidate is an already-verified Safeheron raw-event
// reference that lacks its independent company-fund provider-event row. The
// collector reads raw payload only to apply fail-closed company-wallet
// eligibility; it never logs payload bytes or touches deposit worker state.
type SafeheronRawEventCandidate struct {
	SafeheronWebhookEventID int
	ProviderEventID         string
	EventType               string
	PayloadDigest           string
	RawPayload              []byte
}

// SafeheronRawEventCandidateReader supplies raw-event references for the
// durable bridge compensator. Implementations must never use process_status to
// decide whether a company-fund record is missing.
type SafeheronRawEventCandidateReader interface {
	ListUnbridgedSafeheronWebhookEvents(ctx context.Context, limit int) ([]SafeheronRawEventCandidate, error)
}

// SafeheronProviderEventWriter is the narrow company-fund persistence surface
// required by the compensator. DBRepository satisfies it.
type SafeheronProviderEventWriter interface {
	InsertProviderEvent(ctx context.Context, input ProviderEventInput) (ProviderEventInsertResult, error)
}

type safeheronRawEventQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// PostgresSafeheronRawEventCandidateReader performs an anti-join from the
// Safeheron-owned raw-event table to company-fund provider events. The SQL is
// read-only and intentionally leaves deposit process_status ownership intact.
type PostgresSafeheronRawEventCandidateReader struct {
	db           safeheronRawEventQueryer
	fingerprints SafeheronWebhookEligibilityFingerprintProvider
}

func NewPostgresSafeheronRawEventCandidateReader(
	db safeheronRawEventQueryer,
	fingerprints SafeheronWebhookEligibilityFingerprintProvider,
) *PostgresSafeheronRawEventCandidateReader {
	return &PostgresSafeheronRawEventCandidateReader{db: db, fingerprints: fingerprints}
}

const selectUnbridgedSafeheronWebhookEventsSQL = `
SELECT raw_event.id, raw_event.event_id, raw_event.event_type, raw_event.payload_digest, raw_event.raw_payload
FROM safeheron_webhook_events AS raw_event
WHERE raw_event.payload_digest ~ '^[0-9a-f]{64}$'
  AND NOT EXISTS (
	SELECT 1
	FROM company_fund_provider_events AS provider_event
	WHERE provider_event.channel = 'SAFEHERON'
	  AND provider_event.source_kind = 'EXISTING_SAFEHERON_WEBHOOK_REF'
	  AND provider_event.safeheron_webhook_event_id = raw_event.id
	  AND provider_event.source_payload_digest = raw_event.payload_digest
  )
	AND NOT EXISTS (
	SELECT 1
	FROM company_fund_safeheron_raw_event_exclusions AS exclusion
	WHERE exclusion.safeheron_webhook_event_id = raw_event.id
	  AND exclusion.source_payload_digest = raw_event.payload_digest
	  AND (
			exclusion.exclusion_reason IN ('NON_TRANSACTION_STATUS', 'INVALID_PAYLOAD', 'EVENT_TYPE_MISMATCH')
			OR (
				exclusion.exclusion_reason IN ('UNMAPPED_ASSET', 'NO_CONFIGURED_ADDRESS')
				AND exclusion.configuration_fingerprint = $1
			)
	  )
  )
ORDER BY raw_event.id
LIMIT $2`

func (r *PostgresSafeheronRawEventCandidateReader) ListUnbridgedSafeheronWebhookEvents(ctx context.Context, limit int) ([]SafeheronRawEventCandidate, error) {
	if limit < 1 || limit > maxSafeheronProviderEventCollectorBatchSize {
		return nil, fmt.Errorf("Safeheron provider-event collector limit must be between 1 and %d", maxSafeheronProviderEventCollectorBatchSize)
	}
	if r == nil || r.db == nil || r.fingerprints == nil {
		return nil, fmt.Errorf("Safeheron raw-event candidate reader is not configured")
	}
	configurationFingerprint, err := r.fingerprints.CurrentSafeheronWebhookEligibilityFingerprint()
	if err != nil || !isLowerSHA256Hex(configurationFingerprint) {
		return nil, fmt.Errorf("resolve Safeheron webhook eligibility configuration fingerprint")
	}
	rows, err := r.db.QueryContext(ctx, selectUnbridgedSafeheronWebhookEventsSQL, configurationFingerprint, limit)
	if err != nil {
		return nil, fmt.Errorf("query unbridged Safeheron raw events: %w", err)
	}
	defer rows.Close()

	candidates := make([]SafeheronRawEventCandidate, 0)
	for rows.Next() {
		var candidate SafeheronRawEventCandidate
		if err := rows.Scan(
			&candidate.SafeheronWebhookEventID,
			&candidate.ProviderEventID,
			&candidate.EventType,
			&candidate.PayloadDigest,
			&candidate.RawPayload,
		); err != nil {
			return nil, fmt.Errorf("scan unbridged Safeheron raw event: %w", err)
		}
		if err := candidate.validate(); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unbridged Safeheron raw events: %w", err)
	}
	return candidates, nil
}

func (candidate SafeheronRawEventCandidate) validate() error {
	if candidate.SafeheronWebhookEventID <= 0 ||
		strings.TrimSpace(candidate.ProviderEventID) == "" ||
		strings.TrimSpace(candidate.EventType) == "" ||
		!isLowerSHA256Hex(candidate.PayloadDigest) {
		return fmt.Errorf("invalid Safeheron raw-event candidate")
	}
	return nil
}

// SafeheronProviderEventCollectionResult distinguishes scanned candidates from
// newly inserted rows; duplicate insert attempts are already converged and do
// not count as new work.
type SafeheronProviderEventCollectionResult struct {
	Scanned  int
	Excluded int
	Inserted int
}

// SafeheronProviderEventCollector repairs a raw-event/ledger bridge that was
// interrupted after raw persistence. It makes no provider HTTP calls and does
// not read or write Safeheron's deposit worker state.
type SafeheronProviderEventCollector struct {
	reader      SafeheronRawEventCandidateReader
	writer      SafeheronProviderEventWriter
	eligibility SafeheronWebhookEligibility
}

// NewSafeheronProviderEventCollector requires one eligibility service. The
// variadic form preserves source compatibility for older callers while making
// an omitted eligibility service a construction error instead of silently
// bridging every deposit-owned raw event.
func NewSafeheronProviderEventCollector(
	reader SafeheronRawEventCandidateReader,
	writer SafeheronProviderEventWriter,
	eligibility ...SafeheronWebhookEligibility,
) (*SafeheronProviderEventCollector, error) {
	if reader == nil {
		return nil, fmt.Errorf("Safeheron raw-event candidate reader is required")
	}
	if writer == nil {
		return nil, fmt.Errorf("Safeheron provider-event writer is required")
	}
	if len(eligibility) != 1 || eligibility[0] == nil {
		return nil, fmt.Errorf("Safeheron webhook eligibility service is required")
	}
	return &SafeheronProviderEventCollector{reader: reader, writer: writer, eligibility: eligibility[0]}, nil
}

func (c *SafeheronProviderEventCollector) Collect(ctx context.Context, limit int) (SafeheronProviderEventCollectionResult, error) {
	if c == nil || c.reader == nil || c.writer == nil || c.eligibility == nil {
		return SafeheronProviderEventCollectionResult{}, fmt.Errorf("Safeheron provider-event collector is not configured")
	}
	candidates, err := c.reader.ListUnbridgedSafeheronWebhookEvents(ctx, limit)
	if err != nil {
		return SafeheronProviderEventCollectionResult{}, err
	}
	result := SafeheronProviderEventCollectionResult{}
	for _, candidate := range candidates {
		if err := candidate.validate(); err != nil {
			return result, err
		}
		result.Scanned++
		decision, err := c.eligibility.AssessAndRecord(ctx, SafeheronWebhookEligibilityInput{
			SafeheronWebhookEventID: candidate.SafeheronWebhookEventID,
			EventType:               candidate.EventType,
			PayloadDigest:           candidate.PayloadDigest,
			RawPayload:              candidate.RawPayload,
		})
		if err != nil {
			return result, fmt.Errorf("assess Safeheron raw event %d eligibility: %w", candidate.SafeheronWebhookEventID, err)
		}
		if !decision.Candidate {
			result.Excluded++
			continue
		}
		rawEventID := candidate.SafeheronWebhookEventID
		inserted, err := c.writer.InsertProviderEvent(ctx, ProviderEventInput{
			Channel:                 ChannelSafeheron,
			ProviderEventID:         candidate.ProviderEventID,
			EventType:               candidate.EventType,
			SourceKind:              ProviderEventSourceExistingSafeheronWebhookRef,
			SafeheronWebhookEventID: &rawEventID,
			SourcePayloadDigest:     candidate.PayloadDigest,
		})
		if err != nil {
			return result, fmt.Errorf("bridge Safeheron raw event %d: %w", rawEventID, err)
		}
		if inserted.Inserted {
			result.Inserted++
		}
	}
	return result, nil
}
