package fundrouting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

type AlertEscalator struct {
	db             *sql.DB
	runner         *adaptiveRunner
	onAlertCreated func()
}

// SetOnAlertCreated registers a wake after a durable SLA alert is inserted.
func (e *AlertEscalator) SetOnAlertCreated(fn func()) {
	if e == nil {
		return
	}
	e.onAlertCreated = fn
}

func NewAlertEscalator(db *sql.DB) (*AlertEscalator, error) {
	if db == nil {
		return nil, fmt.Errorf("fund routing alert escalation database is required")
	}
	e := &AlertEscalator{db: db}
	// SLA thresholds are 1h/24h; min idle can be 1m while fully idle backs off to MaxIdle.
	e.runner = newAdaptiveRunner("fund routing OPEN-case SLA escalator", time.Minute, adaptiveschedule.DefaultMaxIdle, e.ProcessOne)
	e.runner.setNextDue(e.NextDue)
	return e, nil
}

// NextDue returns the earliest not-yet-emitted SLA threshold for an OPEN case.
func (e *AlertEscalator) NextDue(ctx context.Context) (time.Time, error) {
	var due sql.NullTime
	err := e.db.QueryRowContext(ctx, `WITH thresholds(level,minimum_age) AS (
  VALUES (1,interval '1 hour'), (2,interval '24 hours')
)
SELECT min(routing.created_at + threshold.minimum_age)
FROM safeheron_transaction_routing_cases routing
CROSS JOIN thresholds threshold
WHERE routing.decision='OPEN'
  AND routing.created_at + threshold.minimum_age > now()
  AND NOT EXISTS (
    SELECT 1 FROM safeheron_transaction_routing_alerts alert
    WHERE alert.case_id=routing.id AND alert.alert_type='SLA_ESCALATION'
      AND alert.transition_key='sla:level:' || threshold.level::text
  )`).Scan(&due)
	if err != nil || !due.Valid {
		return time.Time{}, err
	}
	return due.Time, nil
}

func openCaseSLAEscalationSQL() string {
	return `WITH thresholds(level,minimum_age,severity) AS (
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
RETURNING id`
}

func (e *AlertEscalator) ProcessOne(ctx context.Context) (bool, error) {
	var alertID int64
	err := e.db.QueryRowContext(ctx, openCaseSLAEscalationSQL()).Scan(&alertID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if e.onAlertCreated != nil {
		e.onAlertCreated()
	}
	return true, nil
}

func (e *AlertEscalator) Run(ctx context.Context) {
	if e == nil || e.runner == nil {
		return
	}
	e.runner.Run(ctx)
}
