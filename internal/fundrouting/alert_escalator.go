package fundrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

type AlertEscalator struct {
	db       *sql.DB
	interval time.Duration
}

func NewAlertEscalator(db *sql.DB) (*AlertEscalator, error) {
	if db == nil {
		return nil, fmt.Errorf("fund routing alert escalation database is required")
	}
	return &AlertEscalator{db: db, interval: time.Minute}, nil
}

func (e *AlertEscalator) ProcessOne(ctx context.Context) (bool, error) {
	var alertID int64
	err := e.db.QueryRowContext(ctx, `WITH thresholds(level,minimum_age,severity) AS (
  VALUES (1,interval '1 hour','ERROR'::varchar),
         (2,interval '24 hours','CRITICAL'::varchar)
), candidate AS (
  SELECT routing.id AS case_id, routing.reason_code, threshold.level, threshold.severity
  FROM safeheron_transaction_routing_cases routing
  CROSS JOIN thresholds threshold
  WHERE routing.decision='OPEN' AND routing.created_at <= now()-threshold.minimum_age
    AND NOT EXISTS (
      SELECT 1 FROM safeheron_transaction_routing_alerts alert
      WHERE alert.case_id=routing.id AND alert.alert_type='SLA_ESCALATION'
        AND alert.transition_key='sla:level:' || threshold.level::text
    )
  ORDER BY routing.created_at, routing.id, threshold.level
  LIMIT 1
)
INSERT INTO safeheron_transaction_routing_alerts
  (case_id,alert_type,transition_key,severity,payload)
SELECT case_id,'SLA_ESCALATION','sla:level:' || level::text,severity,
       jsonb_build_object('level',level,'reason_code',reason_code)
FROM candidate
ON CONFLICT (case_id,alert_type,transition_key) DO NOTHING
RETURNING id`).Scan(&alertID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (e *AlertEscalator) Run(ctx context.Context) {
	log.Printf("fund routing OPEN-case SLA escalator started")
	defer log.Printf("fund routing OPEN-case SLA escalator stopped")
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()
	for {
		for {
			processed, err := e.ProcessOne(ctx)
			if err != nil {
				log.Printf("fund routing SLA escalator error: %v", err)
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
