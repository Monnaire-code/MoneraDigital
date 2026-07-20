package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"monera-digital/internal/coreapi"
	"monera-digital/internal/currency"
	"monera-digital/internal/dto"
	"monera-digital/internal/logger"
	"monera-digital/internal/models"
	"monera-digital/internal/repository"
	"strings"
	"time"

	"github.com/google/uuid"
)

type WalletService struct {
	repo          repository.Wallet
	coreAPIClient coreapi.CoreAPIClientInterface
}

func NewWalletService(repo repository.Wallet, coreAPIClient coreapi.CoreAPIClientInterface) *WalletService {
	return &WalletService{repo: repo, coreAPIClient: coreAPIClient}
}

// CreateWallet creates a new wallet for the user with productCode and currency.
func (s *WalletService) CreateWallet(ctx context.Context, userID int, productCode, currencyCode string) (*models.WalletCreationRequest, error) {
	logger.Info("[DEBUG-ACCOUNT-OPENING] WalletService.CreateWallet started", "userId", userID, "productCode", productCode, "currency", currencyCode)

	// Check for existing wallet with same product and currency
	existing, err := s.repo.GetWalletByUserProductCurrency(ctx, userID, productCode, currencyCode)
	if err != nil {
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletByUserProductCurrency failed", "userId", userID, "error", err.Error())
		return nil, err
	}
	if existing != nil && existing.Status == models.WalletCreationStatusSuccess {
		logger.Info("[DEBUG-ACCOUNT-OPENING] CreateWallet returning existing wallet", "userId", userID, "requestId", existing.RequestID, "status", existing.Status)
		return existing, nil
	}

	reqID := uuid.New().String()
	newReq := &models.WalletCreationRequest{
		RequestID:   reqID,
		UserID:      userID,
		ProductCode: productCode,
		Currency:    currencyCode,
		Status:      models.WalletCreationStatusCreating,
	}
	err = s.repo.CreateRequest(ctx, newReq)
	if err != nil {
		logger.Error("[DEBUG-ACCOUNT-OPENING] Failed to create wallet request", "error", err.Error(), "userId", userID, "productCode", productCode, "currency", currencyCode)
		return nil, err
	}
	logger.Info("[DEBUG-ACCOUNT-OPENING] Wallet request created", "requestId", reqID, "userId", userID, "dbId", newReq.ID, "status", newReq.Status)

	if s.coreAPIClient == nil {
		errMsg := "Core API client not initialized"
		logger.Error("[DEBUG-ACCOUNT-OPENING] Core API client not initialized", "userId", userID)
		return nil, errors.New(errMsg)
	}

	// Ensure currency is in full format for Core API (USDC_BEP20 uses long format)
	coreCurrency := currency.ToFullFormat(currencyCode)
	logger.Info("[DEBUG-ACCOUNT-OPENING] Calling Core API CreateWallet", "userId", userID, "productCode", productCode, "currency", currencyCode, "coreCurrency", coreCurrency)
	coreResp, err := s.coreAPIClient.CreateWallet(ctx, coreapi.CreateWalletRequest{
		UserID:      userID,
		ProductCode: productCode,
		Currency:    coreCurrency,
	})
	if err != nil {
		logger.Error("[DEBUG-ACCOUNT-OPENING] Core API wallet creation failed", "error", err.Error(), "userId", userID, "productCode", productCode, "currency", currencyCode)
		updateErr := s.repo.UpdateRequest(ctx, &models.WalletCreationRequest{ID: newReq.ID, RequestID: reqID, Status: models.WalletCreationStatusFailed})
		if updateErr != nil {
			logger.Error("[DEBUG-ACCOUNT-OPENING] Failed to update wallet status to FAILED", "error", updateErr.Error(), "requestId", reqID, "dbId", newReq.ID)
		} else {
			logger.Info("[DEBUG-ACCOUNT-OPENING] Updated wallet status to FAILED", "requestId", reqID, "dbId", newReq.ID)
		}
		return nil, fmt.Errorf("wallet creation failed: %w", err)
	}

	logger.Info("[DEBUG-ACCOUNT-OPENING] Core API wallet created successfully", "walletId", coreResp.WalletID, "userId", userID, "address", coreResp.Address)

	// Fetch address from Core API - CreateWallet may not return address immediately
	address := coreResp.Address
	walletID := coreResp.WalletID

	// Try to fetch address from GetAddress API (use full format for Core API)
	fetchCurrency := currency.ToFullFormat(currencyCode)
	logger.Info("[DEBUG-ACCOUNT-OPENING] Fetching address from GetAddress API", "userId", userID, "currency", currencyCode, "coreCurrency", fetchCurrency)
	addressInfo, addrErr := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
		UserID:      fmt.Sprintf("%d", userID),
		ProductCode: productCode,
		Currency:    fetchCurrency,
	})
	if addrErr == nil && addressInfo.Address != "" {
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetAddress API returned address", "address", addressInfo.Address)
		address = addressInfo.Address
	} else {
		logger.Warn("[DEBUG-ACCOUNT-OPENING] GetAddress API failed or returned empty", "error", addrErr)
	}

	// Validate that we have a valid address before marking as SUCCESS
	if address == "" && walletID == "" {
		logger.Error("[DEBUG-ACCOUNT-OPENING] Core API returned empty walletId and address, marking as FAILED", "userId", userID)
		newReq.Status = models.WalletCreationStatusFailed
		newReq.ErrorMessage = sql.NullString{String: "Core API returned empty walletId and address", Valid: true}
		if err := s.repo.UpdateRequest(ctx, newReq); err != nil {
			logger.Error("[DEBUG-ACCOUNT-OPENING] Failed to update wallet status to FAILED", "error", err.Error())
		}
		return nil, fmt.Errorf("wallet creation failed: Core API returned empty walletId and address")
	}

	newReq.Status = models.WalletCreationStatusSuccess
	newReq.WalletID = sql.NullString{String: walletID, Valid: walletID != ""}
	newReq.Address = sql.NullString{String: address, Valid: address != ""}
	if coreResp.Addresses != nil {
		addressesJSON, _ := json.Marshal(coreResp.Addresses)
		newReq.Addresses = sql.NullString{String: string(addressesJSON), Valid: true}
	}
	newReq.UpdatedAt = time.Now()

	logger.Info("[DEBUG-ACCOUNT-OPENING] Updating wallet status to SUCCESS", "requestId", reqID, "dbId", newReq.ID, "status", newReq.Status)
	if err := s.repo.UpdateRequest(ctx, newReq); err != nil {
		logger.Error("[DEBUG-ACCOUNT-OPENING] Failed to update wallet request", "error", err.Error(), "requestId", reqID, "dbId", newReq.ID)
	} else {
		logger.Info("[DEBUG-ACCOUNT-OPENING] Successfully updated wallet to SUCCESS", "requestId", reqID, "dbId", newReq.ID)
	}

	// Sync to user_wallets table - store individual wallet addresses
	userWallet := &models.UserWallet{
		UserID:    userID,
		WalletID:  walletID,
		Currency:  currencyCode,
		Address:   address,
		Status:    models.UserWalletStatusNormal,
		IsPrimary: true,
	}
	if reqID != "" {
		userWallet.RequestID = sql.NullString{String: reqID, Valid: true}
	}
	if err := s.repo.CreateUserWallet(ctx, userWallet); err != nil {
		logger.Error("Failed to sync user wallet", "error", err.Error(), "userId", userID, "currency", currencyCode)
		// Don't fail the operation if user_wallet sync fails
	}

	return newReq, nil
}

// buildCurrencyKey builds a valid currency key from token and network.
// Normalizes network aliases (TRON -> TRC20) to standard names.
// Returns DB storage format: short format for most, long format for USDC_BEP20.
func buildCurrencyKey(token, network string) string {
	// Normalize network aliases
	network = currency.NormalizeNetwork(network)

	// Build the currency key (short format initially)
	currencyKey := currency.BuildCurrency(token, network)

	// Validate the currency is supported
	if !currency.IsValid(currencyKey) {
		return ""
	}

	// Convert to full format for USDC_BEP20 (特例：使用长格式)
	return currency.ToFullFormat(currencyKey)
}

func (s *WalletService) GetWalletInfo(ctx context.Context, userID int) (*models.WalletCreationRequest, error) {
	logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo started", "userId", userID)

	// First try to find active/success wallet
	w, err := s.repo.GetActiveWalletByUserID(ctx, userID)
	if err != nil {
		logger.Error("[DEBUG-ACCOUNT-OPENING] GetActiveWalletByUserID failed", "userId", userID, "error", err.Error())
		return nil, err
	}

	if w != nil {
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo found active wallet", "userId", userID, "status", w.Status, "requestId", w.RequestID)
	}

	// If not found, check if there is any request (e.g. creating)
	if w == nil {
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo no active wallet, checking for any request", "userId", userID)
		req, err := s.repo.GetRequestByUserID(ctx, userID)
		if err != nil {
			logger.Error("[DEBUG-ACCOUNT-OPENING] GetRequestByUserID failed", "userId", userID, "error", err.Error())
			return nil, err
		}
		if req != nil {
			logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo found request", "userId", userID, "status", req.Status, "requestId", req.RequestID)
			return req, nil
		}
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo no request found, returning nil", "userId", userID)
		return nil, nil
	}

	// If wallet is SUCCESS but address is empty, try to fetch address from Core API
	if w.Status == models.WalletCreationStatusSuccess && (w.Address.String == "" || w.Addresses.String == "" || w.Addresses.String == "{}") {
		logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo: Wallet is SUCCESS but address is empty, fetching from GetAddress API", "userId", userID)

		// Get product code and currency from wallet (use full format for Core API)
		productCode := "X_FINANCE"
		coreCurrency := currency.ToFullFormat(w.Currency)

		// Try to fetch address from GetAddress API
		addressInfo, addrErr := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
			UserID:      fmt.Sprintf("%d", userID),
			ProductCode: productCode,
			Currency:    coreCurrency,
		})

		if addrErr == nil && addressInfo.Address != "" {
			logger.Info("[DEBUG-ACCOUNT-OPENING] GetWalletInfo: GetAddress API returned address", "address", addressInfo.Address)

			// Update wallet with the fetched address
			w.Address = sql.NullString{String: addressInfo.Address, Valid: true}

			// Update addresses JSON
			addresses := make(map[string]string)
			if w.Addresses.Valid && w.Addresses.String != "" && w.Addresses.String != "{}" {
				if err := json.Unmarshal([]byte(w.Addresses.String), &addresses); err != nil {
					logger.Warn("Failed to parse existing addresses", "error", err.Error())
				}
			}
			addresses[w.Currency] = addressInfo.Address
			addressesJSON, _ := json.Marshal(addresses)
			w.Addresses = sql.NullString{String: string(addressesJSON), Valid: true}

			// Update database
			updateReq := &models.WalletCreationRequest{
				ID:        w.ID,
				Address:   w.Address,
				Addresses: w.Addresses,
			}
			if err := s.repo.UpdateRequest(ctx, updateReq); err != nil {
				logger.Warn("[DEBUG-ACCOUNT-OPENING] Failed to update wallet with fetched address", "error", err.Error())
			}
		} else {
			logger.Warn("[DEBUG-ACCOUNT-OPENING] GetWalletInfo: GetAddress API failed or returned empty", "error", addrErr)
		}
	}

	// Merge addresses from user_wallets into wallet_creation_requests
	userWallets, err := s.repo.GetUserWalletsByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	if len(userWallets) > 0 {
		// Parse existing addresses
		addresses := make(map[string]string)
		if w.Addresses.Valid && w.Addresses.String != "" {
			if err := json.Unmarshal([]byte(w.Addresses.String), &addresses); err != nil {
				logger.Warn("Failed to parse existing addresses", "error", err.Error())
			}
		}

		// Merge user_wallets addresses
		for _, uw := range userWallets {
			if uw.Address != "" {
				addresses[uw.Currency] = uw.Address
			}
		}

		// Update addresses JSON
		addressesJSON, _ := json.Marshal(addresses)
		w.Addresses = sql.NullString{String: string(addressesJSON), Valid: true}
	}

	return w, nil
}

type AddAddressRequest struct {
	Chain string
	Token string
}

// AddAddress adds a new wallet address for the given chain and token.
// It gets the address from Core API and stores it in user_wallets table.
// For testnet currencies, generates a local test address instead of calling Core API.
func (s *WalletService) AddAddress(ctx context.Context, userID int, req AddAddressRequest) (*models.UserWallet, error) {
	// Get user's wallet info to get wallet_id and productCode
	wallet, err := s.repo.GetActiveWalletByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Fall back to user_wallets if no wallet_creation_requests
	if wallet == nil {
		userWallet, err := s.repo.GetActiveUserWallet(ctx, userID)
		if err != nil {
			return nil, err
		}
		if userWallet != nil {
			wallet = convertUserWalletToRequest(userWallet)
		}
	}

	if wallet == nil {
		return nil, errors.New("wallet not found")
	}

	// Calculate currency key for the address
	addressKey := buildCurrencyKey(req.Token, req.Chain)
	logger.Info("[DEBUG-ACCOUNT-OPENING] AddAddress: buildCurrencyKey result", "token", req.Token, "chain", req.Chain, "addressKey", addressKey)
	if addressKey == "" {
		logger.Error("[DEBUG-ACCOUNT-OPENING] AddAddress: invalid currency", "token", req.Token, "chain", req.Chain)
		return nil, fmt.Errorf("invalid currency: %s_%s", req.Token, req.Chain)
	}

	// Check if address already exists in user_wallets (for logging purposes)
	existingWallet, err := s.repo.GetUserWalletByUserAndCurrency(ctx, userID, addressKey)
	if err != nil {
		return nil, err
	}
	if existingWallet != nil {
		logger.Info("Address already exists, will fetch fresh address from Core API", "userId", userID, "currency", addressKey)
	}

	// Use wallet's ProductCode or default to X_FINANCE
	productCode := wallet.ProductCode
	if productCode == "" {
		productCode = "X_FINANCE"
	}

	var address string
	var addressType, derivePath *string

	// Check if this is a testnet currency - use local generation instead of Core API
	if isTestnetCurrency(addressKey) {
		logger.Info("[DEBUG-ACCOUNT-OPENING] AddAddress: generating testnet address locally", "addressKey", addressKey, "userId", userID)
		address = generateTestnetAddress(addressKey, userID)
		if address == "" {
			return nil, fmt.Errorf("failed to generate testnet address for %s", addressKey)
		}
		logger.Info("[DEBUG-ACCOUNT-OPENING] AddAddress: testnet address generated", "address", address, "userId", userID)
	} else {
		// Get address from Core API for mainnet currencies
		if s.coreAPIClient == nil {
			return nil, fmt.Errorf("Core API client not initialized")
		}

		// Use DB format for Core API
		coreCurrency := addressKey
		logger.Info("[DEBUG-ACCOUNT-OPENING] AddAddress: calling Core API", "addressKey", addressKey, "coreCurrency", coreCurrency)

		coreResp, err := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
			UserID:      fmt.Sprintf("%d", userID),
			ProductCode: productCode,
			Currency:    coreCurrency,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get address from Core API: %w", err)
		}

		logger.Info("Core API address fetched successfully", "userId", userID, "currency", addressKey)
		address = coreResp.Address
		addressType = coreResp.AddressType
		derivePath = coreResp.DerivePath
	}

	// Create new UserWallet record
	newWallet := &models.UserWallet{
		UserID:    userID,
		WalletID:  wallet.WalletID.String,
		Currency:  addressKey,
		Address:   address,
		Status:    models.UserWalletStatusNormal,
		IsPrimary: false,
	}

	// Set optional fields if available
	if addressType != nil {
		newWallet.AddressType = sql.NullString{String: *addressType, Valid: true}
	}
	if derivePath != nil {
		newWallet.DerivePath = sql.NullString{String: *derivePath, Valid: true}
	}
	if wallet.RequestID != "" {
		newWallet.RequestID = sql.NullString{String: wallet.RequestID, Valid: true}
	}

	// Store in user_wallets table
	result, err := s.repo.AddUserWalletAddress(ctx, newWallet)
	if err != nil {
		return nil, err
	}

	return result, nil
}

// GetAddressIncomeHistory 获取地址链上收款记录
func (s *WalletService) GetAddressIncomeHistory(ctx context.Context, userID int, address string) ([]coreapi.AddressIncomeRecord, error) {
	if s.coreAPIClient == nil {
		return nil, fmt.Errorf("Core API client not initialized")
	}

	return s.coreAPIClient.GetIncomeHistory(ctx, coreapi.GetIncomeHistoryRequest{
		Address: address,
	})
}

// GetWalletAddress 获取钱包地址
// 优先从 Core API 获取，如果失败则从本地数据库获取
func (s *WalletService) GetWalletAddress(ctx context.Context, userID int, req dto.GetWalletAddressRequest) (*dto.WalletAddress, error) {
	logger.Info("[DEBUG-DEPOSIT] GetWalletAddress called", "userId", userID, "productCode", req.ProductCode, "currency", req.Currency)

	// 优先从 Core API 获取 (use full format for Core API)
	if s.coreAPIClient != nil {
		coreCurrency := currency.ToFullFormat(req.Currency)
		logger.Info("[DEBUG-DEPOSIT] Calling Core API GetAddress", "currency", req.Currency, "coreCurrency", coreCurrency)
		addressInfo, err := s.coreAPIClient.GetAddress(ctx, coreapi.GetAddressRequest{
			UserID:      fmt.Sprintf("%d", userID),
			ProductCode: req.ProductCode,
			Currency:    coreCurrency,
		})
		if err == nil && addressInfo != nil {
			logger.Info("[DEBUG-DEPOSIT] Core API response", "address", addressInfo.Address, "addressType", addressInfo.AddressType)
			// Validate that returned address matches requested network
			if isAddressValidForCurrency(addressInfo.Address, req.Currency) {
				return &dto.WalletAddress{
					Address:     addressInfo.Address,
					AddressType: addressInfo.AddressType,
					DerivePath:  addressInfo.DerivePath,
				}, nil
			}
			logger.Warn("[DEBUG-DEPOSIT] Address does not match requested currency, falling back to database", "address", addressInfo.Address, "currency", req.Currency)
		}
		// Core API failure, an empty response, or a network mismatch all fall
		// back to the locally recorded wallet without dereferencing a nil error.
		logger.Info("Core API GetAddress did not provide a usable address, falling back to local database", "error", err)
	}

	// 从本地数据库获取钱包信息作为降级方案
	wallet, err := s.GetWalletInfo(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get wallet address: %v", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("wallet not found")
	}

	// 解析 addresses JSON 并获取对应 currency 的地址
	var address string
	if wallet.Addresses.Valid && wallet.Addresses.String != "" {
		addresses := make(map[string]string)
		if err := json.Unmarshal([]byte(wallet.Addresses.String), &addresses); err != nil {
			logger.Info("Failed to parse addresses JSON", "error", err.Error())
		} else {
			// 优先查找对应 currency 的地址
			address = addresses[req.Currency]
			// 如果找不到，尝试查找任一地址
			if address == "" {
				for _, v := range addresses {
					address = v
					break
				}
			}
		}
	}

	// 如果没有找到地址，尝试使用单一的 address 字段
	if address == "" && wallet.Address.Valid && wallet.Address.String != "" {
		address = wallet.Address.String
	}

	if address == "" {
		return nil, fmt.Errorf("wallet address not found")
	}

	return &dto.WalletAddress{
		Address: address,
	}, nil
}

// Ensure dto types are not optimized away by linker
// This prevents "undefined type" errors in some build environments
var _ = dto.GetWalletAddressRequest{}
var _ = dto.WalletAddress{}

// convertUserWalletToRequest converts a UserWallet to WalletCreationRequest format
// for use in AddAddress flow. This enables looking up wallets from user_wallets
// table when wallet_creation_requests doesn't have a matching record.
func convertUserWalletToRequest(uw *models.UserWallet) *models.WalletCreationRequest {
	addressesJSON, _ := json.Marshal(map[string]string{uw.Currency: uw.Address})
	return &models.WalletCreationRequest{
		UserID:      uw.UserID,
		ProductCode: "X_FINANCE",
		Currency:    uw.Currency,
		Status:      models.WalletCreationStatusSuccess,
		WalletID:    sql.NullString{String: uw.WalletID, Valid: uw.WalletID != ""},
		Address:     sql.NullString{String: uw.Address, Valid: uw.Address != ""},
		Addresses:   sql.NullString{String: string(addressesJSON), Valid: true},
	}
}

// isAddressValidForCurrency checks if an address matches the expected network
func isAddressValidForCurrency(address, currency string) bool {
	if address == "" {
		return false
	}

	// TRC20 addresses start with T
	if currency == "USDT_TRC20" || currency == "TRC20" {
		return len(address) == 34 && address[0] == 'T'
	}

	// ERC20 and BEP20 addresses start with 0x
	if currency == "USDT_ERC20" || currency == "ERC20" ||
		currency == "USDT_BEP20" || currency == "BEP20" {
		return len(address) == 42 && address[:2] == "0x"
	}

	// For unknown currency, just check it's not empty
	return address != ""
}

// isTestnetCurrency checks if the currency is a testnet currency
func isTestnetCurrency(currency string) bool {
	if currency == "" {
		return false
	}
	upper := strings.ToUpper(currency)
	return strings.Contains(upper, "TESTNET") ||
		strings.Contains(upper, "SHASTA") ||
		strings.Contains(upper, "NILE") ||
		strings.Contains(upper, "GOERLI") ||
		strings.Contains(upper, "SEPOLIA") ||
		strings.Contains(upper, "MUMBAI")
}

// generateTestnetAddress generates a deterministic testnet address
// based on userID and currency for testing purposes
func generateTestnetAddress(currency string, userID int) string {
	// Determine network type from currency
	upper := strings.ToUpper(currency)

	// Generate a deterministic suffix based on userID
	suffix := fmt.Sprintf("%08d", userID)

	switch {
	case strings.Contains(upper, "TRC20") || strings.Contains(upper, "TRON"):
		// TRON address: T + 33 alphanumeric characters
		// Format: TTest + userID padded + random alphanumeric to fill 33 chars
		base := fmt.Sprintf("TTest%s", suffix)
		padding := generatePadding(34-len(base), upper)
		return base + padding
	case strings.Contains(upper, "BEP20") || strings.Contains(upper, "BSC"):
		// BSC address: 0x + 40 hex characters
		return fmt.Sprintf("0xTest%s%s", suffix, generateHexPadding(40-8-len("Test")))
	case strings.Contains(upper, "ERC20") || strings.Contains(upper, "ETH"):
		// ETH address: 0x + 40 hex characters
		return fmt.Sprintf("0xTest%s%s", suffix, generateHexPadding(40-8-len("Test")))
	default:
		// Default: use 0x format
		return fmt.Sprintf("0xTest%s%s", suffix, generateHexPadding(40-8-len("Test")))
	}
}

// generatePadding generates alphanumeric padding of specified length
func generatePadding(length int, seed string) string {
	if length <= 0 {
		return ""
	}
	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	seedSum := 0
	for i := 0; i < len(seed) && i < 10; i++ {
		seedSum += int(seed[i])
	}
	for i := 0; i < length; i++ {
		result[i] = chars[(seedSum+i)%len(chars)]
	}
	return string(result)
}

// generateHexPadding generates hex padding of specified length
func generateHexPadding(length int) string {
	if length <= 0 {
		return ""
	}
	chars := "0123456789abcdef"
	result := make([]byte, length)
	for i := 0; i < length; i++ {
		result[i] = chars[i%len(chars)]
	}
	return string(result)
}
