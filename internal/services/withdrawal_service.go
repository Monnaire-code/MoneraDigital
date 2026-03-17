package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
	"monera-digital/internal/utils"
)

type ISafeheronService interface {
	Withdraw(ctx context.Context, req SafeheronWithdrawalRequest) (*SafeheronWithdrawalResponse, error)
}

type WithdrawalService struct {
	repo      *repository.Repository
	safeheron ISafeheronService
	db        *sql.DB
}

func NewWithdrawalService(db *sql.DB, repo *repository.Repository, safeheron ISafeheronService) *WithdrawalService {
	return &WithdrawalService{
		db:        db,
		repo:      repo,
		safeheron: safeheron,
	}
}

// CreateWithdrawal handles the withdrawal process with transaction support
func (s *WithdrawalService) CreateWithdrawal(ctx context.Context, userID int, req models.CreateWithdrawalRequest) (*models.WithdrawalOrder, error) {
	// Validate and get resources
	amount, address, err := s.validateWithdrawalRequest(ctx, userID, req)
	if err != nil {
		return nil, err
	}

	// Call Safeheron first (external API, not in transaction)
	requestID := uuid.New().String()
	shResp, err := s.safeheron.Withdraw(ctx, SafeheronWithdrawalRequest{
		CoinType:  req.Asset,
		ChainType: address.ChainType,
		ToAddress: address.WalletAddress,
		Amount:    req.Amount,
		RequestID: requestID,
	})

	if err != nil {
		// Fail: Unfreeze (outside transaction)
		if unfreezeErr := s.repo.Account.ReleaseFrozenBalance(ctx, userID, amount); unfreezeErr != nil {
			return nil, fmt.Errorf("safeheron failed and failed to unfreeze balance: %w (unfreeze error: %v)", err, unfreezeErr)
		}
		return nil, fmt.Errorf("safeheron failed: %w", err)
	}

	// Success: Execute DB operations in transaction
	var createdOrder *models.WithdrawalOrder
	txErr := s.withdrawWithTransaction(ctx, userID, amount, address, req, shResp, &createdOrder)
	if txErr != nil {
		return nil, txErr
	}

	return createdOrder, nil
}

// withdrawWithTransaction handles the DB operations in a transaction
func (s *WithdrawalService) withdrawWithTransaction(ctx context.Context, userID int, amount string, address *models.WithdrawalAddress, req models.CreateWithdrawalRequest, shResp *SafeheronWithdrawalResponse, orderPtr **models.WithdrawalOrder) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Execute all DB operations using the transaction

	// 1. Freeze Balance - use CAST for precision
	_, err = tx.ExecContext(ctx,
		`UPDATE account SET frozen_balance = frozen_balance + CAST($1 AS NUMERIC(65,8)), version = version + 1, updated_at = $3 WHERE user_id = $2`,
		amount, userID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to freeze balance: %w", err)
	}

	// 2. Deduct Balance - use CAST for precision
	result, err := tx.ExecContext(ctx,
		`UPDATE account SET frozen_balance = frozen_balance - CAST($1 AS NUMERIC(65,8)), balance = balance - CAST($1 AS NUMERIC(65,8)), version = version + 1, updated_at = $3 WHERE user_id = $2 AND balance >= CAST($1 AS NUMERIC(65,8))`,
		amount, userID, time.Now())
	if err != nil {
		return fmt.Errorf("failed to deduct balance: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("failed to deduct balance: account not found or insufficient balance")
	}

	// 3. Create Order
	order := &models.WithdrawalOrder{
		UserID:           userID,
		Amount:           amount,
		NetworkFee:       shResp.NetworkFee,
		PlatformFee:      "0",
		ActualAmount:     amount,
		ChainType:        address.ChainType,
		CoinType:         req.Asset,
		ToAddress:        address.WalletAddress,
		SafeheronOrderID: sql.NullString{String: shResp.SafeheronOrderID, Valid: true},
		TransactionHash:  sql.NullString{String: shResp.TxHash, Valid: true},
		Status:           "SENT",
	}

	err = tx.QueryRowContext(ctx,
		`INSERT INTO withdrawal_order (
			user_id, amount, network_fee, platform_fee, actual_amount,
			chain_type, coin_type, to_address, safeheron_order_id, transaction_hash,
			status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id, created_at`,
		order.UserID, order.Amount, order.NetworkFee, order.PlatformFee, order.ActualAmount,
		order.ChainType, order.CoinType, order.ToAddress, order.SafeheronOrderID, order.TransactionHash,
		order.Status, time.Now(), time.Now(),
	).Scan(&order.ID, &order.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create order: %w", err)
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	*orderPtr = order
	return nil
}

// validateWithdrawalRequest validates the withdrawal request and returns required resources
func (s *WithdrawalService) validateWithdrawalRequest(ctx context.Context, userID int, req models.CreateWithdrawalRequest) (string, *models.WithdrawalAddress, error) {
	// Validate amount using decimal utils
	if !utils.IsValidAmount(req.Amount) {
		return "", nil, errors.New("invalid amount")
	}

	// Normalize amount to 8 decimal places
	normalizedAmount, err := utils.NormalizeString(req.Amount)
	if err != nil {
		return "", nil, errors.New("invalid amount format")
	}

	// Get Account
	account, err := s.repo.Account.GetByUserIDAndType(ctx, userID, "WEALTH")
	if err != nil {
		if err == repository.ErrNotFound {
			return "", nil, errors.New("account not found")
		}
		return "", nil, err
	}

	// Check balance using decimal utils (account.Balance and FrozenBalance are now strings)
	availableBalance, _ := utils.Sub(account.Balance, account.FrozenBalance)
	isSufficient, _ := utils.GTE(availableBalance, normalizedAmount)
	if !isSufficient {
		return "", nil, errors.New("insufficient balance")
	}

	// Get Address
	address, err := s.repo.Address.GetAddressByID(ctx, req.AddressID)
	if err != nil {
		return "", nil, errors.New("address not found")
	}
	if address.UserID != userID {
		return "", nil, errors.New("address does not belong to user")
	}

	return normalizedAmount, address, nil
}

// GetWithdrawalHistory returns the withdrawal history for a user
func (s *WithdrawalService) GetWithdrawalHistory(ctx context.Context, userID int) ([]*models.WithdrawalOrder, error) {
	return s.repo.Withdrawal.GetOrdersByUserID(ctx, userID)
}

func (s *WithdrawalService) GetWithdrawalByID(ctx context.Context, userID int, id int) (*models.WithdrawalOrder, error) {
	order, err := s.repo.Withdrawal.GetOrderByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if order.UserID != userID {
		return nil, errors.New("unauthorized")
	}
	return order, nil
}

func (s *WithdrawalService) EstimateFee(ctx context.Context, asset, chain, amount string) (string, string, error) {
	networkFee := "1.0"
	if asset == "ETH" {
		networkFee = "0.002"
	}

	normalizedAmount, err := utils.NormalizeString(amount)
	if err != nil {
		return networkFee, "0.00000000", fmt.Errorf("invalid amount: %w", err)
	}

	received, calcErr := utils.Sub(normalizedAmount, networkFee)
	if calcErr != nil {
		return networkFee, "0.00000000", fmt.Errorf("failed to calculate: %w", calcErr)
	}

	isNegative, _ := utils.IsNegative(received)
	if isNegative {
		received = "0.00000000"
	}

	return networkFee, received, nil
}
