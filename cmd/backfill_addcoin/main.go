// Backfill AddCoin: sync every active address_pool account's Safeheron coin
// registration with the current coin_chains seed. Necessary because the pool
// replenisher only AddCoins at wallet-creation time — when coin_chains is
// later changed (e.g. testnet → mainnet seed) older addresses keep their
// stale registration and won't see deposits in the new coins.
//
// Usage:
//
//	go run ./cmd/backfill_addcoin --dry-run
//	go run ./cmd/backfill_addcoin --account-key=accountsjmky...
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"monera-digital/internal/db"
	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

const apiCallInterval = 1 * time.Second

func main() {
	dryRun := flag.Bool("dry-run", false, "print planned AddCoin calls without executing")
	accountKey := flag.String("account-key", "", "process a single accountKey only (default: all active)")
	flag.Parse()

	loadEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	database, err := db.InitDB(viper.GetString("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	defer database.Close()

	registry := walletconfig.NewRegistry(walletconfig.NewDBRepository(database), 0)
	if err := registry.Load(ctx); err != nil {
		log.Fatalf("registry load: %v", err)
	}

	client, err := safeheron.NewClient(safeheron.Config{
		BaseURL:               viper.GetString("SAFEHERON_API_BASE_URL"),
		APIKey:                viper.GetString("SAFEHERON_API_KEY"),
		PrivateKeyPath:        viper.GetString("SAFEHERON_PRIVATE_KEY_PATH"),
		PlatformPublicKeyPath: viper.GetString("SAFEHERON_PLATFORM_PUBLIC_KEY_PATH"),
		WebhookPublicKeyPath:  viper.GetString("SAFEHERON_WEBHOOK_PUBLIC_KEY_PATH"),
		WebhookPrivateKeyPath: viper.GetString("SAFEHERON_WEBHOOK_PRIVATE_KEY_PATH"),
		RequestTimeoutMS:      30000,
	})
	if err != nil {
		log.Fatalf("safeheron client init: %v", err)
	}
	defer client.Close()

	targets, err := loadTargets(ctx, database, *accountKey)
	if err != nil {
		log.Fatalf("load targets: %v", err)
	}
	if len(targets) == 0 {
		log.Println("no active accounts to process")
		return
	}
	log.Printf("loaded %d account(s) to inspect (1 req per %v)", len(targets), apiCallInterval)

	throttled := &throttledClient{inner: client}
	results := pool.BackfillAddCoin(ctx, throttled, targets, registry.SafeheronCoinKeysByFamily, *dryRun)

	printReport(results, *dryRun)
}

func loadTargets(ctx context.Context, database *sql.DB, singleAccount string) ([]pool.AccountTarget, error) {
	query := `SELECT safeheron_account_key, network_family, address
	          FROM address_pool
	          WHERE status IN ('AVAILABLE','ASSIGNED')`
	args := []any{}
	if singleAccount != "" {
		query += ` AND safeheron_account_key = $1`
		args = append(args, singleAccount)
	}
	query += ` ORDER BY id`

	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query address_pool: %w", err)
	}
	defer rows.Close()

	var targets []pool.AccountTarget
	for rows.Next() {
		var t pool.AccountTarget
		if err := rows.Scan(&t.AccountKey, &t.NetworkFamily, &t.Address); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

type throttledClient struct {
	inner *safeheron.Client
}

func (t *throttledClient) sleep() { time.Sleep(apiCallInterval) }

func (t *throttledClient) ListAccountCoin(ctx context.Context, accountKey string) ([]safeheron.AccountCoin, error) {
	t.sleep()
	return t.inner.ListAccountCoin(ctx, accountKey)
}

func (t *throttledClient) AddCoin(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
	t.sleep()
	return t.inner.AddCoin(ctx, accountKey, coinKeys)
}

func printReport(results []pool.BackfillResult, dryRun bool) {
	mode := "EXECUTED"
	if dryRun {
		mode = "DRY-RUN (no AddCoin called)"
	}
	fmt.Printf("\n=== Backfill Report (%s) ===\n", mode)

	var skipped, mutated, failed int
	for _, r := range results {
		switch {
		case r.Error != nil:
			failed++
			fmt.Printf("[FAIL] %s (%s) family=%s\n  error: %v\n  current: %s\n  planned add: %s\n",
				r.AccountKey, r.Address, r.Family, r.Error, joinOr(r.CurrentCoins, "<none>"), joinOr(r.AddedCoins, "<none>"))
		case r.Skipped:
			skipped++
		default:
			mutated++
			fmt.Printf("[%s] %s (%s) family=%s\n  current: %s\n  added: %s\n",
				ifelse(dryRun, "PLAN", "ADDED"),
				r.AccountKey, r.Address, r.Family,
				joinOr(r.CurrentCoins, "<none>"), strings.Join(r.AddedCoins, ", "))
		}
	}
	fmt.Printf("\n=== Summary ===\n  skipped (already in sync): %d\n  mutated/planned:           %d\n  failed:                    %d\n  total:                     %d\n",
		skipped, mutated, failed, len(results))
}

func joinOr(s []string, fallback string) string {
	if len(s) == 0 {
		return fallback
	}
	return strings.Join(s, ", ")
}

func ifelse(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func loadEnv() {
	viper.AutomaticEnv()
	if appEnv := viper.GetString("APP_ENV"); appEnv != "production" {
		_ = godotenv.Overload(".env")
	}
}
