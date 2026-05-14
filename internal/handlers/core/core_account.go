package core

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Status constants for CoreAccount
type CoreAccountStatus string

const (
	StatusCreating   CoreAccountStatus = "CREATING"
	StatusPendingKYC CoreAccountStatus = "PENDING_KYC"
	StatusActive     CoreAccountStatus = "ACTIVE"
	StatusSuspended  CoreAccountStatus = "SUSPENDED"
	StatusClosed     CoreAccountStatus = "CLOSED"
	StatusRejected   CoreAccountStatus = "REJECTED"
)

// KYCStatus constants
type KYCStatus string

const (
	KYCNotSubmitted KYCStatus = "NOT_SUBMITTED"
	KYCPending      KYCStatus = "PENDING"
	KYCInReview     KYCStatus = "IN_REVIEW"
	KYCVerified     KYCStatus = "VERIFIED"
	KYCRejected     KYCStatus = "REJECTED"
)

// AccountType constants
type AccountType string

const (
	TypeIndividual AccountType = "INDIVIDUAL"
	TypeCorporate  AccountType = "CORPORATE"
)

// CoreAccount represents the core account model
type CoreAccount struct {
	AccountID   string            `json:"accountId"`
	ExternalID  string            `json:"externalId"`
	AccountType AccountType       `json:"accountType"`
	Status      CoreAccountStatus `json:"status"`
	Profile     AccountProfile    `json:"profile"`
	KYCStatus   KYCStatus         `json:"kycStatus"`
	KYCLevel    int               `json:"kycLevel"`
	WalletIDs   []string          `json:"walletIds"`
	Metadata    map[string]any    `json:"metadata"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// AccountProfile represents user profile information
type AccountProfile struct {
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	FirstName   string   `json:"firstName"`
	LastName    string   `json:"lastName"`
	DateOfBirth string   `json:"dateOfBirth"`
	Nationality string   `json:"nationality"`
	Address     *Address `json:"address,omitempty"`
}

// Address represents a physical address
type Address struct {
	Street     string `json:"street"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postalCode"`
	Country    string `json:"country"`
}

// CreateAccountRequest represents the request body for account creation
type CreateAccountRequest struct {
	ExternalID  string         `json:"externalId"`
	AccountType AccountType    `json:"accountType"`
	Profile     AccountProfile `json:"profile"`
	Metadata    map[string]any `json:"metadata"`
}

// UpdateStatusRequest represents the request body for status update
type UpdateStatusRequest struct {
	Status CoreAccountStatus `json:"status"`
	Reason string            `json:"reason"`
}

// SubmitKYCRequest represents the request body for KYC submission
type SubmitKYCRequest struct {
	DocumentType       string `json:"documentType"`
	DocumentNumber     string `json:"documentNumber"`
	DocumentFrontImage string `json:"documentFrontImage"`
	DocumentBackImage  string `json:"documentBackImage"`
	SelfieImage        string `json:"selfieImage"`
}

// KYCDocument represents a KYC document
type KYCDocument struct {
	Type        string     `json:"type"`
	Status      string     `json:"status"`
	SubmittedAt time.Time  `json:"submittedAt"`
	VerifiedAt  *time.Time `json:"verifiedAt,omitempty"`
}

// KYCStatusResponse represents the KYC status response
type KYCStatusResponse struct {
	AccountID        string        `json:"accountId"`
	KYCStatus        KYCStatus     `json:"kycStatus"`
	KYCLevel         int           `json:"kycLevel"`
	VerificationDate time.Time     `json:"verificationDate"`
	ExpiresAt        time.Time     `json:"expiresAt"`
	Documents        []KYCDocument `json:"documents"`
}

// Response represents the standard API response
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Meta    Meta        `json:"meta"`
}

// Meta represents response metadata
type Meta struct {
	RequestID string `json:"requestId"`
	Timestamp int64  `json:"timestamp"`
}

// ErrorInfo represents error details
type ErrorInfo struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

// InMemoryStore provides thread-safe in-memory storage for accounts
type InMemoryStore struct {
	mu       sync.RWMutex
	accounts map[string]*CoreAccount
}

// Global store instance
var store = &InMemoryStore{
	accounts: make(map[string]*CoreAccount),
}

// GetAccount retrieves an account by its ID
func (s *InMemoryStore) GetAccount(accountID string) (*CoreAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, exists := s.accounts[accountID]
	return account, exists
}

// GetAccountByExternalID retrieves an account by external ID
func (s *InMemoryStore) GetAccountByExternalID(externalID string) (*CoreAccount, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.accounts {
		if account.ExternalID == externalID {
			return account, true
		}
	}
	return nil, false
}

// CreateAccount stores a new account
func (s *InMemoryStore) CreateAccount(account *CoreAccount) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accounts[account.AccountID] = account
}

// UpdateAccount updates an existing account with a custom function
func (s *InMemoryStore) UpdateAccount(accountID string, updateFn func(*CoreAccount)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account, exists := s.accounts[accountID]; exists {
		updateFn(account)
	}
}

// Handler provides HTTP handlers for core account operations
type Handler struct{}

// NewHandler creates a new Handler instance
func NewHandler() *Handler {
	return &Handler{}
}

// createResponse is a helper to create standardized responses
func createResponse(data interface{}, err *ErrorInfo) Response {
	return Response{
		Success: err == nil,
		Data:    data,
		Error:   err,
		Meta: Meta{
			RequestID: uuid.New().String(),
			Timestamp: time.Now().Unix(),
		},
	}
}

// newError creates an ErrorInfo with the given code and message
func newError(code, message string, details map[string]string) *ErrorInfo {
	return &ErrorInfo{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// CreateAccount handles POST /api/core/accounts/create
func (h *Handler) CreateAccount(c *gin.Context) {
	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, createResponse(nil, newError("INVALID_REQUEST", "Invalid request parameters", map[string]string{"error": err.Error()})))
		return
	}

	// Check for duplicate external ID
	if existing, _ := store.GetAccountByExternalID(req.ExternalID); existing != nil {
		c.JSON(http.StatusBadRequest, createResponse(nil, newError("ACCOUNT_EXISTS", "Account already exists", map[string]string{"externalId": req.ExternalID})))
		return
	}

	// Simulate network latency (can be removed in production)
	time.Sleep(100 * time.Millisecond)

	// Create new account
	now := time.Now()
	accountID := "core_" + uuid.New().String()[:32]
	account := &CoreAccount{
		AccountID:   accountID,
		ExternalID:  req.ExternalID,
		AccountType: req.AccountType,
		Status:      StatusCreating,
		Profile:     req.Profile,
		KYCStatus:   KYCNotSubmitted,
		KYCLevel:    0,
		WalletIDs:   []string{},
		Metadata:    req.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	store.CreateAccount(account)

	// Trigger async KYC workflow
	go func() {
		defer func() {
			if rv := recover(); rv != nil {
				log.Printf("KYC workflow panic recovered: %v", rv)
			}
		}()
		// Move to PENDING_KYC after 2 seconds
		time.Sleep(2 * time.Second)
		store.UpdateAccount(accountID, func(acc *CoreAccount) {
			acc.Status = StatusPendingKYC
			acc.UpdatedAt = time.Now()
		})

		// Approve KYC after additional 5 seconds
		time.Sleep(5 * time.Second)
		store.UpdateAccount(accountID, func(acc *CoreAccount) {
			acc.Status = StatusActive
			acc.KYCStatus = KYCVerified
			acc.KYCLevel = 2
			acc.UpdatedAt = time.Now()
		})
	}()

	c.JSON(http.StatusCreated, createResponse(account, nil))
}

// GetAccount handles GET /api/core/accounts/:accountId
func (h *Handler) GetAccount(c *gin.Context) {
	accountID := c.Param("accountId")
	if accountID == "" {
		c.JSON(http.StatusBadRequest, createResponse(nil, newError("INVALID_REQUEST", "Account ID is required", nil)))
		return
	}

	account, exists := store.GetAccount(accountID)
	if !exists {
		c.JSON(http.StatusNotFound, createResponse(nil, newError("ACCOUNT_NOT_FOUND", "Account not found", map[string]string{"accountId": accountID})))
		return
	}

	c.JSON(http.StatusOK, createResponse(account, nil))
}

// UpdateStatus handles PUT /api/core/accounts/:accountId/status
func (h *Handler) UpdateStatus(c *gin.Context) {
	accountID := c.Param("accountId")

	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, createResponse(nil, newError("INVALID_REQUEST", "Invalid request parameters", nil)))
		return
	}

	_, exists := store.GetAccount(accountID)
	if !exists {
		c.JSON(http.StatusNotFound, createResponse(nil, newError("ACCOUNT_NOT_FOUND", "Account not found", nil)))
		return
	}

	store.UpdateAccount(accountID, func(acc *CoreAccount) {
		acc.Status = req.Status
		acc.UpdatedAt = time.Now()
	})

	c.JSON(http.StatusOK, createResponse(map[string]interface{}{
		"accountId": accountID,
		"status":    req.Status,
		"reason":    req.Reason,
		"updatedAt": time.Now(),
	}, nil))
}

// SubmitKYC handles POST /api/core/accounts/:accountId/kyc/submit
func (h *Handler) SubmitKYC(c *gin.Context) {
	accountID := c.Param("accountId")

	var req SubmitKYCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, createResponse(nil, newError("INVALID_REQUEST", "Invalid request parameters", nil)))
		return
	}

	_, exists := store.GetAccount(accountID)
	if !exists {
		c.JSON(http.StatusNotFound, createResponse(nil, newError("ACCOUNT_NOT_FOUND", "Account not found", nil)))
		return
	}

	store.UpdateAccount(accountID, func(acc *CoreAccount) {
		acc.KYCStatus = KYCPending
		acc.UpdatedAt = time.Now()
	})

	c.JSON(http.StatusOK, createResponse(map[string]interface{}{
		"accountId":       accountID,
		"kycStatus":       "PENDING",
		"documentType":    req.DocumentType,
		"submittedAt":     time.Now(),
		"estimatedReview": "24-48 hours",
	}, nil))
}

// GetKYCStatus handles GET /api/core/accounts/:accountId/kyc/status
func (h *Handler) GetKYCStatus(c *gin.Context) {
	accountID := c.Param("accountId")

	account, exists := store.GetAccount(accountID)
	if !exists {
		c.JSON(http.StatusNotFound, createResponse(nil, newError("ACCOUNT_NOT_FOUND", "Account not found", nil)))
		return
	}

	response := KYCStatusResponse{
		AccountID:        accountID,
		KYCStatus:        account.KYCStatus,
		KYCLevel:         account.KYCLevel,
		VerificationDate: time.Now(),
		ExpiresAt:        time.Now().AddDate(1, 0, 0),
		Documents: []KYCDocument{
			{
				Type:        "PASSPORT",
				Status:      string(account.KYCStatus),
				SubmittedAt: account.CreatedAt,
				VerifiedAt:  func() *time.Time { t := time.Now(); return &t }(),
			},
		},
	}

	c.JSON(http.StatusOK, createResponse(response, nil))
}

// SetupRoutes configures the core account API routes
func SetupRoutes(router *gin.Engine) {
	handler := NewHandler()

	core := router.Group("/api/core")
	{
		accounts := core.Group("/accounts")
		{
			accounts.POST("/create", handler.CreateAccount)
			accounts.GET("/:accountId", handler.GetAccount)
			accounts.PUT("/:accountId/status", handler.UpdateStatus)

			kyc := accounts.Group("/:accountId/kyc")
			{
				kyc.POST("/submit", handler.SubmitKYC)
				kyc.GET("/status", handler.GetKYCStatus)
			}
		}
	}

	router.GET("/api/core/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().Unix(),
		})
	})
}

// GetStore returns the global store instance (for testing)
func GetStore() *InMemoryStore {
	return store
}

// ClearStore clears all accounts (for testing)
func ClearStore() {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.accounts = make(map[string]*CoreAccount)
}

// ExportAccountAsJSON exports an account as formatted JSON (for testing)
func ExportAccountAsJSON(accountID string) (string, bool) {
	account, exists := store.GetAccount(accountID)
	if !exists {
		return "", false
	}
	data, _ := json.MarshalIndent(account, "", "  ")
	return string(data), true
}
