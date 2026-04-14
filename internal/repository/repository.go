// internal/repository/repository.go
package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"monera-digital/internal/models"
)

// User 用户仓储接口
type User interface {
	GetByEmail(ctx context.Context, email string) (*models.User, error)
	GetByID(ctx context.Context, id int) (*models.User, error)
	Create(ctx context.Context, email, passwordHash string) (*models.User, error)
	Update(ctx context.Context, user *models.User) error
	UpdateStatus(ctx context.Context, userID int, status models.UserStatus) error
	IsDisabled(ctx context.Context, userID int) (bool, error)
	Delete(ctx context.Context, id int) error
}

// Account 账户仓储接口
type Account interface {
	GetByUserIDAndType(ctx context.Context, userID int, accountType string) (*models.Account, error)
	Create(ctx context.Context, account *models.Account) error
	UpdateFrozenBalance(ctx context.Context, userID int, amount string) error  // Add to frozen
	ReleaseFrozenBalance(ctx context.Context, userID int, amount string) error // Remove from frozen
	DeductBalance(ctx context.Context, userID int, amount string) error        // Deduct from balance and frozen
}

// Lending 借贷仓储接口
type Lending interface {
	CreatePosition(ctx context.Context, position *models.LendingPosition) (*models.LendingPosition, error)
	GetPositionsByUserID(ctx context.Context, userID int) ([]*models.LendingPosition, error)
	GetPositionByID(ctx context.Context, id int) (*models.LendingPosition, error)
	UpdatePosition(ctx context.Context, position *models.LendingPosition) error
}

// Address 地址仓储接口
type Address interface {
	CreateAddress(ctx context.Context, address *models.WithdrawalAddress) (*models.WithdrawalAddress, error)
	GetAddressesByUserID(ctx context.Context, userID int) ([]*models.WithdrawalAddress, error)
	GetAddressByID(ctx context.Context, id int) (*models.WithdrawalAddress, error)
	UpdateAddress(ctx context.Context, address *models.WithdrawalAddress) error
	DeleteAddress(ctx context.Context, id int) error
	GetByAddressAndChain(ctx context.Context, address, chain string) (*models.WithdrawalAddress, error)
	SetPrimary(ctx context.Context, userID int, addressID int) error
}

// Withdrawal 提现仓储接口
type Withdrawal interface {
	CreateOrder(ctx context.Context, order *models.WithdrawalOrder) (*models.WithdrawalOrder, error)
	GetOrdersByUserID(ctx context.Context, userID int) ([]*models.WithdrawalOrder, error)
	GetOrderByID(ctx context.Context, id int) (*models.WithdrawalOrder, error)
	UpdateOrder(ctx context.Context, order *models.WithdrawalOrder) error
	CreateRequest(ctx context.Context, request *models.WithdrawalRequest) error
	GetRequestByID(ctx context.Context, requestID string) (*models.WithdrawalRequest, error)
}

// Deposit 充值仓储接口
type Deposit interface {
	Create(ctx context.Context, deposit *models.Deposit) error
	GetByTxHash(ctx context.Context, txHash string) (*models.Deposit, error)
	GetByUserID(ctx context.Context, userID int, limit, offset int) ([]*models.Deposit, int64, error)
	UpdateStatus(ctx context.Context, id int, status string, confirmedAt string) error
}

// Wallet 钱包仓储接口
type Wallet interface {
	CreateRequest(ctx context.Context, req *models.WalletCreationRequest) error
	GetRequestByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error)
	UpdateRequest(ctx context.Context, req *models.WalletCreationRequest) error
	GetActiveWalletByUserID(ctx context.Context, userID int) (*models.WalletCreationRequest, error)
	GetWalletByUserProductCurrency(ctx context.Context, userID int, productCode, currency string) (*models.WalletCreationRequest, error)
	// UserWallet methods - store individual wallet addresses
	CreateUserWallet(ctx context.Context, wallet *models.UserWallet) error
	GetUserWalletsByUserID(ctx context.Context, userID int) ([]*models.UserWallet, error)
	GetUserWalletByCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error)
	GetActiveUserWallet(ctx context.Context, userID int) (*models.UserWallet, error)
	UpdateUserWalletStatus(ctx context.Context, id int, status models.UserWalletStatus) error
	// AddUserWalletAddress adds a new address for the user, checking if it already exists
	AddUserWalletAddress(ctx context.Context, wallet *models.UserWallet) (*models.UserWallet, error)
	// GetUserWalletByUserAndCurrency gets wallet by user and currency
	GetUserWalletByUserAndCurrency(ctx context.Context, userID int, currency string) (*models.UserWallet, error)
}

// Wealth 理财仓储接口
type Wealth interface {
	GetActiveProducts(ctx context.Context) ([]*WealthProductModel, error)
	GetProductByID(ctx context.Context, id int64) (*WealthProductModel, error)
	CreateOrder(ctx context.Context, order *WealthOrderModel) error
	GetOrdersByUserID(ctx context.Context, userID int64) ([]*WealthOrderModel, error)
	GetOrderByID(ctx context.Context, id int64) (*WealthOrderModel, error)
	UpdateOrder(ctx context.Context, order *WealthOrderModel) error
	UpdateProductSoldQuota(ctx context.Context, id int64, amount string) error
	GetActiveOrders(ctx context.Context) ([]*WealthOrderModel, error)
	GetExpiredOrders(ctx context.Context) ([]*WealthOrderModel, error)
	GetPendingOrders(ctx context.Context) ([]*WealthOrderModel, error)
	ActivateOrder(ctx context.Context, orderID int64) error
	UpdateInterestAccrued(ctx context.Context, orderID int64, interestAccrued string) error
	AccrueInterest(ctx context.Context, orderID int64, amount string, date string) error
	SettleOrder(ctx context.Context, orderID int64, interestPaid string) error
	RenewOrder(ctx context.Context, order *WealthOrderModel, product *WealthProductModel, startDate string, endDate string) (*WealthOrderModel, error)
}

// WealthProductModel 理财产品模型
type WealthProductModel struct {
	ID               int64
	Title            string
	Currency         string
	APY              string
	Duration         int
	MinAmount        string
	MaxAmount        string
	TotalQuota       string
	SoldQuota        string
	Status           int
	AutoRenewAllowed bool
	CreatedAt        string
	UpdatedAt        string
}

// WealthOrderModel 理财订单模型
type WealthOrderModel struct {
	ID                 int64
	UserID             int64
	ProductID          int64
	ProductTitle       string
	Currency           string
	Amount             string
	Duration           int64
	PrincipalRedeemed  string
	InterestExpected   string
	InterestPaid       string
	InterestAccrued    string
	StartDate          string
	EndDate            string
	LastInterestDate   string
	AutoRenew          bool
	Status             int
	RenewedFromOrderID *int64
	RenewedToOrderID   *int64
	RedeemedAt         string
	RedemptionAmount   string
	RedemptionType     sql.NullString
	CreatedAt          string
	UpdatedAt          string
}

// AccountV2 账户仓储接口 (详细版本)
type AccountV2 interface {
	GetAccountByUserIDAndCurrency(ctx context.Context, userID int64, currency string) (*AccountModel, error)
	GetAccountsByUserID(ctx context.Context, userID int64) ([]*AccountModel, error)
	FreezeBalance(ctx context.Context, accountID int64, amount string) error
	UnfreezeBalance(ctx context.Context, accountID int64, amount string) error
	DeductBalance(ctx context.Context, accountID int64, amount string) error
	AddBalance(ctx context.Context, accountID int64, amount string) error
}

// AccountModel 账户模型
type AccountModel struct {
	ID            int64
	UserID        int64
	Type          string
	Currency      string
	Balance       string
	FrozenBalance string
	Version       int64
	CreatedAt     string
	UpdatedAt     string
}

// Journal 资金流水仓储接口
type Journal interface {
	CreateJournalRecord(ctx context.Context, record *JournalModel) error
}

// JournalModel 资金流水模型
type JournalModel struct {
	ID              int64
	SerialNo        string
	UserID          int64
	AccountID       int64
	Amount          string
	BalanceSnapshot string
	BizType         int // 业务类型 ID (1-15)
	RefID           *int64
	CreatedAt       string
}

// DailyInterest 每日利息仓储接口
type DailyInterest interface {
	Create(ctx context.Context, record *DailyInterestModel) error
	CreateWithDate(ctx context.Context, record *DailyInterestModel, dateOverride *time.Time) error
	GetByUserID(ctx context.Context, userID int64, days int) ([]DailyInterestModel, error)
	InvalidateByOrderID(ctx context.Context, orderID int64) error
	SumEffectiveByOrderID(ctx context.Context, orderID int64) (string, error)
}

// DailyInterestModel 每日利息模型
type DailyInterestModel struct {
	ID        int64
	UserID    int64
	OrderID   int64
	Currency  string
	Amount    string
	Effective bool
	CreatedAt string
	UpdatedAt string
}

// Repository 仓储容器
type Repository struct {
	User          User
	Account       Account   // Legacy account interface
	AccountV2     AccountV2 // New detailed account interface
	Lending       Lending
	Address       Address
	Withdrawal    Withdrawal
	Deposit       Deposit
	Wallet        Wallet
	Wealth        Wealth
	Journal       Journal
	DailyInterest DailyInterest
}

// Common errors
var (
	ErrNotFound            = errors.New("record not found")
	ErrAlreadyExists       = errors.New("record already exists")
	ErrInvalidInput        = errors.New("invalid input")
	ErrInsufficientBalance = errors.New("insufficient balance")
)
