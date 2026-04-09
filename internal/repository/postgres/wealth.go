package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"monera-digital/internal/repository"
	"monera-digital/internal/utils"
	"time"
)

type WealthRepository struct {
	db *sql.DB
}

func NewWealthRepository(db *sql.DB) *WealthRepository {
	return &WealthRepository{db: db}
}

func (r *WealthRepository) GetActiveProducts(ctx context.Context) ([]*repository.WealthProductModel, error) {
	query := `
		SELECT id, title, currency, apy, duration, min_amount, max_amount,
		       total_quota, sold_quota, status, auto_renew_allowed, created_at, updated_at
		FROM wealth_product
		WHERE status = 1
		ORDER BY created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var products []*repository.WealthProductModel
	for rows.Next() {
		var p repository.WealthProductModel
		err := rows.Scan(
			&p.ID, &p.Title, &p.Currency, &p.APY, &p.Duration,
			&p.MinAmount, &p.MaxAmount, &p.TotalQuota, &p.SoldQuota,
			&p.Status, &p.AutoRenewAllowed, &p.CreatedAt, &p.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		products = append(products, &p)
	}
	return products, rows.Err()
}

func (r *WealthRepository) GetProductByID(ctx context.Context, id int64) (*repository.WealthProductModel, error) {
	query := `
		SELECT id, title, currency, apy, duration, min_amount, max_amount,
		       total_quota, sold_quota, status, auto_renew_allowed, created_at, updated_at
		FROM wealth_product
		WHERE id = $1
	`
	var p repository.WealthProductModel
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&p.ID, &p.Title, &p.Currency, &p.APY, &p.Duration,
		&p.MinAmount, &p.MaxAmount, &p.TotalQuota, &p.SoldQuota,
		&p.Status, &p.AutoRenewAllowed, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *WealthRepository) CreateOrder(ctx context.Context, order *repository.WealthOrderModel) error {
	query := `
		INSERT INTO wealth_order (user_id, product_id, product_title, currency, amount,
			principal_redeemed, interest_expected, interest_paid, interest_accrued,
			start_date, end_date, auto_renew, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`
	err := r.db.QueryRowContext(ctx, query,
		order.UserID, order.ProductID, order.ProductTitle, order.Currency, order.Amount,
		order.PrincipalRedeemed, order.InterestExpected, order.InterestPaid, order.InterestAccrued,
		order.StartDate, order.EndDate, order.AutoRenew, order.Status,
	).Scan(&order.ID)
	if err != nil {
		fmt.Printf("[DEBUG] CreateOrder - error: %v\n", err)
	}
	return err
}

func (r *WealthRepository) GetOrdersByUserID(ctx context.Context, userID int64) ([]*repository.WealthOrderModel, error) {
	query := `
		SELECT o.id, o.user_id, o.product_id, p.title as product_title, p.currency,
			o.amount, p.duration,
			o.interest_expected, o.interest_paid, o.interest_accrued, o.start_date, o.end_date,
			o.auto_renew, o.status, o.renewed_from_order_id, o.renewed_to_order_id,
			o.redemption_amount, o.redemption_type, o.redeemed_at, o.created_at, o.updated_at
		FROM wealth_order o
		JOIN wealth_product p ON o.product_id = p.id
		WHERE o.user_id = $1
		ORDER BY o.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*repository.WealthOrderModel
	for rows.Next() {
		var o repository.WealthOrderModel
		var redemptionAmount, redeemedAt sql.NullString
		var redemptionType sql.NullString
		err := rows.Scan(
			&o.ID, &o.UserID, &o.ProductID, &o.ProductTitle, &o.Currency,
			&o.Amount, &o.Duration,
			&o.InterestExpected, &o.InterestPaid, &o.InterestAccrued,
			&o.StartDate, &o.EndDate, &o.AutoRenew, &o.Status,
			&o.RenewedFromOrderID, &o.RenewedToOrderID,
			&redemptionAmount, &redemptionType, &redeemedAt,
			&o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		o.RedemptionAmount = redemptionAmount.String
		o.RedeemedAt = redeemedAt.String
		o.RedeemedAt = redeemedAt.String
		orders = append(orders, &o)
	}
	return orders, rows.Err()
}

func (r *WealthRepository) GetOrderByID(ctx context.Context, id int64) (*repository.WealthOrderModel, error) {
	query := `
		SELECT o.id, o.user_id, o.product_id, p.title as product_title, p.currency, o.amount,
			o.interest_expected, o.interest_paid, o.interest_accrued, o.start_date, o.end_date,
			o.auto_renew, o.status, o.renewed_from_order_id, o.renewed_to_order_id,
			o.redemption_amount, o.redemption_type, o.redeemed_at, o.created_at, o.updated_at
		FROM wealth_order o
		JOIN wealth_product p ON o.product_id = p.id
		WHERE o.id = $1
	`
	var o repository.WealthOrderModel
	var redemptionAmount, redeemedAt sql.NullString
	var redemptionType sql.NullString
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&o.ID, &o.UserID, &o.ProductID, &o.ProductTitle, &o.Currency, &o.Amount,
		&o.InterestExpected, &o.InterestPaid, &o.InterestAccrued,
		&o.StartDate, &o.EndDate, &o.AutoRenew, &o.Status,
		&o.RenewedFromOrderID, &o.RenewedToOrderID,
		&redemptionAmount, &redemptionType, &redeemedAt,
		&o.CreatedAt, &o.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, repository.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.RedemptionAmount = redemptionAmount.String
	o.RedeemedAt = redeemedAt.String
	return &o, nil
}

func (r *WealthRepository) UpdateOrder(ctx context.Context, order *repository.WealthOrderModel) error {
	query := `
		UPDATE wealth_order SET
			interest_paid = $1, interest_accrued = $2, status = $3,
			redemption_amount = $4, redemption_type = $5, redeemed_at = $6,
			updated_at = $7
		WHERE id = $8
	`
	_, err := r.db.ExecContext(ctx, query,
		order.InterestPaid, order.InterestAccrued, order.Status,
		order.RedemptionAmount, order.RedemptionType, order.RedeemedAt,
		order.UpdatedAt, order.ID,
	)
	return err
}

func (r *WealthRepository) UpdateProductSoldQuota(ctx context.Context, id int64, amount string) error {
	query := `
		UPDATE wealth_product SET
			sold_quota = CAST(sold_quota AS NUMERIC) + CAST($1 AS NUMERIC),
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, amount, id)
	return err
}

type AccountRepository struct {
	db *sql.DB
}

func NewAccountRepository(db *sql.DB) repository.AccountV2 {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) GetAccountByUserIDAndCurrency(ctx context.Context, userID int64, currency string) (*repository.AccountModel, error) {
	query := `
		SELECT id, user_id, type, currency, balance, frozen_balance, version, created_at, updated_at
		FROM account
		WHERE user_id = $1 AND currency = $2
	`
	var a repository.AccountModel
	err := r.db.QueryRowContext(ctx, query, userID, currency).Scan(
		&a.ID, &a.UserID, &a.Type, &a.Currency,
		&a.Balance, &a.FrozenBalance, &a.Version,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("account not found for user %d and currency %s", userID, currency)
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AccountRepository) GetAccountsByUserID(ctx context.Context, userID int64) ([]*repository.AccountModel, error) {
	query := `
		SELECT id, user_id, type, currency, balance, frozen_balance, version, created_at, updated_at
		FROM account
		WHERE user_id = $1 AND type = 'FUND'
		ORDER BY currency
	`
	rows, err := r.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*repository.AccountModel
	for rows.Next() {
		var a repository.AccountModel
		err := rows.Scan(
			&a.ID, &a.UserID, &a.Type, &a.Currency,
			&a.Balance, &a.FrozenBalance, &a.Version,
			&a.CreatedAt, &a.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, &a)
	}
	return accounts, rows.Err()
}

func (r *AccountRepository) FreezeBalance(ctx context.Context, accountID int64, amount string) error {
	query := `
		UPDATE account SET
			frozen_balance = frozen_balance + CAST($1 AS NUMERIC),
			version = version + 1,
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, amount, accountID)
	return err
}

func (r *AccountRepository) UnfreezeBalance(ctx context.Context, accountID int64, amount string) error {
	query := `
		UPDATE account SET
			frozen_balance = frozen_balance - CAST($1 AS NUMERIC),
			version = version + 1,
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, amount, accountID)
	return err
}

func (r *AccountRepository) DeductBalance(ctx context.Context, accountID int64, amount string) error {
	query := `
		UPDATE account SET
			balance = CAST(balance AS NUMERIC) - CAST($1 AS NUMERIC),
			version = version + 1,
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, amount, accountID)
	return err
}

func (r *AccountRepository) AddBalance(ctx context.Context, accountID int64, amount string) error {
	query := `
		UPDATE account SET
			balance = balance + CAST($1 AS NUMERIC),
			version = version + 1,
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, amount, accountID)
	return err
}

func (r *WealthRepository) GetActiveOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	query := `
		SELECT o.id, o.user_id, o.product_id, p.title as product_title, p.currency, o.amount,
			p.duration,
			o.interest_expected, o.interest_paid, o.interest_accrued, o.start_date, o.end_date,
			o.auto_renew, o.status, o.renewed_from_order_id, o.renewed_to_order_id,
			o.redemption_amount, o.redemption_type, o.redeemed_at, o.created_at, o.updated_at
		FROM wealth_order o
		JOIN wealth_product p ON o.product_id = p.id
		WHERE o.status = 1
		ORDER BY o.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*repository.WealthOrderModel
	for rows.Next() {
		var o repository.WealthOrderModel
		var redemptionAmount, redeemedAt sql.NullString
		var redemptionType sql.NullString
		err := rows.Scan(
			&o.ID, &o.UserID, &o.ProductID, &o.ProductTitle, &o.Currency, &o.Amount,
			&o.Duration,
			&o.InterestExpected, &o.InterestPaid, &o.InterestAccrued,
			&o.StartDate, &o.EndDate, &o.AutoRenew, &o.Status,
			&o.RenewedFromOrderID, &o.RenewedToOrderID,
			&redemptionAmount, &redemptionType, &redeemedAt,
			&o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		o.RedemptionAmount = redemptionAmount.String
		o.RedeemedAt = redeemedAt.String
		orders = append(orders, &o)
	}
	return orders, rows.Err()
}

func (r *WealthRepository) GetExpiredOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	query := `
		SELECT o.id, o.user_id, o.product_id, p.title as product_title, p.currency, o.amount,
			p.duration,
			o.interest_expected, o.interest_paid, o.interest_accrued, o.start_date, o.end_date,
			o.auto_renew, o.status, o.renewed_from_order_id, o.renewed_to_order_id,
			o.redemption_amount, o.redemption_type, o.redeemed_at, o.created_at, o.updated_at
		FROM wealth_order o
		JOIN wealth_product p ON o.product_id = p.id
		WHERE o.status = 1 AND o.end_date <= CURRENT_DATE
		ORDER BY o.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*repository.WealthOrderModel
	for rows.Next() {
		var o repository.WealthOrderModel
		var redemptionAmount, redeemedAt sql.NullString
		var redemptionType sql.NullString
		err := rows.Scan(
			&o.ID, &o.UserID, &o.ProductID, &o.ProductTitle, &o.Currency, &o.Amount,
			&o.Duration,
			&o.InterestExpected, &o.InterestPaid, &o.InterestAccrued,
			&o.StartDate, &o.EndDate, &o.AutoRenew, &o.Status,
			&o.RenewedFromOrderID, &o.RenewedToOrderID,
			&redemptionAmount, &redemptionType, &redeemedAt,
			&o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		o.RedemptionAmount = redemptionAmount.String
		o.RedeemedAt = redeemedAt.String
		orders = append(orders, &o)
	}
	return orders, rows.Err()
}

func (r *WealthRepository) GetPendingOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	query := `
		SELECT o.id, o.user_id, o.product_id, p.title as product_title, p.currency, o.amount,
			p.duration,
			o.interest_expected, o.interest_paid, o.interest_accrued, o.start_date, o.end_date,
			o.auto_renew, o.status, o.renewed_from_order_id, o.renewed_to_order_id,
			o.redemption_amount, o.redemption_type, o.redeemed_at, o.created_at, o.updated_at
		FROM wealth_order o
		JOIN wealth_product p ON o.product_id = p.id
		WHERE o.status = 0 AND o.start_date <= CURRENT_DATE
		ORDER BY o.created_at DESC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []*repository.WealthOrderModel
	for rows.Next() {
		var o repository.WealthOrderModel
		var redemptionAmount, redeemedAt sql.NullString
		var redemptionType sql.NullString
		err := rows.Scan(
			&o.ID, &o.UserID, &o.ProductID, &o.ProductTitle, &o.Currency, &o.Amount,
			&o.Duration,
			&o.InterestExpected, &o.InterestPaid, &o.InterestAccrued,
			&o.StartDate, &o.EndDate, &o.AutoRenew, &o.Status,
			&o.RenewedFromOrderID, &o.RenewedToOrderID,
			&redemptionAmount, &redemptionType, &redeemedAt,
			&o.CreatedAt, &o.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		o.RedemptionAmount = redemptionAmount.String
		o.RedeemedAt = redeemedAt.String
		orders = append(orders, &o)
	}
	return orders, rows.Err()
}

func (r *WealthRepository) ActivateOrder(ctx context.Context, orderID int64) error {
	query := `
		UPDATE wealth_order SET
			status = 1,
			updated_at = NOW()
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query, orderID)
	return err
}

func (r *WealthRepository) UpdateInterestAccrued(ctx context.Context, orderID int64, interestAccrued string) error {
	query := `
		UPDATE wealth_order SET
			interest_accrued = CAST($1 AS NUMERIC),
			updated_at = NOW()
		WHERE id = $2
	`
	_, err := r.db.ExecContext(ctx, query, interestAccrued, orderID)
	return err
}

func (r *WealthRepository) AccrueInterest(ctx context.Context, orderID int64, amount string, date string) error {
	query := `
		UPDATE wealth_order SET
			interest_accrued = interest_accrued + CAST($1 AS NUMERIC),
			last_interest_date = $2,
			updated_at = NOW()
		WHERE id = $4
	`
	_, err := r.db.ExecContext(ctx, query, amount, date, "now()", orderID)
	return err
}

func (r *WealthRepository) SettleOrder(ctx context.Context, orderID int64, interestPaid string) error {
	query := `
		UPDATE wealth_order SET
			interest_paid = CAST(interest_paid AS NUMERIC) + CAST($1 AS NUMERIC),
			interest_accrued = '0',
			status = 3,
			redeemed_at = $2,
			updated_at = $3
		WHERE id = $4
	`
	_, err := r.db.ExecContext(ctx, query, interestPaid, "now()", "now()", orderID)
	return err
}

func (r *WealthRepository) RenewOrder(ctx context.Context, order *repository.WealthOrderModel, product *repository.WealthProductModel, startDate string, endDate string) (*repository.WealthOrderModel, error) {
	now := time.Now()

	interestExpected, err := utils.CalculateInterest(order.Amount, product.APY, product.Duration)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate interest: %v", err)
	}

	newOrder := &repository.WealthOrderModel{
		UserID:             order.UserID,
		ProductID:          product.ID,
		ProductTitle:       product.Title,
		Currency:           product.Currency,
		Amount:             order.Amount,
		AutoRenew:          order.AutoRenew,
		Status:             0, // 待计息，由定时器在 start_date 更新为 1
		StartDate:          startDate,
		EndDate:            endDate,
		PrincipalRedeemed:  "0",
		InterestExpected:   interestExpected,
		InterestPaid:       "0",
		InterestAccrued:    "0",
		LastInterestDate:   "",
		RenewedFromOrderID: &order.ID,
		CreatedAt:          now.Format(time.RFC3339),
		UpdatedAt:          now.Format(time.RFC3339),
	}

	query := `
		INSERT INTO wealth_order (user_id, product_id, product_title, currency, amount,
			auto_renew, status, start_date, end_date,
			principal_redeemed, interest_expected, interest_paid, interest_accrued,
			renewed_from_order_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`
	err = r.db.QueryRowContext(ctx, query,
		newOrder.UserID, newOrder.ProductID, newOrder.ProductTitle, newOrder.Currency, newOrder.Amount,
		newOrder.AutoRenew, newOrder.Status, newOrder.StartDate, newOrder.EndDate,
		newOrder.PrincipalRedeemed, newOrder.InterestExpected, newOrder.InterestPaid, newOrder.InterestAccrued,
		newOrder.RenewedFromOrderID,
	).Scan(&newOrder.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to create renewed order: %v", err)
	}

	updateQuery := `
		UPDATE wealth_order SET
			renewed_to_order_id = $1,
			updated_at = NOW()
		WHERE id = $2
	`
	_, err = r.db.ExecContext(ctx, updateQuery, newOrder.ID, order.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to update original order: %v", err)
	}

	fmt.Printf("[RenewOrder] Order renewed successfully: old_order_id=%d, new_order_id=%d, user_id=%d, amount=%s, start_date=%s, end_date=%s\n",
		order.ID, newOrder.ID, order.UserID, order.Amount, startDate, endDate)

	return newOrder, nil
}

type JournalRepository struct {
	db *sql.DB
}

func NewJournalRepository(db *sql.DB) *JournalRepository {
	return &JournalRepository{db: db}
}

func (r *JournalRepository) CreateJournalRecord(ctx context.Context, record *repository.JournalModel) error {
	query := `
		INSERT INTO account_journal (serial_no, user_id, account_id, amount, balance_snapshot, biz_type, ref_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, CURRENT_TIMESTAMP))
	`
	_, err := r.db.ExecContext(ctx, query,
		record.SerialNo,
		record.UserID,
		record.AccountID,
		record.Amount,
		record.BalanceSnapshot,
		record.BizType,
		record.RefID,
		record.CreatedAt,
	)
	return err
}
