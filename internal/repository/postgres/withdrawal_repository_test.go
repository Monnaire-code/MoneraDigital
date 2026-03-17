package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"monera-digital/internal/models"
)

func TestWithdrawalRepository_CreateOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	repo := NewWithdrawalRepository(db)

	order := &models.WithdrawalOrder{
		UserID:       1,
		Amount:       "100.00",
		ChainType:    "TRC20",
		CoinType:     "USDT",
		ToAddress:    "Txyz...",
		NetworkFee:   "1.00",
		PlatformFee:  "0.50",
		ActualAmount: "98.50",
		Status:       "PENDING",
	}

	mock.ExpectQuery("INSERT INTO withdrawal_order").
		WithArgs(order.UserID, order.Amount, order.NetworkFee, order.PlatformFee, order.ActualAmount,
			order.ChainType, order.CoinType, order.ToAddress, order.SafeheronOrderID, order.TransactionHash,
			order.Status, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))

	createdOrder, err := repo.CreateOrder(context.Background(), order)
	assert.NoError(t, err)
	assert.Equal(t, 1, createdOrder.ID)
	assert.NotZero(t, createdOrder.CreatedAt)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestAccountRepository_UpdateFrozenBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	repo := NewAccountRepositoryV1(db)

	userID := 1
	amount := "100.0"

	// Test case: Success
	mock.ExpectExec("UPDATE account SET frozen_balance = frozen_balance \\+ CAST").
		WithArgs(amount, userID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.UpdateFrozenBalance(context.Background(), userID, amount)
	assert.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
