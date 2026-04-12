package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"monera-digital/internal/repository"
)

type DailyInterestRepository struct {
	db *sql.DB
}

func NewDailyInterestRepository(db *sql.DB) *DailyInterestRepository {
	return &DailyInterestRepository{db: db}
}

func (r *DailyInterestRepository) Create(ctx context.Context, record *repository.DailyInterestModel) error {
	return r.CreateWithDate(ctx, record, nil)
}

func (r *DailyInterestRepository) CreateWithDate(ctx context.Context, record *repository.DailyInterestModel, dateOverride *time.Time) error {
	var query string
	var args []interface{}

	if dateOverride != nil {
		query = `
			INSERT INTO daily_interest (user_id, order_id, currency, amount, effective, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			RETURNING id, created_at, updated_at
		`
		args = []interface{}{record.UserID, record.OrderID, record.Currency, record.Amount, record.Effective, dateOverride}
	} else {
		query = `
			INSERT INTO daily_interest (user_id, order_id, currency, amount, effective, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			RETURNING id, created_at, updated_at
		`
		args = []interface{}{record.UserID, record.OrderID, record.Currency, record.Amount, record.Effective}
	}

	err := r.db.QueryRowContext(ctx, query, args...).Scan(&record.ID, &record.CreatedAt, &record.UpdatedAt)
	return err
}

func (r *DailyInterestRepository) GetByUserID(ctx context.Context, userID int64, days int) ([]repository.DailyInterestModel, error) {
	if days <= 0 {
		days = 7
	}
	if days > 365 {
		days = 365
	}

	query := `
		SELECT id, user_id, order_id, currency, amount, effective, created_at, updated_at
		FROM daily_interest
		WHERE user_id = $1 AND effective = true
		AND created_at >= NOW() - INTERVAL '%d days'
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(query, days), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []repository.DailyInterestModel
	for rows.Next() {
		var rec repository.DailyInterestModel
		var createdAt, updatedAt time.Time

		err := rows.Scan(&rec.ID, &rec.UserID, &rec.OrderID, &rec.Currency, &rec.Amount, &rec.Effective, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		rec.CreatedAt = createdAt.Format(time.RFC3339)
		rec.UpdatedAt = updatedAt.Format(time.RFC3339)
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (r *DailyInterestRepository) InvalidateByOrderID(ctx context.Context, orderID int64) error {
	query := `
		UPDATE daily_interest
		SET effective = false, updated_at = CURRENT_TIMESTAMP
		WHERE order_id = $1 AND effective = true
	`
	_, err := r.db.ExecContext(ctx, query, orderID)
	return err
}

func (r *DailyInterestRepository) SumEffectiveByOrderID(ctx context.Context, orderID int64) (string, error) {
	query := `
		SELECT COALESCE(SUM(amount), 0)
		FROM daily_interest
		WHERE order_id = $1 AND effective = true
	`
	var sum sql.NullString
	err := r.db.QueryRowContext(ctx, query, orderID).Scan(&sum)
	if err != nil {
		return "0", err
	}
	if !sum.Valid || sum.String == "" {
		return "0", nil
	}
	return sum.String, nil
}
