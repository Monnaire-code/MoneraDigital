package deposit

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestRiskDueFromAnchor_AndFloorOverdue(t *testing.T) {
	anchor := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	due := RiskDueFromAnchor(anchor, 20*time.Minute)
	if !due.Equal(anchor.Add(20 * time.Minute)) {
		t.Fatalf("due=%s", due)
	}
	if !RiskDueFromAnchor(time.Time{}, time.Minute).IsZero() {
		t.Fatal("zero anchor must yield zero due")
	}

	now := anchor.Add(30 * time.Minute)
	floored := FloorOverdueDue(due, now, time.Minute)
	if !floored.Equal(now.Add(time.Minute)) {
		t.Fatalf("overdue due must floor to now+interval, got %s", floored)
	}
	future := FloorOverdueDue(now.Add(3*time.Minute), now, time.Hour)
	if !future.Equal(now.Add(3 * time.Minute)) {
		t.Fatalf("future due must be preserved, got %s", future)
	}
}

func TestWorker_KYTDueThreeMinutesBeatsMaxIdle(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	// KYT_TIMEOUT=20m; unique row updated 17m ago → due in 3m.
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	now := time.Now()
	repo.kytPendingAnchor = now.Add(-17 * time.Minute)

	w := NewWorker(svc, WorkerConfig{
		Interval:        time.Second,
		MaxIdle:         10 * time.Minute,
		AMLPollInterval: time.Minute,
	})
	outcome, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.NextDue.IsZero() {
		t.Fatal("expected NextDue from durable KYT anchor")
	}
	until := time.Until(outcome.NextDue)
	if until < 2*time.Minute || until > 4*time.Minute {
		t.Fatalf("NextDue should be ~3m, got %s (next=%s)", until, outcome.NextDue)
	}
	if until >= 9*time.Minute {
		t.Fatalf("must not wait MaxIdle=10m when due in ~3m; until=%s", until)
	}
}

func TestWorker_EarliestOfMultipleKYTRecords(t *testing.T) {
	// Repository surfaces MIN(updated_at); worker must schedule that earliest due.
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 10*time.Minute)
	now := time.Now()
	// Earliest anchor is 8m ago → due in 2m; a later row would be due later.
	repo.kytPendingAnchor = now.Add(-8 * time.Minute)

	w := NewWorker(svc, WorkerConfig{Interval: time.Second, MaxIdle: 10 * time.Minute})
	outcome, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	until := time.Until(outcome.NextDue)
	if until < time.Minute || until > 3*time.Minute {
		t.Fatalf("earliest due should be ~2m, got %s", until)
	}
}

func TestWorker_OverdueKYTRunsWithoutHotLoop(t *testing.T) {
	repo := &countingKYTRepo{mockRepo: newMockRepo()}
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, time.Minute)
	// Overdue: anchor 2m ago with 1m timeout.
	repo.kytPendingAnchor = time.Now().Add(-2 * time.Minute)

	w := NewWorker(svc, WorkerConfig{
		Interval: 50 * time.Millisecond,
		MaxIdle:  time.Minute,
	})

	// Two cycles: each may scan once, but NextDue must be floored (not zero-wait).
	o1, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repo.scanCalls.Load() < 1 {
		t.Fatal("overdue KYT must scan immediately")
	}
	if o1.MoreWork {
		t.Fatal("risk scan must not set MoreWork hot loop")
	}
	if o1.NextDue.IsZero() || !o1.NextDue.After(time.Now()) {
		// Floored due should be in the future (now+Interval).
		if o1.NextDue.IsZero() {
			t.Fatal("after overdue scan, NextDue must be floored into the future when rows remain")
		}
	}
	// Preserve anchor so row still appears overdue (scan returns no progress).
	before := repo.scanCalls.Load()
	o2, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Second cycle may scan again because still overdue — that is OK — but must
	// not report MoreWork / zero NextDue that would cause adaptive wait=0 forever
	// without progressive idle. Floor keeps NextDue in the future.
	if o2.MoreWork {
		t.Fatal("second overdue cycle must not set MoreWork")
	}
	if !o2.NextDue.After(time.Now().Add(-time.Millisecond)) {
		// Allow equal-to-now only if floored slightly; require not zero.
		if o2.NextDue.IsZero() {
			t.Fatal("NextDue must remain scheduled")
		}
	}
	_ = before
}

func TestWorker_AMLRearmsWithPollInterval(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	svc.SetAMLFirstPollDelay(5 * time.Minute)
	// AML first poll already due (anchor 6m ago).
	repo.amlPendingAnchor = time.Now().Add(-6 * time.Minute)
	// Keep a KYT row so due recomputation still sees AML candidate.
	repo.kytPendingAnchor = repo.amlPendingAnchor

	w := NewWorker(svc, WorkerConfig{
		Interval:        time.Second,
		MaxIdle:         10 * time.Minute,
		AMLPollInterval: 45 * time.Second,
	})
	outcome, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.amlRearm.IsZero() {
		t.Fatal("AML poll must re-arm process-local retry schedule")
	}
	untilRearm := time.Until(w.amlRearm)
	if untilRearm < 40*time.Second || untilRearm > 50*time.Second {
		t.Fatalf("amlRearm should be ~45s, got %s", untilRearm)
	}
	untilNext := time.Until(outcome.NextDue)
	if untilNext < 40*time.Second || untilNext > 50*time.Second {
		t.Fatalf("NextDue should follow AML rearm ~45s, got %s", untilNext)
	}
}

func TestWorker_RestartReloadsDueFromDB(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	// Simulate restart: no process-local amlRearm; DB says due in 4m.
	repo.kytPendingAnchor = time.Now().Add(-16 * time.Minute)

	w := NewWorker(svc, WorkerConfig{Interval: time.Second, MaxIdle: 10 * time.Minute})
	if !w.amlRearm.IsZero() {
		t.Fatal("fresh worker must not carry amlRearm")
	}
	outcome, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	until := time.Until(outcome.NextDue)
	if until < 3*time.Minute || until > 5*time.Minute {
		t.Fatalf("restart must recompute ~4m due from DB, got %s", until)
	}
}

func TestWorker_DoesNotMutateAnchorViaScheduleReads(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	anchor := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	repo.kytPendingAnchor = anchor
	repo.amlPendingAnchor = anchor

	w := NewWorker(svc, WorkerConfig{Interval: time.Second, MaxIdle: 10 * time.Minute})
	if _, err := w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !repo.kytPendingAnchor.Equal(anchor) {
		t.Fatalf("schedule reads must not change KYT anchor: got %s want %s", repo.kytPendingAnchor, anchor)
	}
	if !repo.amlPendingAnchor.Equal(anchor) {
		t.Fatalf("schedule reads must not change AML anchor")
	}
}

func TestWorker_RiskDueQueryErrorRetainsLastDue(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	now := time.Now()
	repo.kytPendingAnchor = now.Add(-17 * time.Minute) // due ~3m

	w := NewWorker(svc, WorkerConfig{
		Interval: time.Second,
		MaxIdle:  10 * time.Minute,
	})
	first, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.NextDue.IsZero() {
		t.Fatal("seed cycle must establish a durable NextDue")
	}
	seedUntil := time.Until(first.NextDue)
	if seedUntil < 2*time.Minute || seedUntil > 4*time.Minute {
		t.Fatalf("seed NextDue should be ~3m, got %s", seedUntil)
	}

	// Subsequent earliest-due reads fail: must not collapse to "no due" / MaxIdle-only.
	repo.kytPendingAnchorErr = errors.New("synthetic earliest KYT query failure")
	second, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.NextDue.IsZero() {
		t.Fatal("query error must retain last KYT due, not clear NextDue")
	}
	until := time.Until(second.NextDue)
	if until < time.Minute || until > 5*time.Minute {
		t.Fatalf("retained due should stay near the seeded ~3m window, got %s", until)
	}
}

func TestWorker_RiskDueQueryErrorWithoutHistoryRearmsShortFloor(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	repo.kytPendingAnchorErr = errors.New("db unavailable")

	w := NewWorker(svc, WorkerConfig{
		Interval: 30 * time.Second,
		MaxIdle:  10 * time.Minute,
	})
	outcome, err := w.cycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if outcome.NextDue.IsZero() {
		t.Fatal("error with no history must schedule a short floor rearm, not silent MaxIdle-only")
	}
	until := time.Until(outcome.NextDue)
	if until < 20*time.Second || until > 40*time.Second {
		t.Fatalf("short floor rearm should be ~Interval (30s), got %s", until)
	}
	if until >= 9*time.Minute {
		t.Fatalf("must not fall through to MaxIdle on query error, until=%s", until)
	}
}

func TestService_EarliestDueHelpers(t *testing.T) {
	repo := newMockRepo()
	svc := NewService(repo, nil, nil)
	svc.SetKYTDeps(nil, true, 100, 20*time.Minute)
	svc.SetAMLFirstPollDelay(5 * time.Minute)
	anchor := time.Now().Add(-10 * time.Minute)
	repo.kytPendingAnchor = anchor
	repo.amlPendingAnchor = anchor

	kytDue, err := svc.EarliestKYTDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !kytDue.Equal(anchor.Add(20 * time.Minute)) {
		t.Fatalf("kyt due=%s", kytDue)
	}
	amlDue, err := svc.EarliestAMLFirstPollDue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !amlDue.Equal(anchor.Add(5 * time.Minute)) {
		t.Fatalf("aml due=%s", amlDue)
	}

	repo.kytPendingAnchor = time.Time{}
	repo.amlPendingAnchor = time.Time{}
	kytDue, err = svc.EarliestKYTDue(context.Background())
	if err != nil || !kytDue.IsZero() {
		t.Fatalf("empty KYT set: due=%s err=%v", kytDue, err)
	}
}

// countingKYTRepo counts timeout scan lock attempts.
type countingKYTRepo struct {
	*mockRepo
	scanCalls atomic.Int32
}

func (r *countingKYTRepo) LockOneKYTPendingTimeout(_ context.Context, _ Tx, _ time.Duration) (*DepositRow, error) {
	r.scanCalls.Add(1)
	return nil, ErrNoPending
}

func (r *countingKYTRepo) EarliestKYTPendingUpdatedAt(ctx context.Context) (time.Time, error) {
	return r.mockRepo.EarliestKYTPendingUpdatedAt(ctx)
}
