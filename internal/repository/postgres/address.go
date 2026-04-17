package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

type AddressRepository struct {
	db *sql.DB
}

func NewAddressRepository(db *sql.DB) repository.Address {
	return &AddressRepository{db: db}
}

func (r *AddressRepository) CreateAddress(ctx context.Context, address *models.WithdrawalAddress) (*models.WithdrawalAddress, error) {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO withdrawal_address_whitelist (
			user_id, address_alias, chain_type, wallet_address, verified,
			verified_at, verification_method, frozen_until, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`,
		address.UserID, address.AddressAlias, address.ChainType, address.WalletAddress,
		address.Verified, address.VerifiedAt, address.VerificationMethod, address.FrozenUntil,
		time.Now(), time.Now(),
	).Scan(&address.ID)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return nil, repository.ErrAlreadyExists
		}
		// Fallback legacy check just in case
		if err.Error() == "pq: duplicate key value violates unique constraint \"withdrawal_address_whitelist_user_id_wallet_address_key\"" {
			return nil, repository.ErrAlreadyExists
		}
		return nil, err
	}
	return address, nil
}

func (r *AddressRepository) GetAddressesByUserID(ctx context.Context, userID int) ([]*models.WithdrawalAddress, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, address_alias, chain_type, wallet_address, verified,
			verified_at, verification_method, is_deleted, is_primary, frozen_until, created_at, updated_at
		FROM withdrawal_address_whitelist
		WHERE user_id = $1 AND is_deleted = FALSE`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	addresses := make([]*models.WithdrawalAddress, 0, 10)
	for rows.Next() {
		var addr models.WithdrawalAddress
		if err := rows.Scan(
			&addr.ID, &addr.UserID, &addr.AddressAlias, &addr.ChainType, &addr.WalletAddress,
			&addr.Verified, &addr.VerifiedAt, &addr.VerificationMethod, &addr.IsDeleted, &addr.IsPrimary,
			&addr.FrozenUntil, &addr.CreatedAt, &addr.UpdatedAt,
		); err != nil {
			return nil, err
		}
		addresses = append(addresses, &addr)
	}
	return addresses, nil
}

func (r *AddressRepository) GetAddressByID(ctx context.Context, id int) (*models.WithdrawalAddress, error) {
	var addr models.WithdrawalAddress
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, address_alias, chain_type, wallet_address, verified,
			verified_at, verification_method, is_deleted, is_primary, frozen_until, created_at, updated_at
		FROM withdrawal_address_whitelist WHERE id = $1 AND is_deleted = FALSE`,
		id).Scan(
		&addr.ID, &addr.UserID, &addr.AddressAlias, &addr.ChainType, &addr.WalletAddress,
		&addr.Verified, &addr.VerifiedAt, &addr.VerificationMethod, &addr.IsDeleted, &addr.IsPrimary,
		&addr.FrozenUntil, &addr.CreatedAt, &addr.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &addr, nil
}

func (r *AddressRepository) UpdateAddress(ctx context.Context, address *models.WithdrawalAddress) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist SET
			address_alias = $1, verified = $2, verified_at = $3,
			verification_method = $4, updated_at = $5
		WHERE id = $6`,
		address.AddressAlias, address.Verified, address.VerifiedAt,
		address.VerificationMethod, time.Now(), address.ID)
	return err
}

func (r *AddressRepository) DeleteAddress(ctx context.Context, id int) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist SET is_deleted = TRUE, updated_at = $1 WHERE id = $2`,
		time.Now(), id)
	return err
}

func (r *AddressRepository) GetByAddressAndChain(ctx context.Context, address, chain string) (*models.WithdrawalAddress, error) {
	var addr models.WithdrawalAddress
	err := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, address_alias, chain_type, wallet_address, verified,
			verified_at, verification_method, is_deleted, frozen_until, created_at, updated_at, is_primary
		FROM withdrawal_address_whitelist WHERE wallet_address = $1 AND chain_type = $2 AND is_deleted = FALSE LIMIT 1`,
		address, chain).Scan(
		&addr.ID, &addr.UserID, &addr.AddressAlias, &addr.ChainType, &addr.WalletAddress,
		&addr.Verified, &addr.VerifiedAt, &addr.VerificationMethod, &addr.IsDeleted,
		&addr.FrozenUntil, &addr.CreatedAt, &addr.UpdatedAt, &addr.IsPrimary,
	)
	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &addr, nil
}

func (r *AddressRepository) SetPrimary(ctx context.Context, userID int, addressID int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// First, verify the address belongs to the user
	var count int
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM withdrawal_address_whitelist 
		WHERE id = $1 AND user_id = $2 AND is_deleted = FALSE`,
		addressID, userID).Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		return repository.ErrNotFound
	}

	// Reset all primary addresses for this user to false
	_, err = tx.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist 
		SET is_primary = FALSE, updated_at = $1 
		WHERE user_id = $2 AND is_deleted = FALSE`,
		time.Now(), userID)
	if err != nil {
		return err
	}

	// Set the specified address as primary
	_, err = tx.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist 
		SET is_primary = TRUE, updated_at = $1 
		WHERE id = $2 AND user_id = $3`,
		time.Now(), addressID, userID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (r *AddressRepository) UnfreezeExpiredAddresses(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist 
		SET frozen_until = NULL, updated_at = $1 
		WHERE frozen_until IS NOT NULL AND frozen_until <= $1`,
		time.Now())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *AddressRepository) GetFrozenAddresses(ctx context.Context) ([]*models.WithdrawalAddress, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, address_alias, chain_type, wallet_address, verified,
			verified_at, verification_method, is_deleted, is_primary, frozen_until, created_at, updated_at
		FROM withdrawal_address_whitelist
		WHERE frozen_until IS NOT NULL AND frozen_until > NOW()`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	addresses := make([]*models.WithdrawalAddress, 0, 10)
	for rows.Next() {
		var addr models.WithdrawalAddress
		if err := rows.Scan(
			&addr.ID, &addr.UserID, &addr.AddressAlias, &addr.ChainType, &addr.WalletAddress,
			&addr.Verified, &addr.VerifiedAt, &addr.VerificationMethod, &addr.IsDeleted, &addr.IsPrimary,
			&addr.FrozenUntil, &addr.CreatedAt, &addr.UpdatedAt,
		); err != nil {
			return nil, err
		}
		addresses = append(addresses, &addr)
	}
	return addresses, nil
}

func (r *AddressRepository) UpdateFrozenUntil(ctx context.Context, id int, frozenUntil sql.NullTime) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE withdrawal_address_whitelist SET frozen_until = $1, updated_at = $2 WHERE id = $3`,
		frozenUntil, time.Now(), id)
	return err
}
