package migrations

import (
	"database/sql"
	"fmt"
	"os"

	"monera-digital/internal/migration"
)

type SeedSafeheronPhase1 struct{}

func (m *SeedSafeheronPhase1) Version() string {
	return "021"
}

func (m *SeedSafeheronPhase1) Description() string {
	return "Seed chains, coins, and coin_chains for Safeheron Phase 1"
}

func (m *SeedSafeheronPhase1) Up(db *sql.DB) error {
	seedChains := `
	INSERT INTO chains (code, name, network_family, chain_id, native_symbol, explorer_url, display_order)
	VALUES
		('ETHEREUM', 'Ethereum',        'EVM',  '1',  'ETH', 'https://etherscan.io',  10),
		('BSC',      'BNB Smart Chain', 'EVM',  '56', 'BNB', 'https://bscscan.com',   20),
		('TRON',     'TRON',            'TRON', NULL,  'TRX', 'https://tronscan.org',  30)
	ON CONFLICT (code) DO NOTHING;
	`
	_, err := db.Exec(seedChains)
	if err != nil {
		return fmt.Errorf("failed to seed chains: %w", err)
	}

	seedCoins := `
	INSERT INTO coins (symbol, name, is_stable, display_order)
	VALUES
		('ETH',  'Ether',      false, 10),
		('BNB',  'BNB',        false, 20),
		('TRX',  'TRON',       false, 30),
		('USDT', 'Tether USD', true,  40),
		('USDC', 'USD Coin',   true,  50)
	ON CONFLICT (symbol) DO NOTHING;
	`
	_, err = db.Exec(seedCoins)
	if err != nil {
		return fmt.Errorf("failed to seed coins: %w", err)
	}

	appEnv := os.Getenv("APP_ENV")
	switch appEnv {
	case "production":
		return m.seedProductionCoinChains(db)
	case "local", "development", "test", "":
		return m.seedTestnetCoinChains(db)
	default:
		return fmt.Errorf("unknown APP_ENV %q: expected 'production', 'local', 'development', or 'test'", appEnv)
	}
}

func (m *SeedSafeheronPhase1) seedProductionCoinChains(db *sql.DB) error {
	query := `
	INSERT INTO coin_chains (chain_code, coin_id, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, display_order)
	    SELECT 'ETHEREUM', id, true,  NULL,                                          18, 'ETH',        '0.001', 10 FROM coins WHERE symbol='ETH'
	UNION ALL
	    SELECT 'ETHEREUM', id, false, '0xdAC17F958D2ee523a2206206994597C13D831ec7', 6,  'USDT_ERC20', '1',     20 FROM coins WHERE symbol='USDT'
	UNION ALL
	    SELECT 'ETHEREUM', id, false, '0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48', 6,  'USDC_ERC20', '1',     30 FROM coins WHERE symbol='USDC'
	UNION ALL
	    SELECT 'BSC',      id, true,  NULL,                                          18, 'BNB_BSC',    '0.005', 40 FROM coins WHERE symbol='BNB'
	UNION ALL
	    SELECT 'BSC',      id, false, '0x55d398326f99059fF775485246999027B3197955',  18, 'USDT_BEP20', '1',     50 FROM coins WHERE symbol='USDT'
	UNION ALL
	    SELECT 'BSC',      id, false, '0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d',  18, 'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', '1', 60 FROM coins WHERE symbol='USDC'
	UNION ALL
	    SELECT 'TRON',     id, true,  NULL,                                          6,  'TRX',        '1',     70 FROM coins WHERE symbol='TRX'
	UNION ALL
	    SELECT 'TRON',     id, false, 'TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t',          6,  'USDT_TRC20', '1',     80 FROM coins WHERE symbol='USDT'
	ON CONFLICT (safeheron_coin_key) DO NOTHING;
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to seed production coin_chains: %w", err)
	}
	return nil
}

func (m *SeedSafeheronPhase1) seedTestnetCoinChains(db *sql.DB) error {
	query := `
	INSERT INTO coin_chains (chain_code, coin_id, is_native, token_contract, decimals, safeheron_coin_key, min_deposit_amount, display_order)
	    SELECT 'ETHEREUM', id, true,  NULL,                                          18, 'ETH(SEPOLIA)_ETHEREUM_SEPOLIA',  '0.0001', 10 FROM coins WHERE symbol='ETH'
	UNION ALL
	    SELECT 'ETHEREUM', id, false, '0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238', 6,  'USDCOIN_ERC20_ETHEREUM_SEPOLIA', '0.1',    30 FROM coins WHERE symbol='USDC'
	UNION ALL
	    SELECT 'TRON',     id, true,  NULL,                                          6,  'TRX(SHASTA)_TRON_TESTNET',       '0.1',    70 FROM coins WHERE symbol='TRX'
	ON CONFLICT (safeheron_coin_key) DO NOTHING;
	`
	_, err := db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to seed testnet coin_chains: %w", err)
	}
	return nil
}

func (m *SeedSafeheronPhase1) Down(db *sql.DB) error {
	_, err := db.Exec(`
		DELETE FROM coin_chains WHERE safeheron_coin_key IN (
			'ETH', 'USDT_ERC20', 'USDC_ERC20', 'BNB_BSC', 'USDT_BEP20',
			'USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET', 'TRX', 'USDT_TRC20',
			'ETH(SEPOLIA)_ETHEREUM_SEPOLIA', 'USDCOIN_ERC20_ETHEREUM_SEPOLIA', 'TRX(SHASTA)_TRON_TESTNET'
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 coin_chains: %w", err)
	}

	_, err = db.Exec(`
		DELETE FROM coins WHERE symbol IN ('ETH', 'BNB', 'TRX', 'USDT', 'USDC')
		AND NOT EXISTS (SELECT 1 FROM coin_chains WHERE coin_chains.coin_id = coins.id);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 coins: %w", err)
	}

	_, err = db.Exec(`
		DELETE FROM chains WHERE code IN ('ETHEREUM', 'BSC', 'TRON')
		AND NOT EXISTS (SELECT 1 FROM coin_chains WHERE coin_chains.chain_code = chains.code);
	`)
	if err != nil {
		return fmt.Errorf("failed to delete phase1 chains: %w", err)
	}

	return nil
}

var _ migration.Migration = (*SeedSafeheronPhase1)(nil)
