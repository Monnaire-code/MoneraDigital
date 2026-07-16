package migration

import (
	"database/sql"
	"testing"
)

type ceilingMigration struct{ version string }

func (m ceilingMigration) Version() string     { return m.version }
func (m ceilingMigration) Description() string { return m.version }
func (m ceilingMigration) Up(*sql.DB) error    { return nil }
func (m ceilingMigration) Down(*sql.DB) error  { return nil }

func TestMigrationCeiling(t *testing.T) {
	t.Parallel()
	migrator := NewMigrator(nil)
	migrator.Register(ceilingMigration{version: "001"})
	migrator.Register(ceilingMigration{version: "051"})
	migrator.Register(ceilingMigration{version: "052"})
	if got := migrator.Ceiling(); got != "052" {
		t.Fatalf("Ceiling() = %q, want 052", got)
	}
}
