package db

import (
	"database/sql"
	"log"
	"time"

	"monera-digital/internal/buildinfo"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func InitDB(databaseURL string) (*sql.DB, error) {
	return InitDBWithProvenance(databaseURL, "dev", "")
}

func InitDBWithProvenance(databaseURL, version, invocationID string) (*sql.DB, error) {
	provenanceURL, err := buildinfo.DatabaseURL(databaseURL, version, invocationID)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", provenanceURL)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}

	log.Println("Database connected successfully (using pgx driver)")
	return db, nil
}
