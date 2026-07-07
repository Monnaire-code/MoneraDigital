package postgres

import (
	"context"
	"database/sql"
	"time"

	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

type DepositRepository struct {
	db *sql.DB
}

func NewDepositRepository(db *sql.DB) repository.Deposit {
	return &DepositRepository{db: db}
}

func (r *DepositRepository) Create(ctx context.Context, deposit *models.Deposit) error {
	// amount is $3::numeric to bridge the text parameter to the
	// NUMERIC(32, 8) column installed by migration 047. The implicit
	// text→numeric cast would also work, but the explicit cast makes
	// the type contract obvious at the call site and protects against
	// a future PG upgrade tightening implicit-cast rules.
	query := `
		INSERT INTO deposits (user_id, tx_hash, amount, asset, chain, status, from_address, to_address, created_at)
		VALUES ($1, $2, $3::numeric, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	err := r.db.QueryRowContext(ctx, query,
		deposit.UserID, deposit.TxHash, deposit.Amount, deposit.Asset, deposit.Chain,
		deposit.Status, deposit.FromAddress, deposit.ToAddress, time.Now(),
	).Scan(&deposit.ID)
	return err
}

func (r *DepositRepository) GetByTxHash(ctx context.Context, txHash string) (*models.Deposit, error) {
	query := `
		SELECT id, user_id, tx_hash, amount, asset, chain, status, from_address, to_address, created_at,
		       COALESCE(credited_at, confirmed_at) AS credited_at
		FROM deposits WHERE tx_hash = $1`

	var d models.Deposit

	err := r.db.QueryRowContext(ctx, query, txHash).Scan(
		&d.ID, &d.UserID, &d.TxHash, &d.Amount, &d.Asset, &d.Chain, &d.Status,
		&d.FromAddress, &d.ToAddress, &d.CreatedAt, &d.ConfirmedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *DepositRepository) GetByUserID(ctx context.Context, userID int, limit, offset int) ([]*models.Deposit, int64, error) {
	// Count
	var total int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM deposits WHERE user_id = $1", userID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, user_id, tx_hash, amount, asset, chain, status, from_address, to_address, created_at,
		       COALESCE(credited_at, confirmed_at) AS credited_at
		FROM deposits WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	deposits := make([]*models.Deposit, 0, limit)
	for rows.Next() {
		var d models.Deposit
		if err := rows.Scan(&d.ID, &d.UserID, &d.TxHash, &d.Amount, &d.Asset, &d.Chain, &d.Status, &d.FromAddress, &d.ToAddress, &d.CreatedAt, &d.ConfirmedAt); err != nil {
			return nil, 0, err
		}
		deposits = append(deposits, &d)
	}
	return deposits, total, nil
}

func (r *DepositRepository) UpdateStatus(ctx context.Context, id int, status string, confirmedAt string) error {
	var confirmedTime interface{} = nil
	if confirmedAt != "" {
		t, err := time.Parse(time.RFC3339, confirmedAt)
		if err == nil {
			confirmedTime = t
		} else {
			confirmedTime = time.Now() // Fallback
		}
	}
	query := `UPDATE deposits SET status = $1, confirmed_at = $2 WHERE id = $3`
	_, err := r.db.ExecContext(ctx, query, status, confirmedTime, id)
	return err
}
