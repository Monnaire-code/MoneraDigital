// internal/migration/migrator.go
package migration

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// Migrator manages database migrations
type Migrator struct {
	db         *sql.DB
	migrations []Migration
}

// NewMigrator creates a new migrator instance
func NewMigrator(db *sql.DB) *Migrator {
	return &Migrator{
		db:         db,
		migrations: []Migration{},
	}
}

// Register registers a migration
func (m *Migrator) Register(migration Migration) {
	m.migrations = append(m.migrations, migration)
}

// Init initializes the migration tracking table.
func (m *Migrator) Init() error {
	query := `
	CREATE TABLE IF NOT EXISTS migrations (
		id SERIAL PRIMARY KEY,
		version VARCHAR(50) UNIQUE NOT NULL,
		name VARCHAR(255) NOT NULL,
		executed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)
	`

	_, err := m.db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	log.Println("Migrations table initialized")
	return nil
}

// GetAppliedMigrations returns all applied migrations
func (m *Migrator) GetAppliedMigrations() ([]MigrationRecord, error) {
	query := `SELECT id, version, name, executed_at FROM migrations ORDER BY executed_at ASC`

	rows, err := m.db.Query(query)
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
	if err := m.Init(); err != nil {
		return err
	}

	// 8675309 is an arbitrary 32-bit key. The memorable value makes it
	// easy to look up in pg_locks if a stuck run is suspected.
	const advisoryLockKey int64 = 8675309
	if _, err := m.db.Exec(`SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		if _, err := m.db.Exec(`SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
			log.Printf("warning: failed to release migration lock: %v", err)
		}
	}()

	applied, err := m.GetAppliedMigrations()
	if err != nil {
		return err
	}

	appliedMap := make(map[string]bool)
	for _, record := range applied {
		appliedMap[record.Version] = true
	}

	for _, migration := range m.migrations {
		version := migration.Version()

		if appliedMap[version] {
			log.Printf("Migration %s already applied, skipping\n", version)
			continue
		}

		log.Printf("Running migration %s: %s\n", version, migration.Description())

		if err := migration.Up(m.db); err != nil {
			return fmt.Errorf("migration %s failed: %w", version, err)
		}

		query := `INSERT INTO migrations (version, name) VALUES ($1, $2)`
		_, err := m.db.Exec(query, version, migration.Description())
		if err != nil {
			return fmt.Errorf("failed to record migration %s: %w", version, err)
		}

		log.Printf("Migration %s completed successfully\n", version)
	}

	return nil
}

// Rollback reverts the last applied migration under the same advisory lock
// as Migrate so it cannot race with a concurrent Migrate.
func (m *Migrator) Rollback() error {
	if err := m.Init(); err != nil {
		return err
	}

	const advisoryLockKey int64 = 8675309
	if _, err := m.db.Exec(`SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		if _, err := m.db.Exec(`SELECT pg_advisory_unlock($1)`, advisoryLockKey); err != nil {
			log.Printf("warning: failed to release migration lock: %v", err)
		}
	}()

	applied, err := m.GetAppliedMigrations()
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

	query := `DELETE FROM migrations WHERE version = $1`
	_, err = m.db.Exec(query, lastRecord.Version)
	if err != nil {
		return fmt.Errorf("failed to remove migration record %s: %w", lastRecord.Version, err)
	}

	log.Printf("Migration %s rolled back successfully\n", lastRecord.Version)
	return nil
}
