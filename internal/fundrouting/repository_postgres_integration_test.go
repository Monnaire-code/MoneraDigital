package fundrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"monera-digital/internal/adaptiveschedule"
	"monera-digital/internal/safeheron"
)

func TestRepositoryPostgresUnknownOccurrenceCreatesOnlyOpenCase(t *testing.T) {
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
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-integration-" + suffix
	snapshot.TxHash = "0x" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = "0x00000000000000000000000000000000000000b2"
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("a", 32) + fmt.Sprintf("%032x", time.Now().UnixNano())
	eventID := "routing-integration-event-" + suffix
	var webhookID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'PENDING') RETURNING id`, eventID, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	var caseID int64
	t.Cleanup(func() {
		ctx := context.Background()
		if caseID > 0 {
			_, _ = db.ExecContext(ctx, `DELETE FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID)
			_, _ = db.ExecContext(ctx, `DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.ExecContext(ctx, `DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		_, _ = db.ExecContext(ctx, `DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
		_, _ = db.ExecContext(ctx, `DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
	})

	results, err := NewRepository(db).RouteVerifiedEvent(context.Background(), VerifiedEventInput{
		WebhookEventID: webhookID, EventType: "TRANSACTION_STATUS_CHANGED",
		PayloadDigest: digest, NetworkFamily: "EVM", Snapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Decision.Decision != DecisionOpen {
		t.Fatalf("route results = %#v", results)
	}
	caseID = results[0].CaseID
	var decision, status string
	var sourceCount, alertCount, depositCount, companyCount int
	if err := db.QueryRow(`SELECT decision FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID).Scan(&decision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT process_status FROM safeheron_webhook_events WHERE id=$1`, webhookID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID).Scan(&sourceCount)
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID).Scan(&alertCount)
	_ = db.QueryRow(`SELECT count(*) FROM deposits WHERE safeheron_tx_key=$1`, snapshot.TxKey).Scan(&depositCount)
	if err := db.QueryRow(`SELECT count(*) FROM company_fund_transactions WHERE provider_transaction_id=$1`, snapshot.TxKey).Scan(&companyCount); err != nil {
		t.Fatal(err)
	}
	if decision != "OPEN" || status != "DONE" || sourceCount != 1 || alertCount != 1 || depositCount != 0 || companyCount != 0 {
		t.Fatalf("decision=%s status=%s sources=%d alerts=%d deposits=%d company=%d", decision, status, sourceCount, alertCount, depositCount, companyCount)
	}
}

func TestRepositoryPostgresCompanyOccurrenceReservesOneProjection(t *testing.T) {
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
	address := "0x00000000000000000000000000000000000000c1"
	accountID, err := ensureRoutingTestAccount(db, "__routing_company_projection_fixture__", address, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-company-integration-" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = address
	snapshot.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("b", 64)
	var webhookID, caseID, commandID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'PENDING') RETURNING id`, "routing-company-event-"+suffix, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if commandID > 0 {
			_, _ = db.Exec(`UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL WHERE id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID)
		}
		if caseID > 0 {
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
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Decision.Decision != DecisionCompany || results[0].CommandID == 0 {
		t.Fatalf("route results = %#v", results)
	}
	caseID, commandID = results[0].CaseID, results[0].CommandID
	var actionCount int
	if err := db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_case_actions
WHERE command_id=$1 AND action_type='APPLY_COMPANY' AND target_company_fund_account_id=$2`, commandID, accountID).Scan(&actionCount); err != nil {
		t.Fatal(err)
	}
	if actionCount != 1 {
		t.Fatalf("company action count = %d", actionCount)
	}
}

func TestRepositoryPostgresRecoveryLinksExactExistingDepositWithoutCreditingAgain(t *testing.T) {
	if os.Getenv("RUN_FUND_ROUTING_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_FUND_ROUTING_POSTGRES_INTEGRATION=1 to run PostgreSQL routing coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	address := "0x00000000000000000000000000000000000000d1"
	if _, err := ensureRoutingTestPool(db, "__routing_customer_recovery_fixture__", address); err != nil {
		t.Fatal(err)
	}
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-recovery-" + suffix
	snapshot.TxHash = "0x" + suffix
	snapshot.CoinKey = "ETH"
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = address
	snapshot.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	var webhookID, depositID, caseID, commandID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status,error_message)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'ERROR','deposits_user_id_users_id_fk') RETURNING id`,
		"routing-recovery-event-"+suffix, snapshot.TxKey, payload, strings.Repeat("d", 64)).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO deposits
  (user_id,tx_hash,amount,asset,chain,status,from_address,to_address,safeheron_tx_key,
   safeheron_coin_key,chain_code,coin_chain_id,safeheron_status,status_rank)
VALUES (1,$1,1,'ETH','ETHEREUM','CREDITED',$2,$3,$4,'ETH','ETHEREUM',1,'COMPLETED',100)
RETURNING id`, snapshot.TxHash, snapshot.SourceAddress, address, snapshot.TxKey).Scan(&depositID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if caseID > 0 {
			cleanupTx, cleanupErr := db.Begin()
			if cleanupErr == nil {
				_, _ = cleanupTx.Exec(`UPDATE safeheron_transaction_routing_cases SET deposit_id=NULL,pending_command_id=NULL WHERE id=$1`, caseID)
				_, _ = cleanupTx.Exec(`DELETE FROM safeheron_transaction_routing_case_results WHERE case_id=$1`, caseID)
				_ = cleanupTx.Commit()
			}
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		_, _ = db.Exec(`DELETE FROM deposits WHERE id=$1`, depositID)
		_, _ = db.Exec(`DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
		_, _ = db.Exec(`DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
	})
	repository := NewRepository(db)
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("recovery candidate=%#v err=%v", candidates, err)
	}
	results, err := repository.RouteVerifiedEvent(context.Background(), VerifiedEventInput{
		WebhookEventID: webhookID, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: strings.Repeat("d", 64),
		NetworkFamily: "EVM", Snapshot: snapshot, SuppressOpenAlert: true, PreserveRawEventStatus: true,
		ExistingProjectionLinks: map[string]ExistingProjectionLink{
			candidates[0].RoutingIdentityKey: {RoutingIdentityKey: candidates[0].RoutingIdentityKey, DepositID: depositID},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Decision.Decision != DecisionCustomer {
		t.Fatalf("route results = %#v", results)
	}
	caseID, commandID = results[0].CaseID, results[0].CommandID
	var linkedDeposit int64
	var commandStatus, eventStatus string
	if err := db.QueryRow(`SELECT deposit_id FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID).Scan(&linkedDeposit); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT status FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID).Scan(&commandStatus)
	_ = db.QueryRow(`SELECT process_status FROM safeheron_webhook_events WHERE id=$1`, webhookID).Scan(&eventStatus)
	if linkedDeposit != depositID || commandStatus != "APPLIED" || eventStatus != "ERROR" {
		t.Fatalf("deposit=%d command=%s event=%s", linkedDeposit, commandStatus, eventStatus)
	}
}

func TestRepositoryPostgresRecoveryBindsExactLegacyProviderEvent(t *testing.T) {
	if os.Getenv("RUN_FUND_ROUTING_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_FUND_ROUTING_POSTGRES_INTEGRATION=1 to run PostgreSQL routing coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	address := "0x00000000000000000000000000000000000000c3"
	accountID, err := ensureRoutingTestAccount(db, "__routing_provider_recovery_fixture__", address, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-provider-recovery-" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = address
	snapshot.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("e", 64)
	var webhookID, providerEventID, caseID, commandID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status,error_message)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'ERROR','legacy-provider-only') RETURNING id`,
		"routing-provider-recovery-event-"+suffix, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO company_fund_provider_events
  (channel,provider_event_id,event_type,source_kind,safeheron_webhook_event_id,source_payload_digest,event_state)
VALUES ('SAFEHERON',$1,'TRANSACTION_STATUS_CHANGED','EXISTING_SAFEHERON_WEBHOOK_REF',$2,$3,'PENDING') RETURNING id`,
		"legacy-routing-provider-"+suffix, webhookID, digest).Scan(&providerEventID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM company_fund_provider_events WHERE id=$1`, providerEventID)
		if caseID > 0 {
			_, _ = db.Exec(`UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL WHERE id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		_, _ = db.Exec(`DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
		_, _ = db.Exec(`DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
		_, _ = db.Exec(`UPDATE company_fund_accounts SET is_enabled=false WHERE id=$1`, accountID)
	})
	candidates, err := BuildCandidates(snapshot, "EVM")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	results, err := NewRepository(db).RouteVerifiedEvent(context.Background(), VerifiedEventInput{
		WebhookEventID: webhookID, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: digest,
		NetworkFamily: "EVM", Snapshot: snapshot, SuppressOpenAlert: true, PreserveRawEventStatus: true,
		ExistingProjectionLinks: map[string]ExistingProjectionLink{
			candidates[0].RoutingIdentityKey: {
				RoutingIdentityKey: candidates[0].RoutingIdentityKey,
				ProviderEventID:    providerEventID,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	caseID, commandID = results[0].CaseID, results[0].CommandID
	var actionID int64
	var occurrence, state, commandStatus string
	if err := db.QueryRow(`SELECT authorizing_routing_action_id,authorized_safeheron_occurrence_key,event_state
FROM company_fund_provider_events WHERE id=$1`, providerEventID).Scan(&actionID, &occurrence, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT status FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID).Scan(&commandStatus); err != nil {
		t.Fatal(err)
	}
	if actionID == 0 || occurrence != candidates[0].RoutingIdentityKey || state != "PENDING" || commandStatus != "PENDING" {
		t.Fatalf("action=%d occurrence=%s state=%s command=%s", actionID, occurrence, state, commandStatus)
	}
}

func TestReconcilerPostgresResolvesOpenCaseAfterCompanyAccountEnabled(t *testing.T) {
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
	address := "0x00000000000000000000000000000000000000c2"
	accountID, err := ensureRoutingTestAccount(db, "__routing_reconcile_fixture__", address, false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := routingSnapshot()
	snapshot.TxKey = "routing-reconcile-integration-" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = address
	snapshot.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("c", 64)
	var webhookID, caseID, commandID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'PENDING') RETURNING id`, "routing-reconcile-event-"+suffix, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if commandID > 0 {
			_, _ = db.Exec(`UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL WHERE id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id=$1`, commandID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE id=$1`, commandID)
		}
		if caseID > 0 {
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID)
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
	if err != nil {
		t.Fatal(err)
	}
	caseID = results[0].CaseID
	if results[0].Decision.Decision != DecisionOpen || results[0].Decision.Reason != ReasonCompanyAccountDisabled {
		t.Fatalf("initial route = %#v", results[0])
	}
	if _, err := db.Exec(`UPDATE company_fund_accounts SET is_enabled=true WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	reconciler, _ := NewReconciler(db)
	var decision string
	var pending sql.NullInt64
	for attempt := 0; attempt < 20; attempt++ {
		processed, processErr := reconciler.ProcessOne(context.Background())
		if processErr != nil {
			t.Fatal(processErr)
		}
		if !processed {
			break
		}
		if err := db.QueryRow(`SELECT decision,pending_command_id FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID).Scan(&decision, &pending); err != nil {
			t.Fatal(err)
		}
		if decision == "COMPANY" {
			commandID = pending.Int64
			break
		}
	}
	if decision != "COMPANY" || commandID == 0 {
		t.Fatalf("decision=%s command=%d", decision, commandID)
	}
}

func TestReconcilerPostgresDrainsPastUnresolvedCaseToTerminalCase(t *testing.T) {
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

	ctx := context.Background()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	repository := NewRepository(db)
	var webhookIDs, caseIDs []int64
	var accountID int64
	t.Cleanup(func() {
		for _, caseID := range caseIDs {
			_, _ = db.Exec(`UPDATE safeheron_transaction_routing_cases SET pending_command_id=NULL WHERE id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_actions WHERE command_id IN (
			  SELECT id FROM safeheron_transaction_routing_case_commands WHERE case_id=$1)`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_commands WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		for _, webhookID := range webhookIDs {
			_, _ = db.Exec(`DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
			_, _ = db.Exec(`DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
		}
		if accountID > 0 {
			_, _ = db.Exec(`DELETE FROM safeheron_address_ownerships WHERE company_fund_account_id=$1`, accountID)
			_, _ = db.Exec(`DELETE FROM company_fund_accounts WHERE id=$1`, accountID)
		}
	})

	insertAndRoute := func(eventID, eventType, digest string, snapshot safeheron.TransactionSnapshot) RouteResult {
		t.Helper()
		payload, marshalErr := json.Marshal(map[string]any{"eventType": eventType, "eventDetail": snapshot})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var webhookID int64
		if insertErr := db.QueryRow(`INSERT INTO safeheron_webhook_events
	  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
	VALUES ($1,$2,$3,$4::jsonb,$5,'PENDING') RETURNING id`, eventID, eventType, snapshot.TxKey, payload, digest).Scan(&webhookID); insertErr != nil {
			t.Fatal(insertErr)
		}
		webhookIDs = append(webhookIDs, webhookID)
		results, routeErr := repository.RouteVerifiedEvent(ctx, VerifiedEventInput{
			WebhookEventID: webhookID,
			EventType:      eventType,
			PayloadDigest:  digest,
			NetworkFamily:  "EVM",
			Snapshot:       snapshot,
		})
		if routeErr != nil {
			t.Fatal(routeErr)
		}
		if len(results) != 1 {
			t.Fatalf("route results=%#v, want one occurrence", results)
		}
		return results[0]
	}

	blocker := routingSnapshot()
	blocker.TxKey = "routing-blocker-" + suffix
	blocker.TxHash = "0xblocker" + suffix
	blocker.SourceAddress = fmt.Sprintf("0x%040x", time.Now().UnixNano())
	blocker.DestinationAddress = fmt.Sprintf("0x%040x", time.Now().UnixNano()+1)
	blockerResult := insertAndRoute("routing-blocker-event-"+suffix, "TRANSACTION_STATUS_CHANGED", strings.Repeat("a", 64), blocker)
	if blockerResult.Decision.Decision != DecisionOpen {
		t.Fatalf("blocker route=%#v, want OPEN", blockerResult)
	}
	caseIDs = append(caseIDs, blockerResult.CaseID)
	if _, err := db.Exec(`UPDATE safeheron_transaction_routing_cases SET updated_at=now()-interval '1 hour' WHERE id=$1`, blockerResult.CaseID); err != nil {
		t.Fatal(err)
	}

	companyAddress := fmt.Sprintf("0x%040x", time.Now().UnixNano()+2)
	accountID, err = ensureRoutingTestAccount(db, "__routing_drain_"+suffix, companyAddress, true)
	if err != nil {
		t.Fatal(err)
	}
	target := routingSnapshot()
	target.TxKey = "routing-terminal-" + suffix
	target.TxHash = "0xterminal" + suffix
	target.SourceAddress = fmt.Sprintf("0x%040x", time.Now().UnixNano()+3)
	target.DestinationAddress = companyAddress
	target.TransactionStatus = "CONFIRMING"
	target.CreateTime = time.Now().Add(-time.Minute).UnixMilli()
	initialTarget := insertAndRoute("routing-target-confirming-"+suffix, "TRANSACTION_CREATED", strings.Repeat("b", 64), target)
	if initialTarget.Decision.Decision != DecisionOpen || initialTarget.Decision.Reason != ReasonStatusNotTerminal {
		t.Fatalf("initial target route=%#v, want STATUS_NOT_TERMINAL OPEN", initialTarget)
	}
	caseIDs = append(caseIDs, initialTarget.CaseID)
	target.TransactionStatus = "COMPLETED"
	terminalTarget := insertAndRoute("routing-target-completed-"+suffix, "TRANSACTION_STATUS_CHANGED", strings.Repeat("c", 64), target)
	if terminalTarget.CaseID != initialTarget.CaseID || terminalTarget.Decision.Decision != DecisionCompany || terminalTarget.CommandID != 0 {
		t.Fatalf("terminal target route=%#v, want existing COMPANY candidate awaiting reconciliation", terminalTarget)
	}

	reconciler, err := NewReconciler(db)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adaptiveschedule.DrainProcessOne(ctx, reconciler.ProcessOne, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !outcome.Worked || outcome.MoreWork {
		t.Fatalf("reconcile outcome=%#v, want bounded complete drain", outcome)
	}

	var blockerDecision, targetDecision string
	var commandID sql.NullInt64
	if err := db.QueryRow(`SELECT decision FROM safeheron_transaction_routing_cases WHERE id=$1`, blockerResult.CaseID).Scan(&blockerDecision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT decision,pending_command_id FROM safeheron_transaction_routing_cases WHERE id=$1`, initialTarget.CaseID).Scan(&targetDecision, &commandID); err != nil {
		t.Fatal(err)
	}
	if blockerDecision != "OPEN" || targetDecision != "COMPANY" || !commandID.Valid {
		t.Fatalf("blocker=%s target=%s command=%v", blockerDecision, targetDecision, commandID)
	}
}

func ensureRoutingTestAccount(db *sql.DB, key, address string, enabled bool) (int64, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM company_fund_accounts
WHERE channel='SAFEHERON' AND network_family='EVM' AND normalized_address=$1`, address).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = db.QueryRow(`INSERT INTO company_fund_accounts
  (channel,provider_account_key,wallet_address,normalized_address,network_family,account_name,is_enabled,monitoring_started_at)
VALUES ('SAFEHERON',$1,$2,$2,'EVM',$1,$3,now()-interval '1 hour') RETURNING id`, key, address, enabled).Scan(&id)
	} else if err == nil {
		_, err = db.Exec(`UPDATE company_fund_accounts SET is_enabled=$1 WHERE id=$2`, enabled, id)
	}
	return id, err
}

func ensureRoutingTestPool(db *sql.DB, key, address string) (int64, error) {
	var id int64
	err := db.QueryRow(`SELECT id FROM address_pool WHERE network_family='EVM' AND address=$1`, address).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		err = db.QueryRow(`INSERT INTO address_pool
  (network_family,address,safeheron_account_key,customer_ref_id,status,assigned_user_id,assigned_at)
VALUES ('EVM',$1,$2,$2,'ASSIGNED',1,timestamp '2020-01-01 00:00:00') RETURNING id`, address, key).Scan(&id)
	}
	return id, err
}
