package fundrouting

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMetricsMonitorSnapshotIncludesRoutingBacklogAndTerminalFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("FROM safeheron_transaction_routing_cases routing").WillReturnRows(sqlmock.NewRows([]string{
		"open_cases", "partial_cases", "pending_actions", "retryable_actions", "dead_actions",
		"blocked_commands", "undelivered_alerts", "ambiguous_deliveries", "oldest_open_age_seconds",
		"customer_cases", "company_cases", "dual_cases", "applied_results", "routing_apply_failures", "inconsistent_links", "oldest_action_age",
	}).AddRow(3, 2, 4, 5, 6, 7, 8, 9, 120, 10, 11, 12, 13, 14, 0, 15))
	monitor, err := NewMetricsMonitor(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := monitor.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OpenCases != 3 || snapshot.DeadActions != 6 || snapshot.AmbiguousDeliveries != 9 || snapshot.OldestOpenAgeSeconds != 120 || snapshot.AppliedResults != 13 || snapshot.InconsistentLinks != 0 || snapshot.OldestActionAge != 15 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
