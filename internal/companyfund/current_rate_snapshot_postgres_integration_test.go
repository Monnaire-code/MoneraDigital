package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const currentRateSnapshotPostgresGate = "RUN_COMPANY_FUND_CURRENT_RATE_INTEGRATION"

// TestCurrentRateDefaultsPersistAndPublishOnPostgres is opt-in and uses a
// unique temporary schema in the caller's existing database. It reproduces
// the complete default refresh path without requiring a separate test DB.
func TestCurrentRateDefaultsPersistAndPublishOnPostgres(t *testing.T) {
	if os.Getenv(currentRateSnapshotPostgresGate) != "1" {
		t.Skip("set RUN_COMPANY_FUND_CURRENT_RATE_INTEGRATION=1 to run PostgreSQL current-rate coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when current-rate integration coverage is enabled")
	}

	db := newCurrentRateSnapshotPostgresFixture(t, databaseURL)
	now := time.Date(2026, time.July, 16, 6, 45, 0, 987654321, time.UTC)
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeCoinGeckoCurrentPriceClient{simple: map[string]CoinGeckoPriceBatch{
		"binancecoin,bitcoin,ethereum,tether,usd-coin": fakeCoinGeckoPriceBatch(now,
			CoinGeckoPrice{CoinID: "binancecoin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("581.3927250062417", now)},
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("64671.59609988316", now)},
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "cny", Quote: fakeCoinGeckoQuote("437742.63252127904", now)},
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "hkd", Quote: fakeCoinGeckoQuote("507012.3626951137", now)},
			CoinGeckoPrice{CoinID: "bitcoin", QuoteCurrency: "sgd", Quote: fakeCoinGeckoQuote("83321.5040124543", now)},
			CoinGeckoPrice{CoinID: "ethereum", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("1915.6661142024368", now)},
			CoinGeckoPrice{CoinID: "tether", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("0.9992174435909386", now)},
			CoinGeckoPrice{CoinID: "usd-coin", QuoteCurrency: "usd", Quote: fakeCoinGeckoQuote("0.9998770777211476", now)},
		),
	}}
	cache := newTestCurrentRateCache(t, &now, 10*time.Minute)
	refresher, err := NewCoinGeckoCurrentRateRefresher(
		client,
		newCurrentRateRefresherRegistry(t, nil),
		cache,
		CoinGeckoCurrentRateRefresherConfig{
			Clock: func() time.Time { return now }, SnapshotStore: NewDBRepository(db),
			PolicyVersion: "current-usd-v1", DefaultMappings: mappings,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := refresher.Refresh(context.Background())
	if err != nil || !result.Refreshed || result.QuoteCount != 8 {
		t.Fatalf("default PostgreSQL refresh = %#v, %v", result, err)
	}
	var snapshotCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM company_fund_rate_snapshots`).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 12 {
		t.Fatalf("persisted snapshot count = %d, want 12 (5 crypto + 3 derived fiat + 4 deduplicated BTC legs)", snapshotCount)
	}
}

func newCurrentRateSnapshotPostgresFixture(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	adminDB := stdlib.OpenDB(*adminConfig)
	t.Cleanup(func() { _ = adminDB.Close() })

	schema := fmt.Sprintf("current_rate_%d", time.Now().UnixNano())
	if _, err := adminDB.ExecContext(context.Background(), `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`); err != nil {
			t.Errorf("drop isolated schema %s: %v", schema, err)
		}
	})
	if _, err := adminDB.ExecContext(context.Background(), `CREATE TABLE `+schema+`.company_fund_rate_requests (LIKE public.company_fund_rate_requests INCLUDING ALL)`); err != nil {
		t.Fatalf("create current-rate request fixture: %v", err)
	}
	if _, err := adminDB.ExecContext(context.Background(), `CREATE TABLE `+schema+`.company_fund_rate_snapshots (LIKE public.company_fund_rate_snapshots INCLUDING ALL)`); err != nil {
		t.Fatalf("create current-rate snapshot fixture: %v", err)
	}

	fixtureConfig := adminConfig.Copy()
	if fixtureConfig.RuntimeParams == nil {
		fixtureConfig.RuntimeParams = make(map[string]string)
	}
	fixtureConfig.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*fixtureConfig)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
