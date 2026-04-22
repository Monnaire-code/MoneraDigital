// internal/repository/postgres/user.go
package postgres

import (
	"context"
	"database/sql"
	"time"

	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

// UserRepository PostgreSQL 用户仓储实现
type UserRepository struct {
	db *sql.DB
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db *sql.DB) repository.User {
	return &UserRepository{db: db}
}

// GetByEmail 根据邮箱获取用户
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*models.User, error) {
	var user models.User

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, email, password, status, two_factor_enabled, two_factor_secret,
		        two_factor_backup_codes, activation_code, activation_attempts,
		        activation_expires_at, activated_at, created_at, updated_at
		 FROM users WHERE email = $1`,
		email,
	).Scan(
		&user.ID,
		&user.Email,
		&user.Password,
		&user.Status,
		&user.TwoFactorEnabled,
		&user.TwoFactorSecret,
		&user.TwoFactorBackupCodes,
		&user.ActivationCode,
		&user.ActivationAttempts,
		&user.ActivationExpiresAt,
		&user.ActivatedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// GetByID 根据ID获取用户
func (r *UserRepository) GetByID(ctx context.Context, id int) (*models.User, error) {
	var user models.User

	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, email, password, status, two_factor_enabled, two_factor_secret,
		        two_factor_backup_codes, activation_code, activation_attempts,
		        activation_expires_at, activated_at, created_at, updated_at
		 FROM users WHERE id = $1`,
		id,
	).Scan(
		&user.ID,
		&user.Email,
		&user.Password,
		&user.Status,
		&user.TwoFactorEnabled,
		&user.TwoFactorSecret,
		&user.TwoFactorBackupCodes,
		&user.ActivationCode,
		&user.ActivationAttempts,
		&user.ActivationExpiresAt,
		&user.ActivatedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// Create 创建用户
func (r *UserRepository) Create(ctx context.Context, email, passwordHash string) (*models.User, error) {
	var user models.User

	err := r.db.QueryRowContext(
		ctx,
		`INSERT INTO users (email, password, status, created_at, updated_at)
		 VALUES ($1, $2, 'ACTIVE', $3, $4)
		 RETURNING id, email, password, status, two_factor_enabled, two_factor_secret,
		           two_factor_backup_codes, created_at, updated_at`,
		email,
		passwordHash,
		time.Now(),
		time.Now(),
	).Scan(
		&user.ID,
		&user.Email,
		&user.Password,
		&user.Status,
		&user.TwoFactorEnabled,
		&user.TwoFactorSecret,
		&user.TwoFactorBackupCodes,
		&user.CreatedAt,
		&user.UpdatedAt,
	)

	if err != nil {
		// 检查唯一性约束
		if err.Error() == "pq: duplicate key value violates unique constraint \"users_email_key\"" {
			return nil, repository.ErrAlreadyExists
		}
		return nil, err
	}

	return &user, nil
}

// Update 更新用户
func (r *UserRepository) Update(ctx context.Context, user *models.User) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE users
		 SET email = $1, password = $2, status = $3, two_factor_enabled = $4,
		     two_factor_secret = $5, two_factor_backup_codes = $6, updated_at = $7
		 WHERE id = $8`,
		user.Email,
		user.Password,
		user.Status,
		user.TwoFactorEnabled,
		user.TwoFactorSecret,
		user.TwoFactorBackupCodes,
		time.Now(),
		user.ID,
	)

	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return repository.ErrNotFound
	}

	return nil
}

// UpdateStatus 更新用户状态
func (r *UserRepository) UpdateStatus(ctx context.Context, userID int, status models.UserStatus) error {
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE users SET status = $1, updated_at = $2 WHERE id = $3`,
		status,
		time.Now(),
		userID,
	)

	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return repository.ErrNotFound
	}

	return nil
}

// IsDisabled 检查用户是否被禁用
func (r *UserRepository) IsDisabled(ctx context.Context, userID int) (bool, error) {
	var status string
	err := r.db.QueryRowContext(
		ctx,
		`SELECT status FROM users WHERE id = $1`,
		userID,
	).Scan(&status)

	if err == sql.ErrNoRows {
		return false, repository.ErrNotFound
	}
	if err != nil {
		return false, err
	}

	return status == string(models.UserStatusDisabled), nil
}

// Delete 删除用户
func (r *UserRepository) Delete(ctx context.Context, id int) error {
	result, err := r.db.ExecContext(
		ctx,
		`DELETE FROM users WHERE id = $1`,
		id,
	)

	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return repository.ErrNotFound
	}

	return nil
}
