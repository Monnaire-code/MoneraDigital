//go:build ignore
// +build ignore

package main

import (
	"database/sql"
	"encoding/json"
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

	// Query wallet for user 73
	query := `
		SELECT id, request_id, user_id, product_code, currency, status, wallet_id, address, addresses, created_at, updated_at
		FROM wallet_creation_requests 
		WHERE user_id = 73
		ORDER BY created_at DESC LIMIT 1`

	var id int
	var requestID, userID, productCode, currency, status string
	var walletID, address, addresses sql.NullString
	var createdAt, updatedAt string

	err = db.QueryRow(query).Scan(&id, &requestID, &userID, &productCode, &currency, &status, &walletID, &address, &addresses, &createdAt, &updatedAt)
	if err != nil {
		log.Fatal("Query failed:", err)
	}

	// Simulate the JSON response as Go would marshal it
	type WalletResponse struct {
		ID          int            `json:"id"`
		RequestID   string         `json:"requestId"`
		UserID      int            `json:"userId"`
		ProductCode string         `json:"productCode"`
		Currency    string         `json:"currency"`
		Status      string         `json:"status"`
		WalletID    sql.NullString `json:"walletId"`
		Address     sql.NullString `json:"address"`
		Addresses   sql.NullString `json:"addresses"`
		CreatedAt   string         `json:"createdAt"`
		UpdatedAt   string         `json:"updatedAt"`
	}

	resp := WalletResponse{
		ID:          id,
		RequestID:   requestID,
		UserID:      73,
		ProductCode: productCode,
		Currency:    currency,
		Status:      status,
		WalletID:    walletID,
		Address:     address,
		Addresses:   addresses,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
	}

	jsonBytes, _ := json.MarshalIndent(resp, "", "  ")
	fmt.Println("=== Backend Response for User 73 ===")
	fmt.Println(string(jsonBytes))

	// Also show what the frontend expects
	fmt.Println("\n=== Frontend Expected Format ===")
	fmt.Println(`{
  "status": "SUCCESS",
  "walletId": {"String": "wallet_xxx", "Valid": true},
  "address": {"String": "TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW", "Valid": true},
  "addresses": "{\"TRON\": \"TJCnKsPa7y5okkXvQAidZBzqx3QyQ6sxMW\"}"
}`)
}
