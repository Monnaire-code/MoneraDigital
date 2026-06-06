// internal/migration/migrations/049_create_fund_reports.go
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"monera-digital/internal/migration"
)

// CreateFundReports creates the fund_reports and fund_asset_allocations tables
// for the public homepage AUM widget, seeded with 5 monthly reports
// (Jan–May 2026) and the May 2026 asset allocation snapshot.
type CreateFundReports struct{}

func (m *CreateFundReports) Version() string {
	return "049"
}

func (m *CreateFundReports) Description() string {
	return "Create fund_reports and fund_asset_allocations tables for the public homepage AUM widget, seeded with 2026 Jan–May monthly data (formerly migration 016)"
}

func (m *CreateFundReports) Up(db *sql.DB) error {
	steps := []struct {
		name string
		fn   func(sqlExecutor) error
	}{
		{"CreateFundReportsTable", createFundReportsTable},
		{"CreateFundAssetAllocationsTable", createFundAssetAllocationsTable},
		{"SeedFundReports2026", seedFundReports2026},
		{"SeedFundAssetAllocations2026_05", seedFundAssetAllocations2026_05},
	}

	// Wrap all 4 steps in a single transaction so a failure mid-migration
	// leaves the DB in the pre-migration state. PostgreSQL DDL is
	// transactional; CREATE TABLE / CREATE INDEX / INSERT all roll back
	// cleanly on ROLLBACK.
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, s := range steps {
		if err := s.fn(tx); err != nil {
			return fmt.Errorf("step %s failed: %w", s.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}

func (m *CreateFundReports) Down(db *sql.DB) error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS fund_asset_allocations;`); err != nil {
		return fmt.Errorf("failed to drop fund_asset_allocations: %w", err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS fund_reports;`); err != nil {
		return fmt.Errorf("failed to drop fund_reports: %w", err)
	}
	return nil
}

var _ migration.Migration = (*CreateFundReports)(nil)

// sqlExecutor is the subset of *sql.DB / *sql.Tx that step functions need.
// Both *sql.DB and *sql.Tx implement it, so the same step function runs
// inside a transaction (Up) or directly (Down / ad-hoc smoke).
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func createFundReportsTable(db sqlExecutor) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS fund_reports (
	id              BIGSERIAL PRIMARY KEY,
	report_date     DATE NOT NULL UNIQUE,
	total_aum       NUMERIC(20, 2) NOT NULL,
	initial_aum     NUMERIC(20, 2),
	month_start_aum NUMERIC(20, 2),
	new_capital     NUMERIC(20, 2),
	month_growth    NUMERIC(8, 6),
	actual_apy      NUMERIC(8, 4),
	weighted_apy    NUMERIC(8, 4),
	note            TEXT,
	created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_fund_reports_date
	ON fund_reports (report_date DESC);

COMMENT ON TABLE  fund_reports IS 'Monthly AUM snapshots for the public homepage fund widget';
COMMENT ON COLUMN fund_reports.report_date     IS 'First day of the reporting month (e.g. 2026-05-01 for May 2026)';
COMMENT ON COLUMN fund_reports.total_aum       IS 'Total assets under management at month end, in USD';
COMMENT ON COLUMN fund_reports.initial_aum     IS 'Initial fund size (Jan 2026 baseline)';
COMMENT ON COLUMN fund_reports.month_start_aum IS 'AUM at the start of the reporting month (for month-over-month delta)';
COMMENT ON COLUMN fund_reports.new_capital     IS 'Net new capital added during the month, in USD';
COMMENT ON COLUMN fund_reports.month_growth    IS 'Month-over-month growth rate as decimal (e.g. 0.0461 = 4.61%)';
COMMENT ON COLUMN fund_reports.actual_apy      IS 'Realized annualized yield (decimal) for the month';
COMMENT ON COLUMN fund_reports.weighted_apy    IS 'Weighted APY across all strategies for the month';
COMMENT ON COLUMN fund_reports.note            IS 'Optional free-form monthly narrative / market review';
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		return fmt.Errorf("create fund_reports: %w", err)
	}
	return nil
}

func createFundAssetAllocationsTable(db sqlExecutor) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS fund_asset_allocations (
	id          BIGSERIAL PRIMARY KEY,
	report_id   BIGINT NOT NULL REFERENCES fund_reports(id) ON DELETE CASCADE,
	category    VARCHAR(64) NOT NULL,
	amount      NUMERIC(20, 2) NOT NULL,
	pct         NUMERIC(6, 4) NOT NULL,
	sort_order  INT NOT NULL DEFAULT 0,
	created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

	UNIQUE (report_id, category)
);

CREATE INDEX IF NOT EXISTS idx_fund_allocations_report
	ON fund_asset_allocations (report_id, sort_order);

COMMENT ON TABLE  fund_asset_allocations IS 'Asset category breakdown for each fund_reports row, in USD amounts and percentages';
COMMENT ON COLUMN fund_asset_allocations.category   IS 'Strategy / asset category label, e.g. "DeFi Yield Farming"';
COMMENT ON COLUMN fund_asset_allocations.amount     IS 'USD value allocated to this category at month end';
COMMENT ON COLUMN fund_asset_allocations.pct        IS 'Share of total_aum as decimal (0–1), pre-computed to avoid div on every homepage render';
COMMENT ON COLUMN fund_asset_allocations.sort_order IS 'Display order in the UI (ascending)';
`
	if _, err := db.ExecContext(context.Background(), ddl); err != nil {
		return fmt.Errorf("create fund_asset_allocations: %w", err)
	}
	return nil
}

func seedFundReports2026(db sqlExecutor) error {
	// Only May 2026 has the full metric set + narrative note; earlier months
	// carry just the AUM series so the homepage trend chart has 5 data points.
	const mayNote = "2026年5月加密市场月度回顾：本月宏观因素持续主导市场，地缘政治紧张、通胀压力及美联储政策预期共同施压风险资产。加密市场消化能力明显下降，整体仍震荡下行。BTC月初78,000-81,000美元波动，后回落至73,000-75,000美元附近，小幅下跌；ETH表现更弱，ETH/BTC比率触及低点0.027，跌幅更大。BTC主导地位维持高位，altcoins流动性偏弱，总市值回调。BTC和ETH ETF：5月以流出为主，尤其是中月下旬出现多日连续流出。当前价格区间已显现一定底部特征。若宏观环境不再恶化，二级市场机会将多于一级。短期需关注地缘缓和与ETF资金回流，中期仍看好机构配置驱动下的复苏。"

	const stmt = `
INSERT INTO fund_reports
	(report_date, total_aum, initial_aum, month_start_aum, new_capital, month_growth, actual_apy, weighted_apy, note)
VALUES
	('2026-01-01', 1000000.00,     1000000.00,    NULL,         NULL, NULL, NULL, NULL, NULL),
	('2026-02-01', 3335009.43,     NULL,          1000000.00,   NULL, NULL, NULL, NULL, NULL),
	('2026-03-01', 6780508.82,     NULL,          3335009.43,   NULL, NULL, NULL, NULL, NULL),
	('2026-04-01', 12130239.76,    NULL,          6780508.82,   NULL, NULL, NULL, NULL, NULL),
	('2026-05-01', 14820125.94,    NULL,          12130239.76,  2130800.00, 0.0460902827200177, 0.1623, 0.5871, $1)
ON CONFLICT (report_date) DO NOTHING;
`
	if _, err := db.ExecContext(context.Background(), stmt, mayNote); err != nil {
		return fmt.Errorf("seed fund_reports: %w", err)
	}
	return nil
}

func seedFundAssetAllocations2026_05(db sqlExecutor) error {
	// pct values are pre-rounded to 4 decimals from amount / 14820125.94,
	// chosen so the stored NUMERIC(6,4) values sum to exactly 1.0000.
	// (The runtime service layer always recomputes pct as a sanity check;
	// the DB value is a cache.)
	const stmt = `
INSERT INTO fund_asset_allocations (report_id, category, amount, pct, sort_order)
SELECT
	fr.id,
	v.category,
	v.amount,
	v.pct,
	v.sort_order
FROM fund_reports fr
CROSS JOIN (VALUES
	('DeFi Yield Farming',                   3857328.43, 0.2603, 1),
	('Proactive Trading',                    9879372.87, 0.6666, 2),
	('Venture Investing',                    1000000.00, 0.0675, 3),
	('Token, NFT, Points and other Assets',  83424.64,   0.0056, 4)
) AS v(category, amount, pct, sort_order)
WHERE fr.report_date = '2026-05-01'
ON CONFLICT (report_id, category) DO NOTHING;
`
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		return fmt.Errorf("seed fund_asset_allocations: %w", err)
	}
	return nil
}
