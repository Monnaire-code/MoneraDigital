package fundrouting

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// LinkExistingProjectionResult attaches already-posted ledger outputs to the
// authoritative routing state machine. It never creates financial output and
// validates the exact target, asset, amount, address and occurrence before
// completing the reserved actions.
func (r *Repository) LinkExistingProjectionResult(ctx context.Context, link ExistingProjectionLink) (err error) {
	if r == nil || r.db == nil {
		return fmt.Errorf("fund routing repository is not configured")
	}
	if strings.TrimSpace(link.RoutingIdentityKey) == "" || (link.DepositID <= 0 && link.CompanyFundTransactionID <= 0 && link.ProviderEventID <= 0) {
		return fmt.Errorf("existing projection link requires an identity and result")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var caseID int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM safeheron_transaction_routing_cases WHERE routing_identity_key=$1`, link.RoutingIdentityKey).Scan(&caseID); err != nil {
		return fmt.Errorf("load recovery routing case: %w", err)
	}
	if err = linkExistingProjectionInTransaction(ctx, tx, caseID, link); err != nil {
		return err
	}
	return tx.Commit()
}

func linkExistingProjectionInTransaction(ctx context.Context, tx *sql.Tx, caseID int64, link ExistingProjectionLink) error {
	var commandID sql.NullInt64
	var existingDepositID, existingCompanyID sql.NullInt64
	var decision string
	if err := tx.QueryRowContext(ctx, `SELECT pending_command_id,decision,deposit_id,company_fund_transaction_id
FROM safeheron_transaction_routing_cases WHERE id=$1 FOR UPDATE`, caseID).
		Scan(&commandID, &decision, &existingDepositID, &existingCompanyID); err != nil {
		return fmt.Errorf("lock recovery routing case: %w", err)
	}
	if !commandID.Valid {
		return validateAlreadyLinkedProjection(ctx, tx, caseID, link)
	}
	if err := validateAndLinkExistingProjection(ctx, tx, caseID, commandID.Int64, decision, existingDepositID, existingCompanyID, link); err != nil {
		return err
	}
	if link.ProviderEventID > 0 {
		if err := validateAndBindExistingProviderEvent(ctx, tx, caseID, commandID.Int64, link); err != nil {
			return err
		}
	}
	return completeCommandIfReady(ctx, tx, commandID.Int64, caseID)
}

func validateAlreadyLinkedProjection(ctx context.Context, tx *sql.Tx, caseID int64, link ExistingProjectionLink) error {
	var depositID, companyID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT deposit_id,company_fund_transaction_id
FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID).Scan(&depositID, &companyID); err != nil {
		return err
	}
	if (link.DepositID > 0 && (!depositID.Valid || depositID.Int64 != link.DepositID)) ||
		(link.CompanyFundTransactionID > 0 && (!companyID.Valid || companyID.Int64 != link.CompanyFundTransactionID)) {
		return fmt.Errorf("existing projection link conflicts with completed routing case")
	}
	if link.ProviderEventID > 0 {
		var bound bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM company_fund_provider_events event
  JOIN safeheron_transaction_routing_case_actions action
    ON action.id=event.authorizing_routing_action_id AND action.command_id IN (
      SELECT id FROM safeheron_transaction_routing_case_commands WHERE case_id=$1
    )
  WHERE event.id=$2 AND event.authorized_safeheron_occurrence_key=$3
)`, caseID, link.ProviderEventID, link.RoutingIdentityKey).Scan(&bound); err != nil {
			return err
		}
		if !bound {
			return fmt.Errorf("existing provider event conflicts with completed routing case")
		}
	}
	return nil
}

func validateAndBindExistingProviderEvent(ctx context.Context, tx *sql.Tx, caseID, commandID int64, link ExistingProjectionLink) error {
	var actionID int64
	var exact bool
	err := tx.QueryRowContext(ctx, `SELECT action.id,
  event.channel='SAFEHERON'
  AND event.source_kind='EXISTING_SAFEHERON_WEBHOOK_REF'
  AND event.safeheron_webhook_event_id=source.safeheron_webhook_event_id
  AND event.source_payload_digest=source.payload_digest
  AND event.event_type=webhook.event_type
  AND event.event_state IN ('PENDING','FAILED')
  AND event.authorized_safeheron_occurrence_key IS NULL
  AND event.authorizing_routing_action_id IS NULL
FROM safeheron_transaction_routing_cases routing
JOIN safeheron_transaction_routing_case_actions action
  ON action.command_id=$2 AND action.projection_kind='COMPANY' AND action.status IN ('PENDING','RETRYABLE')
JOIN LATERAL (
  SELECT source.* FROM safeheron_transaction_routing_case_sources source
  WHERE source.case_id=routing.id ORDER BY source.provider_status_rank DESC,source.id DESC LIMIT 1
) source ON true
JOIN safeheron_webhook_events webhook ON webhook.id=source.safeheron_webhook_event_id
JOIN company_fund_provider_events event ON event.id=$3
WHERE routing.id=$1 FOR UPDATE OF action,event`, caseID, commandID, link.ProviderEventID).Scan(&actionID, &exact)
	if err != nil {
		return fmt.Errorf("load existing provider event recovery target: %w", err)
	}
	if !exact {
		return fmt.Errorf("existing provider event does not exactly match recovery routing source")
	}
	result, err := tx.ExecContext(ctx, `UPDATE company_fund_provider_events
SET authorized_safeheron_occurrence_key=$1,authorizing_routing_action_id=$2,
    event_state='PENDING',next_attempt_at=NULL,lease_owner=NULL,lease_expires_at=NULL,
    last_error=NULL,processed_at=NULL,updated_at=now()
WHERE id=$3 AND authorized_safeheron_occurrence_key IS NULL AND authorizing_routing_action_id IS NULL`,
		link.RoutingIdentityKey, actionID, link.ProviderEventID)
	if err != nil {
		return fmt.Errorf("bind existing provider event to routing action: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("existing provider event recovery binding conflicts with routing state")
	}
	return nil
}

func validateAndLinkExistingProjection(
	ctx context.Context,
	tx *sql.Tx,
	caseID, commandID int64,
	decision string,
	existingDepositID, existingCompanyID sql.NullInt64,
	link ExistingProjectionLink,
) error {
	if link.DepositID > 0 {
		if decision != string(DecisionCustomer) && decision != string(DecisionDual) && decision != string(DecisionPartial) {
			return fmt.Errorf("existing deposit is incompatible with routing decision %s", decision)
		}
		if existingDepositID.Valid && existingDepositID.Int64 != link.DepositID {
			return fmt.Errorf("existing deposit conflicts with partially completed routing case")
		}
		if !existingDepositID.Valid {
			if err := validateExistingDeposit(ctx, tx, caseID, commandID, link.RoutingIdentityKey, link.DepositID); err != nil {
				return err
			}
		}
	}
	if link.CompanyFundTransactionID > 0 {
		if decision != string(DecisionCompany) && decision != string(DecisionDual) && decision != string(DecisionPartial) {
			return fmt.Errorf("existing company movement is incompatible with routing decision %s", decision)
		}
		if existingCompanyID.Valid && existingCompanyID.Int64 != link.CompanyFundTransactionID {
			return fmt.Errorf("existing company movement conflicts with partially completed routing case")
		}
		if !existingCompanyID.Valid {
			if err := validateExistingCompanyMovement(ctx, tx, caseID, commandID, link.RoutingIdentityKey, link.CompanyFundTransactionID); err != nil {
				return err
			}
		}
	}
	if link.DepositID > 0 && link.CompanyFundTransactionID > 0 && decision != string(DecisionDual) {
		return fmt.Errorf("both existing outputs require a DUAL routing decision")
	}
	return nil
}

func validateExistingDeposit(ctx context.Context, tx *sql.Tx, caseID, commandID int64, identity string, depositID int64) error {
	var actionID int64
	var exact bool
	err := tx.QueryRowContext(ctx, `SELECT action.id,
  deposit.user_id=action.target_user_id
  AND deposit.safeheron_tx_key=routing.safeheron_tx_key
  AND deposit.safeheron_coin_key=routing.raw_coin_key
  AND deposit.amount=routing.amount
  AND lower(COALESCE(deposit.from_address,''))=lower(routing.normalized_source)
  AND lower(COALESCE(deposit.to_address,''))=lower(routing.normalized_destination)
  AND EXISTS (
    SELECT 1 FROM coin_chains asset JOIN chains chain ON chain.code=asset.chain_code
    WHERE asset.id=deposit.coin_chain_id
      AND asset.safeheron_coin_key=routing.raw_coin_key
      AND upper(chain.network_family)=routing.network_family
  )
FROM safeheron_transaction_routing_cases routing
JOIN safeheron_transaction_routing_case_actions action
  ON action.command_id=$2 AND action.projection_kind='CUSTOMER' AND action.status IN ('PENDING','RETRYABLE')
JOIN deposits deposit ON deposit.id=$3
WHERE routing.id=$1 FOR UPDATE OF action,deposit`, caseID, commandID, depositID).Scan(&actionID, &exact)
	if err != nil {
		return fmt.Errorf("load existing deposit recovery target: %w", err)
	}
	if !exact {
		return fmt.Errorf("existing deposit does not exactly match recovery routing target")
	}
	return insertExistingResult(ctx, tx, caseID, actionID, identity, "CUSTOMER", depositID)
}

func validateExistingCompanyMovement(ctx context.Context, tx *sql.Tx, caseID, commandID int64, identity string, transactionID int64) error {
	var actionID int64
	var exact bool
	err := tx.QueryRowContext(ctx, `SELECT action.id,
  movement.channel='SAFEHERON'
  AND movement.provider_occurrence_key=routing.routing_identity_key
  AND movement.provider_asset_key=routing.raw_coin_key
  AND movement.amount=routing.amount
  AND lower(COALESCE(movement.from_address_or_account,''))=lower(routing.normalized_source)
  AND lower(COALESCE(movement.to_address_or_account,''))=lower(routing.normalized_destination)
  AND movement.transaction_direction=routing.direction
  AND (movement.from_company_fund_account_id=action.target_company_fund_account_id
       OR movement.to_company_fund_account_id=action.target_company_fund_account_id)
FROM safeheron_transaction_routing_cases routing
JOIN safeheron_transaction_routing_case_actions action
  ON action.command_id=$2 AND action.projection_kind='COMPANY' AND action.status IN ('PENDING','RETRYABLE')
JOIN company_fund_transactions movement ON movement.id=$3
WHERE routing.id=$1 FOR UPDATE OF action,movement`, caseID, commandID, transactionID).Scan(&actionID, &exact)
	if err != nil {
		return fmt.Errorf("load existing company recovery target: %w", err)
	}
	if !exact {
		return fmt.Errorf("existing company movement does not exactly match recovery routing target")
	}
	return insertExistingResult(ctx, tx, caseID, actionID, identity, "COMPANY", transactionID)
}

func insertExistingResult(ctx context.Context, tx *sql.Tx, caseID, actionID int64, identity, kind string, resultID int64) error {
	digest := routingResultDigest(identity, resultID)
	depositID, companyID := any(nil), any(nil)
	caseColumn := "deposit_id"
	if kind == "CUSTOMER" {
		depositID = resultID
	} else {
		companyID = resultID
		caseColumn = "company_fund_transaction_id"
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO safeheron_transaction_routing_case_results
  (case_id,action_id,projection_kind,deposit_id,company_fund_transaction_id,result_digest)
VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, caseID, actionID, kind, depositID, companyID, digest)
	if err != nil {
		return err
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("existing %s projection result conflicts with routing state", kind)
	}
	caseResult, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE safeheron_transaction_routing_cases
SET %s=$1,updated_at=now() WHERE id=$2 AND %s IS NULL`, caseColumn, caseColumn), resultID, caseID)
	if err != nil {
		return err
	}
	if rows, rowsErr := caseResult.RowsAffected(); rowsErr != nil || rows != 1 {
		return fmt.Errorf("existing %s projection case link conflicts", kind)
	}
	_, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_case_actions
SET status='APPLIED',completed_at=now(),updated_at=now(),next_attempt_at=NULL,
    lease_owner=NULL,lease_expires_at=NULL,last_error_code=NULL,last_error_detail=NULL
WHERE id=$1`, actionID)
	return err
}
