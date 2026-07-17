package fundrouting

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"monera-digital/internal/companyfund"
)

type ProviderEventInserter interface {
	InsertProviderEvent(context.Context, companyfund.ProviderEventInput) (companyfund.ProviderEventInsertResult, error)
}

type ProjectionWorker struct {
	db       *sql.DB
	events   ProviderEventInserter
	interval time.Duration
	workerID string
}

const maxProjectionActionAttempts = 8

var errProjectionResultConflict = errors.New("fund routing projection result conflict")

type projectionAction struct {
	ID                 int64
	Type               string
	CaseID             int64
	CommandID          int64
	RoutingIdentityKey string
	WebhookEventID     int
	ProviderEventID    string
	EventType          string
	PayloadDigest      string
	TargetUserID       sql.NullInt64
	TargetCompanyID    sql.NullInt64
}

func NewProjectionWorker(db *sql.DB, events ProviderEventInserter) (*ProjectionWorker, error) {
	if db == nil || events == nil {
		return nil, fmt.Errorf("fund routing projection database and provider event inserter are required")
	}
	return &ProjectionWorker{db: db, events: events, interval: time.Second, workerID: newProjectionWorkerID()}, nil
}

func (worker *ProjectionWorker) ProcessOne(ctx context.Context) (bool, error) {
	action, err := worker.nextAction(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch action.Type {
	case "APPLY_CUSTOMER":
		return true, worker.applyCustomer(ctx, action)
	case "APPLY_COMPANY":
		return true, worker.applyCompany(ctx, action)
	case "DISMISS", "REOPEN", "FINALIZE_COMPANY_ONLY":
		return true, worker.applyControl(ctx, action)
	default:
		return false, nil
	}
}

func (worker *ProjectionWorker) nextAction(ctx context.Context) (projectionAction, error) {
	tx, err := worker.db.BeginTx(ctx, nil)
	if err != nil {
		return projectionAction{}, err
	}
	var actionID int64
	err = tx.QueryRowContext(ctx, `
WITH candidate AS (
  SELECT action.id
  FROM safeheron_transaction_routing_case_actions action
	JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
	JOIN safeheron_transaction_routing_cases routing
	  ON routing.id=command.case_id AND routing.pending_command_id=command.id
	  WHERE action.status IN ('PENDING','RETRYABLE')
	AND command.status='PENDING'
	AND NOT EXISTS (
	  SELECT 1 FROM safeheron_transaction_routing_case_actions predecessor
	  WHERE predecessor.command_id=action.command_id AND predecessor.id<action.id
	    AND predecessor.status<>'APPLIED'
	)
    AND (action.next_attempt_at IS NULL OR action.next_attempt_at <= now())
    AND (action.lease_expires_at IS NULL OR action.lease_expires_at <= now())
    AND action.action_type IN ('APPLY_CUSTOMER','APPLY_COMPANY','FINALIZE_COMPANY_ONLY','DISMISS','REOPEN')
  ORDER BY action.id
  FOR UPDATE SKIP LOCKED
  LIMIT 1
)
UPDATE safeheron_transaction_routing_case_actions action
SET status='PENDING', next_attempt_at=NULL,
    lease_owner=$1, lease_expires_at=now()+interval '60 seconds', updated_at=now()
FROM candidate
WHERE action.id=candidate.id
RETURNING action.id`, worker.workerID).Scan(&actionID)
	if err != nil {
		_ = tx.Rollback()
		return projectionAction{}, err
	}
	var action projectionAction
	err = tx.QueryRowContext(ctx, `
SELECT action.id, action.action_type, command.case_id, command.id,
       routing.routing_identity_key, source.safeheron_webhook_event_id,
       webhook.event_id, webhook.event_type, source.payload_digest,
       action.target_user_id, action.target_company_fund_account_id
FROM safeheron_transaction_routing_case_actions action
JOIN safeheron_transaction_routing_case_commands command ON command.id = action.command_id
JOIN safeheron_transaction_routing_cases routing ON routing.id = command.case_id
JOIN LATERAL (
  SELECT source.* FROM safeheron_transaction_routing_case_sources source
  WHERE source.case_id = routing.id ORDER BY source.provider_status_rank DESC, source.id DESC LIMIT 1
) source ON true
JOIN safeheron_webhook_events webhook ON webhook.id = source.safeheron_webhook_event_id
WHERE action.id=$1 AND action.lease_owner=$2`, actionID, worker.workerID).Scan(&action.ID, &action.Type, &action.CaseID, &action.CommandID, &action.RoutingIdentityKey,
		&action.WebhookEventID, &action.ProviderEventID, &action.EventType, &action.PayloadDigest,
		&action.TargetUserID, &action.TargetCompanyID)
	if err != nil {
		_ = tx.Rollback()
		return projectionAction{}, err
	}
	if err = tx.Commit(); err != nil {
		return projectionAction{}, err
	}
	return action, nil
}

func (worker *ProjectionWorker) applyCustomer(ctx context.Context, action projectionAction) error {
	if !action.TargetUserID.Valid || action.TargetUserID.Int64 <= 0 {
		return worker.deadAction(ctx, action, "CUSTOMER_TARGET_INVALID", "customer target is unavailable")
	}
	syntheticEventID := fmt.Sprintf("routing-customer:%d", action.ID)
	result, err := worker.db.ExecContext(ctx, `
WITH authorized AS (
  SELECT 1
  FROM safeheron_transaction_routing_case_actions action
  JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
  JOIN safeheron_transaction_routing_cases current_case
    ON current_case.id=command.case_id AND current_case.pending_command_id=command.id
  WHERE action.id=$4 AND action.lease_owner=$5 AND action.lease_expires_at>now()
    AND action.status IN ('PENDING','RETRYABLE') AND command.status='PENDING'
  FOR UPDATE OF action,command,current_case
)
INSERT INTO safeheron_webhook_events
  (event_id, event_type, safeheron_tx_key, customer_ref_id, raw_payload, payload_digest,
   process_status, authorizing_routing_action_id)
SELECT $1, webhook.event_type, routing.safeheron_tx_key, webhook.customer_ref_id,
       webhook.raw_payload, webhook.payload_digest, 'PENDING', $4
FROM safeheron_transaction_routing_cases routing
JOIN authorized ON true
JOIN LATERAL (
  SELECT source.* FROM safeheron_transaction_routing_case_sources source
  WHERE source.case_id=routing.id ORDER BY source.provider_status_rank DESC, source.id DESC LIMIT 1
) source ON true
JOIN safeheron_webhook_events webhook ON webhook.id=source.safeheron_webhook_event_id
JOIN safeheron_address_ownerships ownership
  ON ownership.network_family=routing.network_family
 AND ownership.normalized_address=routing.normalized_destination
JOIN address_pool pool ON pool.id=ownership.address_pool_id
WHERE routing.id=$2 AND routing.direction='INFLOW'
  AND routing.movement_index=0
  AND routing.effective_event_time IS NOT NULL
  AND routing.effective_event_time >= pool.assigned_at
  AND upper(webhook.event_type)='TRANSACTION_STATUS_CHANGED'
  AND upper(COALESCE(webhook.raw_payload->'eventDetail'->>'transactionStatus',''))
    IN ('COMPLETED','FAILED','CANCELLED','REJECTED')
  AND pool.assigned_user_id=$3
  AND COALESCE(jsonb_array_length(webhook.raw_payload->'eventDetail'->'destinationAddressList'),0)=0
  AND btrim(COALESCE(webhook.raw_payload->'eventDetail'->>'destinationAddress','')) <> ''
ON CONFLICT (event_id) DO NOTHING`, syntheticEventID, action.CaseID, action.TargetUserID.Int64, action.ID, worker.workerID)
	if err != nil {
		return worker.retryAction(ctx, action, "CUSTOMER_EVENT_INSERT_FAILED", err)
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return worker.retryAction(ctx, action, "CUSTOMER_EVENT_INSERT_UNKNOWN", rowsErr)
	}
	if rows == 0 {
		var exists bool
		if lookupErr := worker.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM safeheron_webhook_events WHERE event_id=$1)`, syntheticEventID).Scan(&exists); lookupErr != nil {
			return worker.retryAction(ctx, action, "CUSTOMER_EVENT_LOOKUP_FAILED", lookupErr)
		}
		if !exists {
			return worker.deadAction(ctx, action, "CUSTOMER_ADMISSION_REJECTED", "customer ownership, direction, or single-movement admission failed")
		}
	}
	var depositID int64
	var userID int64
	var exactAmount, exactDestination, exactSource, exactAsset, exactChain bool
	err = worker.db.QueryRowContext(ctx, `
SELECT deposit.id, deposit.user_id, deposit.amount = routing.amount,
       lower(COALESCE(deposit.to_address,'')) = lower(routing.normalized_destination),
       lower(COALESCE(deposit.from_address,'')) = lower(routing.normalized_source),
       deposit.safeheron_coin_key = routing.raw_coin_key,
       EXISTS (SELECT 1 FROM coin_chains asset JOIN chains chain ON chain.code=asset.chain_code
         WHERE asset.id=deposit.coin_chain_id AND asset.safeheron_coin_key=routing.raw_coin_key
           AND upper(chain.network_family)=routing.network_family)
FROM deposits deposit
JOIN safeheron_transaction_routing_cases routing ON routing.safeheron_tx_key=deposit.safeheron_tx_key
WHERE routing.id=$1`, action.CaseID).Scan(&depositID, &userID, &exactAmount, &exactDestination, &exactSource, &exactAsset, &exactChain)
	if errors.Is(err, sql.ErrNoRows) {
		return worker.retryAction(ctx, action, "WAITING_CUSTOMER_PROJECTION", nil)
	}
	if err != nil {
		return worker.retryAction(ctx, action, "CUSTOMER_RESULT_LOOKUP_FAILED", err)
	}
	if userID != action.TargetUserID.Int64 || !exactAmount || !exactDestination || !exactSource || !exactAsset || !exactChain {
		return worker.deadAction(ctx, action, "CUSTOMER_RESULT_CONFLICT", "deposit result does not strictly match routing target")
	}
	err = worker.completeCustomer(ctx, action, depositID)
	if errors.Is(err, errProjectionResultConflict) {
		return worker.deadAction(ctx, action, "CUSTOMER_RESULT_CONFLICT", err.Error())
	}
	return err
}

func (worker *ProjectionWorker) completeCustomer(ctx context.Context, action projectionAction, depositID int64) (err error) {
	tx, err := worker.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = ensureActionLease(ctx, tx, action, worker.workerID); err != nil {
		return err
	}
	digest := routingResultDigest(action.RoutingIdentityKey, depositID)
	if _, err = tx.ExecContext(ctx, `
INSERT INTO safeheron_transaction_routing_case_results
  (case_id, action_id, projection_kind, deposit_id, result_digest)
VALUES ($1,$2,'CUSTOMER',$3,$4)
ON CONFLICT DO NOTHING`, action.CaseID, action.ID, depositID, digest); err != nil {
		return err
	}
	var storedActionID, storedDepositID int64
	var storedDigest string
	if err = tx.QueryRowContext(ctx, `SELECT action_id, deposit_id, result_digest
FROM safeheron_transaction_routing_case_results
WHERE case_id=$1 AND projection_kind='CUSTOMER' FOR UPDATE`, action.CaseID).Scan(&storedActionID, &storedDepositID, &storedDigest); err != nil {
		return fmt.Errorf("%w: customer result missing after insert: %v", errProjectionResultConflict, err)
	}
	if storedActionID != action.ID || storedDepositID != depositID || storedDigest != digest {
		return fmt.Errorf("%w: customer result differs from action", errProjectionResultConflict)
	}
	caseUpdate, err := tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_cases
	SET deposit_id=$1, updated_at=now() WHERE id=$2 AND (deposit_id IS NULL OR deposit_id=$1)`, depositID, action.CaseID)
	if err != nil {
		return err
	}
	if rows, rowsErr := caseUpdate.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("%w: customer case result link differs", errProjectionResultConflict)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='APPLIED', completed_at=now(), updated_at=now(), next_attempt_at=NULL,
    lease_owner=NULL, lease_expires_at=NULL, last_error_code=NULL, last_error_detail=NULL
WHERE id=$1`, action.ID); err != nil {
		return err
	}
	if err = completeCommandIfReady(ctx, tx, action.CommandID, action.CaseID); err != nil {
		return err
	}
	return tx.Commit()
}

func (worker *ProjectionWorker) deadAction(ctx context.Context, action projectionAction, code, detail string) (err error) {
	tx, err := worker.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = ensureActionLease(ctx, tx, action, worker.workerID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='DEAD', completed_at=now(), updated_at=now(), next_attempt_at=NULL,
    lease_owner=NULL, lease_expires_at=NULL, last_error_code=$2, last_error_detail=$3
WHERE id=$1`, action.ID, code, detail); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_commands
SET status='BLOCKED', completed_at=now() WHERE id=$1 AND status='PENDING'`, action.CommandID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='DEAD', completed_at=now(), updated_at=now(), next_attempt_at=NULL,
    lease_owner=NULL, lease_expires_at=NULL, last_error_code='COMMAND_BLOCKED_BY_SIBLING',
    last_error_detail='another projection in the command reached a terminal failure'
WHERE command_id=$1 AND id<>$2 AND status IN ('PENDING','RETRYABLE')`, action.CommandID, action.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO safeheron_transaction_routing_alerts
  (case_id, alert_type, transition_key, severity, payload)
VALUES ($1,'ACTION_DEAD',$2,'ERROR',jsonb_build_object('error_code',$3::text))
ON CONFLICT (case_id, alert_type, transition_key) DO NOTHING`, action.CaseID, fmt.Sprintf("command:%d:action:%d", action.CommandID, action.ID), code); err != nil {
		return err
	}
	return tx.Commit()
}

func (worker *ProjectionWorker) applyCompany(ctx context.Context, action projectionAction) error {
	if !action.TargetCompanyID.Valid || action.TargetCompanyID.Int64 <= 0 {
		return worker.deadAction(ctx, action, "COMPANY_TARGET_INVALID", "company account target is unavailable")
	}
	var providerEventReady, conflictingProviderEvent bool
	if err := worker.db.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM company_fund_provider_events
  WHERE authorizing_routing_action_id=$1
    AND authorized_safeheron_occurrence_key=$2
), EXISTS (
  SELECT 1 FROM company_fund_provider_events
  WHERE safeheron_webhook_event_id=$3
    AND (authorizing_routing_action_id IS DISTINCT FROM $1
      OR authorized_safeheron_occurrence_key IS DISTINCT FROM $2)
)`, action.ID, action.RoutingIdentityKey, action.WebhookEventID).Scan(&providerEventReady, &conflictingProviderEvent); err != nil {
		return worker.retryAction(ctx, action, "PROVIDER_EVENT_LOOKUP_FAILED", err)
	}
	if conflictingProviderEvent {
		return worker.deadAction(ctx, action, "PROVIDER_EVENT_CONFLICT", "raw webhook is already bound to a different or legacy provider event")
	}
	if !providerEventReady {
		safeheronEventID := action.WebhookEventID
		if _, err := worker.events.InsertProviderEvent(ctx, companyfund.ProviderEventInput{
			Channel:                          companyfund.ChannelSafeheron,
			ProviderEventID:                  fmt.Sprintf("routing-company:%d", action.ID),
			EventType:                        action.EventType,
			SourceKind:                       companyfund.ProviderEventSourceExistingSafeheronWebhookRef,
			SafeheronWebhookEventID:          &safeheronEventID,
			SourcePayloadDigest:              action.PayloadDigest,
			AuthorizedSafeheronOccurrenceKey: action.RoutingIdentityKey,
			AuthorizingRoutingActionID:       action.ID,
			AuthorizingRoutingLeaseOwner:     worker.workerID,
		}); err != nil {
			return worker.retryAction(ctx, action, "PROVIDER_EVENT_INSERT_FAILED", err)
		}
	}
	var transactionID int64
	var exactAccount, exactAsset, exactAmount, exactSource, exactDestination, exactDirection bool
	err := worker.db.QueryRowContext(ctx, `
SELECT movement.id,
       ($2 = movement.from_company_fund_account_id OR $2 = movement.to_company_fund_account_id),
       movement.provider_asset_key=routing.raw_coin_key,
       movement.amount=routing.amount,
       lower(COALESCE(movement.from_address_or_account,''))=lower(routing.normalized_source),
       lower(COALESCE(movement.to_address_or_account,''))=lower(routing.normalized_destination),
       movement.transaction_direction=routing.direction
FROM company_fund_transactions movement
JOIN safeheron_transaction_routing_cases routing ON routing.routing_identity_key=movement.provider_occurrence_key
WHERE movement.channel='SAFEHERON' AND movement.provider_occurrence_key=$1`, action.RoutingIdentityKey, action.TargetCompanyID.Int64).
		Scan(&transactionID, &exactAccount, &exactAsset, &exactAmount, &exactSource, &exactDestination, &exactDirection)
	if errors.Is(err, sql.ErrNoRows) {
		return worker.retryAction(ctx, action, "WAITING_COMPANY_PROJECTION", nil)
	}
	if err != nil {
		return worker.retryAction(ctx, action, "COMPANY_RESULT_LOOKUP_FAILED", err)
	}
	if !exactAccount || !exactAsset || !exactAmount || !exactSource || !exactDestination || !exactDirection {
		return worker.deadAction(ctx, action, "COMPANY_RESULT_CONFLICT", "company movement result does not match routing target account")
	}
	err = worker.completeCompany(ctx, action, transactionID)
	if errors.Is(err, errProjectionResultConflict) {
		return worker.deadAction(ctx, action, "COMPANY_RESULT_CONFLICT", err.Error())
	}
	return err
}

func (worker *ProjectionWorker) completeCompany(ctx context.Context, action projectionAction, transactionID int64) (err error) {
	tx, err := worker.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = ensureActionLease(ctx, tx, action, worker.workerID); err != nil {
		return err
	}
	digest := routingResultDigest(action.RoutingIdentityKey, transactionID)
	if _, err = tx.ExecContext(ctx, `
INSERT INTO safeheron_transaction_routing_case_results
  (case_id, action_id, projection_kind, company_fund_transaction_id, result_digest)
VALUES ($1,$2,'COMPANY',$3,$4)
ON CONFLICT DO NOTHING`, action.CaseID, action.ID, transactionID, digest); err != nil {
		return err
	}
	var storedActionID, storedTransactionID int64
	var storedDigest string
	if err = tx.QueryRowContext(ctx, `SELECT action_id, company_fund_transaction_id, result_digest
FROM safeheron_transaction_routing_case_results
WHERE case_id=$1 AND projection_kind='COMPANY' FOR UPDATE`, action.CaseID).Scan(&storedActionID, &storedTransactionID, &storedDigest); err != nil {
		return fmt.Errorf("%w: company result missing after insert: %v", errProjectionResultConflict, err)
	}
	if storedActionID != action.ID || storedTransactionID != transactionID || storedDigest != digest {
		return fmt.Errorf("%w: company result differs from action", errProjectionResultConflict)
	}
	caseUpdate, err := tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_cases
	SET company_fund_transaction_id=$1, updated_at=now() WHERE id=$2 AND (company_fund_transaction_id IS NULL OR company_fund_transaction_id=$1)`, transactionID, action.CaseID)
	if err != nil {
		return err
	}
	if rows, rowsErr := caseUpdate.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("%w: company case result link differs", errProjectionResultConflict)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='APPLIED', completed_at=now(), updated_at=now(), next_attempt_at=NULL,
    lease_owner=NULL, lease_expires_at=NULL, last_error_code=NULL, last_error_detail=NULL
WHERE id=$1`, action.ID); err != nil {
		return err
	}
	if err = completeCommandIfReady(ctx, tx, action.CommandID, action.CaseID); err != nil {
		return err
	}
	return tx.Commit()
}

func (worker *ProjectionWorker) applyControl(ctx context.Context, action projectionAction) (err error) {
	tx, err := worker.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = ensureActionLease(ctx, tx, action, worker.workerID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='APPLIED', completed_at=now(), updated_at=now(),
    lease_owner=NULL, lease_expires_at=NULL
WHERE id=$1`, action.ID); err != nil {
		return err
	}
	if err = completeCommandIfReady(ctx, tx, action.CommandID, action.CaseID); err != nil {
		return err
	}
	return tx.Commit()
}

func completeCommandIfReady(ctx context.Context, tx *sql.Tx, commandID, caseID int64) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM safeheron_transaction_routing_case_actions WHERE command_id=$1 AND status <> 'APPLIED'`, commandID).Scan(&pending); err != nil {
		return err
	}
	if pending != 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_commands SET status='APPLIED', completed_at=now() WHERE id=$1`, commandID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL, version=version+1, updated_at=now() WHERE id=$1 AND pending_command_id=$2`, caseID, commandID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("fund routing command %d lost current-case fence", commandID)
	}
	return nil
}

func ensureActionLease(ctx context.Context, tx *sql.Tx, action projectionAction, workerID string) error {
	var current int64
	if err := tx.QueryRowContext(ctx, `
SELECT action.id FROM safeheron_transaction_routing_case_actions action
JOIN safeheron_transaction_routing_case_commands command ON command.id=action.command_id
JOIN safeheron_transaction_routing_cases routing ON routing.id=command.case_id
WHERE action.id=$1 AND action.lease_owner=$2 AND action.lease_expires_at > now()
  AND command.id=$3 AND command.status='PENDING'
  AND routing.id=$4 AND routing.pending_command_id=command.id
FOR UPDATE OF action, command, routing`, action.ID, workerID, action.CommandID, action.CaseID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("fund routing action %d lease or command fence lost", action.ID)
		}
		return err
	}
	return nil
}

func (worker *ProjectionWorker) retryAction(ctx context.Context, action projectionAction, code string, cause error) error {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	var attemptCount int
	if err := worker.db.QueryRowContext(ctx, `SELECT attempt_count FROM safeheron_transaction_routing_case_actions
WHERE id=$1 AND lease_owner=$2 AND lease_expires_at > now()`, action.ID, worker.workerID).Scan(&attemptCount); err != nil {
		return err
	}
	if attemptCount+1 >= maxProjectionActionAttempts && !strings.HasPrefix(code, "WAITING_") {
		return worker.deadAction(ctx, action, "RETRY_EXHAUSTED_"+code, detail)
	}
	result, err := worker.db.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions action
SET status='RETRYABLE', attempt_count=attempt_count+1,
    next_attempt_at=now()+(power(2,least(attempt_count,6))*interval '5 seconds'),
    lease_owner=NULL, lease_expires_at=NULL,
    last_error_code=$2, last_error_detail=$3, updated_at=now()
WHERE action.id=$1 AND action.lease_owner=$4
  AND EXISTS (
    SELECT 1 FROM safeheron_transaction_routing_case_commands command
    JOIN safeheron_transaction_routing_cases routing
      ON routing.id=command.case_id AND routing.pending_command_id=command.id
    WHERE command.id=action.command_id AND command.status='PENDING'
  )`, action.ID, code, detail, worker.workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("fund routing action %d lease lost", action.ID)
	}
	return nil
}

func newProjectionWorkerID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "fund-routing-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("fund-routing-%d", time.Now().UnixNano())
}

func (worker *ProjectionWorker) Run(ctx context.Context) {
	log.Printf("fund routing projection worker started")
	defer log.Printf("fund routing projection worker stopped")
	ticker := time.NewTicker(worker.interval)
	defer ticker.Stop()
	for {
		for {
			processed, err := worker.ProcessOne(ctx)
			if err != nil {
				log.Printf("fund routing projection worker error: %v", err)
				break
			}
			if !processed {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func routingResultDigest(identity string, resultID int64) string {
	return routingRequestDigest(Candidate{RoutingIdentityKey: identity}, DecisionResult{Reason: ReasonCode(fmt.Sprintf("company:%d", resultID))})
}
