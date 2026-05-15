package pool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Repository interface {
	GetUserAddress(ctx context.Context, userID int, networkFamily string) (*Address, error)
	AssignAvailable(ctx context.Context, userID int, networkFamily string) (*Address, error)
	CountByStatus(ctx context.Context, networkFamily, status string) (int, error)
	BulkInsert(ctx context.Context, addrs []*Address) error
}

type DBRepository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *DBRepository {
	return &DBRepository{db: db}
}

func (r *DBRepository) GetUserAddress(ctx context.Context, userID int, networkFamily string) (*Address, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, network_family, address, safeheron_account_key, customer_ref_id,
		        address_group_key, derive_path, account_tag, hidden_on_ui, auto_fuel,
		        status, assigned_user_id, assigned_at, created_at, updated_at
		 FROM address_pool
		 WHERE assigned_user_id = $1 AND network_family = $2
		 LIMIT 1`,
		userID, networkFamily,
	)
	return scanAddress(row)
}

func (r *DBRepository) AssignAvailable(ctx context.Context, userID int, networkFamily string) (*Address, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	existing, checkErr := scanAddress(tx.QueryRowContext(ctx,
		`SELECT id, network_family, address, safeheron_account_key, customer_ref_id,
		        address_group_key, derive_path, account_tag, hidden_on_ui, auto_fuel,
		        status, assigned_user_id, assigned_at, created_at, updated_at
		 FROM address_pool
		 WHERE assigned_user_id = $1 AND network_family = $2
		 LIMIT 1
		 FOR UPDATE`,
		userID, networkFamily,
	))
	if checkErr == nil {
		_ = tx.Commit()
		return existing, nil
	}
	if !errors.Is(checkErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("check existing: %w", checkErr)
	}

	now := time.Now()
	row := tx.QueryRowContext(ctx,
		`UPDATE address_pool
		 SET status = $1, assigned_user_id = $2, assigned_at = $3, updated_at = $3
		 WHERE id = (
		     SELECT id FROM address_pool
		     WHERE network_family = $4 AND status = $5
		     ORDER BY id
		     FOR UPDATE SKIP LOCKED
		     LIMIT 1
		 )
		 RETURNING id, network_family, address, safeheron_account_key, customer_ref_id,
		           address_group_key, derive_path, account_tag, hidden_on_ui, auto_fuel,
		           status, assigned_user_id, assigned_at, created_at, updated_at`,
		StatusAssigned, userID, now, networkFamily, StatusAvailable,
	)

	addr, err := scanAddress(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPoolEmpty
		}
		return nil, fmt.Errorf("assign available: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit assign: %w", err)
	}
	return addr, nil
}

func (r *DBRepository) CountByStatus(ctx context.Context, networkFamily, status string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM address_pool WHERE network_family = $1 AND status = $2`,
		networkFamily, status,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count by status: %w", err)
	}
	return count, nil
}

const maxBulkInsertBatch = 500

func (r *DBRepository) BulkInsert(ctx context.Context, addrs []*Address) error {
	if len(addrs) == 0 {
		return nil
	}
	if len(addrs) > maxBulkInsertBatch {
		return fmt.Errorf("bulk insert: batch size %d exceeds max %d", len(addrs), maxBulkInsertBatch)
	}

	var b strings.Builder
	b.WriteString(`INSERT INTO address_pool
		(network_family, address, safeheron_account_key, customer_ref_id,
		 address_group_key, derive_path, account_tag, hidden_on_ui, auto_fuel, status)
		VALUES `)

	args := make([]any, 0, len(addrs)*10)
	for i, a := range addrs {
		if i > 0 {
			b.WriteString(", ")
		}
		base := i * 10
		fmt.Fprintf(&b, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5,
			base+6, base+7, base+8, base+9, base+10)
		args = append(args,
			a.NetworkFamily, a.Address, a.SafeheronAccountKey, a.CustomerRefID,
			a.AddressGroupKey, a.DerivePath, a.AccountTag, a.HiddenOnUI, a.AutoFuel, a.Status)
	}
	b.WriteString(` ON CONFLICT (customer_ref_id) DO NOTHING`)

	_, err := r.db.ExecContext(ctx, b.String(), args...)
	if err != nil {
		return fmt.Errorf("bulk insert: %w", err)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAddress(row scanner) (*Address, error) {
	var a Address
	var assignedUserID sql.NullInt64
	var assignedAt sql.NullTime
	var addressGroupKey, derivePath, accountTag sql.NullString

	err := row.Scan(
		&a.ID, &a.NetworkFamily, &a.Address, &a.SafeheronAccountKey, &a.CustomerRefID,
		&addressGroupKey, &derivePath, &accountTag, &a.HiddenOnUI, &a.AutoFuel,
		&a.Status, &assignedUserID, &assignedAt, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if assignedUserID.Valid {
		v := int(assignedUserID.Int64)
		a.AssignedUserID = &v
	}
	if assignedAt.Valid {
		a.AssignedAt = &assignedAt.Time
	}
	if addressGroupKey.Valid {
		a.AddressGroupKey = addressGroupKey.String
	}
	if derivePath.Valid {
		a.DerivePath = derivePath.String
	}
	if accountTag.Valid {
		a.AccountTag = accountTag.String
	}

	return &a, nil
}
