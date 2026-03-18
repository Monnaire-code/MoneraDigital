package services

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

// MockSafeheronService
type MockSafeheronService struct {
	mock.Mock
}

func (m *MockSafeheronService) Withdraw(ctx context.Context, req SafeheronWithdrawalRequest) (*SafeheronWithdrawalResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*SafeheronWithdrawalResponse), args.Error(1)
}

// MockAccountRepositoryForWithdrawal implements repository.Account interface
type MockAccountRepositoryForWithdrawal struct {
	mock.Mock
}

func (m *MockAccountRepositoryForWithdrawal) GetByUserIDAndType(ctx context.Context, userID int, accountType string) (*models.Account, error) {
	args := m.Called(ctx, userID, accountType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Account), args.Error(1)
}

func (m *MockAccountRepositoryForWithdrawal) Create(ctx context.Context, account *models.Account) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockAccountRepositoryForWithdrawal) UpdateFrozenBalance(ctx context.Context, userID int, amount string) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func (m *MockAccountRepositoryForWithdrawal) ReleaseFrozenBalance(ctx context.Context, userID int, amount string) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func (m *MockAccountRepositoryForWithdrawal) DeductBalance(ctx context.Context, userID int, amount string) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func TestWithdrawalService_CreateWithdrawal_InsufficientBalance(t *testing.T) {
	mockAccountRepo := new(MockAccountRepositoryForWithdrawal)
	repo := &repository.Repository{Account: mockAccountRepo}
	service := NewWithdrawalService(nil, repo, nil)

	ctx := context.Background()
	userID := 1
	req := models.CreateWithdrawalRequest{
		AddressID: 10,
		Amount:    "100.0",
		Asset:     "USDT",
	}

	account := &models.Account{
		UserID:        userID,
		Balance:       "50.0",
		FrozenBalance: "0.0",
	}

	mockAccountRepo.On("GetByUserIDAndType", ctx, userID, "WEALTH").Return(account, nil)

	_, err := service.CreateWithdrawal(ctx, userID, req)
	assert.Error(t, err)
	assert.Equal(t, "insufficient balance", err.Error())
}

func TestWithdrawalService_CreateWithdrawal_WithMockDB(t *testing.T) {
	// Setup sqlmock
	db, sqlMock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Setup Mocks
	mockAccountRepo := new(MockAccountRepositoryForWithdrawal)
	mockAddressRepo := new(MockAddressRepository)
	mockWithdrawalRepo := new(MockWithdrawalRepository)
	mockSafeheron := new(MockSafeheronService)

	// Combine into Repository
	repo := &repository.Repository{
		Account:    mockAccountRepo,
		Address:    mockAddressRepo,
		Withdrawal: mockWithdrawalRepo,
	}

	service := NewWithdrawalService(db, repo, mockSafeheron)

	ctx := context.Background()
	userID := 1
	req := models.CreateWithdrawalRequest{
		AddressID: 10,
		Amount:    "100.0",
		Asset:     "USDT",
	}

	// Mock Data
	account := &models.Account{
		UserID:        userID,
		Balance:       "200.0",
		FrozenBalance: "0.0",
	}
	address := &models.WithdrawalAddress{
		ID:            10,
		UserID:        userID,
		ChainType:     "TRC20",
		WalletAddress: "Txyz...",
	}
	shResp := &SafeheronWithdrawalResponse{
		TxHash:           "0xtx",
		SafeheronOrderID: "sh-123",
		NetworkFee:       "1.0",
	}

	// Expectations
	mockAccountRepo.On("GetByUserIDAndType", ctx, userID, "WEALTH").Return(account, nil)
	mockAddressRepo.On("GetAddressByID", ctx, 10).Return(address, nil)

	mockSafeheron.On("Withdraw", ctx, mock.MatchedBy(func(r SafeheronWithdrawalRequest) bool {
		return r.Amount == "100.0" && r.ToAddress == "Txyz..." && r.CoinType == "USDT" && r.ChainType == "TRC20"
	})).Return(shResp, nil)

	// Mock DB transaction operations - use flexible matching
	sqlMock.ExpectBegin()
	sqlMock.ExpectExec("UPDATE account SET frozen_balance = frozen_balance \\+ CAST").
		WithArgs(sqlmock.AnyArg(), userID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	sqlMock.ExpectExec("UPDATE account SET frozen_balance = frozen_balance - CAST\\(\\$1 AS NUMERIC\\(65,8\\)\\), balance = balance - CAST").
		WithArgs(sqlmock.AnyArg(), userID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	sqlMock.ExpectQuery("INSERT INTO withdrawal_order").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(1, time.Now()))
	sqlMock.ExpectCommit()

	// Execute
	order, err := service.CreateWithdrawal(ctx, userID, req)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, 1, order.ID)

	mockAccountRepo.AssertExpectations(t)
	mockAddressRepo.AssertExpectations(t)
	mockSafeheron.AssertExpectations(t)
	mockWithdrawalRepo.AssertExpectations(t)
	assert.NoError(t, sqlMock.ExpectationsWereMet())
}
