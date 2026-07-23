// internal/migration/migrator.go
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// ControlledCommitOutcomeIndeterminateError means PostgreSQL did not confirm
// whether the controlled transaction committed. Callers must reconcile the
// exact live schema and migration provenance before treating the run as done.
type ControlledCommitOutcomeIndeterminateError struct {
	Version string
	Err     error
}

func (err *ControlledCommitOutcomeIndeterminateError) Error() string {
	return fmt.Sprintf("commit controlled migration %s (outcome indeterminate; reconcile migrations row and schema before retry): %v", err.Version, err.Err)
}

func (err *ControlledCommitOutcomeIndeterminateError) Unwrap() error { return err.Err }

func IsControlledCommitOutcomeIndeterminate(err error) bool {
	var target *ControlledCommitOutcomeIndeterminateError
	return errors.As(err, &target)
}

// Migrator manages database migrations
type Migrator struct {
	db          *sql.DB
	migrations  []Migration
	lockTimeout time.Duration
	// now is injectable for tests; defaults to time.Now.
	now func() time.Time
	// lockPollInterval is the sleep between try-lock attempts (tests may shrink).
	lockPollInterval time.Duration
}

// ControlledMigration marks a migration that may only run under an exact
// release ceiling and after a checkpoint existed before this runner started.
type ControlledMigration interface {
	Migration
	RequiredPreexistingVersion() string
	RequiredExpectedCeiling() string
	UpTx(*sql.Tx) error
}

type migrationSession interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// NewMigrator creates a new migrator instance
func NewMigrator(db *sql.DB) *Migrator {
	return &Migrator{
		db:               db,
		migrations:       []Migration{},
		lockTimeout:      DefaultAdvisoryLockTimeout,
		now:              time.Now,
		lockPollInterval: 100 * time.Millisecond,
	}
}

// SetAdvisoryLockTimeout overrides the bound for session advisory lock acquisition.
// Non-positive values are ignored (keep default).
func (m *Migrator) SetAdvisoryLockTimeout(d time.Duration) {
	if m == nil || d <= 0 {
		return
	}
	m.lockTimeout = d
}

// Register registers a migration
func (m *Migrator) Register(migration Migration) {
	m.migrations = append(m.migrations, migration)
}

// Ceiling returns the highest registered migration version. Registration is
// ordered, but comparing protects the release gate from accidental reordering.
func (m *Migrator) Ceiling() string {
	var ceiling string
	for _, registered := range m.migrations {
		if registered.Version() > ceiling {
			ceiling = registered.Version()
		}
	}
	return ceiling
}

// RegisteredVersions returns the exact migration registration order without
// exposing the mutable internal slice or opening a database connection.
func (m *Migrator) RegisteredVersions() []string {
	versions := make([]string, 0, len(m.migrations))
	for _, registered := range m.migrations {
		versions = append(versions, registered.Version())
	}
	return versions
}

// Init initializes the migration tracking table.
func (m *Migrator) Init() error {
	return initMigrations(context.Background(), m.db)
}

func initMigrations(ctx context.Context, session migrationSession) error {
	query := `
	CREATE TABLE IF NOT EXISTS public.migrations (
		id SERIAL PRIMARY KEY,
		version VARCHAR(50) UNIQUE NOT NULL,
		name VARCHAR(255) NOT NULL,
		executed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)
	`

	_, err := session.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	log.Println("Migrations table initialized")
	return nil
}

// GetAppliedMigrations returns all applied migrations
func (m *Migrator) GetAppliedMigrations() ([]MigrationRecord, error) {
	return getAppliedMigrations(context.Background(), m.db)
}

func getAppliedMigrations(ctx context.Context, session migrationSession) ([]MigrationRecord, error) {
	query := `SELECT id, version, name, executed_at FROM public.migrations ORDER BY executed_at ASC`

	rows, err := session.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query migrations: %w", err)
	}
	defer rows.Close()

	var records []MigrationRecord
	for rows.Next() {
		var record MigrationRecord
		if err := rows.Scan(&record.ID, &record.Version, &record.Name, &record.ExecutedAt); err != nil {
			return nil, fmt.Errorf("failed to scan migration record: %w", err)
		}
		records = append(records, record)
	}

	return records, rows.Err()
}

// GetStatus returns the status of all migrations
func (m *Migrator) GetStatus() ([]MigrationStatus, error) {
	applied, err := m.GetAppliedMigrations()
	if err != nil {
		return nil, err
	}

	appliedMap := make(map[string]bool)
	appliedTimeMap := make(map[string]time.Time)

	for _, record := range applied {
		appliedMap[record.Version] = true
		appliedTimeMap[record.Version] = record.ExecutedAt
	}

	var statuses []MigrationStatus
	for _, migration := range m.migrations {
		version := migration.Version()
		status := "pending"
		var executedAt *time.Time

		if appliedMap[version] {
			status = "applied"
			t := appliedTimeMap[version]
			executedAt = &t
		}

		statuses = append(statuses, MigrationStatus{
			Version:    version,
			Name:       migration.Description(),
			Status:     status,
			ExecutedAt: executedAt,
		})
	}

	return statuses, nil
}

// Migrate runs all pending migrations under a session-level advisory lock
// so that two concurrent invocations (e.g., a deploy step racing a local
// ops run) cannot interleave DDL with `migrations` row inserts.
func (m *Migrator) Migrate() error {
	return m.MigrateWithExpectedCeiling("")
}

// MigrateWithExpectedCeiling applies pending migrations while enforcing exact
// artifact and pre-existing checkpoint boundaries for controlled migrations.
func (m *Migrator) MigrateWithExpectedCeiling(expectedCeiling string) error {
	if expectedCeiling != "" && expectedCeiling != m.Ceiling() {
		return fmt.Errorf("expected migration ceiling %s does not match registered ceiling %s", expectedCeiling, m.Ceiling())
	}
	ctx := context.Background()
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration session: %w", err)
	}
	defer conn.Close()
	if err := initMigrations(ctx, conn); err != nil {
		return err
	}

	if err := m.acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	defer m.releaseAdvisoryLock(ctx, conn)

	applied, err := getAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	appliedMap := make(map[string]bool)
	initialAppliedMap := make(map[string]bool)
	for _, record := range applied {
		appliedMap[record.Version] = true
		initialAppliedMap[record.Version] = true
	}

	for _, migration := range m.migrations {
		version := migration.Version()

		if appliedMap[version] {
			log.Printf("Migration %s already applied, skipping\n", version)
			continue
		}
		if controlled, ok := migration.(ControlledMigration); ok {
			if required := controlled.RequiredExpectedCeiling(); required != "" && expectedCeiling != required {
				return fmt.Errorf("migration %s requires explicit expected ceiling %s", version, required)
			}
			if required := controlled.RequiredPreexistingVersion(); required != "" && !initialAppliedMap[required] {
				return fmt.Errorf("migration %s requires migration %s to pre-exist before this invocation", version, required)
			}
			if err := m.runControlledMigration(ctx, conn, controlled); err != nil {
				return fmt.Errorf("migration %s failed: %w", version, err)
			}
			appliedMap[version] = true
			log.Printf("Migration %s completed successfully\n", version)
			continue
		}

		log.Printf("Running migration %s: %s\n", version, migration.Description())

		if err := migration.Up(m.db); err != nil {
			return fmt.Errorf("migration %s failed: %w", version, err)
		}

		query := `INSERT INTO public.migrations (version, name) VALUES ($1, $2)`
		_, err := conn.ExecContext(ctx, query, version, migration.Description())
		if err != nil {
			return fmt.Errorf("failed to record migration %s: %w", version, err)
		}

		log.Printf("Migration %s completed successfully\n", version)
		appliedMap[version] = true
	}

	return nil
}

func (m *Migrator) runControlledMigration(ctx context.Context, conn *sql.Conn, migration ControlledMigration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin controlled transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := migration.UpTx(tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO public.migrations (version, name) VALUES ($1, $2)`, migration.Version(), migration.Description()); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.Version(), err)
	}
	if err := tx.Commit(); err != nil {
		return &ControlledCommitOutcomeIndeterminateError{Version: migration.Version(), Err: err}
	}
	committed = true
	return nil
}

// Rollback reverts the last applied migration under the same advisory lock
// as Migrate so it cannot race with a concurrent Migrate.
func (m *Migrator) Rollback() error {
	ctx := context.Background()
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire rollback session: %w", err)
	}
	defer conn.Close()
	if err := initMigrations(ctx, conn); err != nil {
		return err
	}

	if err := m.acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	defer m.releaseAdvisoryLock(ctx, conn)

	applied, err := getAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	if len(applied) == 0 {
		log.Println("No migrations to rollback")
		return nil
	}

	lastRecord := applied[len(applied)-1]

	var migration Migration
	for _, m := range m.migrations {
		if m.Version() == lastRecord.Version {
			migration = m
			break
		}
	}

	if migration == nil {
		return fmt.Errorf("migration %s not found", lastRecord.Version)
	}

	log.Printf("Rolling back migration %s\n", lastRecord.Version)

	if err := migration.Down(m.db); err != nil {
		return fmt.Errorf("rollback of migration %s failed: %w", lastRecord.Version, err)
	}

	query := `DELETE FROM public.migrations WHERE version = $1`
	_, err = conn.ExecContext(ctx, query, lastRecord.Version)
	if err != nil {
		return fmt.Errorf("failed to remove migration record %s: %w", lastRecord.Version, err)
	}

	log.Printf("Migration %s rolled back successfully\n", lastRecord.Version)
	return nil
}
