//go:build ignore
// +build ignore

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"

	"monera-digital/internal/config"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository"
	"monera-digital/internal/repository/postgres"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	fmt.Println("==============================================")
	fmt.Println("   Monera Digital 利息调度器 - 一次性执行      ")
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

	logger.Info("Starting Monera Digital Interest Scheduler (One-time run)")

	database, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("Failed to connect to database", "error", err.Error())
	}
	defer database.Close()

	if err := database.Ping(); err != nil {
		logger.Fatal("Failed to ping database", "error", err.Error())
	}
	logger.Info("Database connected successfully")

	wealthRepo := postgres.NewWealthRepository(database)
	accountRepo := postgres.NewAccountRepository(database)
	_ = accountRepo

	accountV2, ok := accountRepo.(repository.AccountV2)
	if !ok {
		logger.Fatal("Failed to cast account repository")
	}
	_ = accountV2

	ctx := context.Background()

	activatedCount := 0
	pendingOrders, err := wealthRepo.GetPendingOrders(ctx)
	if err != nil {
		logger.Error("Failed to get pending orders", "error", err.Error())
	} else {
		logger.Info("Found pending orders", "count", len(pendingOrders))
		for _, order := range pendingOrders {
			err := wealthRepo.ActivateOrder(ctx, order.ID)
			if err != nil {
				logger.Error("Failed to activate order", "order_id", order.ID, "error", err.Error())
				continue
			}
			activatedCount++
			logger.Info("Order activated", "order_id", order.ID)
		}
		logger.Info("Pending orders activation completed", "activated_count", activatedCount)
	}

	ordersProcessed := 0
	totalInterest := 0.0

	activeOrders, err := wealthRepo.GetActiveOrders(ctx)
	if err != nil {
		logger.Error("Failed to get active orders", "error", err.Error())
	} else {
		logger.Info("Found active orders", "count", len(activeOrders))

		for _, order := range activeOrders {
			product, err := wealthRepo.GetProductByID(ctx, order.ProductID)
			if err != nil {
				logger.Error("Failed to get product", "order_id", order.ID, "product_id", order.ProductID, "error", err.Error())
				continue
			}

			amount, _ := strconv.ParseFloat(order.Amount, 64)
			apy, _ := strconv.ParseFloat(product.APY, 64)
			dailyInterest := amount * (apy / 100) / 365

			totalInterest += dailyInterest
			ordersProcessed++

			logger.Info("Interest calculated",
				"order_id", order.ID,
				"amount", order.Amount,
				"apy", product.APY,
				"daily_interest", dailyInterest,
				"duration", order.Duration,
			)

			err = wealthRepo.UpdateInterestAccrued(ctx, order.ID, fmt.Sprintf("%.8f", dailyInterest))
			if err != nil {
				logger.Error("Failed to update interest accrued", "order_id", order.ID, "error", err.Error())
				continue
			}
		}

		logger.Info("Daily interest calculation completed",
			"orders_processed", ordersProcessed,
			"total_interest", totalInterest)
	}

	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("   执行完成 - Execution Completed              ")
	fmt.Println("==============================================")
	fmt.Printf("   激活订单数: %d\n", activatedCount)
	fmt.Printf("   计算利息订单数: %d\n", ordersProcessed)
	fmt.Printf("   总利息: %.8f\n", totalInterest)
	fmt.Println("==============================================")
}
