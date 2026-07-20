package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const valuationCandidateOtherAccountPostgresIntegrationGate = "RUN_VALUATION_CANDIDATE_OTHER_ACCOUNT_POSTGRES_INTEGRATION"

func TestValuationCandidateQueriesExcludeOtherAccountMovementsInPostgres(t *testing.T) {
	if os.Getenv(valuationCandidateOtherAccountPostgresIntegrationGate) != "1" {
		t.Skip("set RUN_VALUATION_CANDIDATE_OTHER_ACCOUNT_POSTGRES_INTEGRATION=1 to run isolated PostgreSQL coverage")
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		t.Fatal("DATABASE_URL is required when OTHER valuation PostgreSQL integration is enabled")
	}
	db, schema := newValuationCandidateOtherAccountPostgresFixture(t, databaseURL)
	qualify := func(query string) string {
		return strings.NewReplacer(
			"company_fund_transaction_valuation_history", schema+".company_fund_transaction_valuation_history",
			"company_fund_provider_transaction_facts", schema+".company_fund_provider_transaction_facts",
			"company_fund_transactions", schema+".company_fund_transactions",
			"company_fund_accounts", schema+".company_fund_accounts",
		).Replace(query)
	}

	for _, testCase := range []struct {
		name    string
		query   string
		args    []any
		wantIDs []int64
	}{
		{"from OTHER", selectCompanyFundTransactionValuationCandidateSQL, []any{int64(101)}, nil},
		{"to OTHER", selectCompanyFundTransactionValuationCandidateSQL, []any{int64(102)}, nil},
		{"Safeheron ordinary", selectCompanyFundTransactionValuationCandidateSQL, []any{int64(103)}, []int64{103}},
		{"Airwallex ordinary", selectCompanyFundTransactionValuationCandidateSQL, []any{int64(104)}, []int64{104}},
		{"repair sweep", selectCompanyFundValuationRepairCandidatesSQL, []any{int64(10)}, []int64{103, 104}},
		{"cursor repair sweep", selectCompanyFundValuationRepairCandidatesAfterSQL, []any{int64(0), int64(10)}, []int64{103, 104}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if ids := valuationCandidateIDs(t, db, qualify(testCase.query), testCase.args...); fmt.Sprint(ids) != fmt.Sprint(testCase.wantIDs) {
				t.Fatalf("candidate IDs = %v, want %v", ids, testCase.wantIDs)
			}
		})
	}
}

func valuationCandidateIDs(t *testing.T, db *sql.DB, query string, args ...any) []int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id, new(string), new(string), new(string), new(string), new(string), new(string), new(string), new(string), new(bool), new(sql.NullInt64), new(sql.NullInt64), new(sql.NullTime), new(sql.NullTime), new(time.Time), new(sql.NullInt64), new(sql.NullString), new(string), new(string), new(string), new(string), new(sql.NullInt64), new(string), new(string), new(string)); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func newValuationCandidateOtherAccountPostgresFixture(t *testing.T, databaseURL string) (*sql.DB, string) {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() { _ = db.Close() })
	schema := fmt.Sprintf("valuation_other_%d", time.Now().UnixNano())
	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})
	fixtureSQL := `
CREATE TABLE ` + schema + `.company_fund_accounts (id BIGINT PRIMARY KEY, channel VARCHAR(16) NOT NULL);
CREATE TABLE ` + schema + `.company_fund_provider_transaction_facts (
  id BIGINT PRIMARY KEY, provider_reported_usd_value NUMERIC, value_scope VARCHAR(32), allocation_state VARCHAR(32),
  conversion_from_currency VARCHAR(16), conversion_to_currency VARCHAR(16)
);
CREATE TABLE ` + schema + `.company_fund_transaction_valuation_history (
  id BIGINT PRIMARY KEY, transaction_id BIGINT NOT NULL, dependency_fingerprint VARCHAR(64),
  usd_valuation_status VARCHAR(32), usd_valuation_source VARCHAR(32)
);
CREATE TABLE ` + schema + `.company_fund_transactions (
  id BIGINT PRIMARY KEY, channel VARCHAR(16) NOT NULL, movement_kind VARCHAR(16) NOT NULL,
  transaction_direction VARCHAR(24) NOT NULL, currency VARCHAR(64) NOT NULL, amount NUMERIC NOT NULL,
  chain_code VARCHAR(64), provider_asset_key VARCHAR(256), asset_contract VARCHAR(256),
  is_unrecognized_asset BOOLEAN NOT NULL DEFAULT false, from_company_fund_account_id BIGINT,
  to_company_fund_account_id BIGINT, occurred_at TIMESTAMPTZ, completed_at TIMESTAMPTZ,
  first_seen_at TIMESTAMPTZ NOT NULL, provider_transaction_fact_id BIGINT,
  current_valuation_history_id BIGINT
);
INSERT INTO ` + schema + `.company_fund_accounts (id, channel) VALUES (1, 'SAFEHERON'), (2, 'AIRWALLEX'), (3, 'OTHER');
INSERT INTO ` + schema + `.company_fund_transactions
  (id, channel, movement_kind, transaction_direction, currency, amount, from_company_fund_account_id, to_company_fund_account_id, first_seen_at)
VALUES
  (101, 'MANUAL', 'PRINCIPAL', 'INFLOW', 'USD', 1, 3, NULL, now()),
  (102, 'MANUAL', 'PRINCIPAL', 'INFLOW', 'USD', 1, NULL, 3, now()),
  (103, 'SAFEHERON', 'PRINCIPAL', 'INFLOW', 'USD', 1, NULL, 1, now()),
  (104, 'AIRWALLEX', 'PRINCIPAL', 'INFLOW', 'USD', 1, NULL, 2, now());`
	if _, err := db.Exec(fixtureSQL); err != nil {
		t.Fatal(err)
	}
	return db, schema
}
