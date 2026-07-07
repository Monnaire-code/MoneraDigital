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
	// C-1 fix: load DATABASE_URL from environment. The hardcoded credential
	// was previously leaked in git history and is now rotated; this script
	// must never carry a fallback. See docs/security/ROTATION_RUNBOOK.md.
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	if os.Getenv("APP_ENV") == "production" {
		log.Fatal("BLOCKED: scripts/cleanup.go performs destructive operations (DELETE wealth_order, mutate account balances). " +
			"It must not be run against production. Unset APP_ENV or set APP_ENV=local/development/test.")
	}

	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		log.Fatal("DATABASE_URL environment variable is required. " +
			"Set it in .env (copy from .env.example) or pass it inline: " +
			`DATABASE_URL="postgresql://user:pass@host:port/db?sslmode=require" go run scripts/cleanup.go`)
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	fmt.Println("=== 1. 清除 wealth_order 表 ===")

	var orderCount int
	err = db.QueryRow("SELECT COUNT(*) FROM wealth_order").Scan(&orderCount)
	if err != nil {
		log.Fatal("查询订单数量失败:", err)
	}
	fmt.Printf("当前订单数量: %d\n", orderCount)

	if orderCount > 0 {
		_, err = db.Exec("DELETE FROM wealth_order")
		if err != nil {
			log.Fatal("删除订单失败:", err)
		}
		fmt.Printf("✅ 已删除 %d 条订单记录\n", orderCount)
	} else {
		fmt.Println("订单表已经是空的")
	}

	fmt.Println("\n=== 2. 解冻用户 64 的所有冻结金额 ===")

	accounts, err := db.Query(`
		SELECT id, user_id, currency, balance, frozen_balance 
		FROM account 
		WHERE user_id = 64 AND frozen_balance::numeric > 0
	`)
	if err != nil {
		log.Fatal("查询账户失败:", err)
	}
	defer accounts.Close()

	totalUnfrozen := 0

	for accounts.Next() {
		var id int64
		var userID int
		var currency string
		var balance, frozenBalance string

		err := accounts.Scan(&id, &userID, &currency, &balance, &frozenBalance)
		if err != nil {
			log.Fatal("读取账户数据失败:", err)
		}

		fmt.Printf("账户 %d (%s): 冻结金额 %s\n", id, currency, frozenBalance)

		_, err = db.Exec(`
			UPDATE account 
			SET frozen_balance = '0', 
				balance = CAST(balance AS NUMERIC) + CAST(frozen_balance AS NUMERIC),
				updated_at = NOW()
			WHERE id = $1 AND frozen_balance::numeric > 0
		`, id)
		if err != nil {
			log.Fatal("解冻失败:", err)
		}

		totalUnfrozen++
	}

	if totalUnfrozen > 0 {
		fmt.Printf("\n✅ 已解冻 %d 个账户\n", totalUnfrozen)
	} else {
		fmt.Println("\n用户 64 没有冻结的金额")
	}

	fmt.Println("\n=== 3. 验证结果 ===")

	err = db.QueryRow("SELECT COUNT(*) FROM wealth_order").Scan(&orderCount)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wealth_order 表剩余记录: %d\n", orderCount)

	var frozenTotal string
	err = db.QueryRow(`
		SELECT COALESCE(SUM(frozen_balance::numeric), 0)::text 
		FROM account 
		WHERE user_id = 64
	`).Scan(&frozenTotal)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("用户 64 的总冻结金额: %s\n", frozenTotal)

	fmt.Println("\n✅ 所有操作完成!")
}
