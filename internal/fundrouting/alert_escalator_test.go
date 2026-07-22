package fundrouting

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestAlertEscalatorNextDueReadsEarliestMissingSLAThreshold(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	escalator, err := NewAlertEscalator(db)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Hour).Round(time.Microsecond)
	mock.ExpectQuery("SELECT min\\(routing.created_at \\+ threshold.minimum_age\\)").
		WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(due))

	got, err := escalator.NextDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(due) {
		t.Fatalf("NextDue=%s, want %s", got, due)
	}
}

func TestAlertEscalatorCreatesAtMostOneMissingOpenSLALevel(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	escalator, err := NewAlertEscalator(db)
	if err != nil {
		t.Fatal(err)
	}
	wakeCount := 0
	escalator.SetOnAlertCreated(func() { wakeCount++ })
	mock.ExpectQuery("INSERT INTO safeheron_transaction_routing_alerts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	processed, err := escalator.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if wakeCount != 1 {
		t.Fatalf("notifier wakes=%d, want 1 after alert insert", wakeCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAlertEscalatorReturnsIdleWhenNoThresholdIsDue(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	escalator, _ := NewAlertEscalator(db)
	wakeCount := 0
	escalator.SetOnAlertCreated(func() { wakeCount++ })
	mock.ExpectQuery("INSERT INTO safeheron_transaction_routing_alerts").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	processed, err := escalator.ProcessOne(context.Background())
	if err != nil || processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	if wakeCount != 0 {
		t.Fatalf("idle escalator emitted %d notifier wakes", wakeCount)
	}
}

func TestAlertEscalatorSLAThresholdsIncludeAllOpenCasesRegardlessOfReason(t *testing.T) {
	// Contract: quieting STATUS_NOT_TERMINAL immediate OPEN alerts must not
	// exclude those cases from age-based SLA_ESCALATION (1h ERROR / 24h CRITICAL).
	sqlText := `WITH thresholds(level,minimum_age,severity) AS (
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
	// Keep the production ProcessOne SQL and this contract string aligned.
	if got := openCaseSLAEscalationSQL(); got != sqlText {
		t.Fatalf("SLA escalator SQL drifted from quiet-alert contract:\n got: %s\nwant: %s", got, sqlText)
	}
}
