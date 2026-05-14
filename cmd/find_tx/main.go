// One-shot diagnostic: query Safeheron for INFLOW transactions + list registered
// coinKeys on a given accountKey. Helps locate a deposit that didn't trigger
// a webhook so we can call /v1/webhook/resend manually, or detect missing
// AddCoin registration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	sdk "github.com/Safeheron/safeheron-api-sdk-go/safeheron"
	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
	"github.com/joho/godotenv"
)

func main() {
	accountKey := flag.String("account-key", "", "safeheron accountKey (required)")
	hours := flag.Int("hours", 72, "look back N hours")
	flag.Parse()

	if *accountKey == "" {
		log.Fatal("--account-key required")
	}

	if err := godotenv.Overload(".env"); err != nil {
		log.Fatalf("load .env: %v", err)
	}

	client := sdk.Client{Config: sdk.ApiConfig{
		BaseUrl:               os.Getenv("SAFEHERON_API_BASE_URL"),
		ApiKey:                os.Getenv("SAFEHERON_API_KEY"),
		RsaPrivateKey:         os.Getenv("SAFEHERON_PRIVATE_KEY_PATH"),
		SafeheronRsaPublicKey: os.Getenv("SAFEHERON_PLATFORM_PUBLIC_KEY_PATH"),
		RequestTimeout:        30000,
	}}

	fmt.Println("=== 1. Registered coinKeys on this account ===")
	accApi := api.AccountApi{Client: client}
	var coinResp api.AccountCoinResponse
	if err := accApi.ListAccountCoin(api.ListAccountCoinRequest{AccountKey: *accountKey}, &coinResp); err != nil {
		log.Fatalf("ListAccountCoin: %v", err)
	}
	out, _ := json.MarshalIndent(coinResp, "", "  ")
	fmt.Println(string(out))

	fmt.Println("\n=== 2. INFLOW transactions (any coin) in last", *hours, "hours ===")
	txApi := api.TransactionApi{Client: client}
	var txResp api.TransactionsResponseV2
	if err := txApi.ListTransactionsV2(api.ListTransactionsV2Request{
		Limit:                 30,
		TransactionDirection:  "INFLOW",
		DestinationAccountKey: *accountKey,
		CreateTimeMin:         time.Now().Add(-time.Duration(*hours) * time.Hour).UnixMilli(),
		CreateTimeMax:         time.Now().UnixMilli(),
	}, &txResp); err != nil {
		log.Fatalf("ListTransactionsV2: %v", err)
	}
	out2, _ := json.MarshalIndent(txResp, "", "  ")
	fmt.Println(string(out2))
}
