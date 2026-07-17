// cmd/migrate/main.go
//
// MoneraDigital Go migration runner. Replaces the previous dead state where
// the binary was excluded via //go:build ignore. Run from repo root:
//
//   DATABASE_URL=... go run ./cmd/migrate                  # apply all pending
//   DATABASE_URL=... go run ./cmd/migrate -dry-run        # status only
//   DATABASE_URL=... go run ./cmd/migrate -rollback       # roll back last
//   EXPECTED_MIGRATION_CEILING=050 DATABASE_URL=... \
//     go run ./cmd/migrate -exact-version 050             # apply only 050
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
var artifactMigrationCeiling = "057"

const controlledCommitOutcomeIndeterminateExitCode = 75

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	dryRun := flag.Bool("dry-run", false, "Print migration status and exit without applying anything")
	rollback := flag.Bool("rollback", false, "Roll back the most recently applied migration instead of applying pending ones")
	printCeiling := flag.Bool("print-ceiling", false, "Print the highest registered migration version and exit")
	printVersions := flag.Bool("print-versions", false, "Print the complete registered migration version list as JSON and exit")
	exactVersion := flag.String("exact-version", "", "Register and run exactly one approved production migration")
	flag.Parse()
	if *printVersions {
		m := migration.NewMigrator(nil)
		if err := registerSelectedMigrations(m, *exactVersion); err != nil {
			log.Fatal("Select migrations:", err)
		}
		if err := json.NewEncoder(os.Stdout).Encode(m.RegisteredVersions()); err != nil {
			log.Fatal("Print migration versions:", err)
		}
		return
	}
	if *printCeiling {
		m := migration.NewMigrator(nil)
		if err := registerSelectedMigrations(m, *exactVersion); err != nil {
			log.Fatal("Select migrations:", err)
		}
		fmt.Println(m.Ceiling())
		return
	}
	expectedCeiling := os.Getenv("EXPECTED_MIGRATION_CEILING")
	predecessor, err := validateExactMigrationOptions(*exactVersion, expectedCeiling, *rollback)
	if err != nil {
		log.Fatal("Invalid migration selection:", err)
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
	if err := registerSelectedMigrations(m, *exactVersion); err != nil {
		log.Fatal("Select migrations:", err)
	}
	if predecessor != "" {
		if err := requireAppliedMigration(db, predecessor); err != nil {
			log.Fatal("Validate exact migration predecessor:", err)
		}
	}

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
		if err := m.MigrateWithExpectedCeiling(expectedCeiling); err != nil {
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

func validateExactMigrationOptions(exactVersion, expectedCeiling string, rollback bool) (string, error) {
	if exactVersion == "" {
		return "", nil
	}
	if rollback {
		return "", fmt.Errorf("exact-version cannot be combined with rollback")
	}
	if expectedCeiling != exactVersion {
		return "", fmt.Errorf("exact-version %s requires EXPECTED_MIGRATION_CEILING=%s", exactVersion, exactVersion)
	}
	predecessors := map[string]string{
		"050": "049",
		"051": "050",
		"052": "051",
		"053": "052",
		"054": "053",
		"055": "054",
		"056": "055",
		"057": "056",
	}
	predecessor, ok := predecessors[exactVersion]
	if !ok {
		return "", fmt.Errorf("unsupported exact migration version %q", exactVersion)
	}
	return predecessor, nil
}

func requireAppliedMigration(db *sql.DB, version string) error {
	var applied bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM public.migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
		return fmt.Errorf("query migration %s provenance: %w", version, err)
	}
	if !applied {
		return fmt.Errorf("migration %s must be applied before this exact migration", version)
	}
	return nil
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
	if ceiling != "052" && ceiling != "053" && ceiling != "054" && ceiling != "055" && ceiling != "056" && ceiling != "057" {
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
	if ceiling == "053" || ceiling == "054" || ceiling == "055" || ceiling == "056" || ceiling == "057" {
		m.Register(&migrations.EnforceSafeheronOccurrence{})
	}
	if ceiling == "054" || ceiling == "055" || ceiling == "056" || ceiling == "057" {
		m.Register(&migrations.AllowManualCompanyFundTransactions{})
	}
	if ceiling == "055" || ceiling == "056" || ceiling == "057" {
		m.Register(&migrations.AddCounterpartyNameOverride{})
	}
	if ceiling == "056" || ceiling == "057" {
		m.Register(&migrations.UnifySafeheronAddressOwnership{})
	}
	if ceiling == "057" {
		m.Register(&migrations.CreateSafeheronRoutingCases{})
	}
	return nil
}

func registerSelectedMigrations(m *migration.Migrator, exactVersion string) error {
	if exactVersion == "" {
		registerMigrations(m)
		return nil
	}
	switch exactVersion {
	case "050":
		m.Register(&migrations.CreateCompanyFundLedger{})
	case "051":
		m.Register(&migrations.WidenAmountPrecision{})
	case "052":
		m.Register(&migrations.ExpandCompanyFundOccurrenceAndManualValuation{})
	case "053":
		m.Register(&migrations.EnforceSafeheronOccurrence{})
	case "054":
		m.Register(&migrations.AllowManualCompanyFundTransactions{})
	case "055":
		m.Register(&migrations.AddCounterpartyNameOverride{})
	case "056":
		m.Register(&migrations.UnifySafeheronAddressOwnership{})
	case "057":
		m.Register(&migrations.CreateSafeheronRoutingCases{})
	default:
		return fmt.Errorf("unsupported exact migration version %q", exactVersion)
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
