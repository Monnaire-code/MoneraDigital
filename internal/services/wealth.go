package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"monera-digital/internal/binance"
	"monera-digital/internal/repository"
	"monera-digital/internal/utils"
)

var (
	ErrInsufficientBalance   = errors.New("insufficient balance")
	ErrProductNotFound       = errors.New("product not found")
	ErrOrderNotFound         = errors.New("order not found")
	ErrProductNotAvailable   = errors.New("product not available")
	ErrAmountBelowMin        = errors.New("amount below minimum")
	ErrAmountAboveMax        = errors.New("amount above maximum")
	ErrQuotaExceeded         = errors.New("quota exceeded")
	ErrOrderAlreadyRedeemed  = errors.New("order already redeemed")
	ErrInvalidRedemptionType = errors.New("invalid redemption type")
	ErrPriceFetchFailed      = errors.New("failed to fetch price")
	ErrJournalCreateFailed   = errors.New("failed to create journal record")
	ErrDuplicateRequest      = errors.New("duplicate request, please try again later")
	ErrEarlyRedemption       = errors.New("early redemption not allowed")
	ErrSystemMaintenance     = errors.New("system maintenance in progress, please retry after UTC 00:01")
)

type WealthService struct {
	repo        repository.Wealth
	accountRepo repository.AccountV2
	journalRepo repository.Journal
	lockMap     map[string]bool
	mu          map[string]*sync.Mutex
}

func NewWealthService(wealthRepo repository.Wealth, accountRepo repository.AccountV2, journalRepo repository.Journal) *WealthService {
	return &WealthService{
		repo:        wealthRepo,
		accountRepo: accountRepo,
		journalRepo: journalRepo,
		lockMap:     make(map[string]bool),
		mu:          make(map[string]*sync.Mutex),
	}
}

// getLock returns a mutex for the given key
func (s *WealthService) getLock(key string) *sync.Mutex {
	if s.mu[key] == nil {
		s.mu[key] = &sync.Mutex{}
	}
	return s.mu[key]
}

// generateIdempotencyKey generates a unique key for idempotency check
func (s *WealthService) generateIdempotencyKey(userID int, productID int64, amount string) string {
	return fmt.Sprintf("subscribe:%d:%d:%s", userID, productID, amount)
}

// isDuplicateCheck checks if this is a duplicate request
func (s *WealthService) isDuplicateCheck(key string) bool {
	if s.lockMap[key] {
		return true
	}
	s.lockMap[key] = true
	return false
}

// clearLock clears the lock for a key
func (s *WealthService) clearLock(key string) {
	delete(s.lockMap, key)
}

type Asset struct {
	Currency      string  `json:"currency"`
	Total         string  `json:"total"`
	Available     string  `json:"available"`
	FrozenBalance string  `json:"frozenBalance"`
	UsdValue      float64 `json:"usdValue"`
}

func (s *WealthService) GetAssets(ctx context.Context, userID int) ([]*Asset, error) {
	accounts, err := s.accountRepo.GetAccountsByUserID(ctx, int64(userID))
	if err != nil {
		return nil, err
	}

	var currencies []string
	for _, a := range accounts {
		if a.Currency != "USDT" && a.Currency != "USDC" && a.Currency != "DAI" {
			currencies = append(currencies, a.Currency)
		}
	}
	prices := binance.NewPriceService().GetPricesFromCache(currencies)

	var result []*Asset
	for _, a := range accounts {
		available := subtractStrings(a.Balance, a.FrozenBalance)
		availableFloat, _ := strconv.ParseFloat(available, 64)
		usdValue := availableFloat

		if a.Currency == "USDT" || a.Currency == "USDC" || a.Currency == "DAI" {
			usdValue = availableFloat
		} else if price, ok := prices[a.Currency]; ok {
			usdValue = availableFloat * price
		}

		result = append(result, &Asset{
			Currency:      a.Currency,
			Total:         formatTo7Decimal(a.Balance),
			Available:     available,
			FrozenBalance: formatTo7Decimal(a.FrozenBalance),
			UsdValue:      usdValue,
		})
	}
	return result, nil
}

func subtractStrings(a, b string) string {
	aFloat, errA := strconv.ParseFloat(a, 64)
	bFloat, errB := strconv.ParseFloat(b, 64)

	if errA != nil {
		return a
	}
	if errB != nil {
		return "0"
	}

	diff := aFloat - bFloat
	if diff < 0 {
		diff = 0
	}

	return strconv.FormatFloat(diff, 'f', 7, 64)
}

// formatTo7Decimal formats a string number to 7 decimal places
func formatTo7Decimal(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return strconv.FormatFloat(f, 'f', 7, 64)
}

type Product struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	Currency         string  `json:"currency"`
	APY              float64 `json:"apy"`
	Duration         int     `json:"duration"`
	MinAmount        string  `json:"minAmount"`
	MaxAmount        string  `json:"maxAmount"`
	RemainingQuota   string  `json:"remainingQuota"`
	AutoRenewAllowed bool    `json:"autoRenewAllowed"`
}

type Order struct {
	ID               int64  `json:"id"`
	ProductTitle     string `json:"productTitle"`
	Currency         string `json:"currency"`
	Amount           string `json:"amount"`
	InterestExpected string `json:"interestExpected"`
	InterestPaid     string `json:"interestPaid"`
	InterestAccrued  string `json:"interestAccrued"`
	StartDate        string `json:"startDate"`
	EndDate          string `json:"endDate"`
	Duration         int64  `json:"duration"`
	AutoRenew        bool   `json:"autoRenew"`
	Status           int    `json:"status"`
	RedemptionAmount string `json:"redemptionAmount,omitempty"`
	CreatedAt        string `json:"createdAt"`
}

func (s *WealthService) GetProducts(ctx context.Context, page, pageSize int) ([]*Product, int64, error) {
	products, err := s.repo.GetActiveProducts(ctx)
	if err != nil {
		return nil, 0, err
	}

	total := int64(len(products))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if start >= len(products) {
		return []*Product{}, total, nil
	}
	if end > len(products) {
		end = len(products)
	}

	var result []*Product
	for _, p := range products[start:end] {
		apy, _ := strconv.ParseFloat(p.APY, 64)
		result = append(result, &Product{
			ID:               p.ID,
			Title:            p.Title,
			Currency:         p.Currency,
			APY:              apy,
			Duration:         p.Duration,
			MinAmount:        p.MinAmount,
			MaxAmount:        p.MaxAmount,
			RemainingQuota:   p.TotalQuota,
			AutoRenewAllowed: p.AutoRenewAllowed,
		})
	}
	return result, total, nil
}

func (s *WealthService) Subscribe(ctx context.Context, userID int, productID int64, amount string, autoRenew bool, interestExpected string) (string, error) {
	idempotencyKey := s.generateIdempotencyKey(userID, productID, amount)
	mu := s.getLock(idempotencyKey)

	mu.Lock()
	defer func() {
		mu.Unlock()
		s.clearLock(idempotencyKey)
	}()

	if s.isDuplicateCheck(idempotencyKey) {
		return "", ErrDuplicateRequest
	}

	product, err := s.repo.GetProductByID(ctx, productID)
	if err != nil {
		return "", ErrProductNotFound
	}

	if product.Status != 1 {
		return "", ErrProductNotFound
	}

	// Validate amount using decimal utils
	if !utils.IsValidAmount(amount) {
		return "", ErrAmountBelowMin
	}

	// Check minimum amount
	minAmountOK, _ := utils.GTE(amount, product.MinAmount)
	if !minAmountOK {
		return "", ErrAmountBelowMin
	}

	// Check maximum amount
	maxAmountOK, _ := utils.LTE(amount, product.MaxAmount)
	if !maxAmountOK {
		return "", ErrAmountAboveMax
	}

	// Check quota
	soldQuota, _ := utils.Add(product.SoldQuota, amount)
	quotaOK, _ := utils.GTE(product.TotalQuota, soldQuota)
	if !quotaOK {
		return "", ErrQuotaExceeded
	}

	account, err := s.accountRepo.GetAccountByUserIDAndCurrency(ctx, int64(userID), product.Currency)
	if err != nil {
		return "", ErrInsufficientBalance
	}

	// Check balance using decimal utils
	availableBalance, _ := utils.Sub(account.Balance, account.FrozenBalance)
	balanceOK, _ := utils.GTE(availableBalance, amount)
	if !balanceOK {
		return "", ErrInsufficientBalance
	}

	err = s.accountRepo.FreezeBalance(ctx, account.ID, amount)
	if err != nil {
		return "", err
	}

	now := time.Now().UTC()

	// 记录冻结流水
	balanceAfterFreeze, _ := utils.Sub(account.Balance, amount)
	freezeJournal := &repository.JournalModel{
		SerialNo:        fmt.Sprintf("FREEZE-%s", now.Format("20060102150405")),
		UserID:          int64(userID),
		AccountID:       account.ID,
		Amount:          "-" + amount,
		BalanceSnapshot: balanceAfterFreeze,
		BizType:         1, // WEALTH_SUBSCRIBE (冻结)
		RefID:           nil,
		CreatedAt:       now.Format(time.RFC3339),
	}
	err = s.journalRepo.CreateJournalRecord(ctx, freezeJournal)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create freeze journal record: %v\n", err)
	}
	today := now.Format("2006-01-02")
	todayDate, _ := time.Parse("2006-01-02", today)
	startDate := todayDate.AddDate(0, 0, 1).Format("2006-01-02")
	endDate := todayDate.AddDate(0, 0, 1+product.Duration).Format("2006-01-02")

	// 使用前端计算的利息，如果前端没有提供则使用后端计算作为后备
	var finalInterestExpected string
	if interestExpected != "" {
		finalInterestExpected = interestExpected
		fmt.Printf("[INFO] Using frontend calculated interest: %s\n", interestExpected)
	} else {
		// 后备计算：使用 decimal 工具计算利息
		calcInterest, err := utils.CalculateInterest(amount, product.APY, product.Duration)
		if err != nil {
			return "", err
		}
		finalInterestExpected = calcInterest
		fmt.Printf("[INFO] Using backend calculated interest: %s\n", finalInterestExpected)
	}

	order := &repository.WealthOrderModel{
		UserID:            int64(userID),
		ProductID:         productID,
		ProductTitle:      product.Title,
		Currency:          product.Currency,
		Amount:            amount,
		AutoRenew:         autoRenew,
		Status:            0, // 0 = 待计息, 由定时器在 start_date 更新为 1 = 计息中
		StartDate:         startDate,
		EndDate:           endDate,
		PrincipalRedeemed: "0",
		InterestExpected:  finalInterestExpected,
		InterestPaid:      "0",
		InterestAccrued:   "0",
		LastInterestDate:  "",
		CreatedAt:         now.Format(time.RFC3339),
		UpdatedAt:         now.Format(time.RFC3339),
	}

	err = s.repo.CreateOrder(ctx, order)
	if err != nil {
		s.accountRepo.UnfreezeBalance(ctx, account.ID, amount)
		return "", err
	}

	err = s.repo.UpdateProductSoldQuota(ctx, productID, amount)
	if err != nil {
		fmt.Printf("[WARNING] Failed to update product sold quota: %v\n", err)
	}

	serialNo := fmt.Sprintf("SUBSCRIBE-%s-%d", now.Format("20060102150405"), order.ID)
	journalRecord := &repository.JournalModel{
		SerialNo:        serialNo,
		UserID:          int64(userID),
		AccountID:       account.ID,
		Amount:          "-" + amount,
		BalanceSnapshot: balanceAfterFreeze,
		BizType:         1, // WEALTH_SUBSCRIBE
		RefID:           &order.ID,
		CreatedAt:       now.Format(time.RFC3339),
	}

	err = s.journalRepo.CreateJournalRecord(ctx, journalRecord)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create journal record: %v\n", err)
		return "", ErrJournalCreateFailed
	}

	return strconv.FormatInt(order.ID, 10), nil
}

func (s *WealthService) GetOrders(ctx context.Context, userID int, page, pageSize int) ([]*Order, int64, error) {
	orders, err := s.repo.GetOrdersByUserID(ctx, int64(userID))
	if err != nil {
		return nil, 0, err
	}

	total := int64(len(orders))

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if start >= len(orders) {
		return []*Order{}, total, nil
	}
	if end > len(orders) {
		end = len(orders)
	}

	var result []*Order
	for _, o := range orders[start:end] {
		result = append(result, &Order{
			ID:               o.ID,
			ProductTitle:     o.ProductTitle,
			Currency:         o.Currency,
			Amount:           o.Amount,
			InterestExpected: o.InterestExpected,
			InterestPaid:     o.InterestPaid,
			InterestAccrued:  o.InterestAccrued,
			StartDate:        o.StartDate,
			EndDate:          o.EndDate,
			Duration:         o.Duration,
			AutoRenew:        o.AutoRenew,
			Status:           o.Status,
			CreatedAt:        o.CreatedAt,
		})
	}
	return result, total, nil
}

func (s *WealthService) Redeem(ctx context.Context, userID int, orderID int64, redemptionType string) error {
	fmt.Printf("[DEBUG] Redeem - userID: %d, orderID: %d, redemptionType: %s\n", userID, orderID, redemptionType)
	order, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		fmt.Printf("[DEBUG] Redeem - GetOrderByID error: %v\n", err)
		return ErrOrderNotFound
	}
	fmt.Printf("[DEBUG] Redeem - order found: ID=%d, UserID=%d, Status=%d, Amount=%s\n", order.ID, order.UserID, order.Status, order.Amount)

	if order.UserID != int64(userID) {
		return ErrOrderNotFound
	}

	if order.Status == 3 {
		return ErrOrderAlreadyRedeemed
	}

	now := time.Now().UTC()

	// 检查是否在系统维护时间 (UTC 00:00 - 00:01)
	hour, minute, _ := now.Clock()
	if hour == 0 && minute < 1 {
		fmt.Printf("[DEBUG] Redemption blocked - system maintenance time (UTC 00:00-00:01)\n")
		return ErrSystemMaintenance
	}

	// 统一处理日期格式：只取前10位 (YYYY-MM-DD)
	endDateStr := order.EndDate
	if len(endDateStr) >= 10 {
		endDateStr = endDateStr[:10]
	}
	endDate, parseErr := time.Parse("2006-01-02", endDateStr)
	if parseErr != nil {
		fmt.Printf("[ERROR] Order %d failed to parse endDate: raw=%s, parsed=%s, error=%v\n",
			order.ID, order.EndDate, endDateStr, parseErr)
		return fmt.Errorf("invalid end date format: %s", order.EndDate)
	}

	endDateOnly := time.Date(endDate.Year(), endDate.Month(), endDate.Day(), 0, 0, 0, 0, time.UTC)
	nowDateOnly := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	isExpired := nowDateOnly.After(endDateOnly)

	fmt.Printf("[DEBUG] Order %d redemption check: now=%s, endDate=%s, isExpired=%v\n",
		order.ID, nowDateOnly.Format("2006-01-02"), endDateOnly.Format("2006-01-02"), isExpired)

	if isExpired {
		fmt.Printf("[DEBUG] Order %d is expired - full redemption with interest\n", order.ID)
		order.Status = 3
	} else {
		fmt.Printf("[DEBUG] Order %d is not expired yet - early redemption, principal only\n", order.ID)
		order.Status = 4
	}

	account, err := s.accountRepo.GetAccountByUserIDAndCurrency(ctx, int64(userID), order.Currency)
	if err != nil {
		return err
	}

	err = s.accountRepo.UnfreezeBalance(ctx, account.ID, order.Amount)
	if err != nil {
		return err
	}

	// 记录解冻流水
	balanceAfterUnfreeze, _ := utils.Add(account.Balance, order.Amount)
	unfreezeJournal := &repository.JournalModel{
		SerialNo:        fmt.Sprintf("UNFREEZE-%s-%d", now.Format("20060102150405"), order.ID),
		UserID:          int64(userID),
		AccountID:       account.ID,
		Amount:          order.Amount,
		BalanceSnapshot: balanceAfterUnfreeze,
		BizType:         2, // WEALTH_REDEEM (解冻)
		RefID:           &order.ID,
		CreatedAt:       now.Format(time.RFC3339),
	}
	err = s.journalRepo.CreateJournalRecord(ctx, unfreezeJournal)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create unfreeze journal record: %v\n", err)
	}

	order.RedemptionAmount = order.Amount
	order.RedeemedAt = now.Format(time.RFC3339)

	if isExpired {
		hasInterest, _ := utils.GT(order.InterestAccrued, "0")
		if hasInterest {
			err = s.accountRepo.AddBalance(ctx, account.ID, order.InterestAccrued)
			if err != nil {
				return err
			}

			// 重新获取账户余额以获取准确的 BalanceSnapshot
			account, err = s.accountRepo.GetAccountByUserIDAndCurrency(ctx, int64(userID), order.Currency)
			if err != nil {
				return err
			}

			interestJournalRecord := &repository.JournalModel{
				SerialNo:        fmt.Sprintf("REDEEM-INTEREST-%s-%d", now.Format("20060102150405"), order.ID),
				UserID:          int64(userID),
				AccountID:       account.ID,
				Amount:          order.InterestAccrued,
				BalanceSnapshot: account.Balance,
				BizType:         3,
				RefID:           &order.ID,
				CreatedAt:       now.Format(time.RFC3339),
			}
			err = s.journalRepo.CreateJournalRecord(ctx, interestJournalRecord)
			if err != nil {
				fmt.Printf("[ERROR] Failed to create interest journal record: %v\n", err)
			}

			order.InterestPaid = order.InterestAccrued
			interestPaid := order.InterestAccrued
			order.InterestAccrued = "0"
			fmt.Printf("[DEBUG] Paid interest %s %s for order %d\n", interestPaid, order.Currency, order.ID)
		}
	} else {
		order.InterestAccrued = "0"
		fmt.Printf("[DEBUG] Early redemption - cleared accrued interest for order %d\n", order.ID)
	}

	// 计算本金入账后的最终余额
	newBalance, _ := utils.Add(account.Balance, order.Amount)

	principalJournal := &repository.JournalModel{
		SerialNo:        fmt.Sprintf("REDEEM-PRINCIPAL-%s-%d", now.Format("20060102150405"), order.ID),
		UserID:          int64(userID),
		AccountID:       account.ID,
		Amount:          order.Amount,
		BalanceSnapshot: newBalance,
		BizType:         2,
		RefID:           &order.ID,
		CreatedAt:       now.Format(time.RFC3339),
	}
	err = s.journalRepo.CreateJournalRecord(ctx, principalJournal)
	if err != nil {
		fmt.Printf("[ERROR] Failed to create principal journal record: %v\n", err)
	}

	return s.repo.UpdateOrder(ctx, order)
}
