// internal/repository/postgres/fund_report.go
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"monera-digital/internal/models"
)

var ErrFundNotFound = errors.New("fund report not found")

// pgUndefinedTable is the SQLSTATE for "relation does not exist". A
// homepage /api/fund/stats hit on a database where migration 016 has
// not been applied surfaces this error. Without translation, the
// handler maps it to 500. Mapping it to ErrFundNotFound lets the
// existing service/handler chain return 404 "no fund report available
// yet", which is the right UX for a missing or empty table.
const pgUndefinedTable = "42P01"

func isUndefinedTable(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUndefinedTable
	}
	return false
}

type FundReportRepository struct {
	db *sql.DB
}

func NewFundReportRepository(db *sql.DB) *FundReportRepository {
	return &FundReportRepository{db: db}
}

func (r *FundReportRepository) GetLatest(ctx context.Context) (*models.FundReport, error) {
	const query = `
SELECT id, report_date, total_aum, initial_aum, month_start_aum, new_capital,
       month_growth, actual_apy, weighted_apy, note, created_at, updated_at
FROM fund_reports
ORDER BY report_date DESC
LIMIT 1
`
	var rep models.FundReport
	err := r.db.QueryRowContext(ctx, query).Scan(
		&rep.ID, &rep.ReportDate, &rep.TotalAum,
		&rep.InitialAum, &rep.MonthStartAum, &rep.NewCapital,
		&rep.MonthGrowth, &rep.ActualApy, &rep.WeightedApy,
		&rep.Note, &rep.CreatedAt, &rep.UpdatedAt,
	)
	if err == sql.ErrNoRows || isUndefinedTable(err) {
		return nil, ErrFundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get latest fund report: %w", err)
	}
	return &rep, nil
}

func (r *FundReportRepository) GetTrend(ctx context.Context, limit int) ([]models.FundReport, error) {
	if limit <= 0 {
		limit = 5
	}
	const query = `
SELECT id, report_date, total_aum, initial_aum, month_start_aum, new_capital,
       month_growth, actual_apy, weighted_apy, note, created_at, updated_at
FROM fund_reports
ORDER BY report_date DESC
LIMIT $1
`
	rows, err := r.db.QueryContext(ctx, query, limit)
	if isUndefinedTable(err) {
		return nil, ErrFundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get trend fund reports: %w", err)
	}
	defer rows.Close()

	reports := make([]models.FundReport, 0, limit)
	for rows.Next() {
		var rep models.FundReport
		if err := rows.Scan(
			&rep.ID, &rep.ReportDate, &rep.TotalAum,
			&rep.InitialAum, &rep.MonthStartAum, &rep.NewCapital,
			&rep.MonthGrowth, &rep.ActualApy, &rep.WeightedApy,
			&rep.Note, &rep.CreatedAt, &rep.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan fund report: %w", err)
		}
		reports = append(reports, rep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fund report rows: %w", err)
	}
	return reports, nil
}

func (r *FundReportRepository) GetAllocationsByReportID(ctx context.Context, reportID int64) ([]models.FundAssetAllocation, error) {
	const query = `
SELECT id, report_id, category, amount, pct, sort_order, created_at
FROM fund_asset_allocations
WHERE report_id = $1
ORDER BY sort_order ASC, id ASC
`
	rows, err := r.db.QueryContext(ctx, query, reportID)
	if isUndefinedTable(err) {
		return nil, ErrFundNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get allocations for report %d: %w", reportID, err)
	}
	defer rows.Close()

	allocs := make([]models.FundAssetAllocation, 0, 8)
	for rows.Next() {
		var a models.FundAssetAllocation
		if err := rows.Scan(&a.ID, &a.ReportID, &a.Category, &a.Amount, &a.Pct, &a.SortOrder, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan fund allocation: %w", err)
		}
		allocs = append(allocs, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate fund allocation rows: %w", err)
	}
	return allocs, nil
}
