package migration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestControlledRunnersPinLockDDLProvenanceAndUnlockToOneSessionWithoutInterleaving(t *testing.T) {
	state := newPinnedSessionDriverState()
	db := sql.OpenDB(&pinnedSessionConnector{state: state})
	defer db.Close()
	db.SetMaxOpenConns(2)

	run := func() error {
		calls := 0
		migrator := NewMigrator(db)
		migrator.Register(controlledTestMigration{
			version: "053", prior: "052", ceiling: "053", calls: &calls,
			upTx: func(tx *sql.Tx) error {
				_, err := tx.Exec(`ALTER TABLE controlled ADD CONSTRAINT migration_b CHECK (true)`)
				time.Sleep(25 * time.Millisecond)
				return err
			},
		})
		return migrator.MigrateWithExpectedCeiling("053")
	}
	errors := make(chan error, 2)
	go func() { errors <- run() }()
	go func() { errors <- run() }()
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}

	events := state.snapshotEvents()
	var migrationSession int
	for _, event := range events {
		if strings.Contains(event.action, "alter") {
			migrationSession = event.session
		}
	}
	if migrationSession == 0 {
		t.Fatalf("no controlled DDL event: %#v", events)
	}
	for _, required := range []string{"init", "lock", "query-applied", "begin", "alter", "record-053", "commit", "unlock"} {
		found := false
		for _, event := range events {
			if event.session == migrationSession && event.action == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("session %d missing %s: %#v", migrationSession, required, events)
		}
	}
	lockedSession := 0
	for _, event := range events {
		switch event.action {
		case "lock":
			if lockedSession != 0 {
				t.Fatalf("advisory lock interleaved sessions %d and %d: %#v", lockedSession, event.session, events)
			}
			lockedSession = event.session
		case "unlock":
			if lockedSession != event.session {
				t.Fatalf("session %d unlocked session %d lock: %#v", event.session, lockedSession, events)
			}
			lockedSession = 0
		}
	}
}

func TestPinnedRollbackCannotInterleaveWithControlledMigrate(t *testing.T) {
	state := newPinnedSessionDriverState()
	db := sql.OpenDB(&pinnedSessionConnector{state: state})
	defer db.Close()
	db.SetMaxOpenConns(3)
	entered, release := make(chan struct{}), make(chan struct{})
	migrateCalls := 0
	migrator := NewMigrator(db)
	migrator.Register(controlledTestMigration{
		version: "053", prior: "052", ceiling: "053", calls: &migrateCalls,
		upTx: func(tx *sql.Tx) error {
			if _, err := tx.Exec(`ALTER TABLE controlled ADD CONSTRAINT migration_b CHECK (true)`); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		},
	})
	rollbackCalls := 0
	rollback := NewMigrator(db)
	rollback.Register(sessionRollbackMigration{version: "053", calls: &rollbackCalls})
	migrateResult := make(chan error, 1)
	rollbackResult := make(chan error, 1)
	go func() { migrateResult <- migrator.MigrateWithExpectedCeiling("053") }()
	<-entered
	go func() { rollbackResult <- rollback.Rollback() }()
	<-state.waitObserved
	close(release)
	if err := <-migrateResult; err != nil {
		t.Fatal(err)
	}
	if err := <-rollbackResult; err != nil {
		t.Fatal(err)
	}
	if rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d", rollbackCalls)
	}

	events := state.snapshotEvents()
	lockedSession := 0
	rollbackSession := 0
	for _, event := range events {
		switch event.action {
		case "lock":
			if lockedSession != 0 {
				t.Fatalf("migrate and rollback locks interleaved: %#v", events)
			}
			lockedSession = event.session
		case "delete-053":
			if lockedSession != event.session {
				t.Fatalf("rollback provenance delete session %d escaped lock session %d: %#v", event.session, lockedSession, events)
			}
			rollbackSession = event.session
		case "down-053":
			if lockedSession == 0 {
				t.Fatalf("Down ran without pinned rollback lock: %#v", events)
			}
		case "unlock":
			if lockedSession != event.session {
				t.Fatalf("unlock session mismatch: %#v", events)
			}
			lockedSession = 0
		}
	}
	if rollbackSession == 0 {
		t.Fatalf("rollback did not delete provenance: %#v", events)
	}
	for _, required := range []string{"init", "lock", "query-applied", "delete-053", "unlock"} {
		found := false
		for _, event := range events {
			if event.session == rollbackSession && event.action == required {
				found = true
			}
		}
		if !found {
			t.Fatalf("rollback session %d missing %s: %#v", rollbackSession, required, events)
		}
	}
}

type sessionRollbackMigration struct {
	version string
	calls   *int
}

func (migration sessionRollbackMigration) Version() string     { return migration.version }
func (migration sessionRollbackMigration) Description() string { return migration.version }
func (migration sessionRollbackMigration) Up(*sql.DB) error    { return nil }
func (migration sessionRollbackMigration) Down(db *sql.DB) error {
	*migration.calls++
	_, err := db.Exec("DOWN " + migration.version)
	return err
}

type pinnedSessionEvent struct {
	session int
	action  string
}

type pinnedSessionDriverState struct {
	mu           sync.Mutex
	condition    *sync.Cond
	next         int
	locked       bool
	applied053   bool
	events       []pinnedSessionEvent
	waitOnce     sync.Once
	waitObserved chan struct{}
}

func newPinnedSessionDriverState() *pinnedSessionDriverState {
	state := &pinnedSessionDriverState{waitObserved: make(chan struct{})}
	state.condition = sync.NewCond(&state.mu)
	return state
}

func (state *pinnedSessionDriverState) snapshotEvents() []pinnedSessionEvent {
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]pinnedSessionEvent(nil), state.events...)
}

type pinnedSessionConnector struct{ state *pinnedSessionDriverState }

func (connector *pinnedSessionConnector) Connect(context.Context) (driver.Conn, error) {
	connector.state.mu.Lock()
	defer connector.state.mu.Unlock()
	connector.state.next++
	return &pinnedSessionConn{state: connector.state, id: connector.state.next}, nil
}
func (connector *pinnedSessionConnector) Driver() driver.Driver {
	return pinnedSessionDriver{state: connector.state}
}

type pinnedSessionDriver struct{ state *pinnedSessionDriverState }

func (driver pinnedSessionDriver) Open(string) (driver.Conn, error) {
	return (&pinnedSessionConnector{state: driver.state}).Connect(context.Background())
}

type pinnedSessionConn struct {
	state      *pinnedSessionDriverState
	id         int
	inTx       bool
	pending053 bool
}

func (*pinnedSessionConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*pinnedSessionConn) Close() error                        { return nil }
func (conn *pinnedSessionConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{})
}
func (conn *pinnedSessionConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	conn.inTx = true
	conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "begin"})
	return &pinnedSessionTx{conn: conn}, nil
}
func (conn *pinnedSessionConn) CheckNamedValue(*driver.NamedValue) error { return nil }

func (conn *pinnedSessionConn) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	switch {
	case strings.Contains(query, "pg_advisory_lock"):
		for conn.state.locked {
			conn.state.waitOnce.Do(func() { close(conn.state.waitObserved) })
			conn.state.condition.Wait()
		}
		conn.state.locked = true
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "lock"})
	case strings.Contains(query, "pg_advisory_unlock"):
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "unlock"})
		conn.state.locked = false
		conn.state.condition.Broadcast()
	case strings.Contains(query, "ALTER TABLE controlled"):
		if !conn.inTx {
			return nil, fmt.Errorf("controlled DDL escaped transaction")
		}
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "alter"})
	case strings.Contains(query, "INSERT INTO public.migrations"):
		if !conn.inTx || len(arguments) == 0 || arguments[0].Value != "053" {
			return nil, fmt.Errorf("migration provenance escaped controlled transaction")
		}
		conn.pending053 = true
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "record-053"})
	case strings.Contains(query, "DELETE FROM public.migrations"):
		if len(arguments) == 0 || arguments[0].Value != "053" {
			return nil, fmt.Errorf("unexpected rollback provenance")
		}
		conn.state.applied053 = false
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "delete-053"})
	case strings.Contains(query, "DOWN 053"):
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "down-053"})
	default:
		conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "init"})
	}
	return driver.RowsAffected(1), nil
}

func (conn *pinnedSessionConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	rows := [][]driver.Value{{int64(1), "052", "A", time.Unix(1, 0)}}
	if conn.state.applied053 {
		rows = append(rows, []driver.Value{int64(2), "053", "B", time.Unix(2, 0)})
	}
	conn.state.events = append(conn.state.events, pinnedSessionEvent{session: conn.id, action: "query-applied"})
	return &pinnedSessionRows{rows: rows}, nil
}

type pinnedSessionTx struct{ conn *pinnedSessionConn }

func (tx *pinnedSessionTx) Commit() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	if tx.conn.pending053 {
		tx.conn.state.applied053 = true
	}
	tx.conn.inTx = false
	tx.conn.pending053 = false
	tx.conn.state.events = append(tx.conn.state.events, pinnedSessionEvent{session: tx.conn.id, action: "commit"})
	return nil
}
func (tx *pinnedSessionTx) Rollback() error {
	tx.conn.state.mu.Lock()
	defer tx.conn.state.mu.Unlock()
	tx.conn.inTx = false
	tx.conn.pending053 = false
	tx.conn.state.events = append(tx.conn.state.events, pinnedSessionEvent{session: tx.conn.id, action: "rollback"})
	return nil
}

type pinnedSessionRows struct {
	rows  [][]driver.Value
	index int
}

func (*pinnedSessionRows) Columns() []string { return []string{"id", "version", "name", "executed_at"} }
func (*pinnedSessionRows) Close() error      { return nil }
func (rows *pinnedSessionRows) Next(destination []driver.Value) error {
	if rows.index == len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

var _ driver.ExecerContext = (*pinnedSessionConn)(nil)
var _ driver.QueryerContext = (*pinnedSessionConn)(nil)
var _ driver.ConnBeginTx = (*pinnedSessionConn)(nil)
var _ driver.NamedValueChecker = (*pinnedSessionConn)(nil)
