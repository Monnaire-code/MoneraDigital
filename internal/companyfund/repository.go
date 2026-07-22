package companyfund

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	maxProviderEventIDBytes       = 256
	maxProviderEventTypeBytes     = 128
	maxProviderEventVersionBytes  = 64
	maxProviderEventOwnerBytes    = 128
	maxOwnedPayloadCiphertextSize = 1 << 20
	maxProviderEventErrorBytes    = 4096
)

var (
	ErrProviderEventLeaseNotOwned = errors.New("company-fund provider event lease is not owned or has expired")
	ErrProviderEventClaimLost     = errors.New("company-fund provider event claim was lost")
	// ErrProviderEventIdentityConflict means a provider reused a delivery ID
	// for a different immutable delivery identity. It is deliberately
	// fail-closed: accepting such a record would make an idempotency key hide
	// potentially different source bytes or parser semantics.
	ErrProviderEventIdentityConflict = errors.New("company-fund provider event identity conflict")
)

// SQLDatabase is the narrow database surface used by DBRepository. Every
// public repository operation is context-first; external HTTP work belongs in
// an adapter/worker after the claim transaction has committed.
type SQLDatabase interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Repository is the provider-neutral storage boundary used by future workers.
// It intentionally contains no HTTP/provider-client methods.
type Repository interface {
	InsertProviderEvent(ctx context.Context, input ProviderEventInput) (ProviderEventInsertResult, error)
	ClaimNextProviderEvent(ctx context.Context, owner string, leaseDuration time.Duration) (*ProviderEventLease, error)
	RenewProviderEventLease(ctx context.Context, eventID int64, owner string, leaseDuration time.Duration) (time.Time, error)
	FinalizeProviderEvent(ctx context.Context, eventID int64, owner string, outcome ProviderEventFinalizeOutcome, retryAt *time.Time, failureDetail string) error
	PurgeExpiredOwnedProviderPayloads(ctx context.Context, limit int) (int, error)
	UpsertCompanyFundTransaction(ctx context.Context, input TransactionUpsertInput) (TransactionUpsertResult, error)
}

// DBRepository is the PostgreSQL implementation of the company-fund storage
// boundary. It does not touch Safeheron's deposit-owned process_status column.
type DBRepository struct {
	db                         SQLDatabase
	safeheronProviderClaimMode string
}

const (
	SafeheronProviderClaimAll           = "ALL"
	SafeheronProviderClaimDisabled      = "DISABLED"
	SafeheronProviderClaimRoutingScoped = "ROUTING_SCOPED"
)

func NewDBRepository(db SQLDatabase) *DBRepository {
	return &DBRepository{db: db, safeheronProviderClaimMode: SafeheronProviderClaimAll}
}

func (r *DBRepository) SetSafeheronProviderClaimMode(mode string) {
	if r != nil {
		r.safeheronProviderClaimMode = mode
	}
}

// NewDBRepositoryWithClock remains source-compatible for callers that used
// the old constructor. Provider-event lease expiry is now always authoritative
// from PostgreSQL NOW(), so the application clock is deliberately ignored.
func NewDBRepositoryWithClock(db SQLDatabase, _ func() time.Time) *DBRepository {
	return NewDBRepository(db)
}

type ProviderEventSource string

const (
	ProviderEventSourceExistingSafeheronWebhookRef ProviderEventSource = "EXISTING_SAFEHERON_WEBHOOK_REF"
	ProviderEventSourceOwnedEncryptedPayload       ProviderEventSource = "OWNED_ENCRYPTED_PAYLOAD"
)

// ProviderEventInput owns only the company-fund delivery record. Safeheron
// Webhooks reference their already-verified raw event by INTEGER ID; provider
// API/Airwallex payloads use bounded encrypted bytes owned by this feature.
type ProviderEventInput struct {
	Channel                          Channel
	ProviderEventID                  string
	EventType                        string
	ProviderEventVersion             string
	ProviderOrgKey                   string
	ProviderAccountKey               string
	SourceKind                       ProviderEventSource
	SafeheronWebhookEventID          *int
	SourcePayloadDigest              string
	AuthorizedSafeheronOccurrenceKey string
	AuthorizingRoutingActionID       int64
	AuthorizingRoutingLeaseOwner     string
	OwnedPayloadCiphertext           []byte
	OwnedPayloadDigest               string
	OwnedPayloadKeyVersion           string
	OwnedPayloadRetentionDuration    time.Duration
	OwnedPayloadLegalHold            bool
}

type ProviderEventInsertResult struct {
	ID       int64
	Inserted bool
}

type ProviderEventLease struct {
	ID                               int64
	Channel                          Channel
	ProviderEventID                  string
	EventType                        string
	ProviderEventVersion             string
	ProviderOrgKey                   string
	ProviderAccountKey               string
	SourceKind                       ProviderEventSource
	SafeheronWebhookEventID          *int
	SourcePayloadDigest              string
	AuthorizedSafeheronOccurrenceKey string
	AuthorizingRoutingActionID       int64
	OwnedPayloadCiphertext           []byte
	OwnedPayloadDigest               string
	OwnedPayloadKeyVersion           string
	OwnedPayloadRetentionUntil       *time.Time
	OwnedPayloadPurgedAt             *time.Time
	OwnedPayloadLegalHold            bool
	AttemptCount                     int
	LeaseOwner                       string
	LeaseExpiresAt                   time.Time
}

type ProviderEventFinalizeOutcome string

const (
	ProviderEventFinalizeProcessed ProviderEventFinalizeOutcome = "PROCESSED"
	ProviderEventFinalizeIgnored   ProviderEventFinalizeOutcome = "IGNORED"
	ProviderEventFinalizeRetry     ProviderEventFinalizeOutcome = "RETRY"
	ProviderEventFinalizeFailed    ProviderEventFinalizeOutcome = "FAILED"
)

const insertProviderEventSQL = `
INSERT INTO company_fund_provider_events (
	channel, provider_event_id, event_type, provider_event_version, provider_org_key, provider_account_key,
	source_kind, safeheron_webhook_event_id, source_payload_digest,
	authorized_safeheron_occurrence_key, authorizing_routing_action_id,
	owned_payload_ciphertext, owned_payload_digest, owned_payload_key_version,
	owned_payload_retention_until, owned_payload_legal_hold
) VALUES (
	$1, $2, $3, $4, $5, $6,
	$7, $8, $9,
	$10, $16,
	$11, $12, $13,
	CASE WHEN $14::bigint = 0 THEN NULL
		ELSE NOW() + ($14::bigint * INTERVAL '1 microsecond')
	END,
	$15
)
ON CONFLICT (channel, provider_event_id) DO NOTHING
RETURNING id`

const insertAuthorizedProviderEventSQL = `
WITH authorized AS (
  SELECT 1
  FROM safeheron_transaction_routing_case_actions action
  JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
	  JOIN safeheron_transaction_routing_cases routing
	    ON routing.id=command.case_id AND routing.pending_command_id=command.id
	  JOIN safeheron_transaction_routing_case_sources source
	    ON source.case_id=routing.id AND source.safeheron_webhook_event_id=$8
	  WHERE action.id=$16 AND action.lease_owner=$17 AND action.lease_expires_at>now()
	    AND action.projection_kind='COMPANY'
	    AND action.action_type IN ('APPLY_COMPANY','FINALIZE_COMPANY_ONLY')
	    AND action.target_company_fund_account_id=routing.company_fund_account_id
	    AND routing.routing_identity_key=$10
	    AND action.status IN ('PENDING','RETRYABLE') AND command.status='PENDING'
  FOR UPDATE OF action,command,routing
)
INSERT INTO company_fund_provider_events (
	channel, provider_event_id, event_type, provider_event_version, provider_org_key, provider_account_key,
	source_kind, safeheron_webhook_event_id, source_payload_digest,
	authorized_safeheron_occurrence_key, authorizing_routing_action_id,
	owned_payload_ciphertext, owned_payload_digest, owned_payload_key_version,
	owned_payload_retention_until, owned_payload_legal_hold
)
SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$16,$11,$12,$13,
	CASE WHEN $14::bigint=0 THEN NULL ELSE NOW()+($14::bigint*INTERVAL '1 microsecond') END,$15
FROM authorized
ON CONFLICT (channel,provider_event_id) DO NOTHING
RETURNING id`

const selectProviderEventIdentitySQL = `
SELECT id, event_type, source_kind, source_payload_digest,
       safeheron_webhook_event_id, COALESCE(provider_event_version, ''),
       COALESCE(authorized_safeheron_occurrence_key, ''), authorizing_routing_action_id
FROM company_fund_provider_events
WHERE channel = $1 AND provider_event_id = $2`

const selectSafeheronWebhookPayloadDigestSQL = `
SELECT payload_digest
FROM safeheron_webhook_events
WHERE id = $1`

const claimNextProviderEventSQL = `
SELECT id, channel, provider_event_id, event_type, COALESCE(provider_event_version, ''),
       COALESCE(provider_org_key, ''), COALESCE(provider_account_key, ''), source_kind,
	       safeheron_webhook_event_id, source_payload_digest,
	       COALESCE(authorized_safeheron_occurrence_key, ''),
	       COALESCE(authorizing_routing_action_id, 0),
       owned_payload_ciphertext, owned_payload_digest, owned_payload_key_version,
       owned_payload_retention_until, owned_payload_purged_at, owned_payload_legal_hold, attempt_count
FROM company_fund_provider_events
WHERE owned_payload_purged_at IS NULL
		AND (
		source_kind <> 'OWNED_ENCRYPTED_PAYLOAD'
		OR owned_payload_legal_hold = true
		OR owned_payload_retention_until > NOW()
		)
		AND (
		  authorizing_routing_action_id IS NULL OR EXISTS (
		    SELECT 1 FROM safeheron_transaction_routing_case_actions action
		    JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
		    JOIN safeheron_transaction_routing_cases routing
		      ON routing.id=command.case_id AND routing.pending_command_id=command.id
			    WHERE action.id=company_fund_provider_events.authorizing_routing_action_id
			      AND action.projection_kind='COMPANY'
			      AND action.action_type IN ('APPLY_COMPANY','FINALIZE_COMPANY_ONLY')
			      AND action.status IN ('PENDING','RETRYABLE') AND command.status='PENDING'
		  )
		)
	AND (
		event_state = 'PENDING'
		OR (event_state = 'FAILED' AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
		OR (event_state = 'LEASED' AND lease_expires_at <= NOW())
	)
	AND (
	  $1::text='ALL'
	  OR ($1::text='DISABLED' AND channel<>'SAFEHERON')
	  OR ($1::text='ROUTING_SCOPED' AND (channel<>'SAFEHERON' OR authorized_safeheron_occurrence_key IS NOT NULL))
	)
ORDER BY received_at, id
FOR UPDATE SKIP LOCKED
LIMIT 1`

const updateClaimedProviderEventSQL = `
UPDATE company_fund_provider_events
SET event_state = 'LEASED',
    lease_owner = $2,
    lease_expires_at = NOW() + ($3::bigint * INTERVAL '1 microsecond'),
    next_attempt_at = NULL,
    attempt_count = attempt_count + 1,
    updated_at = NOW()
WHERE id = $1
	AND owned_payload_purged_at IS NULL
	AND (
		source_kind <> 'OWNED_ENCRYPTED_PAYLOAD'
		OR owned_payload_legal_hold = true
		OR owned_payload_retention_until > NOW()
	)
  AND (
	event_state = 'PENDING'
	OR (event_state = 'FAILED' AND next_attempt_at IS NOT NULL AND next_attempt_at <= NOW())
	OR (event_state = 'LEASED' AND lease_expires_at <= NOW())
  )
RETURNING attempt_count, lease_expires_at`

const nextProviderEventDueSQL = `
SELECT min(due_at) FROM (
  SELECT next_attempt_at AS due_at, channel, authorized_safeheron_occurrence_key,
         authorizing_routing_action_id
  FROM company_fund_provider_events
  WHERE event_state='FAILED' AND next_attempt_at > NOW()
  UNION ALL
  SELECT lease_expires_at AS due_at, channel, authorized_safeheron_occurrence_key,
         authorizing_routing_action_id
  FROM company_fund_provider_events
  WHERE event_state='LEASED' AND lease_expires_at > NOW()
) deadlines
WHERE (
  $1::text='ALL'
  OR ($1::text='DISABLED' AND channel<>'SAFEHERON')
  OR ($1::text='ROUTING_SCOPED' AND (
    channel<>'SAFEHERON' OR authorized_safeheron_occurrence_key IS NOT NULL
  ))
) AND (
  authorizing_routing_action_id IS NULL OR EXISTS (
    SELECT 1 FROM safeheron_transaction_routing_case_actions action
    JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
    JOIN safeheron_transaction_routing_cases routing
      ON routing.id=command.case_id AND routing.pending_command_id=command.id
    WHERE action.id=deadlines.authorizing_routing_action_id
      AND action.projection_kind='COMPANY'
      AND action.action_type IN ('APPLY_COMPANY','FINALIZE_COMPANY_ONLY')
      AND action.status IN ('PENDING','RETRYABLE') AND command.status='PENDING'
  )
)`

const renewProviderEventLeaseSQL = `
UPDATE company_fund_provider_events
SET lease_expires_at = NOW() + ($3::bigint * INTERVAL '1 microsecond'),
    updated_at = NOW()
WHERE id = $1
  AND event_state = 'LEASED'
  AND lease_owner = $2
  AND lease_expires_at > NOW()
	AND owned_payload_purged_at IS NULL
	AND (
		source_kind <> 'OWNED_ENCRYPTED_PAYLOAD'
		OR owned_payload_legal_hold = true
		OR owned_payload_retention_until > NOW()
	)
RETURNING lease_expires_at`

const finalizeProviderEventSQL = `
UPDATE company_fund_provider_events
SET event_state = $3,
    processed_at = CASE WHEN $3 IN ('PROCESSED', 'IGNORED', 'DEAD_LETTER') THEN NOW() ELSE NULL END,
    next_attempt_at = $4::TIMESTAMPTZ,
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error = $5,
    updated_at = NOW()
WHERE id = $1
  AND event_state = 'LEASED'
  AND lease_owner = $2
  AND lease_expires_at > NOW()
  AND ($3 <> 'FAILED' OR ($4::TIMESTAMPTZ IS NOT NULL AND $4::TIMESTAMPTZ > NOW()))`

// InsertProviderEvent inserts a logical delivery idempotently. A duplicate
// returns the existing row ID without creating a second delivery reference.
func (r *DBRepository) InsertProviderEvent(ctx context.Context, input ProviderEventInput) (ProviderEventInsertResult, error) {
	if err := input.validate(); err != nil {
		return ProviderEventInsertResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return ProviderEventInsertResult{}, err
	}
	if err := r.verifySafeheronWebhookSource(ctx, input); err != nil {
		return ProviderEventInsertResult{}, err
	}

	insertSQL := insertProviderEventSQL
	if input.AuthorizingRoutingActionID > 0 {
		insertSQL = insertAuthorizedProviderEventSQL
	}
	args := []any{
		input.Channel,
		input.ProviderEventID,
		input.EventType,
		nullableString(input.ProviderEventVersion),
		nullableString(input.ProviderOrgKey),
		nullableString(input.ProviderAccountKey),
		input.SourceKind,
		nullableInt(input.SafeheronWebhookEventID),
		input.SourcePayloadDigest,
		nullableString(input.AuthorizedSafeheronOccurrenceKey),
		nullableBytes(input.OwnedPayloadCiphertext),
		nullableString(input.OwnedPayloadDigest),
		nullableString(input.OwnedPayloadKeyVersion),
		input.OwnedPayloadRetentionDuration.Microseconds(),
		input.OwnedPayloadLegalHold,
		nullablePositiveInt64(input.AuthorizingRoutingActionID),
	}
	if input.AuthorizingRoutingActionID > 0 {
		args = append(args, input.AuthorizingRoutingLeaseOwner)
	}
	row := r.db.QueryRowContext(ctx, insertSQL, args...)
	var id int64
	if err := row.Scan(&id); err == nil {
		return ProviderEventInsertResult{ID: id, Inserted: true}, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ProviderEventInsertResult{}, fmt.Errorf("insert company-fund provider event: %w", err)
	}

	existing, err := readProviderEventIdentity(r.db.QueryRowContext(ctx, selectProviderEventIdentitySQL, input.Channel, input.ProviderEventID))
	if err != nil {
		return ProviderEventInsertResult{}, fmt.Errorf("lookup duplicate company-fund provider event: %w", err)
	}
	if field := providerEventIdentityConflictField(input, existing); field != "" {
		return ProviderEventInsertResult{}, fmt.Errorf("%w: %s for channel %q provider event %q", ErrProviderEventIdentityConflict, field, input.Channel, input.ProviderEventID)
	}
	return ProviderEventInsertResult{ID: existing.ID}, nil
}

// NextProviderEventDue returns the earliest durable retry or lease recovery
// deadline for the currently claimable provider scope.
func (r *DBRepository) NextProviderEventDue(ctx context.Context) (time.Time, error) {
	if err := r.requireDB(); err != nil {
		return time.Time{}, err
	}
	var due sql.NullTime
	err := r.db.QueryRowContext(ctx, nextProviderEventDueSQL, r.safeheronProviderClaimMode).Scan(&due)
	if err != nil || !due.Valid {
		return time.Time{}, err
	}
	return due.Time, nil
}

func (r *DBRepository) verifySafeheronWebhookSource(ctx context.Context, input ProviderEventInput) error {
	if input.SourceKind != ProviderEventSourceExistingSafeheronWebhookRef {
		return nil
	}

	var payloadDigest sql.NullString
	if err := r.db.QueryRowContext(ctx, selectSafeheronWebhookPayloadDigestSQL, *input.SafeheronWebhookEventID).Scan(&payloadDigest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("referenced Safeheron webhook event %d does not exist", *input.SafeheronWebhookEventID)
		}
		return fmt.Errorf("read referenced Safeheron webhook payload digest: %w", err)
	}
	if !payloadDigest.Valid || strings.TrimSpace(payloadDigest.String) == "" || payloadDigest.String != input.SourcePayloadDigest {
		return fmt.Errorf("referenced Safeheron webhook payload digest does not match provider event source digest")
	}
	return nil
}

// ClaimNextProviderEvent commits before returning. A worker therefore performs
// any future HTTP/decryption/normalization outside this database transaction.
func (r *DBRepository) ClaimNextProviderEvent(ctx context.Context, owner string, leaseDuration time.Duration) (*ProviderEventLease, error) {
	if err := validateLeaseOwner(owner); err != nil {
		return nil, err
	}
	leaseDurationMicroseconds, err := providerEventLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return nil, err
	}
	if err := r.requireDB(); err != nil {
		return nil, err
	}
	switch r.safeheronProviderClaimMode {
	case SafeheronProviderClaimAll, SafeheronProviderClaimDisabled, SafeheronProviderClaimRoutingScoped:
	default:
		return nil, fmt.Errorf("invalid Safeheron provider claim mode %q", r.safeheronProviderClaimMode)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin provider event claim: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	lease, err := scanProviderEventLease(tx.QueryRowContext(ctx, claimNextProviderEventSQL, r.safeheronProviderClaimMode))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select claimable provider event: %w", err)
	}
	lease.LeaseOwner = owner
	if err := tx.QueryRowContext(ctx, updateClaimedProviderEventSQL, lease.ID, owner, leaseDurationMicroseconds).Scan(&lease.AttemptCount, &lease.LeaseExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrProviderEventClaimLost
		}
		return nil, fmt.Errorf("claim provider event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit provider event claim: %w", err)
	}
	committed = true
	return lease, nil
}

// RenewProviderEventLease extends a still-live lease using PostgreSQL's clock,
// so it shares the exact time domain used by claim and stale-lease recovery.
func (r *DBRepository) RenewProviderEventLease(ctx context.Context, eventID int64, owner string, leaseDuration time.Duration) (time.Time, error) {
	if eventID <= 0 {
		return time.Time{}, fmt.Errorf("provider event ID must be positive")
	}
	if err := validateLeaseOwner(owner); err != nil {
		return time.Time{}, err
	}
	leaseDurationMicroseconds, err := providerEventLeaseDurationMicroseconds(leaseDuration)
	if err != nil {
		return time.Time{}, err
	}
	if err := r.requireDB(); err != nil {
		return time.Time{}, err
	}

	var expiresAt time.Time
	if err := r.db.QueryRowContext(ctx, renewProviderEventLeaseSQL, eventID, owner, leaseDurationMicroseconds).Scan(&expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrProviderEventLeaseNotOwned
		}
		return time.Time{}, fmt.Errorf("renew provider event lease: %w", err)
	}
	return expiresAt, nil
}

// FinalizeProviderEvent requires the still-live lease owner. Retry clears the
// lease into FAILED with an explicit future backoff; permanent failure moves
// the event to the non-claimable DEAD_LETTER terminal state.
func (r *DBRepository) FinalizeProviderEvent(ctx context.Context, eventID int64, owner string, outcome ProviderEventFinalizeOutcome, retryAt *time.Time, failureDetail string) error {
	if eventID <= 0 {
		return fmt.Errorf("provider event ID must be positive")
	}
	if err := validateLeaseOwner(owner); err != nil {
		return err
	}
	state, nextAttemptAt, lastError, err := finalizeProviderEventValues(outcome, retryAt, failureDetail)
	if err != nil {
		return err
	}
	if err := r.requireDB(); err != nil {
		return err
	}

	result, err := r.db.ExecContext(ctx, finalizeProviderEventSQL, eventID, owner, state, nextAttemptAt, lastError)
	if err != nil {
		return fmt.Errorf("finalize provider event: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("finalize provider event rows affected: %w", err)
	}
	if affected != 1 {
		if outcome == ProviderEventFinalizeRetry {
			return fmt.Errorf("provider event retry was not finalized: retry time must be future according to the database clock or the lease is no longer owned")
		}
		return ErrProviderEventLeaseNotOwned
	}
	return nil
}

func (input ProviderEventInput) validate() error {
	if !input.Channel.Valid() {
		return fmt.Errorf("unsupported provider event channel %q", input.Channel)
	}
	if err := validateRequiredString("provider event ID", input.ProviderEventID, maxProviderEventIDBytes); err != nil {
		return err
	}
	if err := validateRequiredString("provider event type", input.EventType, maxProviderEventTypeBytes); err != nil {
		return err
	}
	if input.ProviderEventVersion != "" {
		if err := validateRequiredString("provider event version", input.ProviderEventVersion, maxProviderEventVersionBytes); err != nil {
			return err
		}
	}
	if !isLowerSHA256Hex(input.SourcePayloadDigest) {
		return fmt.Errorf("provider event source payload digest must be lowercase SHA-256 hex")
	}

	switch input.SourceKind {
	case ProviderEventSourceExistingSafeheronWebhookRef:
		if input.Channel != ChannelSafeheron || input.SafeheronWebhookEventID == nil || *input.SafeheronWebhookEventID <= 0 {
			return fmt.Errorf("existing Safeheron webhook source requires a positive Safeheron raw event INTEGER ID")
		}
		if len(input.OwnedPayloadCiphertext) != 0 || input.OwnedPayloadDigest != "" || input.OwnedPayloadKeyVersion != "" || input.OwnedPayloadRetentionDuration != 0 || input.OwnedPayloadLegalHold {
			return fmt.Errorf("existing Safeheron webhook source cannot own a duplicate payload")
		}
	case ProviderEventSourceOwnedEncryptedPayload:
		if input.AuthorizedSafeheronOccurrenceKey != "" {
			return fmt.Errorf("owned encrypted payload cannot carry a routing occurrence authorization")
		}
		if input.SafeheronWebhookEventID != nil {
			return fmt.Errorf("owned encrypted payload cannot also reference a Safeheron raw event")
		}
		if len(input.OwnedPayloadCiphertext) == 0 || len(input.OwnedPayloadCiphertext) > maxOwnedPayloadCiphertextSize {
			return fmt.Errorf("owned encrypted payload must be non-empty and within the configured size limit")
		}
		if !isLowerSHA256Hex(input.OwnedPayloadDigest) || input.OwnedPayloadDigest != input.SourcePayloadDigest {
			return fmt.Errorf("owned encrypted payload digest must be lowercase SHA-256 hex and equal the source digest")
		}
		if err := validateRequiredString("owned payload key version", input.OwnedPayloadKeyVersion, maxOwnedPayloadKeyVersionBytes); err != nil {
			return err
		}
		if input.OwnedPayloadRetentionDuration <= 0 || input.OwnedPayloadRetentionDuration.Microseconds() <= 0 {
			return fmt.Errorf("owned encrypted payload requires a positive retention duration of at least one microsecond")
		}
	default:
		return fmt.Errorf("unsupported provider event source kind %q", input.SourceKind)
	}
	if input.AuthorizedSafeheronOccurrenceKey != "" && !validSafeheronOccurrenceKey(input.AuthorizedSafeheronOccurrenceKey) {
		return fmt.Errorf("authorized Safeheron occurrence key is invalid")
	}
	if (input.AuthorizingRoutingActionID > 0) != (strings.TrimSpace(input.AuthorizingRoutingLeaseOwner) != "") {
		return fmt.Errorf("routing action authorization requires both action ID and lease owner")
	}
	if input.AuthorizingRoutingActionID > 0 && input.AuthorizedSafeheronOccurrenceKey == "" {
		return fmt.Errorf("routing action authorization requires an authorized Safeheron occurrence")
	}
	if input.AuthorizedSafeheronOccurrenceKey != "" && input.AuthorizingRoutingActionID <= 0 {
		return fmt.Errorf("authorized Safeheron occurrence requires a routing action authorization")
	}
	return nil
}

func validSafeheronOccurrenceKey(value string) bool {
	const prefix = SafeheronOccurrenceAlgorithmVersion + ":"
	return strings.HasPrefix(value, prefix) && isLowerSHA256Hex(strings.TrimPrefix(value, prefix))
}

func validateLeaseOwner(owner string) error {
	return validateRequiredString("provider event lease owner", owner, maxProviderEventOwnerBytes)
}

func providerEventLeaseDurationMicroseconds(leaseDuration time.Duration) (int64, error) {
	if leaseDuration <= 0 || leaseDuration.Microseconds() <= 0 {
		return 0, fmt.Errorf("provider event lease duration must be at least one microsecond")
	}
	return leaseDuration.Microseconds(), nil
}

func validateRequiredString(label, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" || len(value) > maxBytes {
		return fmt.Errorf("%s must be non-empty and at most %d bytes", label, maxBytes)
	}
	return nil
}

func isLowerSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func scanProviderEventLease(row *sql.Row) (*ProviderEventLease, error) {
	var (
		lease             ProviderEventLease
		channel           string
		sourceKind        string
		rawEventID        sql.NullInt64
		ciphertext        []byte
		payloadDigest     sql.NullString
		payloadKeyVersion sql.NullString
		retentionUntil    sql.NullTime
		purgedAt          sql.NullTime
	)
	if err := row.Scan(
		&lease.ID,
		&channel,
		&lease.ProviderEventID,
		&lease.EventType,
		&lease.ProviderEventVersion,
		&lease.ProviderOrgKey,
		&lease.ProviderAccountKey,
		&sourceKind,
		&rawEventID,
		&lease.SourcePayloadDigest,
		&lease.AuthorizedSafeheronOccurrenceKey,
		&lease.AuthorizingRoutingActionID,
		&ciphertext,
		&payloadDigest,
		&payloadKeyVersion,
		&retentionUntil,
		&purgedAt,
		&lease.OwnedPayloadLegalHold,
		&lease.AttemptCount,
	); err != nil {
		return nil, err
	}
	lease.Channel = Channel(channel)
	lease.SourceKind = ProviderEventSource(sourceKind)
	if rawEventID.Valid {
		value := int(rawEventID.Int64)
		lease.SafeheronWebhookEventID = &value
	}
	if len(ciphertext) > 0 {
		lease.OwnedPayloadCiphertext = append([]byte(nil), ciphertext...)
	}
	if payloadDigest.Valid {
		lease.OwnedPayloadDigest = payloadDigest.String
	}
	if payloadKeyVersion.Valid {
		lease.OwnedPayloadKeyVersion = payloadKeyVersion.String
	}
	if retentionUntil.Valid {
		value := retentionUntil.Time
		lease.OwnedPayloadRetentionUntil = &value
	}
	if purgedAt.Valid {
		value := purgedAt.Time
		lease.OwnedPayloadPurgedAt = &value
	}
	return &lease, nil
}

type providerEventIdentity struct {
	ID                               int64
	EventType                        string
	SourceKind                       ProviderEventSource
	SourcePayloadDigest              string
	SafeheronWebhookEventID          sql.NullInt64
	ProviderEventVersion             string
	AuthorizedSafeheronOccurrenceKey string
	AuthorizingRoutingActionID       sql.NullInt64
}

func readProviderEventIdentity(row *sql.Row) (providerEventIdentity, error) {
	var (
		identity   providerEventIdentity
		sourceKind string
	)
	if err := row.Scan(
		&identity.ID,
		&identity.EventType,
		&sourceKind,
		&identity.SourcePayloadDigest,
		&identity.SafeheronWebhookEventID,
		&identity.ProviderEventVersion,
		&identity.AuthorizedSafeheronOccurrenceKey,
		&identity.AuthorizingRoutingActionID,
	); err != nil {
		return providerEventIdentity{}, err
	}
	identity.SourceKind = ProviderEventSource(sourceKind)
	return identity, nil
}

func providerEventIdentityConflictField(input ProviderEventInput, existing providerEventIdentity) string {
	switch {
	case input.EventType != existing.EventType:
		return "event type"
	case input.SourceKind != existing.SourceKind:
		return "source kind"
	case input.SourcePayloadDigest != existing.SourcePayloadDigest:
		return "source payload digest"
	case providerEventRawReferenceConflicts(input.SafeheronWebhookEventID, existing.SafeheronWebhookEventID):
		return "Safeheron raw event reference"
	case input.ProviderEventVersion != existing.ProviderEventVersion:
		return "provider event version"
	case input.AuthorizedSafeheronOccurrenceKey != existing.AuthorizedSafeheronOccurrenceKey:
		return "authorized Safeheron occurrence"
	case input.AuthorizingRoutingActionID > 0 && (!existing.AuthorizingRoutingActionID.Valid || existing.AuthorizingRoutingActionID.Int64 != input.AuthorizingRoutingActionID):
		return "authorizing routing action"
	default:
		return ""
	}
}

func providerEventRawReferenceConflicts(input *int, existing sql.NullInt64) bool {
	if input == nil {
		return existing.Valid
	}
	return !existing.Valid || int64(*input) != existing.Int64
}

func finalizeProviderEventValues(outcome ProviderEventFinalizeOutcome, retryAt *time.Time, failureDetail string) (state string, nextAttemptAt any, lastError any, err error) {
	switch outcome {
	case ProviderEventFinalizeProcessed:
		return "PROCESSED", nil, nil, nil
	case ProviderEventFinalizeIgnored:
		return "IGNORED", nil, nil, nil
	case ProviderEventFinalizeRetry:
		if retryAt == nil || retryAt.IsZero() {
			return "", nil, nil, fmt.Errorf("provider event retry requires a next attempt time")
		}
		return "FAILED", *retryAt, nullableString(truncateProviderEventError(failureDetail)), nil
	case ProviderEventFinalizeFailed:
		return "DEAD_LETTER", nil, nullableString(truncateProviderEventError(failureDetail)), nil
	default:
		return "", nil, nil, fmt.Errorf("unsupported provider event finalize outcome %q", outcome)
	}
}

func truncateProviderEventError(value string) string {
	if len(value) <= maxProviderEventErrorBytes {
		return value
	}
	cut := 0
	for cut < len(value) {
		_, width := utf8.DecodeRuneInString(value[cut:])
		if cut+width > maxProviderEventErrorBytes {
			break
		}
		cut += width
	}
	return value[:cut]
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullablePositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (r *DBRepository) requireDB() error {
	if r == nil || r.db == nil {
		return errors.New("company-fund repository database is not configured")
	}
	return nil
}

type TransactionSeenSource string

const (
	TransactionSeenSourceWebhook        TransactionSeenSource = "WEBHOOK"
	TransactionSeenSourceReconciliation TransactionSeenSource = "RECONCILIATION"
)

// TransactionUpsertInput is deliberately provider/system-owned only. Finance
// classification and manual risk-review fields have no representation here,
// so webhook/reconciliation processing cannot overwrite them.
type TransactionUpsertInput struct {
	MovementKey                        string
	Channel                            Channel
	IdentityAlgorithmVersion           string
	ProviderOccurrenceKey              string
	ProviderOccurrenceAlgorithmVersion string
	ProviderAccountKey                 string
	ProviderTransactionID              string
	ProviderEventID                    string
	ProviderMovementID                 string
	ProviderTransactionFactID          *int64
	MovementIndex                      int
	MovementKind                       MovementKind
	TransferMode                       TransferMode
	Direction                          Direction
	// Linkage is provider-owned structural metadata. Keys are resolved inside
	// the upsert transaction; callers never provide database IDs or derive a
	// parent from nullable provider account/organization metadata.
	ParentMovementKey          string
	ReversalOfMovementKey      string
	ConversionGroupKey         string
	ConversionLeg              ConversionLeg
	ConversionGroupState       ConversionGroupState
	FromCompanyFundAccountID   *int64
	ToCompanyFundAccountID     *int64
	Currency                   string
	Asset                      AssetIdentity
	Amount                     decimal.Decimal
	OccurredAt                 *time.Time
	LatestProviderEventID      *int64
	RawSnapshotDigest          string
	FirstSeenSource            TransactionSeenSource
	Provider                   ProviderOwnedFields
	ProviderStatusRank         int
	ProviderDisplay            ProviderTransactionDisplayInput
	AutomaticRisk              ProviderAutomaticRiskInput
	AuthorizingRoutingActionID int64
}

type TransactionUpsertResult struct {
	ID          int64
	Inserted    bool
	Quarantined bool
}

type TransactionQuarantineError struct {
	MovementKey string
	Reason      string
}

func (e *TransactionQuarantineError) Error() string {
	return fmt.Sprintf("company-fund transaction %q quarantined: %s", e.MovementKey, e.Reason)
}

const selectCompanyFundTransactionForUpdateSQL = `
SELECT id, channel, identity_algorithm_version,
	   COALESCE(provider_account_key, ''), COALESCE(provider_transaction_id, ''),
	   COALESCE(provider_event_id, ''), COALESCE(provider_movement_id, ''),
	   provider_transaction_fact_id, latest_provider_event_id, COALESCE(raw_snapshot_digest, ''),
	   amount::text, currency,
       COALESCE(chain_code, ''), COALESCE(provider_asset_key, ''), COALESCE(asset_contract, ''),
	       COALESCE(provider_status, ''), provider_status_version, provider_fact_source, status_rank, last_seen_source,
	       COALESCE(tx_hash, ''), occurred_at, completed_at, provider_updated_at
FROM company_fund_transactions
WHERE movement_key = $1
FOR UPDATE`

const selectSafeheronCompanyFundTransactionForUpdateSQL = `
SELECT id, movement_key, channel, identity_algorithm_version,
	   COALESCE(provider_occurrence_key, ''), COALESCE(provider_occurrence_algorithm_version, ''),
	   COALESCE(provider_account_key, ''), COALESCE(provider_transaction_id, ''),
	   COALESCE(provider_event_id, ''), COALESCE(provider_movement_id, ''),
	   provider_transaction_fact_id, latest_provider_event_id, COALESCE(raw_snapshot_digest, ''),
	   amount::text, currency,
	   COALESCE(chain_code, ''), COALESCE(provider_asset_key, ''), COALESCE(asset_contract, ''),
	   is_unrecognized_asset,
	   COALESCE(provider_status, ''), provider_status_version, provider_fact_source, status_rank, last_seen_source,
	   COALESCE(tx_hash, ''), occurred_at, completed_at, provider_updated_at
FROM company_fund_transactions
WHERE movement_key = $1 OR provider_occurrence_key = $2
ORDER BY id
FOR UPDATE`

const insertCompanyFundTransactionSQL = `
INSERT INTO company_fund_transactions (
	channel, provider_account_key, provider_transaction_id, provider_event_id, provider_movement_id,
	movement_index, movement_key, identity_algorithm_version,
	movement_kind, transfer_mode, transaction_direction,
	from_company_fund_account_id, to_company_fund_account_id,
	currency, chain_code, provider_asset_key, asset_contract, amount,
	tx_hash, provider_status, provider_status_version, provider_updated_at, provider_fact_source, status_rank,
	occurred_at, completed_at, first_seen_source, last_seen_source,
	latest_provider_event_id, provider_transaction_fact_id, raw_snapshot_digest
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8,
	$9, $10, $11,
	$12, $13,
	$14, $15, $16, $17, $18::numeric,
	$19, $20, $21, $22, $23, $24,
	$25, $26, $27, $28,
	$29, $30, $31
)
ON CONFLICT (movement_key) DO NOTHING
RETURNING id`

const insertSafeheronCompanyFundTransactionSQL = `
INSERT INTO company_fund_transactions (
	channel, provider_account_key, provider_transaction_id, provider_event_id, provider_movement_id,
	movement_index, movement_key, identity_algorithm_version,
	movement_kind, transfer_mode, transaction_direction,
	from_company_fund_account_id, to_company_fund_account_id,
	currency, chain_code, provider_asset_key, asset_contract, amount,
	tx_hash, provider_status, provider_status_version, provider_updated_at, provider_fact_source, status_rank,
	occurred_at, completed_at, first_seen_source, last_seen_source,
	latest_provider_event_id, provider_transaction_fact_id, raw_snapshot_digest,
	provider_occurrence_key, provider_occurrence_algorithm_version
) VALUES (
	$1, $2, $3, $4, $5,
	$6, $7, $8,
	$9, $10, $11,
	$12, $13,
	$14, $15, $16, $17, $18::numeric,
	$19, $20, $21, $22, $23, $24,
	$25, $26, $27, $28,
	$29, $30, $31,
	$32, $33
)
ON CONFLICT DO NOTHING
RETURNING id`

// updateCompanyFundTransactionSQL is intentionally enumerated instead of
// using row replacement. The list is provider-owned only; manual finance/risk
// columns are structurally absent from both input and UPDATE SET.
const updateCompanyFundTransactionSQL = `
UPDATE company_fund_transactions
SET provider_account_key = $2,
	provider_transaction_id = $3,
	provider_event_id = $4,
	provider_movement_id = $5,
	provider_transaction_fact_id = $6,
	amount = $7::numeric,
	currency = $8,
	chain_code = CASE WHEN $9 THEN $10 ELSE chain_code END,
	provider_asset_key = CASE WHEN $9 THEN $11 ELSE provider_asset_key END,
	asset_contract = CASE WHEN $9 THEN $12 ELSE asset_contract END,
	tx_hash = COALESCE($13, tx_hash),
	provider_status = COALESCE($14, provider_status),
	provider_status_version = COALESCE($15, provider_status_version),
	provider_updated_at = COALESCE($16, provider_updated_at),
	provider_fact_source = $17,
	status_rank = $18,
	occurred_at = COALESCE($19, occurred_at),
	completed_at = COALESCE($20, completed_at),
	latest_provider_event_id = $21,
	raw_snapshot_digest = $22,
	last_seen_source = $23,
	last_synced_at = NOW(),
	updated_at = NOW()
WHERE id = $1
RETURNING id`

// updateCompanyFundTransactionProviderSupplementSQL persists provider display
// facts and automatic risk results separately from the core movement merge.
// Newer provider metadata may correct a field; older facts can only fill a
// missing value (with conservative true-risk promotion for safety). Manual
// finance/risk-review columns are intentionally absent.
const updateCompanyFundTransactionProviderSupplementSQL = `
UPDATE company_fund_transactions
SET from_address_or_account = CASE WHEN $2 THEN COALESCE($3, from_address_or_account) ELSE COALESCE(from_address_or_account, $3) END,
	to_address_or_account = CASE WHEN $2 THEN COALESCE($4, to_address_or_account) ELSE COALESCE(to_address_or_account, $4) END,
	payer_name = CASE WHEN $2 THEN COALESCE($5, payer_name) ELSE COALESCE(payer_name, $5) END,
	payee_name = CASE WHEN $2 THEN COALESCE($6, payee_name) ELSE COALESCE(payee_name, $6) END,
	from_company_entity_snapshot = CASE WHEN $2 THEN COALESCE($7, from_company_entity_snapshot) ELSE COALESCE(from_company_entity_snapshot, $7) END,
	from_fund_account_name_snapshot = CASE WHEN $2 THEN COALESCE($8, from_fund_account_name_snapshot) ELSE COALESCE(from_fund_account_name_snapshot, $8) END,
	from_sub_account_name_snapshot = CASE WHEN $2 THEN COALESCE($9, from_sub_account_name_snapshot) ELSE COALESCE(from_sub_account_name_snapshot, $9) END,
	from_account_type_snapshot = CASE WHEN $2 THEN COALESCE($10, from_account_type_snapshot) ELSE COALESCE(from_account_type_snapshot, $10) END,
	to_company_entity_snapshot = CASE WHEN $2 THEN COALESCE($11, to_company_entity_snapshot) ELSE COALESCE(to_company_entity_snapshot, $11) END,
	to_fund_account_name_snapshot = CASE WHEN $2 THEN COALESCE($12, to_fund_account_name_snapshot) ELSE COALESCE(to_fund_account_name_snapshot, $12) END,
	to_sub_account_name_snapshot = CASE WHEN $2 THEN COALESCE($13, to_sub_account_name_snapshot) ELSE COALESCE(to_sub_account_name_snapshot, $13) END,
	to_account_type_snapshot = CASE WHEN $2 THEN COALESCE($14, to_account_type_snapshot) ELSE COALESCE(to_account_type_snapshot, $14) END,
	provider_reported_fee_amount = CASE WHEN $2 THEN COALESCE($15::numeric, provider_reported_fee_amount) ELSE COALESCE(provider_reported_fee_amount, $15::numeric) END,
	provider_reported_fee_currency = CASE WHEN $2 THEN COALESCE($16, provider_reported_fee_currency) ELSE COALESCE(provider_reported_fee_currency, $16) END,
	fee_details = CASE
		WHEN $2 AND $17::jsonb IS NOT NULL THEN $17::jsonb
		WHEN fee_details = '{}'::jsonb AND $17::jsonb IS NOT NULL THEN $17::jsonb
		ELSE fee_details
	END,
	block_height = CASE WHEN $2 THEN COALESCE($18::bigint, block_height) ELSE COALESCE(block_height, $18::bigint) END,
	block_hash = CASE WHEN $2 THEN COALESCE($19, block_hash) ELSE COALESCE(block_hash, $19) END,
	is_dust = CASE WHEN $2 AND $20::boolean IS NOT NULL THEN $20::boolean WHEN $20::boolean = true THEN true ELSE is_dust END,
	dust_policy_id = CASE WHEN $2 THEN COALESCE($21::bigint, dust_policy_id) ELSE COALESCE(dust_policy_id, $21::bigint) END,
	dust_threshold = CASE WHEN $2 THEN COALESCE($22::numeric, dust_threshold) ELSE COALESCE(dust_threshold, $22::numeric) END,
	is_source_phishing = CASE WHEN $2 AND $23::boolean IS NOT NULL THEN $23::boolean WHEN $23::boolean = true THEN true ELSE COALESCE(is_source_phishing, $23::boolean) END,
	is_destination_phishing = CASE WHEN $2 AND $24::boolean IS NOT NULL THEN $24::boolean WHEN $24::boolean = true THEN true ELSE COALESCE(is_destination_phishing, $24::boolean) END,
	is_unrecognized_asset = CASE WHEN $2 AND $25::boolean IS NOT NULL THEN $25::boolean WHEN $25::boolean = true THEN true ELSE is_unrecognized_asset END,
	aml_lock = CASE WHEN $2 AND $26::boolean IS NOT NULL THEN $26::boolean WHEN $26::boolean = true THEN true ELSE COALESCE(aml_lock, $26::boolean) END,
	aml_screening_state = CASE
		WHEN $2 AND $27::varchar IS NOT NULL THEN $27::varchar
		WHEN aml_screening_state = 'NOT_SCREENED' AND $27::varchar IS NOT NULL THEN $27::varchar
		ELSE aml_screening_state
	END,
	aml_risk_level = CASE
		WHEN $2 AND $28::varchar IS NOT NULL THEN $28::varchar
		WHEN aml_risk_level = 'UNKNOWN' AND $28::varchar IS NOT NULL THEN $28::varchar
		ELSE aml_risk_level
	END,
	risk_flags = CASE
		WHEN $2 AND $29::jsonb IS NOT NULL THEN $29::jsonb
		WHEN risk_flags = '[]'::jsonb AND $29::jsonb IS NOT NULL THEN $29::jsonb
		ELSE risk_flags
	END,
	auto_excluded_from_summary = CASE
		WHEN $2 AND $30::boolean IS NOT NULL THEN $30::boolean
		WHEN $30::boolean = true THEN true
		ELSE auto_excluded_from_summary
	END,
	updated_at = NOW()
WHERE id = $1
RETURNING id`

// UpsertCompanyFundTransaction holds a row lock only while it reads/merges and
// writes database facts. No provider HTTP work is performed here. All status
// changes pass through MergeMovementProviderFieldsForChannel.
func (r *DBRepository) UpsertCompanyFundTransaction(ctx context.Context, input TransactionUpsertInput) (TransactionUpsertResult, error) {
	if err := input.validate(); err != nil {
		return TransactionUpsertResult{}, err
	}
	supplement, err := normalizeTransactionProviderSupplement(input.ProviderDisplay, input.AutomaticRisk)
	if err != nil {
		return TransactionUpsertResult{}, err
	}
	if err := r.requireDB(); err != nil {
		return TransactionUpsertResult{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return TransactionUpsertResult{}, fmt.Errorf("begin company-fund transaction upsert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := ensureCompanyRoutingAction(ctx, tx, input.AuthorizingRoutingActionID); err != nil {
		return TransactionUpsertResult{}, err
	}
	incoming := input.providerFields()
	if !incoming.Metadata.Source.valid() {
		return TransactionUpsertResult{}, fmt.Errorf("unsupported provider fact source %q", incoming.Metadata.Source)
	}
	incomingProvenance, err := r.resolveIncomingTransactionProvenance(ctx, tx, input)
	if err != nil {
		return TransactionUpsertResult{Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: err.Error()}
	}

	for attempt := 0; attempt < 2; attempt++ {
		var existing persistedCompanyFundTransaction
		var found bool
		if input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
			existing, found, err = loadSafeheronCompanyFundTransactionForUpdate(ctx, tx, input)
		} else {
			existing, found, err = loadCompanyFundTransactionForUpdate(ctx, tx, input.MovementKey)
		}
		if err != nil {
			return TransactionUpsertResult{}, err
		}
		if !found {
			merged, decision := MergeMovementProviderFieldsForChannel(input.Channel, MovementState{}, incoming)
			if decision.Outcome == MergeOutcomeQuarantine {
				return TransactionUpsertResult{Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: decision.Reason}
			}
			stableIdentity := input.stableIdentity()
			if err := r.validateProviderTransactionFactOwnership(ctx, tx, input.Channel, stableIdentity.ProviderAccountKey, stableIdentity.ProviderTransactionID, incomingProvenance.ProviderTransactionFactID); err != nil {
				return TransactionUpsertResult{Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: err.Error()}
			}
			id, inserted, err := insertCompanyFundTransaction(ctx, tx, input, merged.Provider, effectiveTransactionStatusRank(input.Channel, 0, input.ProviderStatusRank, merged.Provider.Status), stableIdentity, incomingProvenance)
			if err != nil {
				return TransactionUpsertResult{}, err
			}
			if inserted {
				if supplement.hasValues() {
					if err := updateCompanyFundTransactionProviderSupplement(ctx, tx, id, supplement, true); err != nil {
						return TransactionUpsertResult{}, err
					}
				}
				if err := r.applyCompanyFundTransactionProviderLinkage(ctx, tx, id, input); err != nil {
					return TransactionUpsertResult{}, err
				}
				if err := tx.Commit(); err != nil {
					return TransactionUpsertResult{}, fmt.Errorf("commit company-fund transaction insert: %w", err)
				}
				committed = true
				return TransactionUpsertResult{ID: id, Inserted: true}, nil
			}
			// A concurrent insert won the unique key. Read/lock it in the same
			// transaction and apply the protected update on the next iteration.
			continue
		}
		if existing.Channel != input.Channel {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: fmt.Sprintf("stored transaction channel %q does not match incoming channel %q", existing.Channel, input.Channel)}
		}
		if existing.IdentityAlgorithmVersion != input.IdentityAlgorithmVersion &&
			!(input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion) {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: fmt.Sprintf("stored identity algorithm %q does not match incoming algorithm %q", existing.IdentityAlgorithmVersion, input.IdentityAlgorithmVersion)}
		}
		if input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
			incoming, supplement, err = alignSafeheronIncomingRecognitionSnapshot(existing, incoming, supplement)
			if err != nil {
				return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: err.Error()}
			}
		}
		if err := existing.Provenance.validatePair(); err != nil {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: fmt.Sprintf("stored transaction provenance is invalid: %v", err)}
		}
		stableIdentity, err := resolveStableTransactionIdentity(existing.StableIdentity, input.stableIdentity())
		if err != nil {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: err.Error()}
		}

		merged, decision := MergeMovementProviderFieldsForChannel(input.Channel, MovementState{Provider: existing.Provider}, incoming)
		if decision.Outcome == MergeOutcomeQuarantine {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: decision.Reason}
		}
		provenance := resolveLatestTransactionProvenance(existing.Provenance, incomingProvenance, compareProviderMetadata(existing.Provider.Metadata, incoming.Metadata) > 0)
		if err := r.validateProviderTransactionFactOwnership(ctx, tx, input.Channel, stableIdentity.ProviderAccountKey, stableIdentity.ProviderTransactionID, provenance.ProviderTransactionFactID); err != nil {
			return TransactionUpsertResult{ID: existing.ID, Quarantined: true}, &TransactionQuarantineError{MovementKey: input.MovementKey, Reason: err.Error()}
		}
		providerMetadataWins := compareProviderMetadata(existing.Provider.Metadata, incoming.Metadata) > 0
		statusRank := effectiveTransactionStatusRank(input.Channel, existing.StatusRank, input.ProviderStatusRank, merged.Provider.Status)
		replaceAssetIdentity := shouldReplaceProviderAssetIdentity(existing.Provider, incoming)
		if input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
			replaceAssetIdentity = false
		}
		id, err := updateCompanyFundTransaction(ctx, tx, existing.ID, input, merged.Provider, statusRank, stableIdentity, provenance, replaceAssetIdentity)
		if err != nil {
			return TransactionUpsertResult{}, err
		}
		if supplement.hasValues() {
			if err := updateCompanyFundTransactionProviderSupplement(ctx, tx, id, supplement, providerMetadataWins); err != nil {
				return TransactionUpsertResult{}, err
			}
		}
		if err := r.applyCompanyFundTransactionProviderLinkage(ctx, tx, id, input); err != nil {
			return TransactionUpsertResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return TransactionUpsertResult{}, fmt.Errorf("commit company-fund transaction update: %w", err)
		}
		committed = true
		return TransactionUpsertResult{ID: id}, nil
	}
	return TransactionUpsertResult{}, fmt.Errorf("company-fund transaction %q could not be locked after concurrent insert", input.MovementKey)
}

func ensureCompanyRoutingAction(ctx context.Context, tx *sql.Tx, actionID int64) error {
	if actionID <= 0 {
		return nil
	}
	var locked int64
	err := tx.QueryRowContext(ctx, `SELECT action.id
FROM safeheron_transaction_routing_case_actions action
JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
JOIN safeheron_transaction_routing_cases routing
  ON routing.id=command.case_id AND routing.pending_command_id=command.id
WHERE action.id=$1 AND action.action_type='APPLY_COMPANY'
  AND action.projection_kind='COMPANY' AND action.status IN ('PENDING','RETRYABLE')
  AND command.status='PENDING'
FOR UPDATE OF action,command,routing`, actionID).Scan(&locked)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("company routing action %d is no longer authorized", actionID)
	}
	if err != nil {
		return fmt.Errorf("lock company routing action %d: %w", actionID, err)
	}
	return nil
}

func alignSafeheronIncomingRecognitionSnapshot(
	existing persistedCompanyFundTransaction,
	incoming ProviderOwnedFields,
	supplement normalizedTransactionProviderSupplement,
) (ProviderOwnedFields, normalizedTransactionProviderSupplement, error) {
	if existing.Provider.Asset == nil || incoming.Asset == nil {
		return ProviderOwnedFields{}, normalizedTransactionProviderSupplement{}, fmt.Errorf("Safeheron recognition snapshot requires exact provider asset identity")
	}
	if existing.Provider.Asset.ProviderAssetKey == "" || existing.Provider.Asset.ProviderAssetKey != incoming.Asset.ProviderAssetKey {
		return ProviderOwnedFields{}, normalizedTransactionProviderSupplement{}, fmt.Errorf("Safeheron raw CoinKey conflicts with persisted recognition snapshot")
	}
	currency := existing.Provider.Asset.Currency
	asset := *existing.Provider.Asset
	incoming.Currency = &currency
	incoming.Asset = &asset
	unrecognized := existing.IsUnrecognizedAsset
	supplement.Risk.IsUnrecognizedAsset = &unrecognized
	return incoming, supplement, nil
}

type persistedCompanyFundTransaction struct {
	ID                                 int64
	MovementKey                        string
	Channel                            Channel
	IdentityAlgorithmVersion           string
	ProviderOccurrenceKey              string
	ProviderOccurrenceAlgorithmVersion string
	IsUnrecognizedAsset                bool
	StableIdentity                     transactionStableIdentity
	Provenance                         transactionProviderProvenance
	Provider                           ProviderOwnedFields
	StatusRank                         int
}

func resolveSafeheronPersistedIdentityPair(
	candidates []persistedCompanyFundTransaction,
	input TransactionUpsertInput,
) (persistedCompanyFundTransaction, bool, error) {
	switch len(candidates) {
	case 0:
		return persistedCompanyFundTransaction{}, false, nil
	case 1:
		candidate := candidates[0]
		movementMatches := candidate.MovementKey == input.MovementKey
		occurrenceMatches := candidate.ProviderOccurrenceKey == input.ProviderOccurrenceKey
		// Runtime ingestion never repairs a missing occurrence alias. Historical
		// alias-null repair belongs exclusively to the bounded, quiesced G004
		// scanner so an online replay cannot silently claim the wrong movement.
		if candidate.ProviderOccurrenceAlgorithmVersion != SafeheronOccurrenceAlgorithmVersion || !occurrenceMatches {
			return persistedCompanyFundTransaction{}, false, fmt.Errorf("Safeheron identity pair occurrence mismatch")
		}
		if candidate.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
			if !movementMatches {
				return persistedCompanyFundTransaction{}, false, fmt.Errorf("Safeheron occurrence alias points to another v2 movement")
			}
		} else {
			if candidate.IdentityAlgorithmVersion != MovementIdentityAlgorithmVersion {
				return persistedCompanyFundTransaction{}, false, fmt.Errorf("Safeheron occurrence alias uses unsupported legacy identity algorithm %q", candidate.IdentityAlgorithmVersion)
			}
			if movementMatches {
				return persistedCompanyFundTransaction{}, false, fmt.Errorf("Safeheron v2 movement key is stored with a legacy identity algorithm")
			}
		}
		return candidate, true, nil
	default:
		return persistedCompanyFundTransaction{}, false, fmt.Errorf("Safeheron identity pair resolves to %d rows", len(candidates))
	}
}

func loadCompanyFundTransactionForUpdate(ctx context.Context, tx *sql.Tx, movementKey string) (persistedCompanyFundTransaction, bool, error) {
	row := tx.QueryRowContext(ctx, selectCompanyFundTransactionForUpdateSQL, movementKey)
	var (
		persisted          persistedCompanyFundTransaction
		channel            string
		providerFactID     sql.NullInt64
		latestEventID      sql.NullInt64
		rawSnapshotDigest  string
		amountText         string
		currency           string
		chainCode          string
		providerAsset      string
		assetContract      string
		providerStatus     string
		statusVersion      sql.NullInt64
		providerFactSource string
		lastSeenSource     string
		txHash             string
		occurredAt         sql.NullTime
		completedAt        sql.NullTime
		providerUpdatedAt  sql.NullTime
	)
	if err := row.Scan(
		&persisted.ID,
		&channel,
		&persisted.IdentityAlgorithmVersion,
		&persisted.StableIdentity.ProviderAccountKey,
		&persisted.StableIdentity.ProviderTransactionID,
		&persisted.Provenance.ProviderEventID,
		&persisted.StableIdentity.ProviderMovementID,
		&providerFactID,
		&latestEventID,
		&rawSnapshotDigest,
		&amountText,
		&currency,
		&chainCode,
		&providerAsset,
		&assetContract,
		&providerStatus,
		&statusVersion,
		&providerFactSource,
		&persisted.StatusRank,
		&lastSeenSource,
		&txHash,
		&occurredAt,
		&completedAt,
		&providerUpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistedCompanyFundTransaction{}, false, nil
		}
		return persistedCompanyFundTransaction{}, false, fmt.Errorf("lock company-fund transaction: %w", err)
	}
	persisted.Channel = Channel(channel)
	persisted.Provenance.RawSnapshotDigest = rawSnapshotDigest
	if providerFactID.Valid {
		value := providerFactID.Int64
		persisted.Provenance.ProviderTransactionFactID = &value
	}
	if latestEventID.Valid {
		value := latestEventID.Int64
		persisted.Provenance.LatestProviderEventID = &value
	}
	amount, err := decimal.NewFromString(amountText)
	if err != nil {
		return persistedCompanyFundTransaction{}, false, fmt.Errorf("parse persisted transaction amount: %w", err)
	}
	persisted.Provider.Amount = &amount
	persisted.Provider.Currency = stringValuePointer(currency)
	if chainCode != "" || providerAsset != "" || assetContract != "" {
		asset := AssetIdentity{Currency: currency, ChainCode: chainCode, ProviderAssetKey: providerAsset, ContractAddress: assetContract}
		persisted.Provider.Asset = &asset
	}
	if providerStatus != "" {
		status := LifecycleStatus(providerStatus)
		persisted.Provider.Status = &status
	}
	if statusVersion.Valid {
		value := statusVersion.Int64
		persisted.Provider.Metadata.Revision = &value
	}
	if providerFactSource != "" {
		persisted.Provider.Metadata.Source = ProviderFactSource(providerFactSource)
	} else {
		persisted.Provider.Metadata.Source = providerSourceFromSeenSource(TransactionSeenSource(lastSeenSource))
	}
	if providerUpdatedAt.Valid {
		value := providerUpdatedAt.Time
		persisted.Provider.Metadata.UpdatedAt = &value
	}
	if txHash != "" {
		persisted.Provider.TxHash = stringValuePointer(txHash)
	}
	if occurredAt.Valid {
		value := occurredAt.Time
		persisted.Provider.OccurredAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time
		persisted.Provider.CompletedAt = &value
	}
	return persisted, true, nil
}

func loadSafeheronCompanyFundTransactionForUpdate(
	ctx context.Context,
	tx *sql.Tx,
	input TransactionUpsertInput,
) (persistedCompanyFundTransaction, bool, error) {
	rows, err := tx.QueryContext(ctx, selectSafeheronCompanyFundTransactionForUpdateSQL, input.MovementKey, input.ProviderOccurrenceKey)
	if err != nil {
		return persistedCompanyFundTransaction{}, false, fmt.Errorf("lock Safeheron company-fund identity pair: %w", err)
	}
	defer rows.Close()
	candidates := make([]persistedCompanyFundTransaction, 0, 2)
	for rows.Next() {
		persisted, err := scanSafeheronPersistedCompanyFundTransaction(rows)
		if err != nil {
			return persistedCompanyFundTransaction{}, false, err
		}
		candidates = append(candidates, persisted)
		if len(candidates) > 2 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return persistedCompanyFundTransaction{}, false, fmt.Errorf("iterate locked Safeheron company-fund identities: %w", err)
	}
	return resolveSafeheronPersistedIdentityPair(candidates, input)
}

type companyFundTransactionScanner interface {
	Scan(dest ...any) error
}

func scanSafeheronPersistedCompanyFundTransaction(scanner companyFundTransactionScanner) (persistedCompanyFundTransaction, error) {
	var (
		persisted          persistedCompanyFundTransaction
		channel            string
		providerFactID     sql.NullInt64
		latestEventID      sql.NullInt64
		rawSnapshotDigest  string
		amountText         string
		currency           string
		chainCode          string
		providerAsset      string
		assetContract      string
		providerStatus     string
		statusVersion      sql.NullInt64
		providerFactSource string
		lastSeenSource     string
		txHash             string
		occurredAt         sql.NullTime
		completedAt        sql.NullTime
		providerUpdatedAt  sql.NullTime
	)
	if err := scanner.Scan(
		&persisted.ID,
		&persisted.MovementKey,
		&channel,
		&persisted.IdentityAlgorithmVersion,
		&persisted.ProviderOccurrenceKey,
		&persisted.ProviderOccurrenceAlgorithmVersion,
		&persisted.StableIdentity.ProviderAccountKey,
		&persisted.StableIdentity.ProviderTransactionID,
		&persisted.Provenance.ProviderEventID,
		&persisted.StableIdentity.ProviderMovementID,
		&providerFactID,
		&latestEventID,
		&rawSnapshotDigest,
		&amountText,
		&currency,
		&chainCode,
		&providerAsset,
		&assetContract,
		&persisted.IsUnrecognizedAsset,
		&providerStatus,
		&statusVersion,
		&providerFactSource,
		&persisted.StatusRank,
		&lastSeenSource,
		&txHash,
		&occurredAt,
		&completedAt,
		&providerUpdatedAt,
	); err != nil {
		return persistedCompanyFundTransaction{}, fmt.Errorf("scan locked Safeheron company-fund transaction: %w", err)
	}
	persisted.Channel = Channel(channel)
	persisted.Provenance.RawSnapshotDigest = rawSnapshotDigest
	if providerFactID.Valid {
		value := providerFactID.Int64
		persisted.Provenance.ProviderTransactionFactID = &value
	}
	if latestEventID.Valid {
		value := latestEventID.Int64
		persisted.Provenance.LatestProviderEventID = &value
	}
	amount, err := decimal.NewFromString(amountText)
	if err != nil {
		return persistedCompanyFundTransaction{}, fmt.Errorf("parse persisted Safeheron transaction amount: %w", err)
	}
	persisted.Provider.Amount = &amount
	persisted.Provider.Currency = stringValuePointer(currency)
	asset := AssetIdentity{Currency: currency, ChainCode: chainCode, ProviderAssetKey: providerAsset, ContractAddress: assetContract}
	persisted.Provider.Asset = &asset
	if providerStatus != "" {
		status := LifecycleStatus(providerStatus)
		persisted.Provider.Status = &status
	}
	if statusVersion.Valid {
		value := statusVersion.Int64
		persisted.Provider.Metadata.Revision = &value
	}
	if providerFactSource != "" {
		persisted.Provider.Metadata.Source = ProviderFactSource(providerFactSource)
	} else {
		persisted.Provider.Metadata.Source = providerSourceFromSeenSource(TransactionSeenSource(lastSeenSource))
	}
	if providerUpdatedAt.Valid {
		value := providerUpdatedAt.Time
		persisted.Provider.Metadata.UpdatedAt = &value
	}
	if txHash != "" {
		persisted.Provider.TxHash = stringValuePointer(txHash)
	}
	if occurredAt.Valid {
		value := occurredAt.Time
		persisted.Provider.OccurredAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time
		persisted.Provider.CompletedAt = &value
	}
	return persisted, nil
}

func insertCompanyFundTransaction(ctx context.Context, tx *sql.Tx, input TransactionUpsertInput, provider ProviderOwnedFields, statusRank int, stableIdentity transactionStableIdentity, provenance transactionProviderProvenance) (int64, bool, error) {
	chainCode, providerAssetKey, assetContract := providerAssetColumns(provider.Asset)
	args := []any{
		input.Channel,
		nullableString(stableIdentity.ProviderAccountKey),
		nullableString(stableIdentity.ProviderTransactionID),
		nullableString(provenance.ProviderEventID),
		nullableString(stableIdentity.ProviderMovementID),
		input.MovementIndex,
		input.MovementKey,
		input.IdentityAlgorithmVersion,
		input.MovementKind,
		input.TransferMode,
		input.Direction,
		nullableInt64(input.FromCompanyFundAccountID),
		nullableInt64(input.ToCompanyFundAccountID),
		providerString(provider.Currency, input.Currency),
		chainCode,
		providerAssetKey,
		assetContract,
		providerDecimal(provider.Amount, input.Amount),
		nullableStringPointer(provider.TxHash),
		nullableLifecycleStatus(provider.Status),
		nullableInt64(provider.Metadata.Revision),
		nullableTime(provider.Metadata.UpdatedAt),
		provider.Metadata.Source,
		statusRank,
		nullableTime(provider.OccurredAt),
		nullableTime(provider.CompletedAt),
		input.FirstSeenSource,
		seenSourceForProvider(provider.Metadata.Source, input.FirstSeenSource),
		nullableInt64(provenance.LatestProviderEventID),
		nullableInt64(provenance.ProviderTransactionFactID),
		nullableString(provenance.RawSnapshotDigest),
	}
	query := insertCompanyFundTransactionSQL
	if input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
		query = insertSafeheronCompanyFundTransactionSQL
		args = append(args, input.ProviderOccurrenceKey, input.ProviderOccurrenceAlgorithmVersion)
	}
	row := tx.QueryRowContext(ctx, query, args...)
	var id int64
	if err := row.Scan(&id); err == nil {
		return id, true, nil
	} else if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	} else {
		return 0, false, fmt.Errorf("insert company-fund transaction: %w", err)
	}
}

func updateCompanyFundTransaction(ctx context.Context, tx *sql.Tx, id int64, input TransactionUpsertInput, provider ProviderOwnedFields, statusRank int, stableIdentity transactionStableIdentity, provenance transactionProviderProvenance, replaceAssetIdentity bool) (int64, error) {
	chainCode, providerAssetKey, assetContract := providerAssetColumns(provider.Asset)
	row := tx.QueryRowContext(ctx, updateCompanyFundTransactionSQL,
		id,
		nullableString(stableIdentity.ProviderAccountKey),
		nullableString(stableIdentity.ProviderTransactionID),
		nullableString(provenance.ProviderEventID),
		nullableString(stableIdentity.ProviderMovementID),
		nullableInt64(provenance.ProviderTransactionFactID),
		providerDecimal(provider.Amount, input.Amount),
		providerString(provider.Currency, input.Currency),
		replaceAssetIdentity,
		chainCode,
		providerAssetKey,
		assetContract,
		nullableStringPointer(provider.TxHash),
		nullableLifecycleStatus(provider.Status),
		nullableInt64(provider.Metadata.Revision),
		nullableTime(provider.Metadata.UpdatedAt),
		provider.Metadata.Source,
		statusRank,
		nullableTime(provider.OccurredAt),
		nullableTime(provider.CompletedAt),
		nullableInt64(provenance.LatestProviderEventID),
		nullableString(provenance.RawSnapshotDigest),
		seenSourceForProvider(provider.Metadata.Source, input.FirstSeenSource),
	)
	var updatedID int64
	if err := row.Scan(&updatedID); err != nil {
		return 0, fmt.Errorf("update company-fund transaction: %w", err)
	}
	return updatedID, nil
}

func updateCompanyFundTransactionProviderSupplement(
	ctx context.Context,
	tx *sql.Tx,
	id int64,
	supplement normalizedTransactionProviderSupplement,
	providerMetadataWins bool,
) error {
	display := supplement.Display
	risk := supplement.Risk
	var updatedID int64
	if err := tx.QueryRowContext(ctx, updateCompanyFundTransactionProviderSupplementSQL,
		id,
		providerMetadataWins,
		nullableStringPointer(display.From.AddressOrAccount),
		nullableStringPointer(display.To.AddressOrAccount),
		nullableStringPointer(display.PayerName),
		nullableStringPointer(display.PayeeName),
		nullableStringPointer(display.From.CompanyEntity),
		nullableStringPointer(display.From.FundAccountName),
		nullableStringPointer(display.From.SubAccountName),
		nullableStringPointer(display.From.AccountType),
		nullableStringPointer(display.To.CompanyEntity),
		nullableStringPointer(display.To.FundAccountName),
		nullableStringPointer(display.To.SubAccountName),
		nullableStringPointer(display.To.AccountType),
		nullableTransactionSupplementDecimal(display.FeeAmount),
		nullableStringPointer(display.FeeCurrency),
		nullableStringPointer(display.FeeDetailsJSON),
		nullableInt64(display.BlockHeight),
		nullableStringPointer(display.BlockHash),
		nullableTransactionSupplementBool(risk.IsDust),
		nullableInt64(risk.DustPolicyID),
		nullableTransactionSupplementDecimal(risk.DustThreshold),
		nullableTransactionSupplementBool(risk.IsSourcePhishing),
		nullableTransactionSupplementBool(risk.IsDestinationPhishing),
		nullableTransactionSupplementBool(risk.IsUnrecognizedAsset),
		nullableTransactionSupplementBool(risk.AMLLock),
		nullableAMLScreeningState(risk.AMLScreeningState),
		nullableAMLRiskLevel(risk.AMLRiskLevel),
		nullableStringPointer(risk.RiskFlagsJSON),
		nullableTransactionSupplementBool(risk.AutoExcludedFromSummary),
	).Scan(&updatedID); err != nil {
		return fmt.Errorf("update company-fund transaction provider supplement: %w", err)
	}
	if updatedID != id {
		return fmt.Errorf("provider supplement updated transaction %d, want %d", updatedID, id)
	}
	return nil
}

func (input TransactionUpsertInput) validate() error {
	if !input.Channel.Valid() {
		return fmt.Errorf("unsupported transaction channel %q", input.Channel)
	}
	if err := validateRequiredString("movement key", input.MovementKey, 256); err != nil {
		return err
	}
	if err := validateRequiredString("movement identity algorithm version", input.IdentityAlgorithmVersion, 64); err != nil {
		return err
	}
	if input.Channel == ChannelSafeheron && input.IdentityAlgorithmVersion == SafeheronMovementIdentityAlgorithmVersion {
		if err := validateRequiredString("Safeheron provider occurrence key", input.ProviderOccurrenceKey, 256); err != nil {
			return err
		}
		if input.ProviderOccurrenceAlgorithmVersion != SafeheronOccurrenceAlgorithmVersion {
			return fmt.Errorf("unsupported Safeheron provider occurrence algorithm version %q", input.ProviderOccurrenceAlgorithmVersion)
		}
	}
	if !input.MovementKind.Valid() || !input.TransferMode.Valid() || !input.Direction.Valid() {
		return fmt.Errorf("transaction movement kind, transfer mode, and direction must be supported")
	}
	if err := input.validateProviderLinkage(); err != nil {
		return err
	}
	if input.Amount.IsNegative() || strings.TrimSpace(input.Currency) == "" || input.ProviderStatusRank < 0 {
		return fmt.Errorf("company-fund transaction amount, currency, or status rank is invalid")
	}
	if !input.FirstSeenSource.valid() {
		return fmt.Errorf("unsupported transaction first-seen source %q", input.FirstSeenSource)
	}
	if input.RawSnapshotDigest != "" && !isLowerSHA256Hex(input.RawSnapshotDigest) {
		return fmt.Errorf("transaction raw snapshot digest must be lowercase SHA-256 hex")
	}
	if _, err := normalizeTransactionProviderSupplement(input.ProviderDisplay, input.AutomaticRisk); err != nil {
		return err
	}
	return nil
}

func (input TransactionUpsertInput) providerFields() ProviderOwnedFields {
	provider := input.Provider
	if provider.Amount == nil {
		value := input.Amount
		provider.Amount = &value
	}
	if provider.Currency == nil {
		value := input.Currency
		provider.Currency = &value
	}
	if provider.Asset == nil && (input.Asset.ChainCode != "" || input.Asset.ProviderAssetKey != "" || input.Asset.ContractAddress != "") {
		asset := input.Asset
		asset.Currency = input.Currency
		provider.Asset = &asset
	}
	if provider.OccurredAt == nil && input.OccurredAt != nil {
		value := *input.OccurredAt
		provider.OccurredAt = &value
	}
	if provider.Metadata.Source == "" {
		provider.Metadata.Source = providerSourceFromSeenSource(input.FirstSeenSource)
	}
	return provider
}

func effectiveTransactionStatusRank(channel Channel, existingRank, incomingRank int, status *LifecycleStatus) int {
	rank := existingRank
	if incomingRank > rank {
		rank = incomingRank
	}
	if channel == ChannelSafeheron && status != nil {
		if lifecycleRank, ok := safeheronStatusRank(*status); ok && lifecycleRank > rank {
			rank = lifecycleRank
		}
	}
	return rank
}

func (source TransactionSeenSource) valid() bool {
	return source == TransactionSeenSourceWebhook || source == TransactionSeenSourceReconciliation
}

func providerSourceFromSeenSource(source TransactionSeenSource) ProviderFactSource {
	if source == TransactionSeenSourceReconciliation {
		return ProviderSourceReconciliation
	}
	return ProviderSourceWebhook
}

func seenSourceForProvider(source ProviderFactSource, fallback TransactionSeenSource) TransactionSeenSource {
	if source == ProviderSourceReconciliation || source == ProviderSourceProductDetail {
		return TransactionSeenSourceReconciliation
	}
	if source == ProviderSourceWebhook {
		return TransactionSeenSourceWebhook
	}
	return fallback
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableStringPointer(value *string) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func nullableTransactionSupplementDecimal(value *decimal.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func nullableTransactionSupplementBool(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableAMLScreeningState(value *AMLScreeningState) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableAMLRiskLevel(value *AMLRiskLevel) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableLifecycleStatus(value *LifecycleStatus) any {
	if value == nil || *value == "" {
		return nil
	}
	return *value
}

func providerDecimal(value *decimal.Decimal, fallback decimal.Decimal) string {
	if value == nil {
		return fallback.String()
	}
	return value.String()
}

func providerString(value *string, fallback string) string {
	if value == nil || *value == "" {
		return fallback
	}
	return *value
}

func providerAssetColumns(asset *AssetIdentity) (chainCode, providerAssetKey, assetContract any) {
	if asset == nil {
		return nil, nil, nil
	}
	return nullableString(asset.ChainCode), nullableString(asset.ProviderAssetKey), nullableString(asset.ContractAddress)
}

func stringValuePointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
