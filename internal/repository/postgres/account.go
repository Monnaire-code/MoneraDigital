package postgres

import (
	"context"
	"database/sql"
	"time"

	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

type AccountRepositoryV1 struct {
	db *sql.DB
}

func NewAccountRepositoryV1(db *sql.DB) repository.Account {
	return &AccountRepositoryV1{db: db}
}

func (r *AccountRepositoryV1) GetByUserIDAndType(ctx context.Context, userID int, accountType string) (*models.Account, error) {
	var account models.Account
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, type, currency, balance, frozen_balance, version, created_at, updated_at
		 FROM account WHERE user_id = $1 AND type = $2`,
		userID, accountType).Scan(
		&account.ID, &account.UserID, &account.Type, &account.Currency,
		&account.Balance, &account.FrozenBalance, &account.Version,
		&account.CreatedAt, &account.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *AccountRepositoryV1) Create(ctx context.Context, account *models.Account) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO account (user_id, type, currency, balance, frozen_balance, version, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id`,
		account.UserID, account.Type, account.Currency, account.Balance, account.FrozenBalance,
		1, time.Now(), time.Now()).Scan(&account.ID)
	return err
}

func (r *AccountRepositoryV1) UpdateFrozenBalance(ctx context.Context, userID int, amount string) error {
	// Add to frozen (and check balance if needed, but logic usually in Service.
	// PRD says: UPDATE account SET frozen_balance = frozen_balance + amount ...
	result, err := r.db.ExecContext(ctx,
		`UPDATE account
		 SET frozen_balance = frozen_balance + CAST($1 AS NUMERIC(65,8)), version = version + 1, updated_at = $3
		 WHERE user_id = $2`,
		amount, userID, time.Now())
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return repository.ErrNotFound
	}
	return nil
}

func (r *AccountRepositoryV1) ReleaseFrozenBalance(ctx context.Context, userID int, amount string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE account
		 SET frozen_balance = frozen_balance - CAST($1 AS NUMERIC(65,8)), version = version + 1, updated_at = $3
		 WHERE user_id = $2 AND frozen_balance >= CAST($1 AS NUMERIC(65,8))`,
		amount, userID, time.Now())
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Could mean not found OR frozen balance insufficient (should not happen if logic correct)
		return repository.ErrNotFound
	}
	return nil
}

func (r *AccountRepositoryV1) DeductBalance(ctx context.Context, userID int, amount string) error {
	// Deduct from BOTH balance and frozen (since it was frozen first).
	// PRD 5.3: frozen_balance = frozen_balance - amount, balance = balance - amount
	result, err := r.db.ExecContext(ctx,
		`UPDATE account
		 SET frozen_balance = frozen_balance - CAST($1 AS NUMERIC(65,8)), balance = balance - CAST($1 AS NUMERIC(65,8)), version = version + 1, updated_at = $3
		 WHERE user_id = $2 AND frozen_balance >= CAST($1 AS NUMERIC(65,8))`,
		amount, userID, time.Now())
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return repository.ErrNotFound
	}
	return nil
}
