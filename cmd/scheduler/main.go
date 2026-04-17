//go:build ignore
// +build ignore

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"monera-digital/internal/binance"
	"monera-digital/internal/config"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository"
	"monera-digital/internal/repository/postgres"
	"monera-digital/internal/scheduler"
)

func main() {
	fmt.Println("==============================================")
	fmt.Println("   Monera Digital 利息调度器 - 独立启动脚本     ")
	fmt.Println("==============================================")
	fmt.Println()

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	env := os.Getenv("ENV")
	if env == "" {
		env = os.Getenv("GO_ENV")
	}
	if env == "" {
		if os.Getenv("GIN_MODE") == "release" {
			env = "production"
		} else {
			env = "development"
		}
	}

	if err := logger.Init(env); err != nil {
		log.Fatal("Failed to initialize logger: ", err)
	}
	defer logger.GetLogger().Sync()

	logger.Info("Starting Monera Digital Interest Scheduler",
		"environment", env)

	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("Failed to connect to database",
			"error", err.Error())
	}
	defer database.Close()

	if err := database.Ping(); err != nil {
		logger.Fatal("Failed to ping database",
			"error", err.Error())
	}
	logger.Info("Database connected successfully")

	wealthRepo := postgres.NewWealthRepository(database)
	accountRepo := postgres.NewAccountRepository(database)
	journalRepo := postgres.NewJournalRepository(database)
	addressRepo := postgres.NewAddressRepository(database)

	accountV2, ok := accountRepo.(repository.AccountV2)
	if !ok {
		logger.Fatal("Failed to cast account repository")
	}

	priceService := binance.NewPriceService()
	addressScheduler := scheduler.NewAddressScheduler(addressRepo)
	interestScheduler := NewStandaloneScheduler(
		wealthRepo,
		accountV2,
		journalRepo,
		priceService,
	)

	logger.Info("Scheduler initialized",
		"next_run", "00:00:05 UTC (tomorrow)")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go addressScheduler.Start(ctx)
	go interestScheduler.Start(ctx)

	logger.Info("Schedulers started successfully",
		"schedulers", []string{"address", "interest"})

	sig := <-sigChan
	logger.Info("Received shutdown signal",
		"signal", sig.String())

	cancel()

	logger.Info("Waiting for current task to complete...")
	time.Sleep(5 * time.Second)

	logger.Info("Interest scheduler stopped")
	fmt.Println("\n==============================================")
	fmt.Println("   调度器已停止 - Scheduler Stopped           ")
	fmt.Println("==============================================")
}

type Scheduler struct {
	repo         *postgres.WealthRepository
	accountRepo  repository.AccountV2
	journalRepo  *postgres.JournalRepository
	priceService *binance.PriceService
}

func NewStandaloneScheduler(
	wealthRepo *postgres.WealthRepository,
	accountRepo repository.AccountV2,
	journalRepo *postgres.JournalRepository,
	priceService *binance.PriceService,
) *Scheduler {
	return &Scheduler{
		repo:         wealthRepo,
		accountRepo:  accountRepo,
		journalRepo:  journalRepo,
		priceService: priceService,
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	loc := time.UTC

	nextMidnight := time.Now().In(loc)
	nextMidnight = time.Date(nextMidnight.Year(), nextMidnight.Month(), nextMidnight.Day(), 0, 0, 5, 0, loc)
	if nextMidnight.Before(time.Now().In(loc)) {
		nextMidnight = nextMidnight.AddDate(0, 0, 1)
	}
	duration := nextMidnight.Sub(time.Now().In(loc))

	logger.Info("First run scheduled",
		"scheduled_time", nextMidnight.Format("2006-01-02 15:04:05"),
		"delay_seconds", duration.Seconds())

	time.Sleep(duration)

	logger.Info("Scheduler started - running daily at 00:00:05 UTC")

	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("Scheduler context cancelled, stopping...")
			return
		case <-ticker.C:
			s.runDailyTask(ctx)
		}
	}
}

func (s *Scheduler) runDailyTask(ctx context.Context) {
	startTime := time.Now()
	logger.Info("Daily task started", "timestamp", startTime.Format("2006-01-02 15:04:05"))

	ordersProcessed, interestAccrued, err := s.calculateDailyInterest(ctx)
	if err != nil {
		logger.Error("Failed to calculate daily interest",
			"error", err.Error())
	}

	settledCount, settleErr := s.settleExpiredOrders(ctx)
	if settleErr != nil {
		logger.Error("Failed to settle expired orders",
			"error", settleErr.Error())
	}

	duration := time.Since(startTime)
	if err == nil && settleErr == nil {
		logger.Info("Daily task completed successfully",
			"orders_processed", ordersProcessed,
			"interest_accrued", interestAccrued,
			"orders_settled", settledCount,
			"duration_seconds", duration.Seconds())
	} else {
		logger.Error("Daily task completed with errors",
			"orders_processed", ordersProcessed,
			"interest_error", err != nil,
			"settle_error", settleErr != nil,
			"duration_seconds", duration.Seconds())
	}
}

func (s *Scheduler) calculateDailyInterest(ctx context.Context) (int, float64, error) {
	logger.Info("Calculating daily interest...")

	orders, err := s.repo.GetActiveOrders(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get active orders: %w", err)
	}

	today := time.Now().UTC()
	totalInterest := 0.0
	processed := 0

	for _, order := range orders {
		startDate, err := time.Parse("2006-01-02", order.StartDate)
		if err != nil {
			logger.Error("Failed to parse start date",
				"order_id", order.ID,
				"start_date", order.StartDate)
			continue
		}

		daysSinceStart := int(today.Sub(startDate).Hours() / 24)
		if daysSinceStart < 1 {
			logger.Debug("Order started today, skipping",
				"order_id", order.ID)
			continue
		}

		if daysSinceStart >= int(order.Duration) {
			logger.Debug("Order exceeded duration, will be settled",
				"order_id", order.ID,
				"days_held", daysSinceStart,
				"duration", order.Duration)
			continue
		}

		product, err := s.repo.GetProductByID(ctx, order.ProductID)
		if err != nil {
			logger.Error("Failed to get product",
				"order_id", order.ID,
				"product_id", order.ProductID)
			continue
		}

		amount, _ := strconv.ParseFloat(order.Amount, 64)
		apy, _ := strconv.ParseFloat(product.APY, 64)

		dailyInterest := amount * (apy / 100) / 365
		accruedInterest := dailyInterest * float64(daysSinceStart)

		err = s.repo.UpdateInterestAccrued(ctx, order.ID, fmt.Sprintf("%.8f", accruedInterest))
		if err != nil {
			logger.Error("Failed to update interest accrued",
				"order_id", order.ID,
				"error", err.Error())
			continue
		}

		totalInterest += accruedInterest
		processed++
		logger.Debug("Interest calculated",
			"order_id", order.ID,
			"days_held", daysSinceStart,
			"daily_interest", dailyInterest,
			"total_accrued", accruedInterest)
	}

	logger.Info("Daily interest calculation completed",
		"orders_processed", processed,
		"total_interest", totalInterest)

	return processed, totalInterest, nil
}

func (s *Scheduler) settleExpiredOrders(ctx context.Context) (int, error) {
	logger.Info("Settling expired orders...")

	orders, err := s.repo.GetExpiredOrders(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get expired orders: %w", err)
	}

	settled := 0

	for _, order := range orders {
		if order.Status != 1 {
			logger.Debug("Order already processed",
				"order_id", order.ID,
				"status", order.Status)
			continue
		}

		account, err := s.accountRepo.GetAccountByUserIDAndCurrency(ctx, order.UserID, order.Currency)
		if err != nil {
			logger.Error("Failed to get account",
				"order_id", order.ID,
				"user_id", order.UserID,
				"error", err.Error())
			continue
		}

		err = s.accountRepo.UnfreezeBalance(ctx, account.ID, order.Amount)
		if err != nil {
			logger.Error("Failed to unfreeze balance",
				"order_id", order.ID,
				"error", err.Error())
			continue
		}

		interestAccrued, _ := strconv.ParseFloat(order.InterestAccrued, 64)
		if interestAccrued > 0 {
			err = s.accountRepo.AddBalance(ctx, account.ID, order.InterestAccrued)
			if err != nil {
				logger.Error("Failed to add interest",
					"order_id", order.ID,
					"error", err.Error())
				continue
			}
		}

		err = s.repo.SettleOrder(ctx, order.ID, fmt.Sprintf("%.8f", interestAccrued))
		if err != nil {
			logger.Error("Failed to settle order",
				"order_id", order.ID,
				"error", err.Error())
			continue
		}

		settled++
		logger.Info("Order settled successfully",
			"order_id", order.ID,
			"user_id", order.UserID,
			"amount", order.Amount,
			"interest_paid", interestAccrued)
	}

	logger.Info("Expired orders settlement completed",
		"orders_settled", settled)

	return settled, nil
}
