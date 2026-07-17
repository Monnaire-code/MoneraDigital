package fundrouting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"monera-digital/internal/alert"
)

type RoutingAlertSender interface {
	RoutingSinks() []alert.RoutingSink
	SendRouting(context.Context, string, string, string, string, map[string]string) alert.RoutingDeliveryOutcome
}

type AlertNotifier struct {
	db       *sql.DB
	sender   RoutingAlertSender
	workerID string
	interval time.Duration
}

type claimedDelivery struct {
	ID                    int64
	AttemptID             int64
	Attempt               int
	AutomaticAttemptCount int
	SinkKind              string
	Fingerprint           string
	Severity              string
	AlertType             string
	Payload               []byte
}

func NewAlertNotifier(db *sql.DB, sender RoutingAlertSender) (*AlertNotifier, error) {
	if db == nil || sender == nil {
		return nil, fmt.Errorf("routing alert database and sender are required")
	}
	return &AlertNotifier{db: db, sender: sender, workerID: newProjectionWorkerID(), interval: time.Second}, nil
}

func (n *AlertNotifier) ProcessOne(ctx context.Context) (bool, error) {
	if err := n.sweepExpired(ctx); err != nil {
		return false, err
	}
	if err := n.ensureDeliveries(ctx); err != nil {
		return false, err
	}
	delivery, err := n.claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fields := map[string]string{}
	if len(delivery.Payload) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(delivery.Payload, &raw); err == nil {
			for key, value := range raw {
				fields[key] = fmt.Sprint(value)
			}
		}
	}
	outcome := n.sender.SendRouting(ctx, delivery.SinkKind, delivery.Fingerprint, delivery.Severity, "Safeheron routing "+delivery.AlertType, fields)
	return true, n.finish(ctx, delivery, outcome)
}

func (n *AlertNotifier) ensureDeliveries(ctx context.Context) (err error) {
	sinks := n.sender.RoutingSinks()
	if len(sinks) == 0 {
		return nil
	}
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var alertID int64
	err = tx.QueryRowContext(ctx, `SELECT alert.id
FROM safeheron_transaction_routing_alerts alert
WHERE NOT EXISTS (
  SELECT 1 FROM safeheron_transaction_routing_alert_deliveries delivery WHERE delivery.alert_id=alert.id
)
ORDER BY alert.id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&alertID)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		err = nil
		return nil
	}
	if err != nil {
		return err
	}
	for _, sink := range sinks {
		if _, err = tx.ExecContext(ctx, `INSERT INTO safeheron_transaction_routing_alert_deliveries
  (alert_id,sink_kind,recipient_fingerprint) VALUES ($1,$2,$3)
ON CONFLICT (alert_id,sink_kind,recipient_fingerprint) DO NOTHING`, alertID, sink.Kind, sink.Fingerprint); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (n *AlertNotifier) claim(ctx context.Context) (_ claimedDelivery, err error) {
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return claimedDelivery{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	var delivery claimedDelivery
	err = tx.QueryRowContext(ctx, `WITH candidate AS (
  SELECT id FROM safeheron_transaction_routing_alert_deliveries
  WHERE status='PENDING'
     OR (status='FAILED_DEFINITE' AND next_attempt_at IS NOT NULL AND next_attempt_at<=now())
  ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE safeheron_transaction_routing_alert_deliveries delivery
SET status='DISPATCHING',
    lease_owner=$1, lease_expires_at=now()+interval '30 seconds', next_attempt_at=NULL, updated_at=now()
FROM candidate WHERE delivery.id=candidate.id
RETURNING delivery.id`, n.workerID).Scan(&delivery.ID)
	if err != nil {
		return claimedDelivery{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,attempt_number
FROM safeheron_transaction_routing_alert_delivery_attempts
WHERE delivery_id=$1 AND attempt_kind='MANUAL_REPLAY' AND outcome='IN_PROGRESS'
ORDER BY attempt_number DESC LIMIT 1 FOR UPDATE`, delivery.ID).Scan(&delivery.AttemptID, &delivery.Attempt)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `UPDATE safeheron_transaction_routing_alert_deliveries
SET automatic_attempt_count=automatic_attempt_count+1 WHERE id=$1
	RETURNING automatic_attempt_count`, delivery.ID).Scan(&delivery.AutomaticAttemptCount)
		if err != nil {
			return claimedDelivery{}, err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO safeheron_transaction_routing_alert_delivery_attempts
  (delivery_id,attempt_number,attempt_kind,outcome)
SELECT $1,COALESCE(max(attempt_number),0)+1,'AUTO','IN_PROGRESS'
FROM safeheron_transaction_routing_alert_delivery_attempts WHERE delivery_id=$1
RETURNING id,attempt_number`, delivery.ID).Scan(&delivery.AttemptID, &delivery.Attempt)
	} else if err == nil {
		err = tx.QueryRowContext(ctx, `SELECT automatic_attempt_count
FROM safeheron_transaction_routing_alert_deliveries WHERE id=$1 FOR UPDATE`, delivery.ID).Scan(&delivery.AutomaticAttemptCount)
	}
	if err != nil {
		return claimedDelivery{}, err
	}
	err = tx.QueryRowContext(ctx, `SELECT delivery.sink_kind,delivery.recipient_fingerprint,
  alert.severity,alert.alert_type,alert.payload
FROM safeheron_transaction_routing_alert_deliveries delivery
JOIN safeheron_transaction_routing_alerts alert ON alert.id=delivery.alert_id
WHERE delivery.id=$1 AND delivery.lease_owner=$2`, delivery.ID, n.workerID).Scan(
		&delivery.SinkKind, &delivery.Fingerprint, &delivery.Severity, &delivery.AlertType, &delivery.Payload)
	if err != nil {
		return claimedDelivery{}, err
	}
	if err = tx.Commit(); err != nil {
		return claimedDelivery{}, err
	}
	return delivery, nil
}

func (n *AlertNotifier) finish(ctx context.Context, delivery claimedDelivery, outcome alert.RoutingDeliveryOutcome) (err error) {
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	status, attemptOutcome, completed, nextAttempt := "AMBIGUOUS", "DELIVERY_UNKNOWN", true, false
	switch outcome {
	case alert.RoutingDeliverySent:
		status, attemptOutcome = "SENT", "SENT"
	case alert.RoutingDeliveryDefinitelyNotSent:
		status, attemptOutcome, completed = "FAILED_DEFINITE", "DEFINITELY_NOT_SENT", false
		nextAttempt = delivery.AutomaticAttemptCount < 3
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_alert_delivery_attempts
SET outcome=$2,finished_at=now() WHERE id=$1 AND outcome='IN_PROGRESS'`, delivery.AttemptID, attemptOutcome); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_alert_deliveries
SET status=$2,lease_owner=NULL,lease_expires_at=NULL,updated_at=now(),
    completed_at=CASE WHEN $3 THEN now() ELSE NULL END,
    next_attempt_at=CASE WHEN $4 THEN now()+interval '30 seconds' ELSE NULL END
WHERE id=$1 AND lease_owner=$5`, delivery.ID, status, completed, nextAttempt, n.workerID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("routing alert delivery %d lease lost", delivery.ID)
	}
	return tx.Commit()
}

func (n *AlertNotifier) sweepExpired(ctx context.Context) (err error) {
	tx, err := n.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_alert_delivery_attempts attempt
SET outcome='DELIVERY_UNKNOWN',finished_at=now()
FROM safeheron_transaction_routing_alert_deliveries delivery
WHERE delivery.id=attempt.delivery_id AND delivery.status='DISPATCHING'
  AND delivery.lease_expires_at<=now() AND attempt.outcome='IN_PROGRESS'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE safeheron_transaction_routing_alert_deliveries
SET status='AMBIGUOUS',lease_owner=NULL,lease_expires_at=NULL,completed_at=now(),updated_at=now()
WHERE status='DISPATCHING' AND lease_expires_at<=now()`); err != nil {
		return err
	}
	return tx.Commit()
}

func (n *AlertNotifier) Run(ctx context.Context) {
	log.Printf("fund routing alert notifier started")
	defer log.Printf("fund routing alert notifier stopped")
	ticker := time.NewTicker(n.interval)
	defer ticker.Stop()
	for {
		if _, err := n.ProcessOne(ctx); err != nil {
			log.Printf("fund routing alert notifier error: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
