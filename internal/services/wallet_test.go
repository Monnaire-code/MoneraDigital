package services

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/dto"
	"monera-digital/internal/logger"
	"monera-digital/internal/models"
)

func init() {
	_ = logger.Init("test")
}

func TestWalletService_GetWalletAddress_FallsBackAfterMismatchedCoreAddress(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	const localAddress = "0x1111111111111111111111111111111111111111"
	mockCoreAPI.On("GetAddress", mock.Anything, mock.MatchedBy(func(request coreapi.GetAddressRequest) bool {
		return request.UserID == "123" && request.ProductCode == "C_SPOT" && request.Currency == "USDT_ERC20"
	})).Return(&coreapi.AddressInfo{Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW"}, nil).Once()
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 123).Return(&models.WalletCreationRequest{
		Status:    models.WalletCreationStatusSuccess,
		Address:   sql.NullString{String: localAddress, Valid: true},
		Addresses: sql.NullString{String: `{"USDT_ERC20":"` + localAddress + `"}`, Valid: true},
	}, nil).Once()
	mockRepo.On("GetUserWalletsByUserID", mock.Anything, 123).Return([]*models.UserWallet{}, nil).Once()

	address, err := service.GetWalletAddress(context.Background(), 123, dto.GetWalletAddressRequest{
		ProductCode: "C_SPOT",
		Currency:    "USDT_ERC20",
	})

	assert.NoError(t, err)
	assert.Equal(t, localAddress, address.Address)
	mockCoreAPI.AssertExpectations(t)
	mockRepo.AssertExpectations(t)
}

func TestWalletService_AddAddress_Success(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()
	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Setup expectations - wallet exists, no existing address for this currency
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", result.Address)
	assert.Equal(t, "USDT_TRC20", result.Currency)
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, mock.Anything)
	mockRepo.AssertCalled(t, "AddUserWalletAddress", mock.Anything, mock.Anything)
}

func TestWalletService_AddAddress_AlreadyExists(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()
	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-1",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)

	// Address already exists for this currency
	existingUserWallet := &models.UserWallet{
		ID:        5,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Updated address that will be returned after repository update
	updatedUserWallet := &models.UserWallet{
		ID:        5,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJNewAddress123456789ABCDEFGHIJKL", // New address from Core API
		Status:    models.UserWalletStatusNormal,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(existingUserWallet, nil)

	// Mock Core API to return new address
	mockCoreAPI.On("GetAddress", mock.Anything, coreapi.GetAddressRequest{
		UserID:      "1",
		ProductCode: "X_FINANCE",
		Currency:    "USDT_TRC20",
	}).Return(&coreapi.AddressInfo{
		Address:     "TJNewAddress123456789ABCDEFGHIJKL",
		AddressType: func() *string { s := "TRC20"; return &s }(),
	}, nil)

	// AddUserWalletAddress should be called to update existing address
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.MatchedBy(func(w *models.UserWallet) bool {
		return w.UserID == 1 && w.Currency == "USDT_TRC20"
	})).Return(updatedUserWallet, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Should return the new address from Core API
	assert.Equal(t, "TJNewAddress123456789ABCDEFGHIJKL", result.Address)
	assert.Equal(t, "USDT_TRC20", result.Currency)
	// Verify Core API was called
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, coreapi.GetAddressRequest{
		UserID:      "1",
		ProductCode: "X_FINANCE",
		Currency:    "USDT_TRC20",
	})
}

func TestWalletService_AddAddress_WalletNotFound(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	service := NewWalletService(mockRepo, nil)

	// No wallet found
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(nil, nil)
	mockRepo.On("GetActiveUserWallet", mock.Anything, 1).Return(nil, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "wallet not found")
}

func TestWalletService_AddAddress_CoreAPIFails(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()
	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("Core API unavailable"))

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get address from Core API")
}

func TestWalletService_AddAddress_UserWalletFallback(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)
	now := time.Now()

	// wallet_creation_requests returns nil (no active request)
	// but user_wallets has an active wallet
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(nil, nil)
	mockRepo.On("GetActiveUserWallet", mock.Anything, 1).Return(&models.UserWallet{
		ID:        1,
		UserID:    1,
		WalletID:  "wallet123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	// Request for USDT_ERC20 - should call Core API since it doesn't exist yet
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_ERC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "0xTMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9",
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet123",
		Currency:  "USDT_ERC20",
		Address:   "0xTMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "ERC20",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "0xTMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9", result.Address)
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, mock.Anything)
	mockRepo.AssertCalled(t, "AddUserWalletAddress", mock.Anything, mock.Anything)
}

func TestBuildCurrencyKey(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		network  string
		expected string
	}{
		{
			name:     "USDT on ERC20",
			token:    "USDT",
			network:  "ERC20",
			expected: "USDT_ERC20",
		},
		{
			name:     "USDT on TRC20",
			token:    "USDT",
			network:  "TRC20",
			expected: "USDT_TRC20",
		},
		{
			name:     "USDT on BEP20",
			token:    "USDT",
			network:  "BEP20",
			expected: "USDT_BEP20",
		},
		{
			name:     "USDC on ERC20",
			token:    "USDC",
			network:  "ERC20",
			expected: "USDC_ERC20",
		},
		{
			name:     "USDC on TRC20",
			token:    "USDC",
			network:  "TRC20",
			expected: "USDC_TRC20",
		},
		{
			name:     "USDC on BEP20",
			token:    "USDC",
			network:  "BEP20",
			expected: "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET", // 特例：返回长格式
		},
		{
			name:     "Invalid token",
			token:    "INVALID",
			network:  "ERC20",
			expected: "",
		},
		{
			name:     "Invalid network",
			token:    "USDT",
			network:  "INVALID",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildCurrencyKey(tt.token, tt.network)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWalletService_AddAddress_EmptyProductCode_UsesDefault(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()

	// Simulate wallet with empty ProductCode in database
	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "", // Empty in database!
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Setup expectations
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)

	// Capture the actual request to verify ProductCode defaults to X_FINANCE
	var capturedRequest coreapi.GetAddressRequest
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedRequest = args.Get(1).(coreapi.GetAddressRequest)
	}).Return(&coreapi.AddressInfo{
		Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
	}, nil)

	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", result.Address)

	// Verify that ProductCode defaults to X_FINANCE when empty
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, mock.Anything)
	assert.Equal(t, "X_FINANCE", capturedRequest.ProductCode, "ProductCode should default to X_FINANCE when wallet.ProductCode is empty")
}

func TestWalletService_AddAddress_UserWalletFallback_UsesDefault(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()

	// First call returns nil (no wallet_creation_requests)
	// Second call returns user_wallet (which doesn't have ProductCode at all)
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(nil, nil)
	mockRepo.On("GetActiveUserWallet", mock.Anything, 1).Return(&models.UserWallet{
		ID:        1,
		UserID:    1,
		WalletID:  "wallet123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: true,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	// Request for USDT_ERC20
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_ERC20").Return(nil, nil)

	// Capture the actual request
	var capturedRequest coreapi.GetAddressRequest
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedRequest = args.Get(1).(coreapi.GetAddressRequest)
	}).Return(&coreapi.AddressInfo{
		Address: "0xTMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9",
	}, nil)

	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet123",
		Currency:  "USDT_ERC20",
		Address:   "0xTMuA6YqfCeX8EhbfYEg5y7S4DqzSJireY9",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "ERC20",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that ProductCode defaults to X_FINANCE
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, mock.Anything)
	assert.Equal(t, "X_FINANCE", capturedRequest.ProductCode, "ProductCode should default to X_FINANCE for user_wallets fallback")
}

func TestWalletService_AddAddress_WithOptionalFields(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()
	addressType := "TRC20"
	derivePath := "m/44'/195'/0'/0/0"

	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address:     "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		AddressType: &addressType,
		DerivePath:  &derivePath,
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:          10,
		UserID:      1,
		WalletID:    "wallet-123",
		Currency:    "USDT_TRC20",
		Address:     "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		AddressType: sql.NullString{String: "TRC20", Valid: true},
		DerivePath:  sql.NullString{String: "m/44'/195'/0'/0/0", Valid: true},
		Status:      models.UserWalletStatusNormal,
		IsPrimary:   false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", result.Address)
	mockRepo.AssertCalled(t, "AddUserWalletAddress", mock.Anything, mock.Anything)
}

func TestWalletService_AddAddress_WithRequestID(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()

	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		wallet := args.Get(1).(*models.UserWallet)
		assert.Equal(t, "req-123", wallet.RequestID.String)
		assert.True(t, wallet.RequestID.Valid)
	}).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		RequestID: sql.NullString{String: "req-123", Valid: true},
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", result.Address)
	mockRepo.AssertCalled(t, "AddUserWalletAddress", mock.Anything, mock.Anything)
}

func TestWalletService_AddAddress_AddUserWalletAddressFails(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()

	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("database error"))

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "database error")
}

func TestWalletService_AddAddress_UserWalletNil(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()

	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRC20").Return(nil, nil)
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
	}, nil)
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRC20",
		Address:   "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRON",
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", result.Address)
}

func TestWalletService_CreateWallet_FetchesAddress(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	// No existing wallet
	mockRepo.On("GetWalletByUserProductCurrency", mock.Anything, 1, "X_FINANCE", "TRON").Return(nil, nil)

	// Capture the CreateWallet request
	var capturedCreateReq coreapi.CreateWalletRequest
	mockCoreAPI.On("CreateWallet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedCreateReq = args.Get(1).(coreapi.CreateWalletRequest)
	}).Return(&coreapi.CreateWalletResponse{
		WalletID: "wallet-123",
		Address:  "", // CreateWallet doesn't return address
		Status:   "SUCCESS",
	}, nil)

	// Capture the GetAddress request
	var capturedGetAddrReq coreapi.GetAddressRequest
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedGetAddrReq = args.Get(1).(coreapi.GetAddressRequest)
	}).Return(&coreapi.AddressInfo{
		Address: "TJ123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	}, nil)

	// Create request in DB
	mockRepo.On("CreateRequest", mock.Anything, mock.Anything).Return(nil)

	// Update request with address
	mockRepo.On("UpdateRequest", mock.Anything, mock.Anything).Return(nil)

	// Create user wallet
	mockRepo.On("CreateUserWallet", mock.Anything, mock.Anything).Return(nil)

	// Execute
	result, err := service.CreateWallet(context.Background(), 1, "X_FINANCE", "TRON")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "wallet-123", result.WalletID.String)
	assert.Equal(t, "TJ123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", result.Address.String)
	assert.True(t, result.Address.Valid)

	// Verify CreateWallet was called with correct parameters
	mockCoreAPI.AssertCalled(t, "CreateWallet", mock.Anything, mock.Anything)
	assert.Equal(t, 1, capturedCreateReq.UserID)
	assert.Equal(t, "X_FINANCE", capturedCreateReq.ProductCode)
	assert.Equal(t, "TRON", capturedCreateReq.Currency)

	// Verify GetAddress was called
	mockCoreAPI.AssertCalled(t, "GetAddress", mock.Anything, mock.Anything)
	assert.Equal(t, "1", capturedGetAddrReq.UserID)
	assert.Equal(t, "X_FINANCE", capturedGetAddrReq.ProductCode)
	assert.Equal(t, "TRON", capturedGetAddrReq.Currency)
}

func TestWalletService_CreateWallet_GetAddressFails_UsesCreateWalletAddress(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	// No existing wallet
	mockRepo.On("GetWalletByUserProductCurrency", mock.Anything, 1, "X_FINANCE", "TRON").Return(nil, nil)

	// CreateWallet returns address
	mockCoreAPI.On("CreateWallet", mock.Anything, mock.Anything).Return(&coreapi.CreateWalletResponse{
		WalletID: "wallet-456",
		Address:  "TCreateWallet123456789",
		Status:   "SUCCESS",
	}, nil)

	// GetAddress fails
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("Core API unavailable"))

	// Create request in DB
	mockRepo.On("CreateRequest", mock.Anything, mock.Anything).Return(nil)

	// Update request - should use CreateWallet's address since GetAddress failed
	mockRepo.On("UpdateRequest", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req := args.Get(1).(*models.WalletCreationRequest)
		// Should have address from CreateWallet
		assert.Equal(t, "TCreateWallet123456789", req.Address.String)
		assert.True(t, req.Address.Valid)
	}).Return(nil)

	// Create user wallet
	mockRepo.On("CreateUserWallet", mock.Anything, mock.Anything).Return(nil)

	// Execute
	result, err := service.CreateWallet(context.Background(), 1, "X_FINANCE", "TRON")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Should have address from CreateWallet response
	assert.Equal(t, "TCreateWallet123456789", result.Address.String)
}

func TestWalletService_CreateWallet_GetAddressReturnsEmpty_UsesCreateWalletAddress(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	// No existing wallet
	mockRepo.On("GetWalletByUserProductCurrency", mock.Anything, 1, "X_FINANCE", "TRON").Return(nil, nil)

	// CreateWallet returns address
	mockCoreAPI.On("CreateWallet", mock.Anything, mock.Anything).Return(&coreapi.CreateWalletResponse{
		WalletID: "wallet-789",
		Address:  "TCreate123456789",
		Status:   "SUCCESS",
	}, nil)

	// GetAddress returns empty address
	mockCoreAPI.On("GetAddress", mock.Anything, mock.Anything).Return(&coreapi.AddressInfo{
		Address: "",
	}, nil)

	// Create request in DB
	mockRepo.On("CreateRequest", mock.Anything, mock.Anything).Return(nil)

	// Update request - should use CreateWallet's address since GetAddress returned empty
	mockRepo.On("UpdateRequest", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req := args.Get(1).(*models.WalletCreationRequest)
		// Should have address from CreateWallet since GetAddress returned empty
		assert.Equal(t, "TCreate123456789", req.Address.String)
		assert.True(t, req.Address.Valid)
	}).Return(nil)

	// Create user wallet
	mockRepo.On("CreateUserWallet", mock.Anything, mock.Anything).Return(nil)

	// Execute
	result, err := service.CreateWallet(context.Background(), 1, "X_FINANCE", "TRON")

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Should have address from CreateWallet response
	assert.Equal(t, "TCreate123456789", result.Address.String)
}

func TestWalletService_AddAddress_TestnetCurrency_GeneratesLocally(t *testing.T) {
	mockRepo := new(MockWalletRepository)
	mockCoreAPI := new(MockCoreAPIClient)
	service := NewWalletService(mockRepo, mockCoreAPI)

	now := time.Now()
	existingWallet := &models.WalletCreationRequest{
		ID:          1,
		RequestID:   "req-123",
		UserID:      1,
		ProductCode: "X_FINANCE",
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: "wallet-123", Valid: true},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Setup expectations - wallet exists, no existing address for this testnet currency
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)
	mockRepo.On("GetUserWalletByUserAndCurrency", mock.Anything, 1, "USDT_TRON_TESTNET").Return(nil, nil)

	// Mock AddUserWalletAddress for testnet - use Once to ensure it's called exactly once
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.MatchedBy(func(uw *models.UserWallet) bool {
		return uw.Currency == "USDT_TRON_TESTNET" && uw.UserID == 1
	})).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRON_TESTNET",
		Address:   "TTest00000001FGHIJKLMNOPQRSTUVWXYZ",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil).Once()
	mockRepo.On("AddUserWalletAddress", mock.Anything, mock.Anything).Return(&models.UserWallet{
		ID:        10,
		UserID:    1,
		WalletID:  "wallet-123",
		Currency:  "USDT_TRON_TESTNET",
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil)
	mockRepo.On("GetActiveWalletByUserID", mock.Anything, 1).Return(existingWallet, nil)

	// For testnet, Core API should NOT be called - address is generated locally
	// So we don't set up any mock expectations for Core API

	result, err := service.AddAddress(context.Background(), 1, AddAddressRequest{
		Chain: "TRX (SHASTA) - TRON Testnet", // This maps to USDT_TRON_TESTNET
		Token: "USDT",
	})

	assert.NoError(t, err)
	assert.NotNil(t, result)
	// Should have generated a testnet address (starts with TTest for TRON)
	assert.Contains(t, result.Address, "TTest")
	assert.Equal(t, "USDT_TRON_TESTNET", result.Currency)

	// Verify Core API was NOT called
	mockCoreAPI.AssertNotCalled(t, "GetAddress", mock.Anything, mock.Anything)
	mockRepo.AssertCalled(t, "AddUserWalletAddress", mock.Anything, mock.Anything)
}
