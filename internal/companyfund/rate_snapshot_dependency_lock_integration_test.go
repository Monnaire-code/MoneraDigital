package companyfund

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// This uses an explicitly supplied disposable PostgreSQL database. AppendRateSnapshot
// takes this lock before both a root correction's descendant scan and a derived
// append's input read, so a blocked second transaction demonstrates that those
// two operations cannot interleave around the dependency graph.
func TestRateSnapshotDependencyGraphLockSerializesConcurrentAppendBoundaries(t *testing.T) {
	databaseURL := os.Getenv("COMPANY_FUND_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set COMPANY_FUND_TEST_DATABASE_URL to run PostgreSQL advisory-lock integration coverage")
	}

	firstDB := openValuationTestPostgres(t, databaseURL)
	secondDB := openValuationTestPostgres(t, databaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstTx, err := firstDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	defer firstTx.Rollback()
	if _, err := firstTx.ExecContext(ctx, rateSnapshotDependencyGraphAdvisoryLockSQL); err != nil {
		t.Fatalf("acquire first graph lock: %v", err)
	}

	secondAttempting := make(chan struct{})
	secondAcquired := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondTx, err := secondDB.BeginTx(ctx, nil)
		if err != nil {
			secondDone <- err
			return
		}
		defer secondTx.Rollback()
		close(secondAttempting)
		if _, err := secondTx.ExecContext(ctx, rateSnapshotDependencyGraphAdvisoryLockSQL); err != nil {
			secondDone <- err
			return
		}
		close(secondAcquired)
		secondDone <- nil
	}()

	select {
	case <-secondAttempting:
	case <-ctx.Done():
		t.Fatalf("second transaction did not attempt graph lock: %v", ctx.Err())
	}
	select {
	case <-secondAcquired:
		t.Fatal("derived append boundary acquired graph lock before root correction committed")
	case <-time.After(100 * time.Millisecond):
	}
	if err := firstTx.Commit(); err != nil {
		t.Fatalf("commit root correction boundary: %v", err)
	}
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second transaction did not acquire graph lock after root commit: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("second transaction remained blocked after root commit: %v", ctx.Err())
	}
}

func openValuationTestPostgres(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
