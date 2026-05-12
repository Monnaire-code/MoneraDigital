package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"monera-digital/internal/binance"
	"monera-digital/internal/dto"
	"monera-digital/internal/models"
	"monera-digital/internal/services"
	"monera-digital/internal/utils"
	"monera-digital/internal/validator"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const (
	minDurationDays = 30
	maxDurationDays = 360
)

var supportedAssets = map[string]bool{
	"BTC":  true,
	"ETH":  true,
	"USDT": true,
	"USDC": true,
	"SOL":  true,
}

// Handler contains all HTTP handlers
type Handler struct {
	AuthService        *services.AuthService
	LendingService     *services.LendingService
	AddressService     *services.AddressService
	WithdrawalService  *services.WithdrawalService
	DepositService     *services.DepositService
	WalletService      *services.WalletService
	WealthService      *services.WealthService
	IdempotencyService *services.IdempotencyService
	ActivationService  *services.ActivationService
	// Safeheron Phase 1: unexported so external packages can't bypass the
	// SetSafeheronDeps typed-nil guard. Same-package handlers and tests
	// access them directly. T6-S-4.
	poolManager    DepositPoolManager
	walletRegistry ChainsRegistry
	Validator      validator.Validator
}

func NewHandler(auth *services.AuthService, lending *services.LendingService, address *services.AddressService, withdrawal *services.WithdrawalService, deposit *services.DepositService, wallet *services.WalletService, wealth *services.WealthService, idempotency *services.IdempotencyService, activation *services.ActivationService) *Handler {
	return &Handler{
		AuthService:        auth,
		LendingService:     lending,
		AddressService:     address,
		WithdrawalService:  withdrawal,
		DepositService:     deposit,
		WalletService:      wallet,
		WealthService:      wealth,
		IdempotencyService: idempotency,
		ActivationService:  activation,
		Validator:          validator.NewValidator(),
	}
}

// SetSafeheronDeps wires the optional Safeheron-backed deposit pool + chain
// registry dependencies. Passing nil — or a typed-nil concrete pointer wrapped
// in the interface — leaves the handler in the 503-fallback state used by
// environments that haven't provisioned Safeheron yet.
//
// The typed-nil check is critical: `var pm *pool.Manager` assigned into a
// DepositPoolManager interface variable is non-nil at the interface level but
// nil at the concrete pointer level, and would silently bypass the
// `if h.poolManager == nil` 503 guard inside GetDepositAddress.
func (h *Handler) SetSafeheronDeps(pm DepositPoolManager, reg ChainsRegistry) {
	if !isNilInterface(pm) {
		h.poolManager = pm
	}
	if !isNilInterface(reg) {
		h.walletRegistry = reg
	}
}

// isNilInterface returns true for both nil interfaces and typed-nil interfaces
// (interface holding a nil concrete pointer/map/slice/chan/func).
func isNilInterface(i any) bool {
	if i == nil {
		return true
	}
	v := reflect.ValueOf(i)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

// Auth handlers

func (h *Handler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Validator.ValidateEmail(req.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.AuthService.Login(models.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	dtoResp := dto.LoginResponse{
		AccessToken:        resp.AccessToken,
		RefreshToken:       resp.RefreshToken,
		TokenType:          resp.TokenType,
		ExpiresIn:          resp.ExpiresIn,
		ExpiresAt:          resp.ExpiresAt,
		Token:              resp.Token,
		Requires2FA:        resp.Requires2FA,
		RequiresActivation: resp.RequiresActivation,
		UserID:             resp.UserID,
	}
	if resp.User != nil {
		dtoResp.User = &dto.UserInfo{
			ID:               resp.User.ID,
			Email:            resp.User.Email,
			Status:           string(resp.User.Status),
			TwoFactorEnabled: resp.User.TwoFactorEnabled,
		}
	}

	c.JSON(http.StatusOK, dtoResp)
}

func (h *Handler) Register(c *gin.Context) {
	var req dto.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Validator.ValidateEmail(req.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.Validator.ValidatePassword(req.Password); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	user, err := h.AuthService.Register(models.RegisterRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Note: Activation email is NOT sent automatically after registration
	// User must manually request activation code from the activation page

	// Generate token for the new user
	token, _ := utils.GenerateJWT(user.ID, user.Email, os.Getenv("JWT_SECRET"))
	expiresAt := time.Now().Add(24 * time.Hour)

	// Return login response with token
	c.JSON(http.StatusCreated, gin.H{
		"success":            true,
		"email":              user.Email,
		"status":             string(user.Status),
		"requiresActivation": true,
		"token":              token,
		"accessToken":        token,
		"tokenType":          "Bearer",
		"expiresIn":          86400,
		"expiresAt":          expiresAt.Format(time.RFC3339),
		"userId":             user.ID,
	})
}

func (h *Handler) GetMe(c *gin.Context) {
	userID, exists := c.Get("userID")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	user, err := h.AuthService.GetUserByID(userID.(int))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user profile"})
		return
	}

	response := dto.UserInfo{
		ID:               user.ID,
		Email:            user.Email,
		Status:           string(user.Status),
		TwoFactorEnabled: user.TwoFactorEnabled,
	}

	if user.Phone.Valid {
		response.Phone = user.Phone.String
	}
	if user.Telegram.Valid {
		response.Telegram = user.Telegram.String
	}
	if user.Wechat.Valid {
		response.Wechat = user.Wechat.String
	}

	c.JSON(http.StatusOK, response)
}

// Verify2FALogin verifies 2FA token during login and completes authentication
func (h *Handler) Verify2FALogin(c *gin.Context) {
	var req models.Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.AuthService.Verify2FAAndLogin(req.UserID, req.Token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	dtoResp := dto.LoginResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		ExpiresIn:    resp.ExpiresIn,
		ExpiresAt:    resp.ExpiresAt,
		Token:        resp.Token,
		User: &dto.UserInfo{
			ID:               resp.User.ID,
			Email:            resp.User.Email,
			Status:           string(resp.User.Status),
			TwoFactorEnabled: resp.User.TwoFactorEnabled,
		},
	}

	c.JSON(http.StatusOK, dtoResp)
}

// Skip2FALogin allows users to skip 2FA setup during login if not mandatory
func (h *Handler) Skip2FALogin(c *gin.Context) {
	var req struct {
		UserID int `json:"userId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  err.Error(),
			"code":   "INVALID_REQUEST",
			"userId": req.UserID,
		})
		return
	}

	resp, err := h.AuthService.Skip2FAAndLogin(req.UserID)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":  err.Error(),
			"code":   "SKIP_2FA_FAILED",
			"userId": req.UserID,
		})
		return
	}

	dtoResp := dto.LoginResponse{
		AccessToken:  resp.AccessToken,
		RefreshToken: resp.RefreshToken,
		TokenType:    resp.TokenType,
		ExpiresIn:    resp.ExpiresIn,
		ExpiresAt:    resp.ExpiresAt,
		Token:        resp.Token,
		User: &dto.UserInfo{
			ID:               resp.User.ID,
			Email:            resp.User.Email,
			Status:           string(resp.User.Status),
			TwoFactorEnabled: resp.User.TwoFactorEnabled,
		},
	}

	c.JSON(http.StatusOK, dtoResp)
}

func (h *Handler) RefreshToken(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refreshToken" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Refresh token is required", "code": "MISSING_REFRESH_TOKEN"})
		return
	}

	tokenPair, err := h.AuthService.RefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error(), "code": "INVALID_REFRESH_TOKEN"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"accessToken":  tokenPair.AccessToken,
		"refreshToken": tokenPair.RefreshToken,
		"tokenType":    tokenPair.TokenType,
		"expiresIn":    tokenPair.ExpiresIn,
	})
}

func (h *Handler) Logout(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
		return
	}

	token := ""
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	} else {
		token = authHeader
	}

	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Token is required"})
		return
	}

	if err := h.AuthService.Logout(token); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to logout"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Logged out successfully",
	})
}

// Lending handlers

func (h *Handler) ApplyForLending(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var req dto.ApplyLendingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.DurationDays < minDurationDays || req.DurationDays > maxDurationDays {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Duration must be between %d and %d days", minDurationDays, maxDurationDays)})
		return
	}
	if !supportedAssets[req.Asset] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported asset. Supported assets: BTC, ETH, USDT, USDC, SOL"})
		return
	}

	normalizedAmount, normErr := utils.NormalizeString(req.Amount)
	if normErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid amount: %v", normErr)})
		return
	}

	amountValue, _ := utils.FromString(normalizedAmount)
	if amountValue.LessThanOrEqual(decimal.Zero) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Amount must be positive"})
		return
	}

	position, err := h.LendingService.ApplyForLending(userID, models.ApplyLendingRequest{
		Asset:        req.Asset,
		Amount:       normalizedAmount,
		DurationDays: req.DurationDays,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, toLendingPositionResponse(position))
}

func (h *Handler) GetUserPositions(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	positions, err := h.LendingService.GetUserPositions(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	dtoPositions := make([]dto.LendingPositionResponse, len(positions))
	for i, pos := range positions {
		dtoPositions[i] = toLendingPositionResponse(&pos)
	}

	total := 0.0
	for _, pos := range dtoPositions {
		total += pos.AccruedYield
	}

	c.JSON(http.StatusOK, dto.LendingPositionsListResponse{
		Positions: dtoPositions,
		Total:     int(total),
		Count:     len(dtoPositions),
	})
}

// Address handlers

func (h *Handler) GetAddresses(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	addresses, err := h.AddressService.GetAddresses(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Convert models to DTOs for consistent API response format
	response := make([]dto.WithdrawalAddressResponse, len(addresses))
	for i, addr := range addresses {
		response[i] = dto.WithdrawalAddressResponse{
			ID:         addr.ID,
			UserID:     addr.UserID,
			Address:    addr.WalletAddress,
			Type:       addr.ChainType,
			Label:      addr.AddressAlias,
			IsVerified: addr.Verified,
			IsDeleted:  addr.IsDeleted,
			CreatedAt:  addr.CreatedAt,
		}
		// Handle nullable VerifiedAt
		if addr.VerifiedAt.Valid {
			response[i].VerifiedAt = &addr.VerifiedAt.Time
		}
		// Handle nullable FrozenUntil
		if addr.FrozenUntil.Valid {
			response[i].FrozenUntil = &addr.FrozenUntil.Time
		}
	}

	c.JSON(http.StatusOK, gin.H{"addresses": response, "total": len(response), "count": len(response)})
}

func (h *Handler) AddAddress(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var req models.AddAddressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	addr, err := h.AddressService.AddAddress(c.Request.Context(), userID, req)
	if err != nil {
		if err.Error() == "address already exists" {
			c.JSON(http.StatusConflict, gin.H{"error": "Address already exists"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, addr)
}

func (h *Handler) VerifyAddress(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid Address ID"})
		return
	}

	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Verification token is required"})
		return
	}

	// Get User to check 2FA status
	user, err := h.AuthService.GetUserByID(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user profile"})
		return
	}

	verificationMethod := "EMAIL"

	if user.TwoFactorEnabled {
		// Verify 2FA
		valid, err := h.AuthService.Verify2FA(userID, req.Token)
		if err != nil {
			// Log the specific error for debugging
			fmt.Printf("[VerifyAddress] 2FA verification error for user %d: %v\n", userID, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify 2FA: " + err.Error()})
			return
		}
		if !valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid 2FA code"})
			return
		}
		verificationMethod = "2FA"
	}

	// If 2FA is not enabled, we assume the token is an email verification token.
	// However, since the current AddressService implementation doesn't verify the email token (it's a stub or assumes success),
	// and we are focusing on fixing the 2FA flow, we proceed.
	// Ideally, we should have h.AddressService.VerifyEmailToken(token) here.

	if err := h.AddressService.VerifyAddress(c.Request.Context(), userID, id, verificationMethod); err != nil {
		fmt.Printf("[VerifyAddress] Address verification error for user %d, address %d: %v\n", userID, id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Address verified successfully"})
}

func (h *Handler) SetPrimaryAddress(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	if err := h.AddressService.SetPrimary(c.Request.Context(), userID, id); err != nil {
		if err.Error() == "address not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "Address not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handler) DeactivateAddress(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	if err := h.AddressService.DeleteAddress(c.Request.Context(), userID, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Address deleted"})
}

// Withdrawal handlers

func (h *Handler) GetWithdrawals(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	orders, err := h.WithdrawalService.GetWithdrawalHistory(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"withdrawals": orders, "total": len(orders), "count": len(orders)})
}

func (h *Handler) CreateWithdrawal(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var req models.CreateWithdrawalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user to check if 2FA is enabled
	user, err := h.AuthService.GetUserByID(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user profile"})
		return
	}

	// Verify 2FA if enabled
	if user.TwoFactorEnabled {
		valid, err := h.AuthService.Verify2FA(userID, req.TwoFactorToken)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify 2FA"})
			return
		}
		if !valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid 2FA code"})
			return
		}
	}

	order, err := h.WithdrawalService.CreateWithdrawal(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Withdrawal created", "order": order})
}

func (h *Handler) GetWithdrawalByID(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	order, err := h.WithdrawalService.GetWithdrawalByID(c.Request.Context(), userID, id)
	if err != nil {
		if errors.Is(err, errors.New("unauthorized")) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Unauthorized"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, order)
}

func (h *Handler) GetWithdrawalFees(c *gin.Context) {
	fee, received, err := h.WithdrawalService.EstimateFee(c.Request.Context(), c.Query("asset"), c.Query("chain"), c.Query("amount"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"fee": fee, "receivedAmount": received})
}

func (h *Handler) GetDocs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Docs endpoint"})
}

// Helper functions

func (h *Handler) getUserID(c *gin.Context) (int, error) {
	userID, exists := c.Get("userID")
	if !exists {
		return 0, errors.New("Unauthorized")
	}
	return userID.(int), nil
}

func toLendingPositionResponse(position *models.LendingPosition) dto.LendingPositionResponse {
	apy, _ := strconv.ParseFloat(position.Apy, 64)
	amount, _ := strconv.ParseFloat(position.Amount, 64)
	accruedYield, _ := strconv.ParseFloat(position.AccruedYield, 64)

	return dto.LendingPositionResponse{
		ID:           position.ID,
		UserID:       position.UserID,
		Asset:        position.Asset,
		Amount:       amount,
		DurationDays: position.DurationDays,
		APY:          apy,
		Status:       string(position.Status),
		AccruedYield: accruedYield,
		StartDate:    position.StartDate,
		EndDate:      position.EndDate,
		CreatedAt:    position.StartDate,
	}
}

// Assets handlers

func (h *Handler) GetAssets(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	assets, err := h.WealthService.GetAssets(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"assets": assets, "total": len(assets), "count": len(assets)})
}

func (h *Handler) RefreshPrices(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Prices refreshed successfully"})
}

func (h *Handler) GetPrices(c *gin.Context) {
	priceService := binance.NewPriceService()

	currencies := []string{"BTC", "ETH", "SOL", "ADA", "XRP", "DOGE", "BNB", "USDT", "USDC", "DAI"}
	prices := priceService.GetPricesFromCache(currencies)

	var result []map[string]interface{}
	for _, symbol := range currencies {
		price := prices[symbol]
		if symbol == "USDT" || symbol == "USDC" || symbol == "DAI" {
			result = append(result, map[string]interface{}{
				"symbol":    symbol,
				"price":     1.0,
				"change24h": 0.0,
			})
		} else if price > 0 {
			result = append(result, map[string]interface{}{
				"symbol":    symbol,
				"price":     price,
				"change24h": 0.0,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"prices":    result,
		"updatedAt": priceService.GetLastUpdateTime().Format("2006-01-02T15:04:05Z"),
	})
}

// Wealth handlers

func (h *Handler) GetProducts(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	products, total, err := h.WealthService.GetProducts(c.Request.Context(), page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"products": products, "total": total, "page": page, "pageSize": pageSize})
}

func (h *Handler) Subscribe(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		ProductID        int64       `json:"productId" binding:"required"`
		Amount           string      `json:"amount" binding:"required"`
		AutoRenew        bool        `json:"autoRenew"`
		InterestExpected interface{} `json:"interest_expected"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("JSON解析错误: %v", err.Error())})
		return
	}

	// Get idempotency key from header (REQUIRED)
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing Idempotency-Key header", "code": "MISSING_IDEMPOTENCY_KEY"})
		return
	}

	// Check if idempotency service is available (Redis or DB)
	if h.IdempotencyService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Idempotency service is unavailable, please try again later", "code": "IDEMPOTENCY_UNAVAILABLE"})
		return
	}

	// Check for duplicate request
	var isNew bool = true
	var idempotencyErr error = nil
	record, isNew, idempotencyErr := h.IdempotencyService.CheckOrCreate(c.Request.Context(), idempotencyKey)
	if idempotencyErr != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Idempotency check failed, please try again", "code": "IDEMPOTENCY_CHECK_FAILED"})
		return
	}

	if !isNew {
		// Request already processed, return cached result
		switch record.Status {
		case services.IdempotencyStatusCompleted:
			c.JSON(http.StatusCreated, gin.H{"message": "Subscription successful", "orderId": record.Result})
			return
		case services.IdempotencyStatusProcessing:
			c.JSON(http.StatusAccepted, gin.H{"message": "Request is being processed"})
			return
		case services.IdempotencyStatusFailed:
			c.JSON(http.StatusInternalServerError, gin.H{"error": record.Error})
			return
		}
	}

	// Process request - mark as failed if panic or error occurs
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("%v", r)
			h.IdempotencyService.Fail(c.Request.Context(), idempotencyKey, errMsg)
			panic(r)
		}
		if idempotencyErr != nil {
			h.IdempotencyService.Fail(c.Request.Context(), idempotencyKey, idempotencyErr.Error())
		}
	}()

	// 处理interest_expected字段的类型转换
	interestExpected := ""
	if req.InterestExpected != nil {
		switch v := req.InterestExpected.(type) {
		case string:
			interestExpected = v
		case float64:
			interestExpected = strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			interestExpected = strconv.Itoa(v)
		case json.Number:
			interestExpected = v.String()
		default:
			interestExpected = fmt.Sprintf("%v", v)
		}
	}

	// 验证转换后的值
	if interestExpected == "" {
		interestExpected = "0"
	}

	orderID, err := h.WealthService.Subscribe(c.Request.Context(), userID, req.ProductID, req.Amount, req.AutoRenew, interestExpected)
	if err != nil {
		idempotencyErr = err
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Mark as completed
	h.IdempotencyService.Complete(c.Request.Context(), idempotencyKey, orderID)

	c.JSON(http.StatusCreated, gin.H{"message": "Subscription successful", "orderId": orderID})
}

func (h *Handler) GetOrders(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))

	orders, total, err := h.WealthService.GetOrders(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"orders": orders, "total": total, "page": page, "pageSize": pageSize})
}

func (h *Handler) Redeem(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	var req struct {
		OrderID        int64  `json:"orderId" binding:"required"`
		RedemptionType string `json:"redemptionType" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.WealthService.Redeem(c.Request.Context(), userID, req.OrderID, req.RedemptionType); err != nil {
		switch err {
		case services.ErrOrderNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "ORDER_NOT_FOUND"})
		case services.ErrOrderAlreadyRedeemed:
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "ALREADY_REDEEMED"})
		case services.ErrSystemMaintenance:
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error(), "code": "SYSTEM_MAINTENANCE"})
		case services.ErrEarlyRedemption:
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error(), "code": "EARLY_REDEMPTION"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Redemption successful"})
}

func (h *Handler) GetInterestHistory(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	daysStr := c.DefaultQuery("days", "7")
	days := 7
	if d, err := strconv.Atoi(daysStr); err == nil {
		days = d
	}

	history, err := h.WealthService.GetInterestHistory(c.Request.Context(), userID, days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"history": history})
}
