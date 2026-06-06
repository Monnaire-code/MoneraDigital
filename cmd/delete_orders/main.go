package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	// C-1 fix: env-driven DSN + production guard. See docs/security/ROTATION_RUNBOOK.md.
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	if os.Getenv("APP_ENV") == "production" {
		log.Fatal("BLOCKED: cmd/delete_orders performs DELETE on wealth_order. " +
			"It must not run against production. Unset APP_ENV or set APP_ENV=local/development/test.")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required. " +
			"Set it in .env (copy from .env.example) or pass it inline: " +
			`DATABASE_URL="postgresql://user:pass@host:port/db?sslmode=require" go run ./cmd/delete_orders`)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 查找 test@test.com 用户的ID
	var userID int64
	err = db.QueryRow("SELECT id FROM users WHERE email = $1", "test@test.com").Scan(&userID)
	if err != nil {
		log.Printf("User not found: %v", err)
		return
	}
	fmt.Printf("Found user ID: %d\n", userID)

	// 查看该用户的申购记录
	rows, err := db.Query("SELECT id, product_id, amount, status, start_date, end_date FROM wealth_order WHERE user_id = $1", userID)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println("\nCurrent orders:")
	count := 0
	for rows.Next() {
		var id, productID int64
		var amount string
		var status int
		var startDate, endDate string
		rows.Scan(&id, &productID, &amount, &status, &startDate, &endDate)
		fmt.Printf("  Order ID: %d, Product: %d, Amount: %s, Status: %d, Period: %s to %s\n",
			id, productID, amount, status, startDate, endDate)
		count++
	}

	if count == 0 {
		fmt.Println("  No orders found")
	}

	// 删除该用户的申购记录
	if count > 0 {
		fmt.Printf("\n⚠️  About to DELETE %d order(s) for user %d. Type 'yes' to confirm: ", count, userID)
		reader := bufio.NewReader(os.Stdin)
		confirm, _ := reader.ReadString('\n')
		confirm = strings.TrimSpace(confirm)
		if confirm != "yes" {
			fmt.Println("Aborted.")
			return
		}
	}

	result, err := db.Exec("DELETE FROM wealth_order WHERE user_id = $1", userID)
	if err != nil {
		log.Fatal(err)
	}

	deletedCount, _ := result.RowsAffected()
	fmt.Printf("\nDeleted %d order(s)\n", deletedCount)
}
