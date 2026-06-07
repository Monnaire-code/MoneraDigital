package services

import (
	"context"
	"database/sql"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
)

// MockDepositRepository
type MockDepositRepository struct {
	mock.Mock
}

func (m *MockDepositRepository) Create(ctx context.Context, deposit *models.Deposit) error {
	args := m.Called(ctx, deposit)
	if args.Get(0) == nil {
		deposit.ID = 1
	}
	return args.Error(0)
}

func (m *MockDepositRepository) GetByTxHash(ctx context.Context, txHash string) (*models.Deposit, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Deposit), args.Error(1)
}

func (m *MockDepositRepository) GetByUserID(ctx context.Context, userID int, limit, offset int) ([]*models.Deposit, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*models.Deposit), args.Get(1).(int64), args.Error(2)
}

func (m *MockDepositRepository) UpdateStatus(ctx context.Context, id int, status string, confirmedAt string) error {
	args := m.Called(ctx, id, status, confirmedAt)
	return args.Error(0)
}

// MockWalletRepository
type MockWalletRepository struct {
	mock.Mock
}

func (m *MockWalletRepository) CreateRequest(ctx context.Context, req *models.WalletCreationRequest) error {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		req.ID = 1
	}
	return args.Error(0)
}

func (m *MockWalletRepository) GetRequestByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WalletCreationRequest), args.Error(1)
}

func (m *MockWalletRepository) UpdateRequest(ctx context.Context, req *models.WalletCreationRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockWalletRepository) GetActiveWalletByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WalletCreationRequest), args.Error(1)
}

func (m *MockWalletRepository) GetWalletByUserProductCurrency(ctx context.Context, userID int, productCode, currency string) (*models.WalletCreationRequest, error) {
	args := m.Called(ctx, userID, productCode, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WalletCreationRequest), args.Error(1)
}

func (m *MockWalletRepository) CreateUserWallet(ctx context.Context, wallet *models.UserWallet) error {
	args := m.Called(ctx, wallet)
	return args.Error(0)
}

func (m *MockWalletRepository) GetUserWalletsByUserID(ctx context.Context, userID int) ([]*models.UserWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.UserWallet), args.Error(1)
}

func (m *MockWalletRepository) GetUserWalletByCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error) {
	args := m.Called(ctx, userID, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.UserWallet), args.Error(1)
}

func (m *MockWalletRepository) UpdateUserWalletStatus(ctx context.Context, id int, status models.UserWalletStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockWalletRepository) GetActiveUserWallet(ctx context.Context, userID int) (*models.UserWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.UserWallet), args.Error(1)
}

func (m *MockWalletRepository) AddUserWalletAddress(ctx context.Context, wallet *models.UserWallet) (*models.UserWallet, error) {
	args := m.Called(ctx, wallet)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.UserWallet), args.Error(1)
}

func (m *MockWalletRepository) GetUserWalletByUserAndCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error) {
	args := m.Called(ctx, userID, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.UserWallet), args.Error(1)
}

// MockCoreAPIClient for testing Core API calls
type MockCoreAPIClient struct {
	mock.Mock
}

func (m *MockCoreAPIClient) CreateWallet(ctx context.Context, req coreapi.CreateWalletRequest) (*coreapi.CreateWalletResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*coreapi.CreateWalletResponse), args.Error(1)
}

func (m *MockCoreAPIClient) GetAddress(ctx context.Context, req coreapi.GetAddressRequest) (*coreapi.AddressInfo, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*coreapi.AddressInfo), args.Error(1)
}

func (m *MockCoreAPIClient) GetIncomeHistory(ctx context.Context, req coreapi.GetIncomeHistoryRequest) ([]coreapi.AddressIncomeRecord, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]coreapi.AddressIncomeRecord), args.Error(1)
}

// MockAccountRepository
type MockAccountRepository struct {
	mock.Mock
}

func (m *MockAccountRepository) GetByUserIDAndType(ctx context.Context, userID int, accountType string) (*models.Account, error) {
	args := m.Called(ctx, userID, accountType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.Account), args.Error(1)
}

func (m *MockAccountRepository) Create(ctx context.Context, account *models.Account) error {
	args := m.Called(ctx, account)
	return args.Error(0)
}

func (m *MockAccountRepository) UpdateFrozenBalance(ctx context.Context, userID int, amount string) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) ReleaseFrozenBalance(ctx context.Context, userID int, amount string) error {
	args := m.Called(ctx, userID, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) DeductBalance(ctx context.Context, accountID int64, amount string) error {
	args := m.Called(ctx, accountID, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) AddBalance(ctx context.Context, accountID int64, amount string) error {
	args := m.Called(ctx, accountID, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) GetAccountsByUserID(ctx context.Context, userID int64) ([]*repository.AccountModel, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.AccountModel), args.Error(1)
}

func (m *MockAccountRepository) GetAccountByUserIDAndCurrency(ctx context.Context, userID int64, currency string) (*repository.AccountModel, error) {
	args := m.Called(ctx, userID, currency)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*repository.AccountModel), args.Error(1)
}

func (m *MockAccountRepository) FreezeBalance(ctx context.Context, accountID int64, amount string) error {
	args := m.Called(ctx, accountID, amount)
	return args.Error(0)
}

func (m *MockAccountRepository) UnfreezeBalance(ctx context.Context, accountID int64, amount string) error {
	args := m.Called(ctx, accountID, amount)
	return args.Error(0)
}

// MockJournalRepository
type MockJournalRepository struct {
	mock.Mock
}

func (m *MockJournalRepository) CreateJournal(ctx context.Context, journal *repository.JournalModel) error {
	args := m.Called(ctx, journal)
	return args.Error(0)
}

func (m *MockJournalRepository) GetJournalsByAccountID(ctx context.Context, accountID int64, limit, offset int) ([]*repository.JournalModel, int64, error) {
	args := m.Called(ctx, accountID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*repository.JournalModel), args.Get(1).(int64), args.Error(2)
}

func (m *MockJournalRepository) CreateJournalRecord(ctx context.Context, record *repository.JournalModel) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

// MockWithdrawalRepository
type MockWithdrawalRepository struct {
	mock.Mock
}

func (m *MockWithdrawalRepository) CreateOrder(ctx context.Context, order *models.WithdrawalOrder) (*models.WithdrawalOrder, error) {
	args := m.Called(ctx, order)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalOrder), args.Error(1)
}

func (m *MockWithdrawalRepository) GetOrdersByUserID(ctx context.Context, userID int) ([]*models.WithdrawalOrder, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.WithdrawalOrder), args.Error(1)
}

func (m *MockWithdrawalRepository) GetOrderByID(ctx context.Context, id int) (*models.WithdrawalOrder, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalOrder), args.Error(1)
}

func (m *MockWithdrawalRepository) UpdateOrder(ctx context.Context, order *models.WithdrawalOrder) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) CreateRequest(ctx context.Context, req *models.WithdrawalRequest) error {
	args := m.Called(ctx, req)
	return args.Error(0)
}

func (m *MockWithdrawalRepository) GetRequestByID(ctx context.Context, requestID string) (*models.WithdrawalRequest, error) {
	args := m.Called(ctx, requestID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalRequest), args.Error(1)
}

// MockAddressRepository
type MockAddressRepository struct {
	mock.Mock
}

func (m *MockAddressRepository) CreateAddress(ctx context.Context, address *models.WithdrawalAddress) (*models.WithdrawalAddress, error) {
	args := m.Called(ctx, address)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalAddress), args.Error(1)
}

func (m *MockAddressRepository) GetAddressesByUserID(ctx context.Context, userID int) ([]*models.WithdrawalAddress, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*models.WithdrawalAddress), args.Error(1)
}

func (m *MockAddressRepository) GetAddressByID(ctx context.Context, id int) (*models.WithdrawalAddress, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalAddress), args.Error(1)
}

func (m *MockAddressRepository) UpdateAddress(ctx context.Context, address *models.WithdrawalAddress) error {
	args := m.Called(ctx, address)
	return args.Error(0)
}

func (m *MockAddressRepository) DeleteAddress(ctx context.Context, id int) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockAddressRepository) GetByAddressAndChain(ctx context.Context, address, chain string) (*models.WithdrawalAddress, error) {
	args := m.Called(ctx, address, chain)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*models.WithdrawalAddress), args.Error(1)
}

func (m *MockAddressRepository) SetPrimary(ctx context.Context, userID int, addressID int) error {
	args := m.Called(ctx, userID, addressID)
	return args.Error(0)
}

func (m *MockAddressRepository) UnfreezeExpiredAddresses(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return int64(args.Int(0)), args.Error(1)
}

func (m *MockAddressRepository) UpdateFrozenUntil(ctx context.Context, id int, frozenUntil sql.NullTime) error {
	args := m.Called(ctx, id, frozenUntil)
	return args.Error(0)
}

// MockWealthRepository
type MockWealthRepository struct {
	mock.Mock
}

func (m *MockWealthRepository) GetActiveProducts(ctx context.Context) ([]*repository.WealthProductModel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.WealthProductModel), args.Error(1)
}

func (m *MockWealthRepository) GetProductByCode(ctx context.Context, code string) (*repository.WealthProductModel, error) {
	args := m.Called(ctx, code)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*repository.WealthProductModel), args.Error(1)
}

func (m *MockWealthRepository) CreateOrder(ctx context.Context, order *repository.WealthOrderModel) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockWealthRepository) GetOrdersByUserID(ctx context.Context, userID int64) ([]*repository.WealthOrderModel, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) GetOrderByID(ctx context.Context, id int64) (*repository.WealthOrderModel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) UpdateOrder(ctx context.Context, order *repository.WealthOrderModel) error {
	args := m.Called(ctx, order)
	return args.Error(0)
}

func (m *MockWealthRepository) GetProductByID(ctx context.Context, id int64) (*repository.WealthProductModel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*repository.WealthProductModel), args.Error(1)
}

func (m *MockWealthRepository) UpdateProductSoldQuota(ctx context.Context, id int64, amount string) error {
	args := m.Called(ctx, id, amount)
	return args.Error(0)
}

func (m *MockWealthRepository) GetActiveOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) GetExpiredOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) GetPendingOrders(ctx context.Context) ([]*repository.WealthOrderModel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) AccrueInterest(ctx context.Context, orderID int64, amount string, date string) error {
	args := m.Called(ctx, orderID, amount, date)
	return args.Error(0)
}

func (m *MockWealthRepository) SettleOrder(ctx context.Context, orderID int64, interestPaid string) error {
	args := m.Called(ctx, orderID, interestPaid)
	return args.Error(0)
}

func (m *MockWealthRepository) RenewOrder(ctx context.Context, order *repository.WealthOrderModel, product *repository.WealthProductModel, startDate string, endDate string) (*repository.WealthOrderModel, error) {
	args := m.Called(ctx, order, product, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*repository.WealthOrderModel), args.Error(1)
}

func (m *MockWealthRepository) UpdateInterestAccrued(ctx context.Context, orderID int64, interestAccrued string) error {
	args := m.Called(ctx, orderID, interestAccrued)
	return args.Error(0)
}

func (m *MockWealthRepository) ActivateOrder(ctx context.Context, orderID int64) error {
	args := m.Called(ctx, orderID)
	return args.Error(0)
}
