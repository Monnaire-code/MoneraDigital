package fundrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestProjectionWorkerPostgresRequeuesAuthorizedDeadProviderEvent(t *testing.T) {
	if os.Getenv("RUN_FUND_ROUTING_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_FUND_ROUTING_POSTGRES_INTEGRATION=1 to run PostgreSQL routing coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	address := "0x00000000000000000000000000000000000000e1"
	accountID, err := ensureRoutingTestAccount(db, "__routing_provider_requeue_fixture__", address, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-provider-requeue-" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = address
	snapshot.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("e", 64)
	var webhookID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'DONE') RETURNING id`,
		"routing-provider-requeue-event-"+suffix, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	var caseID, oldCommandID, oldActionID, newCommandID, newActionID, providerEventID int64
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM company_fund_provider_events WHERE id=$1`, providerEventID)
		if caseID > 0 {
			_, _ = db.Exec(`UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL WHERE id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id IN ($1,$2)`, oldCommandID, newCommandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE id IN ($1,$2)`, oldCommandID, newCommandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		_, _ = db.Exec(`DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
		_, _ = db.Exec(`DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
		_, _ = db.Exec(`UPDATE company_fund_accounts SET is_enabled=false WHERE id=$1`, accountID)
	})

	results, err := NewRepository(db).RouteVerifiedEvent(context.Background(), VerifiedEventInput{
		WebhookEventID: webhookID, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: digest,
		NetworkFamily: "EVM", Snapshot: snapshot,
	})
	if err != nil || len(results) != 1 || results[0].Decision.Decision != DecisionCompany {
		t.Fatalf("RouteVerifiedEvent() = %#v, %v", results, err)
	}
	caseID, oldCommandID = results[0].CaseID, results[0].CommandID
	if err := db.QueryRow(`SELECT id FROM safeheron_transaction_routing_case_actions WHERE command_id=$1`, oldCommandID).Scan(&oldActionID); err != nil {
		t.Fatal(err)
	}
	candidate, err := BuildCandidates(snapshot, "EVM")
	if err != nil || len(candidate) != 1 {
		t.Fatalf("BuildCandidates() = %#v, %v", candidate, err)
	}
	if err := db.QueryRow(`INSERT INTO company_fund_provider_events
  (channel,provider_event_id,event_type,source_kind,safeheron_webhook_event_id,source_payload_digest,
   event_state,processed_at,last_error,authorized_safeheron_occurrence_key,authorizing_routing_action_id)
VALUES ('SAFEHERON',$1,'TRANSACTION_STATUS_CHANGED','EXISTING_SAFEHERON_WEBHOOK_REF',$2,$3,
        'DEAD_LETTER',now(),'Safeheron transaction mapping is unavailable',$4,$5)
RETURNING id`, fmt.Sprintf("routing-company:%d", oldActionID), webhookID, digest,
		candidate[0].RoutingIdentityKey, oldActionID).Scan(&providerEventID); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.Exec(`UPDATE safeheron_transaction_routing_case_actions
SET status='DEAD',completed_at=now(),last_error_code='COMPANY_PROVIDER_EVENT_DEAD_LETTER'
WHERE id=$1`, oldActionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE safeheron_transaction_routing_case_commands SET status='CANCELLED',completed_at=now() WHERE id=$1`, oldCommandID); err != nil {
		t.Fatal(err)
	}
	var version int
	if err = tx.QueryRow(`SELECT version FROM safeheron_transaction_routing_cases WHERE id=$1 FOR UPDATE`, caseID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO safeheron_transaction_routing_case_commands
  (case_id,command_type,initiator,actor_scope,actor_admin_user_id,reason,idempotency_key,request_digest,expected_case_version)
VALUES ($1,'REQUEUE','ADMIN',$2,1,'integration replay',$3,$4,$5) RETURNING id`,
		caseID, "integration:"+suffix, "requeue:"+suffix, strings.Repeat("f", 64), version).Scan(&newCommandID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO safeheron_transaction_routing_case_actions
  (command_id,action_type,projection_kind,target_company_fund_account_id)
VALUES ($1,'APPLY_COMPANY','COMPANY',$2) RETURNING id`, newCommandID, accountID).Scan(&newActionID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE safeheron_transaction_routing_cases
SET pending_command_id=$2,version=version+1,updated_at=now() WHERE id=$1`, caseID, newCommandID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	worker, err := NewProjectionWorker(db, &projectionEventInserterStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE safeheron_transaction_routing_case_actions
SET lease_owner=$2,lease_expires_at=now()+interval '1 minute' WHERE id=$1`, newActionID, worker.workerID); err != nil {
		t.Fatal(err)
	}
	action := projectionAction{
		ID: newActionID, Type: "APPLY_COMPANY", CaseID: caseID, CommandID: newCommandID,
		RoutingIdentityKey: candidate[0].RoutingIdentityKey, WebhookEventID: int(webhookID), PayloadDigest: digest,
		TargetCompanyID: sql.NullInt64{Int64: accountID, Valid: true},
	}
	requeued, err := worker.requeueCompanyProviderEvent(context.Background(), action)
	if err != nil || !requeued {
		t.Fatalf("requeueCompanyProviderEvent() = %v, %v", requeued, err)
	}
	var state, providerEventKey, occurrenceKey, storedDigest string
	var authorizingActionID int64
	if err := db.QueryRow(`SELECT event_state,provider_event_id,authorized_safeheron_occurrence_key,
authorizing_routing_action_id,source_payload_digest FROM company_fund_provider_events WHERE id=$1`, providerEventID).
		Scan(&state, &providerEventKey, &occurrenceKey, &authorizingActionID, &storedDigest); err != nil {
		t.Fatal(err)
	}
	if state != "PENDING" || providerEventKey != fmt.Sprintf("routing-company:%d", oldActionID) ||
		authorizingActionID != newActionID || occurrenceKey != candidate[0].RoutingIdentityKey || storedDigest != digest {
		t.Fatalf("requeued provider event state=%s key=%s occurrence=%s action=%d digest=%s", state, providerEventKey, occurrenceKey, authorizingActionID, storedDigest)
	}
	second, err := worker.requeueCompanyProviderEvent(context.Background(), action)
	if err != nil || second {
		t.Fatalf("second requeueCompanyProviderEvent() = %v, %v; want idempotent no-op", second, err)
	}
}
