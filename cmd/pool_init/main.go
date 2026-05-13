package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"monera-digital/internal/db"
	"monera-digital/internal/safeheron"
	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

func main() {
	evmCount := flag.Int("evm-count", 100, "target number of available EVM addresses in pool")
	tronCount := flag.Int("tron-count", 100, "target number of available TRON addresses in pool")
	dryRun := flag.Bool("dry-run", false, "print planned operations without executing")
	retryErrors := flag.Bool("retry-errors", false, "retry addresses in ERROR status")
	flag.Parse()

	loadEnv()

	if *dryRun {
		runDryRun(*evmCount, *tronCount)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	database, err := db.InitDB(viper.GetString("DATABASE_URL"))
	if err != nil {
		log.Fatalf("database init: %v", err)
	}
	defer database.Close()

	registry := walletconfig.NewRegistry(walletconfig.NewDBRepository(database), 0)
	if err := registry.Load(ctx); err != nil {
		log.Fatalf("registry load: %v", err)
	}

	client, err := safeheron.NewClient(safeheron.Config{
		BaseURL:              viper.GetString("SAFEHERON_API_BASE_URL"),
		APIKey:               viper.GetString("SAFEHERON_API_KEY"),
		PrivateKeyPEM:        viper.GetString("SAFEHERON_PRIVATE_KEY_PEM"),
		PlatformPublicKeyPEM: viper.GetString("SAFEHERON_PLATFORM_PUBLIC_KEY_PEM"),
		WebhookPublicKeyPEM:  viper.GetString("SAFEHERON_WEBHOOK_PUBLIC_KEY_PEM"),
		WebhookPrivateKeyPEM: viper.GetString("SAFEHERON_WEBHOOK_PRIVATE_KEY_PEM"),
		RequestTimeoutMS:     30000,
	})
	if err != nil {
		log.Fatalf("safeheron client init: %v", err)
	}
	defer client.Close()

	repo := pool.NewRepository(database)
	mgr := pool.NewManager(repo, client, registry)

	if *retryErrors {
		log.Println("--retry-errors not yet implemented")
	}

	families := []struct {
		name  string
		count int
	}{
		{"EVM", *evmCount},
		{"TRON", *tronCount},
	}

	for _, f := range families {
		if f.count <= 0 {
			continue
		}
		log.Printf("Creating %d %s addresses...", f.count, f.name)
		if err := mgr.Replenish(ctx, f.name, f.count); err != nil {
			log.Printf("ERROR creating %s addresses: %v", f.name, err)
		} else {
			log.Printf("Done: %s %d addresses created", f.name, f.count)
		}
	}
}

func runDryRun(evmCount, tronCount int) {
	fmt.Println("=== DRY RUN ===")
	fmt.Printf("Would create %d EVM + %d TRON addresses\n\n", evmCount, tronCount)

	for _, f := range []struct {
		name  string
		count int
	}{{"EVM", evmCount}, {"TRON", tronCount}} {
		fmt.Printf("--- %s (%d) ---\n", f.name, f.count)
		for i := range f.count {
			fmt.Printf("  [%d] customer_ref_id=%s\n", i+1, uuid.New().String())
		}
	}
	fmt.Println("\n=== END DRY RUN (no DB writes) ===")
}

func loadEnv() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}
	viper.AutomaticEnv()
}
