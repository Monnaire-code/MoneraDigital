package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"
	"testing"
	"time"
)

func TestAcquireAdvisoryLock_TimeoutHardFailure(t *testing.T) {
	state := newPinnedSessionDriverState()
	state.locked = true // already held — try_lock always false
	db := sql.OpenDB(&pinnedSessionConnector{state: state})
	defer db.Close()

	m := NewMigrator(db)
	if err := m.SetAdvisoryLockTimeout(30 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	m.lockPollInterval = 5 * time.Millisecond

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	err = m.acquireAdvisoryLock(context.Background(), conn)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !IsAdvisoryLockTimeout(err) {
		t.Fatalf("want AdvisoryLockTimeoutError, got %T %v", err, err)
	}
	if !strings.Contains(err.Error(), "key=8675309") {
		t.Fatalf("error should include lock key: %v", err)
	}
	if !strings.Contains(err.Error(), "holder_diagnostics=unavailable") && !strings.Contains(err.Error(), "holders=") {
		t.Fatalf("error should include diagnostics marker: %v", err)
	}
}

func TestAcquireAdvisoryLock_Success(t *testing.T) {
	state := newPinnedSessionDriverState()
	db := sql.OpenDB(&pinnedSessionConnector{state: state})
	defer db.Close()

	m := NewMigrator(db)
	if err := m.SetAdvisoryLockTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := m.acquireAdvisoryLock(context.Background(), conn); err != nil {
		t.Fatal(err)
	}
	m.releaseAdvisoryLock(context.Background(), conn)
}

func TestCollectHolderDiagnostics_FormatsSafeFields(t *testing.T) {
	db := sql.OpenDB(&diagConnector{})
	defer db.Close()
	m := NewMigrator(db)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	diag := m.collectAdvisoryLockHolderDiagnostics(context.Background(), conn, MigrationAdvisoryLockKey)
	if !strings.Contains(diag, "pid=42") || !strings.Contains(diag, "idle") {
		t.Fatalf("diag=%q", diag)
	}
	if strings.Contains(diag, "SELECT secrets") {
		t.Fatalf("must not include query text: %q", diag)
	}
}

// --- diagnostics fake driver ---

type diagConnector struct{}

func (diagConnector) Connect(context.Context) (driver.Conn, error) { return &diagConn{}, nil }
func (diagConnector) Driver() driver.Driver                        { return diagDriver{} }

type diagDriver struct{}

func (diagDriver) Open(string) (driver.Conn, error) { return &diagConn{}, nil }

type diagConn struct{}

func (*diagConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*diagConn) Close() error                        { return nil }
func (*diagConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (*diagConn) CheckNamedValue(*driver.NamedValue) error {
	return nil
}

func (*diagConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "pg_locks") {
		return &diagRows{
			cols: []string{"pid", "state", "application_name", "age"},
			rows: [][]driver.Value{{int64(42), "idle", "stuck-migrate", int64(99)}},
		}, nil
	}
	return &diagRows{cols: []string{"x"}, rows: nil}, nil
}

func (*diagConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

type diagRows struct {
	cols  []string
	rows  [][]driver.Value
	index int
}

func (r *diagRows) Columns() []string { return r.cols }
func (*diagRows) Close() error        { return nil }
func (r *diagRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}

var _ driver.QueryerContext = (*diagConn)(nil)
var _ driver.ExecerContext = (*diagConn)(nil)
var _ driver.NamedValueChecker = (*diagConn)(nil)

// SetAdvisoryLockTimeout must fail closed on non-positive bounds so library
// callers cannot accidentally request an infinite lock wait. This mirrors the
// CLI-side ParseAdvisoryLockTimeout fail-closed semantics (ADR 0003).
func TestSetAdvisoryLockTimeout_RejectsNonPositive(t *testing.T) {
	t.Parallel()
	m := NewMigrator(nil)
	before := m.lockTimeout

	cases := []time.Duration{0, -1, -time.Second}
	for _, d := range cases {
		if err := m.SetAdvisoryLockTimeout(d); err == nil {
			t.Fatalf("SetAdvisoryLockTimeout(%s) should error", d)
		}
		if m.lockTimeout != before {
			t.Fatalf("SetAdvisoryLockTimeout(%s) mutated lockTimeout to %s", d, m.lockTimeout)
		}
	}

	if err := m.SetAdvisoryLockTimeout(5 * time.Second); err != nil {
		t.Fatalf("positive duration should succeed: %v", err)
	}
	if m.lockTimeout != 5*time.Second {
		t.Fatalf("lockTimeout=%s want 5s", m.lockTimeout)
	}
}
