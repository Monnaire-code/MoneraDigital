package fundrouting

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"monera-digital/internal/safeheron"
	"monera-digital/internal/wallet/deposit"
)

type VerifiedEventInput struct {
	WebhookEventID    int64
	EventType         string
	PayloadDigest     string
	NetworkFamily     string
	Snapshot          safeheron.TransactionSnapshot
	SuppressOpenAlert bool
	// ExistingProjectionLinks is used only by the audited recovery command. Any
	// supplied ledger output is validated and linked in the same transaction as
	// the routing case, command and raw-event completion.
	ExistingProjectionLinks map[string]ExistingProjectionLink
	// PreserveRawEventStatus is reserved for the audited recovery command. It
	// keeps the original ERROR evidence until existing ledger outputs have been
	// linked to the newly-created routing case. Normal runtime routing must leave
	// this false.
	PreserveRawEventStatus bool
}

type RouteResult struct {
	CaseID             int64
	CommandID          int64
	RoutingIdentityKey string
	Decision           DecisionResult
}

type ExistingProjectionLink struct {
	RoutingIdentityKey       string
	DepositID                int64
	CompanyFundTransactionID int64
	ProviderEventID          int64
}

type PendingEvent struct {
	ID            int64
	EventType     string
	PayloadDigest string
	RawPayload    []byte
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// PreviewVerifiedEvent evaluates the authoritative ownership and admission
// rules without creating routing state or financial projections.
func (r *Repository) PreviewVerifiedEvent(ctx context.Context, input VerifiedEventInput) (_ []RouteResult, err error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("fund routing repository is not configured")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin Safeheron routing preview: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	candidates, err := BuildCandidates(input.Snapshot, input.NetworkFamily)
	if err != nil {
		return nil, err
	}
	results := make([]RouteResult, 0, len(candidates))
	for _, candidate := range candidates {
		decision, found, decisionErr := loadStoredDecision(ctx, tx, candidate.RoutingIdentityKey)
		if decisionErr != nil {
			return nil, decisionErr
		}
		if !found {
			decision, decisionErr = decideCandidate(ctx, tx, input, candidate)
			if decisionErr != nil {
				return nil, decisionErr
			}
		}
		results = append(results, RouteResult{RoutingIdentityKey: candidate.RoutingIdentityKey, Decision: decision})
	}
	return results, nil
}

func loadStoredDecision(ctx context.Context, tx *sql.Tx, identity string) (DecisionResult, bool, error) {
	var rawDecision, rawReason string
	var requiresCustomer, requiresCompany bool
	var customerID sql.NullInt64
	var companyID sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT decision,reason_code,requires_customer_projection,requires_company_projection,
customer_user_id,company_fund_account_id FROM safeheron_transaction_routing_cases WHERE routing_identity_key=$1`, identity).
		Scan(&rawDecision, &rawReason, &requiresCustomer, &requiresCompany, &customerID, &companyID)
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionResult{}, false, nil
	}
	if err != nil {
		return DecisionResult{}, false, fmt.Errorf("load stored Safeheron routing decision: %w", err)
	}
	result := DecisionResult{
		Decision: Decision(rawDecision), Reason: ReasonCode(rawReason),
		RequiresCustomerProjection: requiresCustomer, RequiresCompanyProjection: requiresCompany,
	}
	if customerID.Valid {
		value := int(customerID.Int64)
		result.CustomerUserID = &value
	}
	if companyID.Valid {
		value := companyID.Int64
		result.CompanyFundAccountID = &value
	}
	return result, true, nil
}

func (r *Repository) NextPendingTransactionEvent(ctx context.Context) (*PendingEvent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("fund routing repository is not configured")
	}
	var event PendingEvent
	err := r.db.QueryRowContext(ctx, `
SELECT id, event_type, payload_digest, raw_payload
FROM safeheron_webhook_events
WHERE process_status = 'PENDING'
  AND event_type IN ('TRANSACTION_CREATED', 'TRANSACTION_STATUS_CHANGED')
  AND event_id NOT LIKE 'routing-customer:%'
ORDER BY received_at, id
LIMIT 1`).Scan(&event.ID, &event.EventType, &event.PayloadDigest, &event.RawPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingTransactionEvent
	}
	if err != nil {
		return nil, fmt.Errorf("read pending Safeheron routing event: %w", err)
	}
	return &event, nil
}

func (r *Repository) RejectPendingTransactionEvent(ctx context.Context, eventID int64, code string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE safeheron_webhook_events
SET process_status='ERROR', process_attempts=process_attempts+1, error_message=$2, processed_at=now()
WHERE id=$1 AND process_status='PENDING'`, eventID, code)
	if err != nil {
		return fmt.Errorf("mark invalid Safeheron routing event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("invalid Safeheron routing event %d is no longer pending", eventID)
	}
	return nil
}

func (r *Repository) RouteVerifiedEvent(ctx context.Context, input VerifiedEventInput) (_ []RouteResult, err error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("fund routing repository is not configured")
	}
	if input.WebhookEventID <= 0 {
		return nil, fmt.Errorf("Safeheron webhook event ID must be positive")
	}
	if len(input.PayloadDigest) != 64 {
		return nil, fmt.Errorf("Safeheron payload digest must be SHA-256 hex")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin Safeheron routing transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	candidates, err := BuildCandidates(input.Snapshot, input.NetworkFamily)
	if err != nil {
		return nil, err
	}
	results := make([]RouteResult, 0, len(candidates))
	for _, candidate := range candidates {
		decision, err := decideCandidate(ctx, tx, input, candidate)
		if err != nil {
			return nil, err
		}
		caseID, created, err := insertRoutingCase(ctx, tx, candidate, decision)
		if err != nil {
			return nil, err
		}
		if err := insertRoutingSource(ctx, tx, caseID, input, candidate); err != nil {
			return nil, err
		}
		result := RouteResult{CaseID: caseID, RoutingIdentityKey: candidate.RoutingIdentityKey, Decision: decision}
		if created {
			if decision.Decision == DecisionOpen {
				// STATUS_NOT_TERMINAL is normal mid-flight progress; notify only via SLA
				// escalator if the case remains OPEN past age thresholds.
				if !input.SuppressOpenAlert && decision.Reason != ReasonStatusNotTerminal {
					if err := insertOpenAlert(ctx, tx, caseID, candidate, decision); err != nil {
						return nil, err
					}
				}
			} else {
				commandID, reserveErr := reserveAutomaticProjection(ctx, tx, caseID, candidate, decision)
				if reserveErr != nil {
					return nil, reserveErr
				}
				result.CommandID = commandID
			}
		}
		if link, ok := input.ExistingProjectionLinks[candidate.RoutingIdentityKey]; ok {
			if err := linkExistingProjectionInTransaction(ctx, tx, caseID, link); err != nil {
				return nil, err
			}
		}
		results = append(results, result)
	}
	if !input.PreserveRawEventStatus {
		if err := markRawEventDone(ctx, tx, input.WebhookEventID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit Safeheron routing transaction: %w", err)
	}
	return results, nil
}

func decideCandidate(ctx context.Context, tx *sql.Tx, input VerifiedEventInput, candidate Candidate) (DecisionResult, error) {
	sourceOwner, err := lookupOwnership(ctx, tx, candidate.NetworkFamily, candidate.Occurrence.NormalizedSource)
	if err != nil {
		return DecisionResult{}, err
	}
	destinationOwner, err := lookupOwnership(ctx, tx, candidate.NetworkFamily, candidate.Occurrence.NormalizedDestination)
	if err != nil {
		return DecisionResult{}, err
	}
	return Decide(DecisionInput{
		Candidate:         candidate,
		SourceOwner:       sourceOwner,
		DestinationOwner:  destinationOwner,
		CustomerAdmission: AutomaticCustomerAdmission(input.EventType, input.Snapshot),
	}), nil
}

const lookupOwnershipSQL = `
SELECT ownership.owner_kind,
       pool.assigned_user_id,
       pool.assigned_at,
       account.id,
       COALESCE(account.is_enabled, false),
       account.monitoring_started_at
FROM safeheron_address_ownerships ownership
LEFT JOIN address_pool pool ON pool.id = ownership.address_pool_id
LEFT JOIN company_fund_accounts account ON account.id = ownership.company_fund_account_id
WHERE ownership.network_family = $1
  AND ownership.normalized_address = $2`

func lookupOwnership(ctx context.Context, tx *sql.Tx, networkFamily, address string) (*Ownership, error) {
	var kind string
	var userID sql.NullInt64
	var assignedAt sql.NullTime
	var accountID sql.NullInt64
	var enabled bool
	var monitoringAt sql.NullTime
	err := tx.QueryRowContext(ctx, lookupOwnershipSQL, networkFamily, address).Scan(
		&kind, &userID, &assignedAt, &accountID, &enabled, &monitoringAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lookup Safeheron address ownership: %w", err)
	}
	owner := &Ownership{Kind: OwnerKind(kind), Enabled: enabled}
	switch owner.Kind {
	case OwnerKindCustomerPool:
		if userID.Valid {
			value := int(userID.Int64)
			owner.UserID = &value
		}
		if assignedAt.Valid {
			owner.EffectiveFrom = assignedAt.Time.UTC()
		}
	case OwnerKindCompanyAccount:
		if accountID.Valid {
			value := accountID.Int64
			owner.CompanyFundAccountID = &value
		}
		if monitoringAt.Valid {
			owner.EffectiveFrom = monitoringAt.Time.UTC()
		}
	default:
		return nil, fmt.Errorf("unsupported Safeheron ownership kind %q", kind)
	}
	return owner, nil
}

const insertRoutingCaseSQL = `
INSERT INTO safeheron_transaction_routing_cases
  (routing_identity_key, identity_algorithm_version,
   provider_transaction_group_key, safeheron_tx_key, raw_coin_key,
   network_family, normalized_source, normalized_destination,
   amount, direction, movement_kind, movement_index, duplicate_ordinal,
   effective_event_time, event_time_source, reason_code, decision,
   requires_customer_projection, requires_company_projection,
   customer_user_id, company_fund_account_id)
VALUES
  ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10, 'PRINCIPAL', $11, $12,
   $13, $14, $15, $16, $17, $18, $19, $20)
ON CONFLICT (routing_identity_key) DO NOTHING
RETURNING id`

func insertRoutingCase(ctx context.Context, tx *sql.Tx, candidate Candidate, decision DecisionResult) (int64, bool, error) {
	var caseID int64
	err := tx.QueryRowContext(ctx, insertRoutingCaseSQL,
		candidate.RoutingIdentityKey,
		candidate.IdentityAlgorithmVersion,
		candidate.ProviderTransactionGroupKey,
		candidate.SafeheronTxKey,
		candidate.RawCoinKey,
		candidate.NetworkFamily,
		candidate.Occurrence.NormalizedSource,
		candidate.Occurrence.NormalizedDestination,
		candidate.Occurrence.Amount.String(),
		candidate.Direction,
		candidate.Occurrence.MovementIndex,
		candidate.Occurrence.DuplicateOrdinal,
		nullableTime(candidate.EffectiveEventTime),
		nullableString(candidate.EventTimeSource),
		string(decision.Reason),
		string(decision.Decision),
		decision.RequiresCustomerProjection,
		decision.RequiresCompanyProjection,
		decision.CustomerUserID,
		decision.CompanyFundAccountID,
	).Scan(&caseID)
	if err == nil {
		return caseID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("insert Safeheron routing case: %w", err)
	}
	var exact bool
	if err := tx.QueryRowContext(ctx, `SELECT id,
  identity_algorithm_version=$2 AND provider_transaction_group_key=$3
  AND safeheron_tx_key=$4 AND raw_coin_key=$5 AND network_family=$6
  AND normalized_source=$7 AND normalized_destination=$8 AND amount=$9::numeric
  AND direction=$10 AND movement_index=$11 AND duplicate_ordinal=$12
  AND effective_event_time IS NOT DISTINCT FROM $13::timestamptz
  AND event_time_source IS NOT DISTINCT FROM $14::text
FROM safeheron_transaction_routing_cases WHERE routing_identity_key=$1 FOR UPDATE`,
		candidate.RoutingIdentityKey, candidate.IdentityAlgorithmVersion,
		candidate.ProviderTransactionGroupKey, candidate.SafeheronTxKey,
		candidate.RawCoinKey, candidate.NetworkFamily,
		candidate.Occurrence.NormalizedSource, candidate.Occurrence.NormalizedDestination,
		candidate.Occurrence.Amount.String(), candidate.Direction,
		candidate.Occurrence.MovementIndex, candidate.Occurrence.DuplicateOrdinal,
		nullableTime(candidate.EffectiveEventTime), nullableString(candidate.EventTimeSource),
	).Scan(&caseID, &exact); err != nil {
		return 0, false, fmt.Errorf("lock existing Safeheron routing case: %w", err)
	}
	if !exact {
		return 0, false, fmt.Errorf("%w: existing routing case differs from verified occurrence", ErrRoutingEventConflict)
	}
	return caseID, false, nil
}

const insertRoutingSourceSQL = `
INSERT INTO safeheron_transaction_routing_case_sources
  (case_id, safeheron_webhook_event_id, source_line_slot_key,
   payload_digest, provider_status, provider_status_rank,
   effective_event_time, event_time_source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT DO NOTHING`

func insertRoutingSource(ctx context.Context, tx *sql.Tx, caseID int64, input VerifiedEventInput, candidate Candidate) error {
	status := strings.TrimSpace(input.Snapshot.TransactionStatus)
	rank := deposit.StatusRank(status)
	result, err := tx.ExecContext(ctx, insertRoutingSourceSQL,
		caseID,
		input.WebhookEventID,
		candidate.RoutingIdentityKey,
		input.PayloadDigest,
		status,
		rank,
		nullableTime(candidate.EffectiveEventTime),
		nullableString(candidate.EventTimeSource),
	)
	if err != nil {
		return fmt.Errorf("insert Safeheron routing source: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect Safeheron routing source insert: %w", err)
	}
	if rows == 1 {
		return nil
	}
	var storedCaseID int64
	var digest, storedStatus string
	var storedRank int
	var effectiveTime sql.NullTime
	var timeSource sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT case_id,payload_digest,provider_status,provider_status_rank,
       effective_event_time,event_time_source
FROM safeheron_transaction_routing_case_sources
WHERE safeheron_webhook_event_id=$1 AND source_line_slot_key=$2 FOR UPDATE`,
		input.WebhookEventID, candidate.RoutingIdentityKey,
	).Scan(&storedCaseID, &digest, &storedStatus, &storedRank, &effectiveTime, &timeSource)
	if err != nil {
		return fmt.Errorf("%w: routing source conflict is not exactly idempotent: %v", ErrRoutingEventConflict, err)
	}
	if storedCaseID != caseID || digest != input.PayloadDigest || storedStatus != status || storedRank != rank ||
		!nullableTimeEqual(effectiveTime, candidate.EffectiveEventTime) || !nullableStringEqual(timeSource, candidate.EventTimeSource) {
		return fmt.Errorf("%w: routing source differs from verified occurrence", ErrRoutingEventConflict)
	}
	return nil
}

func nullableTimeEqual(stored sql.NullTime, expected *time.Time) bool {
	if expected == nil {
		return !stored.Valid
	}
	return stored.Valid && stored.Time.Equal(*expected)
}

func nullableStringEqual(stored sql.NullString, expected string) bool {
	expected = strings.TrimSpace(expected)
	return stored.Valid == (expected != "") && (!stored.Valid || stored.String == expected)
}

func insertOpenAlert(ctx context.Context, tx *sql.Tx, caseID int64, candidate Candidate, decision DecisionResult) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO safeheron_transaction_routing_alerts
  (case_id, alert_type, transition_key, severity, payload)
VALUES ($1, 'OPEN', $2, 'WARN', jsonb_build_object(
  'case_id',$1::bigint,
  'reason_code',$3::text,
  'routing_identity_key',$4::text,
  'safeheron_tx_key',$5::text,
  'raw_coin_key',$6::text,
  'network_family',$7::text,
  'source_address',$8::text,
  'destination_address',$9::text,
  'amount',$10::text,
  'movement_index',$11::integer
))
ON CONFLICT (case_id, alert_type, transition_key) DO NOTHING`,
		caseID, "open:"+candidate.RoutingIdentityKey, string(decision.Reason),
		candidate.RoutingIdentityKey, candidate.SafeheronTxKey, candidate.RawCoinKey,
		candidate.NetworkFamily, candidate.Occurrence.NormalizedSource,
		candidate.Occurrence.NormalizedDestination, candidate.Occurrence.Amount.String(),
		candidate.Occurrence.MovementIndex,
	)
	if err != nil {
		return fmt.Errorf("insert Safeheron routing OPEN alert: %w", err)
	}
	return nil
}

func reserveAutomaticProjection(ctx context.Context, tx *sql.Tx, caseID int64, candidate Candidate, decision DecisionResult) (int64, error) {
	requestDigest := routingRequestDigest(candidate, decision)
	var commandID int64
	err := tx.QueryRowContext(ctx, `
INSERT INTO safeheron_transaction_routing_case_commands
  (case_id, command_type, initiator, actor_scope, reason,
   idempotency_key, request_digest, expected_case_version)
VALUES ($1, 'AUTO_ROUTE', 'SYSTEM', 'system:auto-route', $2, $3, $4, 1)
RETURNING id`,
		caseID,
		string(decision.Reason),
		candidate.RoutingIdentityKey,
		requestDigest,
	).Scan(&commandID)
	if err != nil {
		return 0, fmt.Errorf("reserve Safeheron automatic routing command: %w", err)
	}
	if decision.RequiresCustomerProjection {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO safeheron_transaction_routing_case_actions
  (command_id, action_type, projection_kind, target_user_id)
VALUES ($1, 'APPLY_CUSTOMER', 'CUSTOMER', $2)`, commandID, decision.CustomerUserID); err != nil {
			return 0, fmt.Errorf("reserve Safeheron customer projection: %w", err)
		}
	}
	if decision.RequiresCompanyProjection {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO safeheron_transaction_routing_case_actions
  (command_id, action_type, projection_kind, target_company_fund_account_id)
VALUES ($1, 'APPLY_COMPANY', 'COMPANY', $2)`, commandID, decision.CompanyFundAccountID); err != nil {
			return 0, fmt.Errorf("reserve Safeheron company projection: %w", err)
		}
	}
	result, err := tx.ExecContext(ctx, `
UPDATE safeheron_transaction_routing_cases
SET pending_command_id = $1, updated_at = now()
WHERE id = $2 AND pending_command_id IS NULL`, commandID, caseID)
	if err != nil {
		return 0, fmt.Errorf("attach Safeheron routing command: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return 0, fmt.Errorf("attach Safeheron routing command affected %d rows", rows)
	}
	return commandID, nil
}

func markRawEventDone(ctx context.Context, tx *sql.Tx, eventID int64) error {
	result, err := tx.ExecContext(ctx, `
UPDATE safeheron_webhook_events
SET process_status = 'DONE', processed_at = now(), error_message = NULL
WHERE id = $1`, eventID)
	if err != nil {
		return fmt.Errorf("mark routed Safeheron raw event DONE: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("mark routed Safeheron raw event DONE affected %d rows", rows)
	}
	return nil
}

func routingRequestDigest(candidate Candidate, decision DecisionResult) string {
	value := strings.Join([]string{
		candidate.RoutingIdentityKey,
		string(decision.Decision),
		string(decision.Reason),
		fmt.Sprint(decision.CustomerUserID),
		fmt.Sprint(decision.CompanyFundAccountID),
	}, "\x1f")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
