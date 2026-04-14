package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"monera-digital/internal/binance"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository"
	"monera-digital/internal/utils"
)

type InterestScheduler struct {
	repo              repository.Wealth
	accountRepo       repository.AccountV2
	journalRepo       repository.Journal
	dailyInterestRepo repository.DailyInterest
	priceService      *binance.PriceService
	metrics           *SchedulerMetrics
}

func NewInterestScheduler(wealthRepo repository.Wealth, accountRepo repository.AccountV2, journalRepo repository.Journal, dailyInterestRepo repository.DailyInterest) *InterestScheduler {
	return &InterestScheduler{
		repo:              wealthRepo,
		accountRepo:       accountRepo,
		journalRepo:       journalRepo,
		dailyInterestRepo: dailyInterestRepo,
		priceService:      binance.NewPriceService(),
		metrics:           NewSchedulerMetrics(),
	}
}

func (s *InterestScheduler) Start() {
	// 使用 UTC 时区
	loc := time.UTC
	timeZoneName := "UTC"

	// 临时改为10分钟后执行（用于测试）
	testDuration := 10 * time.Minute
	nextRun := time.Now().In(loc).Add(testDuration)

	logger.Info("[InterestScheduler] First run scheduled (TEST MODE - 10 min)",
		"scheduled_time", nextRun.Format("2006-01-02 15:04:05"),
		"delay_seconds", testDuration.Seconds(),
		"timezone", timeZoneName)

	time.Sleep(testDuration)

	logger.Info("[InterestScheduler] Started - running daily at 00:00:05 UTC")

	for {
		ctx := context.Background()
		now := time.Now().In(loc)
		logger.Info("[InterestScheduler] Execution started", "timestamp", now.Format("2006-01-02 15:04:05"))

		// Step 0: Activate pending orders (status 0 -> 1)
		activatedCount, activateErr := s.ActivatePendingOrders(ctx)

		// Step 1: Calculate daily interest
		ordersProcessed, interestAccrued, err := s.CalculateDailyInterest(ctx, nil)

		// Step 2: Settle expired orders
		settledCount, settleErr := s.SettleExpiredOrders(ctx)

		success := err == nil && settleErr == nil && activateErr == nil
		errorMsg := ""
		if err != nil {
			errorMsg = err.Error()
		}
		if settleErr != nil {
			if errorMsg != "" {
				errorMsg += "; "
			}
			errorMsg += fmt.Sprintf("settle error: %v", settleErr)
		}
		if activateErr != nil {
			if errorMsg != "" {
				errorMsg += "; "
			}
			errorMsg += fmt.Sprintf("activate error: %v", activateErr)
		}

		totalInterestFloat, _ := strconv.ParseFloat(interestAccrued, 64)
		s.metrics.RecordInterestRun(success, ordersProcessed, totalInterestFloat, errorMsg)

		if !success {
			logger.Error("[InterestScheduler] Execution failed", "error", errorMsg)
		} else {
			logger.Info("[InterestScheduler] Execution completed",
				"orders_activated", activatedCount,
				"orders_processed", ordersProcessed,
				"interest_accrued", interestAccrued,
				"orders_settled", settledCount)
		}

		// Calculate wait time until next run at UTC 00:00:05
		nextRun := time.Now().In(loc)
		nextRun = time.Date(nextRun.Year(), nextRun.Month(), nextRun.Day(), 0, 0, 5, 0, loc)
		nextRun = nextRun.AddDate(0, 0, 1)
		waitDuration := nextRun.Sub(time.Now().In(loc))

		logger.Debug("[InterestScheduler] Waiting until next run", "next_run", nextRun.Format("2006-01-02 15:04:05"))
		time.Sleep(waitDuration)
	}
}

func (s *InterestScheduler) CalculateDailyInterest(ctx context.Context, dateOverride *time.Time) (int, string, error) {
	logger.Info("[InterestScheduler] Calculating daily interest...")

	today := time.Now().UTC()
	if dateOverride != nil {
		today = dateOverride.UTC()
	}

	orders, err := s.repo.GetActiveOrders(ctx)
	if err != nil {
		return 0, "0", fmt.Errorf("failed to get active orders: %v", err)
	}

	logger.Info("[InterestScheduler] Found active orders", "count", len(orders))

	ordersProcessed := 0
	totalInterestAccrued := "0"

	for _, order := range orders {
		startDate, err := time.Parse("2006-01-02", order.StartDate[:10])
		if err != nil {
			logger.Error("[InterestScheduler] Failed to parse start date",
				"order_id", order.ID, "start_date", order.StartDate, "error", err.Error())
			continue
		}

		endDate, err := time.Parse("2006-01-02", order.EndDate[:10])
		if err != nil {
			logger.Error("[InterestScheduler] Failed to parse end date",
				"order_id", order.ID, "end_date", order.EndDate, "error", err.Error())
			continue
		}

		daysSinceStart := int(today.Sub(startDate).Hours() / 24)
		if daysSinceStart < 1 {
			logger.Debug("[InterestScheduler] Order skipped - started today or not yet",
				"order_id", order.ID, "start_date", order.StartDate, "days_since_start", daysSinceStart)
			continue
		}

		if daysSinceStart >= int(order.Duration) {
			logger.Debug("[InterestScheduler] Order skipped - duration exceeded or expired",
				"order_id", order.ID, "start_date", order.StartDate, "duration", order.Duration, "days_since_start", daysSinceStart)
			continue
		}

		if today.After(endDate) || today.Equal(endDate) {
			logger.Debug("[InterestScheduler] Order skipped - already expired",
				"order_id", order.ID, "end_date", order.EndDate)
			continue
		}

		product, err := s.repo.GetProductByID(ctx, order.ProductID)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to get product",
				"order_id", order.ID, "error", err.Error())
			continue
		}

		interestAccrued, err := utils.CalculateInterest(order.Amount, product.APY, daysSinceStart)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to calculate interest",
				"order_id", order.ID, "error", err.Error())
			continue
		}

		err = s.repo.UpdateInterestAccrued(ctx, order.ID, interestAccrued)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to update interest accrued",
				"order_id", order.ID, "error", err.Error())
			continue
		}

		dailyInterestAmount, _ := utils.Sub(interestAccrued, order.InterestAccrued)
		isPositive, _ := utils.GT(dailyInterestAmount, "0")
		if isPositive {
			dailyInterestFloat, _ := strconv.ParseFloat(dailyInterestAmount, 64)
			usdValue := s.priceService.GetUSDValueFromCache(dailyInterestFloat, order.Currency)
			usdAmount := strconv.FormatFloat(usdValue, 'f', 10, 64)

			dailyInterest := &repository.DailyInterestModel{
				UserID:    order.UserID,
				OrderID:   order.ID,
				Currency:  "USD",
				Amount:    usdAmount,
				Effective: true,
				CreatedAt: today.Format(time.RFC3339),
			}
			if err := s.dailyInterestRepo.CreateWithDate(ctx, dailyInterest, dateOverride); err != nil {
				logger.Error("[InterestScheduler] Failed to create daily interest record",
					"order_id", order.ID, "amount_usd", usdAmount, "error", err.Error())
			} else {
				logger.Info("[InterestScheduler] Daily interest recorded",
					"order_id", order.ID,
					"amount_usd", usdAmount,
					"currency", order.Currency,
					"record_id", dailyInterest.ID)
			}
		}

		ordersProcessed++
		totalInterestAccrued, _ = utils.Add(totalInterestAccrued, interestAccrued)

		logger.Info("[InterestScheduler] Interest accrued",
			"order_id", order.ID,
			"interest_accrued", interestAccrued,
			"days_subscribed", daysSinceStart,
			"currency", order.Currency)
	}

	logger.Info("[InterestScheduler] Daily interest calculation completed",
		"orders_processed", ordersProcessed,
		"total_interest", totalInterestAccrued)

	return ordersProcessed, totalInterestAccrued, nil
}

// SettleOrder Settle a single order
func (s *InterestScheduler) SettleOrder(ctx context.Context, orderID int64) error {
	logger.Info("[InterestScheduler] Settling order", "order_id", orderID)

	order, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order: %v", err)
	}

	if order.Status != 1 {
		return fmt.Errorf("order status is not active: %d", order.Status)
	}

	account, err := s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	now := time.Now()

	// Step 1: Unfreeze principal
	err = s.accountRepo.UnfreezeBalance(ctx, account.ID, order.Amount)
	if err != nil {
		return fmt.Errorf("failed to unfreeze balance: %v", err)
	}

	// 重新获取账户以获取准确的可用余额快照
	account, err = s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// 计算解冻后的可用余额: balance - frozen_balance
	availableAfterUnfreeze, _ := utils.Sub(account.Balance, account.FrozenBalance)
	unfreezeSnapshot, _ := utils.NormalizeString(availableAfterUnfreeze)
	unfreezeJournal := &repository.JournalModel{
		SerialNo:        fmt.Sprintf("UNFREEZE-%s-%d", now.Format("20060102150405"), order.ID),
		UserID:          order.UserID,
		AccountID:       account.ID,
		Amount:          order.Amount,
		BalanceSnapshot: unfreezeSnapshot,
		BizType:         2, // WEALTH_REDEEM (解冻)
		RefID:           &order.ID,
		CreatedAt:       now.Format(time.RFC3339),
	}
	err = s.journalRepo.CreateJournalRecord(ctx, unfreezeJournal)
	if err != nil {
		logger.Error("[InterestScheduler] Failed to create unfreeze journal record",
			"order_id", orderID, "error", err.Error())
	}

	// Step 2: Pay interest if accrued
	isInterestPositive, _ := utils.IsPositive(order.InterestAccrued)
	if isInterestPositive {
		err = s.accountRepo.AddBalance(ctx, account.ID, order.InterestAccrued)
		if err != nil {
			return fmt.Errorf("failed to add interest to balance: %v", err)
		}

		// 重新获取账户余额以获取准确的可用余额快照
		account, err = s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
		if err != nil {
			return fmt.Errorf("failed to get account: %v", err)
		}

		// 计算利息入账后的可用余额: balance - frozen_balance
		availableAfterInterest, _ := utils.Sub(account.Balance, account.FrozenBalance)
		interestSnapshot, _ := utils.NormalizeString(availableAfterInterest)
		interestJournal := &repository.JournalModel{
			SerialNo:        fmt.Sprintf("SETTLE-INTEREST-%s-%d", now.Format("20060102150405"), order.ID),
			UserID:          order.UserID,
			AccountID:       account.ID,
			Amount:          order.InterestAccrued,
			BalanceSnapshot: interestSnapshot,
			BizType:         3,
			RefID:           &order.ID,
			CreatedAt:       now.Format(time.RFC3339),
		}
		err = s.journalRepo.CreateJournalRecord(ctx, interestJournal)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to create interest journal record",
				"order_id", orderID, "error", err.Error())
		}
	}

	// Step 3: Update order status
	err = s.repo.SettleOrder(ctx, orderID, order.InterestAccrued)
	if err != nil {
		return fmt.Errorf("failed to settle order: %v", err)
	}

	logger.Info("[InterestScheduler] Order settled",
		"order_id", orderID,
		"amount_unfrozen", order.Amount,
		"currency", order.Currency,
		"interest_paid", order.InterestAccrued)

	return nil
}

// SettleExpiredOrders Find and settle all orders that have expired
func (s *InterestScheduler) SettleExpiredOrders(ctx context.Context) (int, error) {
	today := time.Now().UTC().Format("2006-01-02")

	logger.Info("[InterestScheduler] Settling expired orders", "date", today)

	orders, err := s.repo.GetExpiredOrders(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get expired orders: %v", err)
	}

	settledCount := 0
	renewedCount := 0

	for _, order := range orders {
		if order.AutoRenew {
			err = s.RenewOrder(ctx, order)
			if err != nil {
				logger.Error("[InterestScheduler] Failed to renew order",
					"order_id", order.ID, "error", err.Error())
				continue
			}
			renewedCount++
			logger.Info("[InterestScheduler] Order auto-renewed",
				"order_id", order.ID,
				"user_id", order.UserID,
				"amount", order.Amount,
				"currency", order.Currency)
		} else {
			err = s.SettleOrder(ctx, order.ID)
			if err != nil {
				logger.Error("[InterestScheduler] Failed to settle order",
					"order_id", order.ID, "error", err.Error())
				continue
			}
			settledCount++
			logger.Info("[InterestScheduler] Order auto-settled",
				"order_id", order.ID,
				"user_id", order.UserID,
				"amount", order.Amount,
				"currency", order.Currency)
		}
	}

	logger.Info("[InterestScheduler] Expired orders settlement completed",
		"settled_count", settledCount,
		"renewed_count", renewedCount)
	return settledCount + renewedCount, nil
}

// RenewOrder Auto-renew an expired order
func (s *InterestScheduler) RenewOrder(ctx context.Context, order *repository.WealthOrderModel) error {
	logger.Info("[InterestScheduler] Renewing order", "order_id", order.ID)

	// Get product info
	product, err := s.repo.GetProductByID(ctx, order.ProductID)
	if err != nil {
		return fmt.Errorf("failed to get product: %v", err)
	}

	// Check if product is still available
	if product.Status != 1 {
		logger.Warn("[InterestScheduler] Product not available for renewal, settling normally",
			"order_id", order.ID, "product_id", product.ID)
		return s.SettleOrder(ctx, order.ID)
	}

	// Check if auto-renew is still allowed
	if !product.AutoRenewAllowed {
		logger.Warn("[InterestScheduler] Auto-renew not allowed for product, settling normally",
			"order_id", order.ID, "product_id", product.ID)
		return s.SettleOrder(ctx, order.ID)
	}

	// Get user account
	account, err := s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
	if err != nil {
		return fmt.Errorf("failed to get account: %v", err)
	}

	// Check if user has sufficient available balance for principal freeze
	availableBalance, _ := utils.Sub(account.Balance, account.FrozenBalance)
	hasEnough, _ := utils.GTE(availableBalance, order.Amount)
	if !hasEnough {
		logger.Error("[InterestScheduler] Insufficient balance for renewal",
			"order_id", order.ID, "user_id", order.UserID,
			"available", availableBalance, "required", order.Amount)
		return fmt.Errorf("insufficient balance for renewal: available %s, required %s", availableBalance, order.Amount)
	}

	now := time.Now().UTC()
	today := now.Format("2006-01-02")
	todayDate, _ := time.Parse("2006-01-02", today)
	startDate := todayDate.AddDate(0, 0, 1).Format("2006-01-02")
	endDate := todayDate.AddDate(0, 0, 1+product.Duration).Format("2006-01-02")

	logger.Info("[InterestScheduler] Renewal dates calculated",
		"order_id", order.ID,
		"start_date", startDate,
		"end_date", endDate)

	// Step 1: Pay interest from old order
	hasInterest, _ := utils.GT(order.InterestAccrued, "0")
	if hasInterest {
		err = s.accountRepo.AddBalance(ctx, account.ID, order.InterestAccrued)
		if err != nil {
			return fmt.Errorf("failed to add interest: %v", err)
		}

		// 重新获取账户以获取准确的可用余额快照
		account, err = s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
		if err != nil {
			return fmt.Errorf("failed to get account: %v", err)
		}

		// 计算利息入账后的可用余额: balance - frozen_balance
		availableAfterInterest, _ := utils.Sub(account.Balance, account.FrozenBalance)
		interestJournal := &repository.JournalModel{
			SerialNo:        fmt.Sprintf("RENEW-INTEREST-%s-%d", now.Format("20060102150405"), order.ID),
			UserID:          order.UserID,
			AccountID:       account.ID,
			Amount:          order.InterestAccrued,
			BalanceSnapshot: availableAfterInterest,
			BizType:         3,
			RefID:           &order.ID,
			CreatedAt:       now.Format(time.RFC3339),
		}
		err = s.journalRepo.CreateJournalRecord(ctx, interestJournal)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to create interest journal record",
				"order_id", order.ID, "error", err.Error())
		}
	}

	// Step 2: Create new order (principal stays frozen, 无需记录流水)
	newOrder, err := s.repo.RenewOrder(ctx, order, product, startDate, endDate)
	if err != nil {
		return fmt.Errorf("failed to create renewed order: %v", err)
	}

	// Step 3: Update old order status
	err = s.repo.SettleOrder(ctx, order.ID, order.InterestAccrued)
	if err != nil {
		logger.Error("[InterestScheduler] Failed to update old order status",
			"order_id", order.ID, "error", err.Error())
	}

	logger.Info("[InterestScheduler] Order renewed successfully",
		"old_order_id", order.ID,
		"new_order_id", newOrder.ID,
		"amount", order.Amount,
		"currency", order.Currency,
		"start_date", startDate,
		"end_date", endDate,
		"interest_paid", order.InterestAccrued)

	return nil
}

func (s *InterestScheduler) ActivatePendingOrders(ctx context.Context) (int, error) {
	logger.Info("[InterestScheduler] Activating pending orders...")

	orders, err := s.repo.GetPendingOrders(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get pending orders: %v", err)
	}

	logger.Info("[InterestScheduler] Found pending orders", "count", len(orders))

	activatedCount := 0
	for _, order := range orders {
		err := s.repo.ActivateOrder(ctx, order.ID)
		if err != nil {
			logger.Error("[InterestScheduler] Failed to activate order",
				"order_id", order.ID, "error", err.Error())
			continue
		}
		logger.Info("[InterestScheduler] Order activated",
			"order_id", order.ID,
			"user_id", order.UserID,
			"start_date", order.StartDate)
		activatedCount++
	}

	logger.Info("[InterestScheduler] Pending orders activation completed",
		"activated_count", activatedCount)

	return activatedCount, nil
}

func (s *InterestScheduler) GetMetrics() *SchedulerMetrics {
	return s.metrics
}
