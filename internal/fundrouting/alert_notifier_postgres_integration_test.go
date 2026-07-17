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

	"monera-digital/internal/alert"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type sequenceRoutingAlertSender struct {
	outcomes []alert.RoutingDeliveryOutcome
}

func (s *sequenceRoutingAlertSender) RoutingSinks() []alert.RoutingSink {
	return []alert.RoutingSink{{Kind: "LARK", Fingerprint: strings.Repeat("e", 64)}}
}

func (s *sequenceRoutingAlertSender) SendRouting(context.Context, string, string, string, string, map[string]string) alert.RoutingDeliveryOutcome {
	outcome := s.outcomes[0]
	s.outcomes = s.outcomes[1:]
	return outcome
}

func TestAlertNotifierPostgresPreservesUnknownAttemptAcrossManualReplay(t *testing.T) {
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
	snapshot.TxKey = "routing-alert-integration-" + suffix
	snapshot.TxHash = "0x" + suffix
	snapshot.SourceAddress = "0x00000000000000000000000000000000000000a1"
	snapshot.DestinationAddress = "0x00000000000000000000000000000000000000b2"
	payload, _ := json.Marshal(map[string]any{"eventType": "TRANSACTION_STATUS_CHANGED", "eventDetail": snapshot})
	digest := strings.Repeat("d", 64)
	var webhookID, caseID, alertID, deliveryID int64
	if err := db.QueryRow(`INSERT INTO safeheron_webhook_events
  (event_id,event_type,safeheron_tx_key,raw_payload,payload_digest,process_status)
VALUES ($1,'TRANSACTION_STATUS_CHANGED',$2,$3::jsonb,$4,'PENDING') RETURNING id`, "routing-alert-event-"+suffix, snapshot.TxKey, payload, digest).Scan(&webhookID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if deliveryID > 0 {
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1`, deliveryID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alert_deliveries WHERE id=$1`, deliveryID)
		}
		if caseID > 0 {
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_case_sources WHERE case_id=$1`, caseID)
			_, _ = db.Exec(`DELETE FROM safeheron_transaction_routing_cases WHERE id=$1`, caseID)
		}
		_, _ = db.Exec(`DELETE FROM company_fund_safeheron_raw_event_exclusions WHERE safeheron_webhook_event_id=$1`, webhookID)
		_, _ = db.Exec(`DELETE FROM safeheron_webhook_events WHERE id=$1`, webhookID)
	})

	results, err := NewRepository(db).RouteVerifiedEvent(context.Background(), VerifiedEventInput{
		WebhookEventID: webhookID, EventType: "TRANSACTION_STATUS_CHANGED", PayloadDigest: digest,
		NetworkFamily: "EVM", Snapshot: snapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	caseID = results[0].CaseID
	if err := db.QueryRow(`SELECT id FROM safeheron_transaction_routing_alerts WHERE case_id=$1`, caseID).Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	deliveryID = -time.Now().UnixNano()
	if _, err := db.Exec(`INSERT INTO safeheron_transaction_routing_alert_deliveries
  (id,alert_id,sink_kind,recipient_fingerprint)
VALUES ($1,$2,'LARK',$3)`, deliveryID, alertID, strings.Repeat("e", 64)); err != nil {
		t.Fatal(err)
	}

	sender := &sequenceRoutingAlertSender{outcomes: []alert.RoutingDeliveryOutcome{
		alert.RoutingDeliveryUnknown,
		alert.RoutingDeliveryDefinitelyNotSent,
		alert.RoutingDeliverySent,
	}}
	notifier, _ := NewAlertNotifier(db, sender)
	processed, err := notifier.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("first process processed=%v err=%v", processed, err)
	}
	if err := db.QueryRow(`SELECT id FROM safeheron_transaction_routing_alert_deliveries WHERE alert_id=$1`, alertID).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	var status string
	var automaticAttempts int
	if err := db.QueryRow(`SELECT status,automatic_attempt_count FROM safeheron_transaction_routing_alert_deliveries WHERE id=$1`, deliveryID).Scan(&status, &automaticAttempts); err != nil {
		t.Fatal(err)
	}
	if status != "AMBIGUOUS" || automaticAttempts != 1 {
		t.Fatalf("after unknown status=%s automatic_attempts=%d", status, automaticAttempts)
	}

	if _, err := db.Exec(`INSERT INTO safeheron_transaction_routing_alert_delivery_attempts
  (delivery_id,attempt_number,attempt_kind,outcome,actor_admin_user_id,reason,idempotency_key)
VALUES ($1,2,'MANUAL_REPLAY','IN_PROGRESS',1,'integration replay','integration-replay')`, deliveryID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE safeheron_transaction_routing_alert_deliveries SET status='PENDING',completed_at=NULL WHERE id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	processed, err = notifier.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("manual replay processed=%v err=%v", processed, err)
	}
	if _, err := db.Exec(`UPDATE safeheron_transaction_routing_alert_deliveries
SET next_attempt_at=now() WHERE id=$1 AND status='FAILED_DEFINITE'`, deliveryID); err != nil {
		t.Fatal(err)
	}
	processed, err = notifier.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("automatic retry after manual failure processed=%v err=%v", processed, err)
	}

	var unknownAttempts, failedManualAttempts, sentAutoAttempts, totalAttempts, maxAttempt int
	if err := db.QueryRow(`SELECT status,automatic_attempt_count FROM safeheron_transaction_routing_alert_deliveries WHERE id=$1`, deliveryID).Scan(&status, &automaticAttempts); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1`, deliveryID).Scan(&totalAttempts)
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1 AND attempt_kind='AUTO' AND outcome='DELIVERY_UNKNOWN'`, deliveryID).Scan(&unknownAttempts)
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1 AND attempt_kind='MANUAL_REPLAY' AND outcome='DEFINITELY_NOT_SENT'`, deliveryID).Scan(&failedManualAttempts)
	_ = db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1 AND attempt_kind='AUTO' AND outcome='SENT'`, deliveryID).Scan(&sentAutoAttempts)
	_ = db.QueryRow(`SELECT max(attempt_number) FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1`, deliveryID).Scan(&maxAttempt)
	if status != "SENT" || automaticAttempts != 2 || totalAttempts != 3 || unknownAttempts != 1 || failedManualAttempts != 1 || sentAutoAttempts != 1 || maxAttempt != 3 {
		t.Fatalf("status=%s automatic=%d total=%d unknown=%d manual_failed=%d auto_sent=%d max_attempt=%d", status, automaticAttempts, totalAttempts, unknownAttempts, failedManualAttempts, sentAutoAttempts, maxAttempt)
	}

	if _, err := db.Exec(`UPDATE safeheron_transaction_routing_cases SET created_at=now()-interval '25 hours' WHERE id=$1`, caseID); err != nil {
		t.Fatal(err)
	}
	escalator, _ := NewAlertEscalator(db)
	for level := 1; level <= 2; level++ {
		processed, err = escalator.ProcessOne(context.Background())
		if err != nil || !processed {
			t.Fatalf("SLA level %d processed=%v err=%v", level, processed, err)
		}
	}
	var slaAlerts int
	if err := db.QueryRow(`SELECT count(*) FROM safeheron_transaction_routing_alerts WHERE case_id=$1 AND alert_type='SLA_ESCALATION'`, caseID).Scan(&slaAlerts); err != nil || slaAlerts != 2 {
		t.Fatalf("SLA alerts=%d err=%v", slaAlerts, err)
	}
}
