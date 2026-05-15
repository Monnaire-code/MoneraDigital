//go:build ignore
// +build ignore

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}
	defer db.Close()

	// Drop the wrong table
	_, err = db.Exec(`DROP TABLE IF EXISTS wallet_creation_request CASCADE`)
	if err != nil {
		log.Fatal("Failed to drop table:", err)
	}
	fmt.Println("✅ Dropped table: wallet_creation_request")

	// Drop associated indexes
	indexes := []string{
		"idx_wallet_creation_request_user_id",
		"uk_wallet_creation_request_user_id",
		"uk_wallet_creation_request_request_id",
		"idx_wallet_requests_user_product_currency",
	}
	for _, idx := range indexes {
		_, err = db.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS %s`, idx))
		if err != nil {
			log.Printf("Warning: failed to drop index %s: %v", idx, err)
		} else {
			fmt.Printf("✅ Dropped index: %s\n", idx)
		}
	}

	// Verify correct table exists
	var tableName string
	err = db.QueryRow(`SELECT table_name FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_name = 'wallet_creation_requests'`).Scan(&tableName)
	if err != nil {
		log.Fatal("Correct table not found:", err)
	}
	fmt.Printf("✅ Verified table exists: %s\n", tableName)

	fmt.Println("\nDone!")
}
