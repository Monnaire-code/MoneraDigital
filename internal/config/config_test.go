package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_DotEnvOverridesShellEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("DATABASE_URL=postgres://right-host/right-db\n"), 0644); err != nil {
		t.Fatal(err)
	}
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://wrong-host/wrong-db")

	cfg := Load()
	if cfg.DatabaseURL != "postgres://right-host/right-db" {
		t.Errorf("expected .env to override shell env, got DatabaseURL=%s", cfg.DatabaseURL)
	}
}

func TestLoad_ProductionIgnoresDotEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("DATABASE_URL=postgres://dotenv-host/dotenv-db\n"), 0644); err != nil {
		t.Fatal(err)
	}
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://prod-host/prod-db")

	cfg := Load()
	if cfg.DatabaseURL != "postgres://prod-host/prod-db" {
		t.Errorf("production should keep shell env, got DatabaseURL=%s", cfg.DatabaseURL)
	}
}

func TestLoad_MissingDotEnvNoPanic(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://shell-host/shell-db")

	cfg := Load()
	if cfg.DatabaseURL != "postgres://shell-host/shell-db" {
		t.Errorf("missing .env should fall through to shell env, got DatabaseURL=%s", cfg.DatabaseURL)
	}
}
