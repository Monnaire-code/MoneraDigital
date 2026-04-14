package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"monera-digital/internal/config"
	"monera-digital/internal/db"
	"monera-digital/internal/logger"
	"monera-digital/internal/repository/postgres"
	"monera-digital/internal/scheduler"
)

func main() {
	fmt.Println("=== Manual Interest Scheduler Trigger ===")

	if err := logger.Init("development"); err != nil {
		fmt.Printf("Failed to init logger: %v\n", err)
		os.Exit(1)
	}

	cfg := config.Load()

	database, err := db.InitDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	defer database.Close()
	fmt.Println("✅ Database connected")

	wealthRepo := postgres.NewWealthRepository(database)
	accountRepo := postgres.NewAccountRepository(database)
	journalRepo := postgres.NewJournalRepository(database)
	dailyInterestRepo := postgres.NewDailyInterestRepository(database)

	s := scheduler.NewInterestScheduler(wealthRepo, accountRepo, journalRepo, dailyInterestRepo)

	ctx := context.Background()

	var dateOverride *time.Time
	if len(os.Args) > 1 {
		dateStr := os.Args[1]
		parsedDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			fmt.Printf("❌ Invalid date format: %s (expected: 2006-01-02)\n", dateStr)
			os.Exit(1)
		}
		dateOverride = &parsedDate
		fmt.Printf("📅 Simulating date: %s\n", dateStr)
	}

	fmt.Println("\n📊 Running CalculateDailyInterest...")
	ordersProcessed, totalInterest, err := s.CalculateDailyInterest(ctx, dateOverride)
	if err != nil {
		log.Printf("❌ Error: %v", err)
	} else {
		fmt.Printf("✅ Success!\n")
		fmt.Printf("   Orders processed: %d\n", ordersProcessed)
		fmt.Printf("   Total interest: %s\n", totalInterest)
	}

	fmt.Println("\n📊 Running SettleExpiredOrders...")
	settledCount, err := s.SettleExpiredOrders(ctx)
	if err != nil {
		log.Printf("❌ Error: %v", err)
	} else {
		fmt.Printf("✅ Success! Settled/Renewed %d orders\n", settledCount)
	}

	fmt.Println("\n=== Done ===")
}
