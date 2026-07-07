package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	// C-1 fix: env-driven DSN, no fallback. See docs/security/ROTATION_RUNBOOK.md.
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL environment variable is required. " +
			"Set it in .env (copy from .env.example) or pass it inline: " +
			`DATABASE_URL="postgresql://user:pass@host:port/db?sslmode=require" go run ./cmd/db_check`)
	}

	fmt.Println("==============================================")
	fmt.Println("   数据库数据检查 - Database Data Check       ")
	fmt.Println("==============================================")
	fmt.Println()

	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer db.Close()

	// Check users
	fmt.Println("【用户表 - users】")
	rows, err := db.Query("SELECT id, email, created_at FROM users ORDER BY id DESC LIMIT 5")
	if err != nil {
		log.Fatal("Query users failed:", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var email string
		var createdAt string
		rows.Scan(&id, &email, &createdAt)
		fmt.Printf("  ID: %d, Email: %s, Created: %s\n", id, email, createdAt)
	}
	fmt.Println()

	// Check accounts
	fmt.Println("【账户表 - account】")
	rows, err = db.Query("SELECT id, user_id, currency, balance, frozen_balance, type FROM account ORDER BY id DESC LIMIT 10")
	if err != nil {
		log.Fatal("Query accounts failed:", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var userID int
		var currency string
		var balance, frozenBalance string
		var accountType string
		rows.Scan(&id, &userID, &currency, &balance, &frozenBalance, &accountType)
		fmt.Printf("  ID: %d, UserID: %d, Currency: %s, Balance: %s, Frozen: %s, Type: %s\n",
			id, userID, currency, balance, frozenBalance, accountType)
	}
	fmt.Println()

	// Check products
	fmt.Println("【理财产品表 - wealth_product】")
	rows, err = db.Query("SELECT id, title, currency, apy, duration, status, total_quota, sold_quota FROM wealth_product ORDER BY id DESC LIMIT 10")
	if err != nil {
		log.Fatal("Query products failed:", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var title, currency string
		var apy, duration string
		var status int
		var totalQuota, soldQuota string
		rows.Scan(&id, &title, &currency, &apy, &duration, &status, &totalQuota, &soldQuota)
		fmt.Printf("  ID: %d, Title: %s, Currency: %s, APY: %s%%, Duration: %s days, Status: %d\n",
			id, title, currency, apy, duration, status)
		fmt.Printf("    Total: %s, Sold: %s\n", totalQuota, soldQuota)
	}
	fmt.Println()

	// Check orders
	fmt.Println("【订单表 - wealth_order (最新 10 条)】")
	rows, err = db.Query(`SELECT id, user_id, product_title, currency, amount, status, 
		interest_accrued, start_date, end_date, created_at 
		FROM wealth_order ORDER BY id DESC LIMIT 10`)
	if err != nil {
		log.Fatal("Query orders failed:", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var userID int
		var productTitle, currency, amount, interestAccrued, startDate, endDate, createdAt string
		var status int
		rows.Scan(&id, &userID, &productTitle, &currency, &amount, &status,
			&interestAccrued, &startDate, &endDate, &createdAt)
		fmt.Printf("  ID: %d, User: %d, %s, %s %s, Status: %d\n",
			id, userID, productTitle, amount, currency, status)
		fmt.Printf("    Interest: %s, %s ~ %s\n", interestAccrued, startDate, endDate)
	}
	fmt.Println()

	// Summary
	fmt.Println("==============================================")
	fmt.Println("              数据概览 - Data Summary        ")
	fmt.Println("==============================================")

	var userCount, accountCount, productCount, orderCount int
	db.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	db.QueryRow("SELECT COUNT(*) FROM account").Scan(&accountCount)
	db.QueryRow("SELECT COUNT(*) FROM wealth_product").Scan(&productCount)
	db.QueryRow("SELECT COUNT(*) FROM wealth_order").Scan(&orderCount)

	fmt.Printf("  用户总数: %d\n", userCount)
	fmt.Printf("  账户总数: %d\n", accountCount)
	fmt.Printf("  产品总数: %d\n", productCount)
	fmt.Printf("  订单总数: %d\n", orderCount)
	fmt.Println()
}
