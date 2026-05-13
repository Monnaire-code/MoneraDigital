package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	"monera-digital/internal/services"

	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

func main() {
	if os.Getenv("APP_ENV") != "production" {
		_ = godotenv.Overload(".env")
	}

	// Get database URL
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	// Connect to database
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Check connection
	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	fmt.Println("✓ Connected to database")

	// Query user by email
	email := "gyc567@gmail.com"
	var userID int
	var twoFactorEnabled bool
	var twoFactorSecret sql.NullString
	var twoFactorBackupCodes sql.NullString

	err = db.QueryRow(
		`SELECT id, two_factor_enabled, two_factor_secret, two_factor_backup_codes 
		 FROM users WHERE email = $1`,
		email,
	).Scan(&userID, &twoFactorEnabled, &twoFactorSecret, &twoFactorBackupCodes)

	if err == sql.ErrNoRows {
		log.Fatalf("User %s not found", email)
	}
	if err != nil {
		log.Fatalf("Failed to query user: %v", err)
	}

	fmt.Printf("\nUser: %s\n", email)
	fmt.Printf("User ID: %d\n", userID)
	fmt.Printf("2FA Enabled: %v\n", twoFactorEnabled)

	if twoFactorSecret.Valid {
		fmt.Printf("2FA Secret (encrypted): %s...\n", twoFactorSecret.String[:20])
		fmt.Printf("2FA Secret length: %d\n", len(twoFactorSecret.String))
	} else {
		fmt.Println("2FA Secret: NOT SET")
	}

	if twoFactorBackupCodes.Valid {
		fmt.Printf("Backup codes: %s...\n", twoFactorBackupCodes.String[:30])
	} else {
		fmt.Println("Backup codes: NOT SET")
	}

	// Try to decrypt the secret
	if twoFactorSecret.Valid {
		encryptionKey := os.Getenv("ENCRYPTION_KEY")
		if encryptionKey == "" {
			fmt.Println("\n⚠️  ENCRYPTION_KEY not set, cannot decrypt secret")
			return
		}

		encService, err := services.NewEncryptionService(encryptionKey)
		if err != nil {
			log.Fatalf("Failed to create encryption service: %v", err)
		}

		decryptedSecret, err := encService.Decrypt(twoFactorSecret.String)
		if err != nil {
			fmt.Printf("\n❌ Failed to decrypt secret: %v\n", err)
			fmt.Println("This is likely the cause of the 500 error!")
			return
		}

		fmt.Printf("\n✓ Successfully decrypted secret\n")
		fmt.Printf("Decrypted secret: %s\n", decryptedSecret)
		fmt.Printf("Expected secret:  YO5CXNI64PL3ZDCUFPIFWJWMCHWECV6O\n")

		if decryptedSecret == "YO5CXNI64PL3ZDCUFPIFWJWMCHWECV6O" {
			fmt.Println("\n✓ Secret matches!")
		} else {
			fmt.Println("\n⚠️  Secret does NOT match!")
			fmt.Println("The stored secret is different from what Google Authenticator has.")
		}
	}
}
