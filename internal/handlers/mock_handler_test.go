package handlers

import (
	"context"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
	"monera-digital/internal/services"
	"time"

	"github.com/stretchr/testify/mock"
)

// MockWalletRepository for handler tests
type MockWalletRepository struct {
	wallets map[int]*models.WalletCreationRequest
}

func NewMockWalletRepository() *MockWalletRepository {
	return &MockWalletRepository{
		wallets: make(map[int]*models.WalletCreationRequest),
	}
}

func (m *MockWalletRepository) CreateRequest(ctx context.Context, req *models.WalletCreationRequest) error {
	req.ID = len(m.wallets) + 1
	req.CreatedAt = time.Now()
	req.UpdatedAt = time.Now()
	// Set status to Success by default for tests unless specified otherwise
	if req.Status == "" {
		req.Status = models.WalletCreationStatusSuccess
	}
	m.wallets[req.ID] = req
	return nil
}

func (m *MockWalletRepository) GetRequestByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error) {
	return nil, nil
}

func (m *MockWalletRepository) GetRequestByID(ctx context.Context, id int) (*models.WalletCreationRequest, error) {
	return nil, nil
}

func (m *MockWalletRepository) UpdateRequest(ctx context.Context, req *models.WalletCreationRequest) error {
	m.wallets[req.ID] = req
	return nil
}

func (m *MockWalletRepository) GetWalletByUserProductCurrency(ctx context.Context, userID int, productCode, currency string) (*models.WalletCreationRequest, error) {
	// For simplicity in handler tests, always return nil (no existing wallet)
	return nil, nil
}

func (m *MockWalletRepository) GetActiveWalletByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error) {
	return nil, nil
}

func (m *MockWalletRepository) CreateUserWallet(ctx context.Context, wallet *models.UserWallet) error {
	return nil
}

func (m *MockWalletRepository) GetUserWalletsByUserID(ctx context.Context, userID int) ([]*models.UserWallet, error) {
	return nil, nil
}

func (m *MockWalletRepository) GetUserWalletByCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error) {
	return nil, nil
}

func (m *MockWalletRepository) UpdateUserWalletStatus(ctx context.Context, id int, status models.UserWalletStatus) error {
	return nil
}

func (m *MockWalletRepository) GetActiveUserWallet(ctx context.Context, userID int) (*models.UserWallet, error) {
	return nil, nil
}

func (m *MockWalletRepository) AddUserWalletAddress(ctx context.Context, wallet *models.UserWallet) (*models.UserWallet, error) {
	return wallet, nil
}

func (m *MockWalletRepository) GetUserWalletByUserAndCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error) {
	return nil, nil
}

// Interface check
var _ repository.Wallet = (*MockWalletRepository)(nil)

// MockCoreAPIClient for handler tests
type MockCoreAPIClientForHandler struct {
	mock.Mock
}

func (m *MockCoreAPIClientForHandler) CreateWallet(ctx context.Context, req coreapi.CreateWalletRequest) (*coreapi.CreateWalletResponse, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*coreapi.CreateWalletResponse), args.Error(1)
}

func (m *MockCoreAPIClientForHandler) GetAddress(ctx context.Context, req coreapi.GetAddressRequest) (*coreapi.AddressInfo, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*coreapi.AddressInfo), args.Error(1)
}

func (m *MockCoreAPIClientForHandler) GetIncomeHistory(ctx context.Context, req coreapi.GetIncomeHistoryRequest) ([]coreapi.AddressIncomeRecord, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]coreapi.AddressIncomeRecord), args.Error(1)
}

// Setup test handler with mock service
func newMockHandler() *Handler {
	mockRepo := NewMockWalletRepository()
	mockCoreAPI := new(MockCoreAPIClientForHandler)
	const testEVMAddress = "0x1111111111111111111111111111111111111111"
	mockCoreAPI.On("CreateWallet", mock.Anything, mock.Anything).Return(&coreapi.CreateWalletResponse{
		WalletID:  "wallet_test_123",
		Address:   testEVMAddress,
		Addresses: map[string]string{"USD": testEVMAddress},
		Status:    "SUCCESS",
	}, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: testEVMAddress,
	}, nil)
	walletService := services.NewWalletService(mockRepo, mockCoreAPI)

	h := &Handler{
		WalletService: walletService,
	}
	return h
}
