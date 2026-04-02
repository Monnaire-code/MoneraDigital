package models

import (
	"database/sql"
	"time"
)

// Enums
type LendingStatus string

const (
	LendingStatusActive     LendingStatus = "ACTIVE"
	LendingStatusCompleted  LendingStatus = "COMPLETED"
	LendingStatusTerminated LendingStatus = "TERMINATED"
)

type AddressType string

const (
	AddressTypeBTC  AddressType = "BTC"
	AddressTypeETH  AddressType = "ETH"
	AddressTypeUSDC AddressType = "USDC"
	AddressTypeUSDT AddressType = "USDT"
)

type WithdrawalStatus string

const (
	WithdrawalStatusPending    WithdrawalStatus = "PENDING"
	WithdrawalStatusProcessing WithdrawalStatus = "PROCESSING"
	WithdrawalStatusCompleted  WithdrawalStatus = "COMPLETED"
	WithdrawalStatusFailed     WithdrawalStatus = "FAILED"
)

type DepositStatus string

const (
	DepositStatusPending   DepositStatus = "PENDING"
	DepositStatusConfirmed DepositStatus = "CONFIRMED"
	DepositStatusFailed    DepositStatus = "FAILED"
)

type WalletCreationStatus string

const (
	WalletCreationStatusCreating WalletCreationStatus = "CREATING"
	WalletCreationStatusSuccess  WalletCreationStatus = "SUCCESS"
	WalletCreationStatusFailed   WalletCreationStatus = "FAILED"
)

// UserWalletStatus represents the status of a user wallet
type UserWalletStatus string

const (
	UserWalletStatusNormal    UserWalletStatus = "NORMAL"
	UserWalletStatusFrozen    UserWalletStatus = "FROZEN"
	UserWalletStatusCancelled UserWalletStatus = "CANCELLED"
)

// UserStatus represents the status of a user account
type UserStatus string

const (
	UserStatusActive   UserStatus = "ACTIVE"
	UserStatusDisabled UserStatus = "DISABLED"
)

// User model
type User struct {
	ID                   int            `json:"id" db:"id"`
	Email                string         `json:"email" db:"email"`
	Password             string         `json:"-" db:"password"`
	Status               UserStatus     `json:"status" db:"status"`
	TwoFactorSecret      sql.NullString `json:"-" db:"two_factor_secret"`
	TwoFactorEnabled     bool           `json:"twoFactorEnabled" db:"two_factor_enabled"`
	TwoFactorBackupCodes sql.NullString `json:"-" db:"two_factor_backup_codes"`
	CreatedAt            time.Time      `json:"createdAt" db:"created_at"`
	UpdatedAt            time.Time      `json:"updatedAt" db:"updated_at"`
}

// Account model (New)
type Account struct {
	ID            int       `json:"id" db:"id"`
	UserID        int       `json:"user_id" db:"user_id"`
	Type          string    `json:"type" db:"type"` // WEALTH, FUND
	Currency      string    `json:"currency" db:"currency"`
	Balance       string    `json:"balance" db:"balance"`
	FrozenBalance string    `json:"frozen_balance" db:"frozen_balance"`
	Version       int64     `json:"version" db:"version"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// Deposit model
type Deposit struct {
	ID          int            `json:"id" db:"id"`
	UserID      int            `json:"userId" db:"user_id"`
	TxHash      string         `json:"txHash" db:"tx_hash"`
	Amount      string         `json:"amount" db:"amount"`
	Asset       string         `json:"asset" db:"asset"`
	Chain       string         `json:"chain" db:"chain"`
	Status      DepositStatus  `json:"status" db:"status"`
	FromAddress sql.NullString `json:"fromAddress" db:"from_address"`
	ToAddress   sql.NullString `json:"toAddress" db:"to_address"`
	CreatedAt   time.Time      `json:"createdAt" db:"created_at"`
	ConfirmedAt sql.NullTime   `json:"confirmedAt" db:"confirmed_at"`
}

// WalletCreationRequest model
type WalletCreationRequest struct {
	ID           int                  `json:"id" db:"id"`
	RequestID    string               `json:"requestId" db:"request_id"`
	UserID       int                  `json:"userId" db:"user_id"`
	ProductCode  string               `json:"productCode" db:"product_code"`
	Currency     string               `json:"currency" db:"currency"`
	Status       WalletCreationStatus `json:"status" db:"status"`
	WalletID     sql.NullString       `json:"walletId" db:"wallet_id"`
	Address      sql.NullString       `json:"address" db:"address"`
	Addresses    sql.NullString       `json:"addresses" db:"addresses"` // JSON string
	ErrorMessage sql.NullString       `json:"errorMessage" db:"error_message"`
	CreatedAt    time.Time            `json:"createdAt" db:"created_at"`
	UpdatedAt    time.Time            `json:"updatedAt" db:"updated_at"`
}

// UserWallet model - stores individual wallet addresses for users
type UserWallet struct {
	ID          int              `json:"id" db:"id"`
	UserID      int              `json:"userId" db:"user_id"`
	RequestID   sql.NullString   `json:"requestId,omitempty" db:"request_id"` // Reference to wallet_creation_requests (nullable for manual additions)
	WalletID    string           `json:"walletId" db:"wallet_id"`
	Currency    string           `json:"currency" db:"currency"` // e.g., USDT_ERC20, TRON
	Address     string           `json:"address" db:"address"`
	AddressType sql.NullString   `json:"addressType,omitempty" db:"address_type"`
	DerivePath  sql.NullString   `json:"derivePath,omitempty" db:"derive_path"`
	Status      UserWalletStatus `json:"status" db:"status"`
	IsPrimary   bool             `json:"isPrimary" db:"is_primary"`
	CreatedAt   time.Time        `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time        `json:"updatedAt" db:"updated_at"`
}

// LendingPosition model
type LendingPosition struct {
	ID           int           `json:"id" db:"id"`
	UserID       int           `json:"userId" db:"user_id"`
	Asset        string        `json:"asset" db:"asset"`
	Amount       string        `json:"amount" db:"amount"` // Using string for decimal precision
	DurationDays int           `json:"duration_days" db:"duration_days"`
	Apy          string        `json:"apy" db:"apy"`
	Status       LendingStatus `json:"status" db:"status"`
	AccruedYield string        `json:"accrued_yield" db:"accrued_yield"`
	StartDate    time.Time     `json:"start_date" db:"start_date"`
	EndDate      time.Time     `json:"end_date" db:"end_date"`
}

// WithdrawalAddress model (Updated to match withdrawal_address_whitelist)
type WithdrawalAddress struct {
	ID                 int            `json:"id" db:"id"`
	UserID             int            `json:"user_id" db:"user_id"`
	AddressAlias       string         `json:"address_alias" db:"address_alias"`
	ChainType          string         `json:"chain_type" db:"chain_type"`
	WalletAddress      string         `json:"wallet_address" db:"wallet_address"`
	Verified           bool           `json:"verified" db:"verified"`
	VerifiedAt         sql.NullTime   `json:"verified_at" db:"verified_at"`
	VerificationMethod sql.NullString `json:"verification_method" db:"verification_method"`
	IsDeleted          bool           `json:"is_deleted" db:"is_deleted"`
	IsPrimary          bool           `json:"is_primary" db:"is_primary"`
	CreatedAt          time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at" db:"updated_at"`
}

// WithdrawalRequest model (New)
type WithdrawalRequest struct {
	ID           int            `json:"id" db:"id"`
	UserID       int            `json:"user_id" db:"user_id"`
	RequestID    string         `json:"request_id" db:"request_id"`
	Status       string         `json:"status" db:"status"`
	ErrorCode    sql.NullString `json:"error_code" db:"error_code"`
	ErrorMessage sql.NullString `json:"error_message" db:"error_message"`
	CreatedAt    time.Time      `json:"created_at" db:"created_at"`
}

// WithdrawalOrder model (New/Updated Withdrawal)
type WithdrawalOrder struct {
	ID               int            `json:"id" db:"id"`
	UserID           int            `json:"user_id" db:"user_id"`
	Amount           string         `json:"amount" db:"amount"`
	NetworkFee       string         `json:"network_fee" db:"network_fee"`
	PlatformFee      string         `json:"platform_fee" db:"platform_fee"`
	ActualAmount     string         `json:"actual_amount" db:"actual_amount"`
	ChainType        string         `json:"chain_type" db:"chain_type"`
	CoinType         string         `json:"coin_type" db:"coin_type"`
	ToAddress        string         `json:"to_address" db:"to_address"`
	SafeheronOrderID sql.NullString `json:"safeheron_order_id" db:"safeheron_order_id"`
	TransactionHash  sql.NullString `json:"transaction_hash" db:"transaction_hash"`
	Status           string         `json:"status" db:"status"`
	CreatedAt        time.Time      `json:"created_at" db:"created_at"`
	SentAt           sql.NullTime   `json:"sent_at" db:"sent_at"`
	ConfirmedAt      sql.NullTime   `json:"confirmed_at" db:"confirmed_at"`
	CompletedAt      sql.NullTime   `json:"completed_at" db:"completed_at"`
	UpdatedAt        time.Time      `json:"updated_at" db:"updated_at"`
}

// WithdrawalVerification model (New)
type WithdrawalVerification struct {
	ID                 int            `json:"id" db:"id"`
	UserID             int            `json:"user_id" db:"user_id"`
	WithdrawalOrderID  int            `json:"withdrawal_order_id" db:"withdrawal_order_id"`
	VerificationMethod string         `json:"verification_method" db:"verification_method"`
	VerificationCode   sql.NullString `json:"-" db:"verification_code"`
	Attempts           int            `json:"attempts" db:"attempts"`
	MaxAttempts        int            `json:"max_attempts" db:"max_attempts"`
	Verified           bool           `json:"verified" db:"verified"`
	VerifiedAt         sql.NullTime   `json:"verified_at" db:"verified_at"`
	ExpiresAt          time.Time      `json:"expires_at" db:"expires_at"`
	CreatedAt          time.Time      `json:"created_at" db:"created_at"`
}

// WithdrawalFreezeLog model (New)
type WithdrawalFreezeLog struct {
	ID         int          `json:"id" db:"id"`
	UserID     int          `json:"user_id" db:"user_id"`
	OrderID    int          `json:"order_id" db:"order_id"`
	Amount     string       `json:"amount" db:"amount"`
	FrozenAt   time.Time    `json:"frozen_at" db:"frozen_at"`
	ReleasedAt sql.NullTime `json:"released_at" db:"released_at"`
	Reason     string       `json:"reason" db:"reason"`
	CreatedAt  time.Time    `json:"created_at" db:"created_at"`
}

// Request/Response structs for API
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type ApplyLendingRequest struct {
	Asset        string `json:"asset" binding:"required"`
	Amount       string `json:"amount" binding:"required"`
	DurationDays int    `json:"duration_days" binding:"required,min=1"`
}

type AddAddressRequest struct {
	WalletAddress string `json:"wallet_address" binding:"required"`
	ChainType     string `json:"chain_type" binding:"required"`
	AddressAlias  string `json:"address_alias" binding:"required"`
}

type CreateWithdrawalRequest struct {
	AddressID      int    `json:"addressId" binding:"required"`
	Amount         string `json:"amount" binding:"required"`
	Asset          string `json:"asset" binding:"required"`
	TwoFactorToken string `json:"twoFactorToken"`
}

type Verify2FARequest struct {
	UserID int    `json:"userId" binding:"required"`
	Token  string `json:"token" binding:"required,len=6"`
}
