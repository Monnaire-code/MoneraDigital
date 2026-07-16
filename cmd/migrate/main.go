// cmd/migrate/main.go
//
// MoneraDigital Go migration runner. Replaces the previous dead state where
// the binary was excluded via //go:build ignore. Run from repo root:
//
//   DATABASE_URL=... go run ./cmd/migrate                  # apply all pending
//   DATABASE_URL=... go run ./cmd/migrate -dry-run        # status only
//   DATABASE_URL=... go run ./cmd/migrate -rollback       # roll back last
//
// In production the binary is intended to be invoked as a one-shot step
// in the deployment pipeline, not at server boot. See
// docs/security/MIGRATION-NOTES.md for the operational model.

package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"monera-digital/internal/buildinfo"
	"monera-digital/internal/migration"
	"monera-digital/internal/migration/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

var version = "dev"
var artifactMigrationCeiling = "052"

const controlledCommitOutcomeIndeterminateExitCode = 75

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	dryRun := flag.Bool("dry-run", false, "Print migration status and exit without applying anything")
	rollback := flag.Bool("rollback", false, "Roll back the most recently applied migration instead of applying pending ones")
	printCeiling := flag.Bool("print-ceiling", false, "Print the highest registered migration version and exit")
	printVersions := flag.Bool("print-versions", false, "Print the complete registered migration version list as JSON and exit")
	flag.Parse()
	if *printVersions {
		m := migration.NewMigrator(nil)
		registerMigrations(m)
		if err := json.NewEncoder(os.Stdout).Encode(m.RegisteredVersions()); err != nil {
			log.Fatal("Print migration versions:", err)
		}
		return
	}
	if *printCeiling {
		m := migration.NewMigrator(nil)
		registerMigrations(m)
		fmt.Println(m.Ceiling())
		return
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	provenanceURL, err := buildinfo.DatabaseURL(dbURL, version, os.Getenv("INVOCATION_ID"))
	if err != nil {
		log.Fatal("Invalid DATABASE_URL:", err)
	}
	db, err := sql.Open("pgx", provenanceURL)
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer db.Close()

	m := migration.NewMigrator(db)
	// Register in version order. Order matters: each migration is
	// recorded in the `migrations` tracking table with its version, and
	// the runner refuses to re-apply an already-recorded version.
	registerMigrations(m)

	switch {
	case *rollback:
		if err := m.Rollback(); err != nil {
			log.Fatal("Rollback failed:", err)
		}
		fmt.Println("Rollback complete.")
	case *dryRun:
		status, err := m.GetStatus()
		if err != nil {
			log.Fatal("Failed to get status:", err)
		}
		printStatus(status)
	default:
		if err := m.MigrateWithExpectedCeiling(os.Getenv("EXPECTED_MIGRATION_CEILING")); err != nil {
			log.Print("Migration failed:", err)
			os.Exit(migrationFailureExitCode(err))
		}
		status, err := m.GetStatus()
		if err != nil {
			log.Fatal("Failed to get status after migrate:", err)
		}
		printStatus(status)
	}
}

func migrationFailureExitCode(err error) int {
	if migration.IsControlledCommitOutcomeIndeterminate(err) {
		return controlledCommitOutcomeIndeterminateExitCode
	}
	return 1
}

// registerMigrations wires every known Go migration into the runner. Adding
// a new migration? Append it here AND add the corresponding *.go file under
// internal/migration/migrations/. The CI guard
// scripts/check-secrets.sh's migration entrypoint check enforces both
// pieces exist.
func registerMigrations(m *migration.Migrator) {
	if err := registerMigrationsForArtifact(m, artifactMigrationCeiling); err != nil {
		panic(err)
	}
}

func registerMigrationsForArtifact(m *migration.Migrator, ceiling string) error {
	if ceiling != "052" && ceiling != "053" {
		return fmt.Errorf("unsupported compiled migration ceiling %q", ceiling)
	}
	m.Register(&migrations.CreateUsersTable{})
	m.Register(&migrations.CreateLendingPositionsTable{})
	m.Register(&migrations.CreateWithdrawalTables{})
	m.Register(&migrations.AddTwoFactorColumnsMigration{})
	m.Register(&migrations.AddTwoFactorTimestampMigration{})
	m.Register(&migrations.UpdateWalletRequestsTable{})
	m.Register(&migrations.CreateUserWalletsTable{})
	m.Register(&migrations.AddUserWalletStatusField{})
	m.Register(&migrations.AddIsPrimaryToWhitelist{})
	m.Register(&migrations.CreateDepositsTable{})
	m.Register(&migrations.AddUserStatus{})
	m.Register(&migrations.AddFrozenUntilToWhitelist{})
	m.Register(&migrations.AddEmailVerifiedStatusAndContactFields{})
	m.Register(&migrations.SafeheronPhase1{})
	m.Register(&migrations.AccountFrozenBalanceDefault{})
	m.Register(&migrations.AddPendingStatusAndActivationFields{})
	m.Register(&migrations.NormalizeAmountTypes{})
	m.Register(&migrations.AddMissingForeignKeys{})
	m.Register(&migrations.CreateFundReports{})
	m.Register(&migrations.CreateCompanyFundLedger{})
	m.Register(&migrations.WidenAmountPrecision{})
	m.Register(&migrations.ExpandCompanyFundOccurrenceAndManualValuation{})
	if ceiling == "053" {
		m.Register(&migrations.EnforceSafeheronOccurrence{})
	}
	return nil
}

func printStatus(status []migration.MigrationStatus) {
	fmt.Println("\nMigration Status:")
	for _, s := range status {
		executed := "not run"
		if s.ExecutedAt != nil {
			executed = s.ExecutedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  %s  %-8s  %s  (executed: %s)\n",
			s.Version, s.Status, s.Name, executed)
	}
	fmt.Println()
}
