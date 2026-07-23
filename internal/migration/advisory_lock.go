package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// AdvisoryLockTimeoutError means the migration session could not take the
// advisory lock within the configured bound (ADR 0003).
type AdvisoryLockTimeoutError struct {
	Key         int64
	Timeout     time.Duration
	Diagnostics string
}

func (err *AdvisoryLockTimeoutError) Error() string {
	if err == nil {
		return "migration advisory lock timed out"
	}
	msg := fmt.Sprintf("migration advisory lock timed out: key=%d timeout=%s", err.Key, err.Timeout)
	if err.Diagnostics != "" {
		msg += "; " + err.Diagnostics
	}
	return msg
}

func IsAdvisoryLockTimeout(err error) bool {
	var target *AdvisoryLockTimeoutError
	return errors.As(err, &target)
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (m *Migrator) acquireAdvisoryLock(ctx context.Context, conn queryRower) error {
	timeout := m.lockTimeout
	if timeout <= 0 {
		timeout = DefaultAdvisoryLockTimeout
	}
	poll := m.lockPollInterval
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	if poll > timeout {
		poll = timeout
	}
	key := MigrationAdvisoryLockKey

	// Bound the whole acquire loop with a single context deadline so we do not
	// stack time.After timers and so cancellation is consistent.
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	timer := time.NewTimer(0)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	defer timer.Stop()

	for {
		if err := waitCtx.Err(); err != nil {
			return m.advisoryLockTimeout(conn, key, timeout)
		}

		var acquired bool
		err := conn.QueryRowContext(waitCtx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired)
		if err != nil {
			if waitCtx.Err() != nil {
				return m.advisoryLockTimeout(conn, key, timeout)
			}
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if acquired {
			return nil
		}

		timer.Reset(poll)
		select {
		case <-waitCtx.Done():
			return m.advisoryLockTimeout(conn, key, timeout)
		case <-timer.C:
		}
	}
}

func (m *Migrator) advisoryLockTimeout(conn queryRower, key int64, timeout time.Duration) error {
	// Short independent budget: waitCtx is already expired when we get here.
	diagCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	diag := m.collectAdvisoryLockHolderDiagnostics(diagCtx, conn, key)
	log.Printf("migration advisory lock timeout: key=%d timeout=%s %s", key, timeout, diag)
	return &AdvisoryLockTimeoutError{Key: key, Timeout: timeout, Diagnostics: diag}
}

func (m *Migrator) releaseAdvisoryLock(ctx context.Context, conn queryRower) {
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, MigrationAdvisoryLockKey); err != nil {
		log.Printf("warning: failed to release migration lock: %v", err)
	}
}

// collectAdvisoryLockHolderDiagnostics is best-effort and never terminates backends.
func (m *Migrator) collectAdvisoryLockHolderDiagnostics(ctx context.Context, conn queryRower, key int64) string {
	classid, objid := AdvisoryLockClassAndObj(key)
	// objsubid=2 is session-level advisory lock (PostgreSQL locktag convention).
	// Scope to current_database() so other DBs on a shared instance are ignored.
	rows, err := conn.QueryContext(ctx, `
SELECT l.pid,
       COALESCE(a.state, ''),
       COALESCE(a.application_name, ''),
       COALESCE(EXTRACT(EPOCH FROM (now() - a.state_change))::bigint, 0)
  FROM pg_locks l
  LEFT JOIN pg_stat_activity a ON a.pid = l.pid
 WHERE l.locktype = 'advisory'
   AND l.classid = $1
   AND l.objid = $2
   AND l.objsubid = 2
   AND l.granted = true
   AND l.database = (SELECT oid FROM pg_database WHERE datname = current_database())
 LIMIT 5`, classid, objid)
	if err != nil {
		return "holder_diagnostics=unavailable"
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var pid int64
		var state, app string
		var ageSec int64
		if err := rows.Scan(&pid, &state, &app, &ageSec); err != nil {
			return "holder_diagnostics=unavailable"
		}
		// Do not include query text.
		parts = append(parts, fmt.Sprintf("pid=%d state=%s age_sec=%d application_name=%q",
			pid, sanitizeDiagToken(state), ageSec, sanitizeDiagToken(app)))
	}
	if err := rows.Err(); err != nil {
		return "holder_diagnostics=unavailable"
	}
	if len(parts) == 0 {
		return "holder_diagnostics=unavailable"
	}
	return "holders=[" + strings.Join(parts, "; ") + "]"
}

func sanitizeDiagToken(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
