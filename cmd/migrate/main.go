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
	"flag"
	"fmt"
	"log"
	"os"

	"monera-digital/internal/migration"
	"monera-digital/internal/migration/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	dryRun := flag.Bool("dry-run", false, "Print migration status and exit without applying anything")
	rollback := flag.Bool("rollback", false, "Roll back the most recently applied migration instead of applying pending ones")
	flag.Parse()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := sql.Open("pgx", dbURL)
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
		if err := m.Migrate(); err != nil {
			log.Fatal("Migration failed:", err)
		}
		status, err := m.GetStatus()
		if err != nil {
			log.Fatal("Failed to get status after migrate:", err)
		}
		printStatus(status)
	}
}

// registerMigrations wires every known Go migration into the runner. Adding
// a new migration? Append it here AND add the corresponding *.go file under
// internal/migration/migrations/. The CI guard
// scripts/check-secrets.sh's migration entrypoint check enforces both
// pieces exist.
func registerMigrations(m *migration.Migrator) {
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
	// Approval Callback Service: approval_records + sweep_transactions
	m.Register(&migrations.CreateApprovalAndSweepTables{})
	// v1.1 Phase 1 AML hard block: approval_records.aml_risk_level
	m.Register(&migrations.AddAmlRiskLevelToApprovalRecords{})
	m.Register(&migrations.CreateFundReports{})
	m.Register(&migrations.AddPendingStatusAndActivationFields{})
	m.Register(&migrations.NormalizeAmountTypes{})
	m.Register(&migrations.AddMissingForeignKeys{})
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
