package fundrouting

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"monera-digital/internal/adaptiveschedule"
)

type MetricsSnapshot struct {
	OpenCases            int64
	PartialCases         int64
	PendingActions       int64
	RetryableActions     int64
	DeadActions          int64
	BlockedCommands      int64
	UndeliveredAlerts    int64
	AmbiguousDeliveries  int64
	OldestOpenAgeSeconds int64
	CustomerCases        int64
	CompanyCases         int64
	DualCases            int64
	AppliedResults       int64
	RoutingApplyFailures int64
	InconsistentLinks    int64
	OldestActionAge      int64
}

type MetricsMonitor struct {
	db     *sql.DB
	runner *adaptiveRunner
}

func NewMetricsMonitor(db *sql.DB) (*MetricsMonitor, error) {
	if db == nil {
		return nil, fmt.Errorf("fund routing metrics database is required")
	}
	monitor := &MetricsMonitor{db: db}
	// Metrics are maintenance work: collect on activity-driven cycles via the
	// shared idle budget (startup + progressive backoff to MaxIdle).
	monitor.runner = newAdaptiveRunner("fund routing metrics", time.Minute, adaptiveschedule.DefaultMaxIdle, func(ctx context.Context) (bool, error) {
		monitor.record(ctx)
		// Always report empty so progressive backoff can reach MaxIdle unless
		// another worker wakes the process; metrics must not pin minute polls.
		return false, nil
	})
	return monitor, nil
}

func (monitor *MetricsMonitor) Snapshot(ctx context.Context) (MetricsSnapshot, error) {
	var snapshot MetricsSnapshot
	err := monitor.db.QueryRowContext(ctx, `SELECT
  count(*) FILTER (WHERE routing.decision='OPEN'),
  count(*) FILTER (WHERE routing.decision='PARTIAL'),
  (SELECT count(*) FROM safeheron_transaction_routing_case_actions WHERE status='PENDING'),
  (SELECT count(*) FROM safeheron_transaction_routing_case_actions WHERE status='RETRYABLE'),
  (SELECT count(*) FROM safeheron_transaction_routing_case_actions WHERE status='DEAD'),
  (SELECT count(*) FROM safeheron_transaction_routing_case_commands WHERE status='BLOCKED'),
  (SELECT count(*) FROM safeheron_transaction_routing_alert_deliveries WHERE status IN ('PENDING','FAILED_DEFINITE')),
  (SELECT count(*) FROM safeheron_transaction_routing_alert_deliveries WHERE status='AMBIGUOUS'),
  COALESCE(extract(epoch FROM now()-(min(routing.created_at) FILTER (WHERE routing.decision='OPEN'))),0)::bigint,
  count(*) FILTER (WHERE routing.decision='CUSTOMER'),
  count(*) FILTER (WHERE routing.decision='COMPANY'),
  count(*) FILTER (WHERE routing.decision='DUAL'),
  (SELECT count(*) FROM safeheron_transaction_routing_case_results),
  (SELECT count(*) FROM safeheron_webhook_events WHERE process_status='ERROR' AND error_message='ROUTING_APPLY_FAILED'),
  (SELECT count(*) FROM safeheron_transaction_routing_cases checked
    WHERE (checked.deposit_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM safeheron_transaction_routing_case_results result WHERE result.case_id=checked.id AND result.projection_kind='CUSTOMER' AND result.deposit_id=checked.deposit_id))
       OR (checked.company_fund_transaction_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM safeheron_transaction_routing_case_results result WHERE result.case_id=checked.id AND result.projection_kind='COMPANY' AND result.company_fund_transaction_id=checked.company_fund_transaction_id))),
  COALESCE((SELECT extract(epoch FROM now()-min(created_at))::bigint FROM safeheron_transaction_routing_case_actions WHERE status IN ('PENDING','RETRYABLE')),0)
FROM safeheron_transaction_routing_cases routing`).Scan(
		&snapshot.OpenCases, &snapshot.PartialCases, &snapshot.PendingActions,
		&snapshot.RetryableActions, &snapshot.DeadActions, &snapshot.BlockedCommands,
		&snapshot.UndeliveredAlerts, &snapshot.AmbiguousDeliveries, &snapshot.OldestOpenAgeSeconds,
		&snapshot.CustomerCases, &snapshot.CompanyCases, &snapshot.DualCases,
		&snapshot.AppliedResults, &snapshot.RoutingApplyFailures, &snapshot.InconsistentLinks, &snapshot.OldestActionAge,
	)
	return snapshot, err
}

func (monitor *MetricsMonitor) Run(ctx context.Context) {
	if monitor == nil || monitor.runner == nil {
		return
	}
	monitor.runner.Run(ctx)
}

func (monitor *MetricsMonitor) record(ctx context.Context) {
	snapshot, err := monitor.Snapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("fund_routing_metrics query_failed=true")
		}
		return
	}
	log.Printf("fund_routing_metrics open_cases=%d partial_cases=%d pending_actions=%d retryable_actions=%d dead_actions=%d blocked_commands=%d undelivered_alerts=%d ambiguous_deliveries=%d oldest_open_age_seconds=%d customer_cases=%d company_cases=%d dual_cases=%d applied_results=%d routing_apply_failures=%d inconsistent_links=%d oldest_action_age_seconds=%d",
		snapshot.OpenCases, snapshot.PartialCases, snapshot.PendingActions,
		snapshot.RetryableActions, snapshot.DeadActions, snapshot.BlockedCommands,
		snapshot.UndeliveredAlerts, snapshot.AmbiguousDeliveries, snapshot.OldestOpenAgeSeconds,
		snapshot.CustomerCases, snapshot.CompanyCases, snapshot.DualCases, snapshot.AppliedResults,
		snapshot.RoutingApplyFailures, snapshot.InconsistentLinks, snapshot.OldestActionAge)
}
