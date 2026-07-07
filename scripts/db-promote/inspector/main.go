// scripts/db-promote/inspector/main.go
// Inspector + apply/rollback tool for the fund_reports migration 049.
//
// Subcommands:
//
//	preflight   read-only: confirm DB reachable, env sane, migration source
//	apply       idempotent: run the Go Migrator (skips already-applied)
//	verify      read-only: post-apply schema/data integrity check
//	rollback    destructive: drop fund_reports + fund_asset_allocations
//	info        read-only: dump current migration + table state
//
// go run scripts/db-promote/inspector/main.go <subcommand> [--env-file PATH]
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"monera-digital/internal/migration"
	"monera-digital/internal/migration/migrations"
)

const (
	migrationVersion = "049"
	migrationName    = "Create fund_reports and fund_asset_allocations tables for the public homepage AUM widget, seeded with 2026 Jan–May monthly data (formerly migration 016)"

	expectedFundReportsCols = 12
	expectedAllocCols       = 7
	expectedFundReportsRows = 5
	expectedAllocRows       = 4
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	envFile := pickEnvFile(args)

	if err := godotenv.Overload(envFile); err != nil {
		log.Printf("(env file %s not loaded: %v)", envFile, err)
	}

	switch cmd {
	case "preflight":
		os.Exit(runPreflight())
	case "apply":
		os.Exit(runApply())
	case "verify":
		os.Exit(runVerify())
	case "rollback":
		os.Exit(runRollback())
	case "info":
		os.Exit(runInfo())
	case "dsn":
		// Print the loaded DATABASE_URL to stdout. Used by shell scripts
		// to pass DSN to other tools (e.g. pg_dump) without re-parsing
		// the env file.
		if v := os.Getenv("DATABASE_URL"); v != "" {
			fmt.Println(v)
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "DATABASE_URL is empty")
		os.Exit(1)
	case "help", "-h", "--help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `inspector — fund_reports migration 016 tool

Subcommands:
  preflight   read-only pre-deploy check (env, DB reachability, source migration)
  apply       idempotent: run the Go Migrator (skips 016 if already applied)
  verify      read-only: post-apply schema + data integrity
  rollback    destructive: drop fund_reports and fund_asset_allocations
  info        read-only: dump current state
  dsn         print the loaded DATABASE_URL to stdout
  help        this help

Env: DATABASE_URL (loaded from --env-file, then .env.prod, .env, os env).
      ENV_FILE=<path> can override the env-file selection.
`)
}

// pickEnvFile chooses which .env to load for Go DB operations.
//
// This inspector targets the Go backend. The project's .env files have
// split responsibilities:
//   - .env         DATABASE_URL + Go-side secrets (used by cmd/migrate)
//   - .env.prod    Vercel/frontend-only env (ENCRYPTION_KEY, JWT_SECRET,
//     VERCEL_OIDC_TOKEN, Upstash) — NOT for the Go backend
//
// Priority:
//  1. --env-file <path>  CLI arg
//  2. ENV_FILE env var
//  3. .env              (only — never .env.prod, which is wrong file)
func pickEnvFile(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--env-file" && i+1 < len(args) {
			return args[i+1]
		}
	}
	if v := os.Getenv("ENV_FILE"); v != "" {
		return v
	}
	cwd, _ := os.Getwd()
	for _, name := range []string{".env"} {
		p := filepath.Join(cwd, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if _, err := os.Stat(name); err == nil {
			return name
		}
	}
	return filepath.Join(cwd, ".env")
}

func openDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is empty after loading env file")
	}
	// Register an AfterConnect hook so every new pooled connection
	// has search_path = public. Necessary because Neon's default for
	// this user is empty, which breaks unqualified DDL like
	// `CREATE TABLE migrations` ("no schema has been selected").
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.AfterConnect = pgconn.AfterConnectFunc(func(ctx context.Context, conn *pgconn.PgConn) error {
		return conn.Exec(ctx, "SET search_path TO public").Close()
	})
	db := sql.OpenDB(stdlib.GetConnector(*cfg))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return db, nil
}

// mustOK reports a check's outcome. It returns (ok, err) so callers can
// branch without the error-typed value. On FAIL it also prints a
// `hint` line with the next action to take.
func mustOK(cond bool, label string, detail string) (bool, error) {
	if cond {
		fmt.Printf("  \033[32mOK\033[0m   %s\n", label)
		return true, nil
	}
	fmt.Printf("  \033[31mFAIL\033[0m %s", label)
	if detail != "" {
		fmt.Printf(" — %s", detail)
	}
	fmt.Println()
	return false, errors.New(label)
}

// mustOKHint is mustOK with an actionable hint printed on FAIL.
func mustOKHint(cond bool, label, detail, hint string) (bool, error) {
	ok, err := mustOK(cond, label, detail)
	if !ok && hint != "" {
		fmt.Printf("        \033[36mHint:\033[0m %s\n", hint)
	}
	return ok, err
}

func runPreflight() int {
	fmt.Println("=== preflight ===")
	failed := 0

	fmt.Println("\n[1] env file + DATABASE_URL")
	dsn := os.Getenv("DATABASE_URL")
	if ok, _ := mustOKHint(dsn != "", "DATABASE_URL is set", "",
		"check that .env exists at the project root, or pass --env-file <path>"); !ok {
		failed++
	}
	if ok, _ := mustOKHint(strings.HasPrefix(dsn, "postgres"), "DATABASE_URL looks like postgres URL", "",
		"verify the DSN in .env starts with postgresql:// (not postgres:// or psql://)"); !ok {
		failed++
	}

	fmt.Println("\n[2] DB reachability")
	db, err := openDB()
	if ok, _ := mustOKHint(err == nil, "DB connect+ping", errString(err),
		"check DATABASE_URL, EC2 security group (5432 outbound), Neon DB pause state, or proxy/firewall rules"); !ok {
		failed++
		return 1
	}
	defer db.Close()

	fmt.Println("\n[3] migrations table is initialised")
	var t string
	qerr := db.QueryRow(`SELECT to_regclass('public.migrations')`).Scan(&t)
	if ok, _ := mustOKHint(qerr == nil && t != "", "public.migrations exists", "",
		"first-time DB; run 'go run cmd/migrate/main.go' once to apply 007+ and create the table"); !ok {
		failed++
	}

	fmt.Println("\n[4] current state")
	applied, _ := getApplied(db)
	has049 := false
	for _, v := range applied {
		if v == migrationVersion {
			has049 = true
		}
	}
	if has049 {
		fmt.Printf("  \033[33mINFO\033[0m migration %s is ALREADY applied — apply() will be a no-op\n", migrationVersion)
	} else {
		fmt.Printf("  \033[32mOK\033[0m   migration %s not yet applied\n", migrationVersion)
	}

	hasTable, _ := tableExists(db, "fund_reports")
	hasAlloc, _ := tableExists(db, "fund_asset_allocations")
	if hasTable || hasAlloc {
		fmt.Printf("  \033[33mINFO\033[0m fund_reports=%v  fund_asset_allocations=%v (will reuse existing)\n", hasTable, hasAlloc)
	}

	fmt.Println("\n[5] source migration file exists and matches")
	srcPath := filepath.Join("internal", "migration", "migrations", migrationVersion+"_create_fund_reports.go")
	_, statErr := os.Stat(srcPath)
	if ok, _ := mustOKHint(statErr == nil, "source migration file present", srcPath,
		"on the prod host run 'cd /home/ec2-user/monera && git pull' to get the latest migration source"); !ok {
		failed++
	} else {
		fmt.Printf("  \033[32mOK\033[0m   %s\n", srcPath)
	}

	fmt.Println()
	if failed == 0 {
		fmt.Println("PREFLIGHT: PASS (ready to apply)")
		return 0
	}
	fmt.Printf("PREFLIGHT: FAIL (%d issue(s))\n", failed)
	return 1
}

func runApply() int {
	fmt.Println("=== apply ===")
	db, err := openDB()
	if err != nil {
		fmt.Println("FATAL:", err)
		return 1
	}
	defer db.Close()

	m := migration.NewMigrator(db)
	if err := m.Init(); err != nil {
		fmt.Println("FATAL init:", err)
		return 1
	}
	// Register all migrations (must match cmd/migrate/main.go exactly)
	m.Register(&migrations.UpdateWalletRequestsTable{})
	m.Register(&migrations.AddIsPrimaryToWhitelist{})
	m.Register(&migrations.CreateDepositsTable{})
	m.Register(&migrations.AddFrozenUntilToWhitelist{})
	m.Register(&migrations.SafeheronPhase1{})
	m.Register(&migrations.AccountFrozenBalanceDefault{})
	m.Register(&migrations.CreateFundReports{})

	if err := m.Migrate(); err != nil {
		fmt.Println("FATAL migrate:", err)
		return 1
	}
	fmt.Println("APPLY: OK (migrator ran; 016 may be no-op if already applied)")
	return 0
}

func runVerify() int {
	fmt.Println("=== verify ===")
	failed := 0

	db, err := openDB()
	if ok, _ := mustOK(err == nil, "DB connect+ping", errString(err)); !ok {
		failed++
		return 1
	}
	defer db.Close()

	fmt.Println("\n[1] migrations row")
	row := db.QueryRow(`SELECT version, name, executed_at FROM migrations WHERE version = $1`, migrationVersion)
	var v, n string
	var t time.Time
	scanErr := row.Scan(&v, &n, &t)
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		if ok, _ := mustOK(false, "migration 016 registered", "not found in migrations table"); !ok {
			failed++
		}
		return 1
	case scanErr != nil:
		if ok, _ := mustOK(false, "migration 016 registered", scanErr.Error()); !ok {
			failed++
		}
		return 1
	default:
		if ok, _ := mustOK(v == migrationVersion, "migration 016 registered", fmt.Sprintf("executed_at=%s", t.Format(time.RFC3339))); !ok {
			failed++
		}
		_ = n // name read but not asserted
	}

	fmt.Println("\n[2] fund_reports schema")
	if err := checkColumns(db, "fund_reports", []string{
		"id", "report_date", "total_aum", "initial_aum", "month_start_aum",
		"new_capital", "month_growth", "actual_apy", "weighted_apy", "note",
		"created_at", "updated_at",
	}); err != nil {
		failed++
	}
	cols, _ := countColumns(db, "fund_reports")
	if ok, _ := mustOK(cols == expectedFundReportsCols,
		fmt.Sprintf("fund_reports column count = %d", expectedFundReportsCols),
		fmt.Sprintf("actual=%d", cols)); !ok {
		failed++
	}

	rows, _ := countRows(db, "fund_reports")
	if ok, _ := mustOK(rows == expectedFundReportsRows,
		fmt.Sprintf("fund_reports row count = %d", expectedFundReportsRows),
		fmt.Sprintf("actual=%d", rows)); !ok {
		failed++
	}

	fmt.Println("\n[3] fund_asset_allocations schema")
	if err := checkColumns(db, "fund_asset_allocations", []string{
		"id", "report_id", "category", "amount", "pct", "sort_order", "created_at",
	}); err != nil {
		failed++
	}
	acols, _ := countColumns(db, "fund_asset_allocations")
	if ok, _ := mustOK(acols == expectedAllocCols,
		fmt.Sprintf("fund_asset_allocations column count = %d", expectedAllocCols),
		fmt.Sprintf("actual=%d", acols)); !ok {
		failed++
	}

	arows, _ := countRows(db, "fund_asset_allocations")
	if ok, _ := mustOK(arows == expectedAllocRows,
		fmt.Sprintf("fund_asset_allocations row count = %d", expectedAllocRows),
		fmt.Sprintf("actual=%d", arows)); !ok {
		failed++
	}

	fmt.Println("\n[4] data integrity")
	var pctSum float64
	if err := db.QueryRow(`SELECT COALESCE(SUM(pct), 0) FROM fund_asset_allocations`).Scan(&pctSum); err != nil {
		if ok, _ := mustOK(false, "sum of allocation pct", err.Error()); !ok {
			failed++
		}
	} else {
		// NUMERIC(6,4) is exact; sum of 4 categories must be exactly 1.0.
		// If it isn't, the seed values were mis-rounded.
		if ok, _ := mustOK(pctSum == 1.0,
			fmt.Sprintf("sum of pct = %.4f (must be exactly 1.0)", pctSum),
			"re-apply the seed via the migration; seed values are wrong"); !ok {
			failed++
		}
	}

	var latestTotal float64
	var latestDate time.Time
	if err := db.QueryRow(`SELECT total_aum, report_date FROM fund_reports ORDER BY report_date DESC LIMIT 1`).Scan(&latestTotal, &latestDate); err != nil {
		if ok, _ := mustOK(false, "latest report row", err.Error()); !ok {
			failed++
		}
	} else {
		if ok, _ := mustOK(latestTotal > 0,
			fmt.Sprintf("latest report total_aum = %.2f (date %s)", latestTotal, latestDate.Format("2006-01-02")),
			""); !ok {
			failed++
		}
	}

	fmt.Println("\n[5] FK integrity (allocations ↔ reports)")
	var orphans int
	if err := db.QueryRow(`
SELECT count(*) FROM fund_asset_allocations a
LEFT JOIN fund_reports r ON a.report_id = r.id
WHERE r.id IS NULL
`).Scan(&orphans); err != nil {
		if ok, _ := mustOK(false, "fk check", err.Error()); !ok {
			failed++
		}
	} else {
		if ok, _ := mustOK(orphans == 0, "no orphan allocations", fmt.Sprintf("count=%d", orphans)); !ok {
			failed++
		}
	}

	fmt.Println()
	if failed == 0 {
		fmt.Println("VERIFY: PASS")
		return 0
	}
	fmt.Printf("VERIFY: FAIL (%d issue(s))\n", failed)
	return 1
}

func runRollback() int {
	fmt.Println("=== rollback ===")
	fmt.Println("  \033[33mWARN\033[0m this will DROP fund_reports and fund_asset_allocations")

	if os.Getenv("CONFIRM_ROLLBACK") != "yes" {
		fmt.Println("  set CONFIRM_ROLLBACK=yes to actually run")
		return 2
	}

	db, err := openDB()
	if err != nil {
		fmt.Println("FATAL:", err)
		return 1
	}
	defer db.Close()

	if _, err := db.Exec(`DROP TABLE IF EXISTS fund_asset_allocations;`); err != nil {
		fmt.Println("FATAL drop fund_asset_allocations:", err)
		return 1
	}
	fmt.Println("  dropped fund_asset_allocations")
	if _, err := db.Exec(`DROP TABLE IF EXISTS fund_reports;`); err != nil {
		fmt.Println("FATAL drop fund_reports:", err)
		return 1
	}
	fmt.Println("  dropped fund_reports")
	if _, err := db.Exec(`DELETE FROM migrations WHERE version = $1;`, migrationVersion); err != nil {
		fmt.Println("FATAL unregister migration:", err)
		return 1
	}
	fmt.Println("  unregistered migration 016 from migrations table")
	fmt.Println("ROLLBACK: OK")
	return 0
}

func runInfo() int {
	fmt.Println("=== info ===")
	db, err := openDB()
	if err != nil {
		fmt.Println("FATAL:", err)
		return 1
	}
	defer db.Close()

	applied, _ := getApplied(db)
	fmt.Println("Applied migrations:")
	for _, v := range applied {
		marker := "  "
		if v == migrationVersion {
			marker = "* "
		}
		fmt.Printf("  %s%s\n", marker, v)
	}

	for _, t := range []string{"fund_reports", "fund_asset_allocations"} {
		exists, _ := tableExists(db, t)
		cols, _ := countColumns(db, t)
		rows, _ := countRows(db, t)
		status := "missing"
		if exists {
			status = fmt.Sprintf("%d cols, %d rows", cols, rows)
		}
		fmt.Printf("  %-25s %s\n", t+":", status)
	}
	return 0
}

func getApplied(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT version FROM migrations ORDER BY executed_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, name).Scan(&n)
	return n > 0, err
}

func countColumns(db *sql.DB, name string) (int, error) {
	var n int
	err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=$1`, name).Scan(&n)
	return n, err
}

func countRows(db *sql.DB, name string) (int, error) {
	var n int
	err := db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", name)).Scan(&n)
	return n, err
}

func checkColumns(db *sql.DB, table string, expected []string) error {
	rows, err := db.Query(`SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position`, table)
	if err != nil {
		_, _ = mustOK(false, fmt.Sprintf("%s columns read", table), err.Error())
		return err
	}
	defer rows.Close()
	actual := []string{}
	for rows.Next() {
		var c string
		_ = rows.Scan(&c)
		actual = append(actual, c)
	}
	if len(actual) != len(expected) {
		_, _ = mustOK(false, fmt.Sprintf("%s columns", table), fmt.Sprintf("expected %d got %d: %v", len(expected), len(actual), actual))
		return fmt.Errorf("column count mismatch")
	}
	for i, e := range expected {
		if actual[i] != e {
			_, _ = mustOK(false, fmt.Sprintf("%s columns", table), fmt.Sprintf("col[%d]: expected %q got %q", i, e, actual[i]))
			return fmt.Errorf("column mismatch at %d", i)
		}
	}
	_, _ = mustOK(true, fmt.Sprintf("%s columns (%d)", table, len(actual)), "")
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
