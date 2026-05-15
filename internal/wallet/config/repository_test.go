package config

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestDBRepository_LoadAll_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT code, name, network_family").
		WillReturnRows(sqlmock.NewRows([]string{"code", "name", "network_family", "chain_id", "native_symbol", "explorer_url", "short_name", "enabled", "display_order"}).
			AddRow("ETHEREUM", "Ethereum", "EVM", "1", "ETH", "https://etherscan.io", "ETH", true, 10).
			AddRow("TRON", "TRON", "TRON", "", "TRX", "https://tronscan.org", "TRON", true, 30))

	mock.ExpectQuery("SELECT id, symbol, name, is_stable").
		WillReturnRows(sqlmock.NewRows([]string{"id", "symbol", "name", "is_stable", "enabled", "display_order"}).
			AddRow(1, "ETH", "Ether", false, true, 10).
			AddRow(4, "USDT", "Tether USD", true, true, 40))

	mock.ExpectQuery("SELECT id, chain_code, coin_id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "chain_code", "coin_id", "is_native", "token_contract", "decimals", "safeheron_coin_key", "min_deposit_amount", "deposit_enabled", "withdraw_enabled", "required_confirmations", "token_standard", "estimated_arrival_minutes", "display_order"}).
			AddRow(1, "ETHEREUM", 1, true, "", 18, "ETH", "0.001", true, false, 0, "Native", 2, 10).
			AddRow(2, "ETHEREUM", 4, false, "0xdAC17F958D2ee523a2206206994597C13D831ec7", 6, "USDT_ERC20", "1", true, false, 0, "ERC20", 2, 20))

	repo := NewDBRepository(db)
	chains, coins, coinChains, err := repo.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(chains) != 2 {
		t.Fatalf("expected 2 chains, got %d", len(chains))
	}
	if chains[0].Code != "ETHEREUM" || chains[0].NetworkFamily != "EVM" {
		t.Fatalf("unexpected chain: %+v", chains[0])
	}
	if len(coins) != 2 {
		t.Fatalf("expected 2 coins, got %d", len(coins))
	}
	if coins[1].Symbol != "USDT" || !coins[1].IsStable {
		t.Fatalf("unexpected coin: %+v", coins[1])
	}
	if len(coinChains) != 2 {
		t.Fatalf("expected 2 coin_chains, got %d", len(coinChains))
	}
	if coinChains[0].SafeheronCoinKey != "ETH" || coinChains[0].Decimals != 18 {
		t.Fatalf("unexpected coin_chain: %+v", coinChains[0])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestDBRepository_LoadAll_ChainsQueryError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT code, name, network_family").
		WillReturnError(sqlmock.ErrCancelled)

	repo := NewDBRepository(db)
	_, _, _, err := repo.LoadAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDBRepository_LoadAll_CoinsQueryError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT code, name, network_family").
		WillReturnRows(sqlmock.NewRows([]string{"code", "name", "network_family", "chain_id", "native_symbol", "explorer_url", "short_name", "enabled", "display_order"}))

	mock.ExpectQuery("SELECT id, symbol, name, is_stable").
		WillReturnError(sqlmock.ErrCancelled)

	repo := NewDBRepository(db)
	_, _, _, err := repo.LoadAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDBRepository_LoadAll_CoinChainsQueryError(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT code, name, network_family").
		WillReturnRows(sqlmock.NewRows([]string{"code", "name", "network_family", "chain_id", "native_symbol", "explorer_url", "short_name", "enabled", "display_order"}))

	mock.ExpectQuery("SELECT id, symbol, name, is_stable").
		WillReturnRows(sqlmock.NewRows([]string{"id", "symbol", "name", "is_stable", "enabled", "display_order"}))

	mock.ExpectQuery("SELECT id, chain_code, coin_id").
		WillReturnError(sqlmock.ErrCancelled)

	repo := NewDBRepository(db)
	_, _, _, err := repo.LoadAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDBRepository_LoadAll_EmptyTables(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()

	mock.ExpectQuery("SELECT code, name, network_family").
		WillReturnRows(sqlmock.NewRows([]string{"code", "name", "network_family", "chain_id", "native_symbol", "explorer_url", "short_name", "enabled", "display_order"}))

	mock.ExpectQuery("SELECT id, symbol, name, is_stable").
		WillReturnRows(sqlmock.NewRows([]string{"id", "symbol", "name", "is_stable", "enabled", "display_order"}))

	mock.ExpectQuery("SELECT id, chain_code, coin_id").
		WillReturnRows(sqlmock.NewRows([]string{"id", "chain_code", "coin_id", "is_native", "token_contract", "decimals", "safeheron_coin_key", "min_deposit_amount", "deposit_enabled", "withdraw_enabled", "required_confirmations", "token_standard", "estimated_arrival_minutes", "display_order"}))

	repo := NewDBRepository(db)
	chains, coins, coinChains, err := repo.LoadAll(context.Background())
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(chains) != 0 || len(coins) != 0 || len(coinChains) != 0 {
		t.Fatal("expected empty results")
	}
}
