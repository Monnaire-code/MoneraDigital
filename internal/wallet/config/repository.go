package config

import (
	"context"
	"database/sql"
	"fmt"
)

type Repository interface {
	LoadAll(ctx context.Context) ([]*Chain, []*Coin, []*CoinChain, error)
}

type DBRepository struct {
	db *sql.DB
}

func NewDBRepository(db *sql.DB) *DBRepository {
	return &DBRepository{db: db}
}

func (r *DBRepository) LoadAll(ctx context.Context) ([]*Chain, []*Coin, []*CoinChain, error) {
	chains, err := r.loadChains(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load chains: %w", err)
	}

	coins, err := r.loadCoins(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load coins: %w", err)
	}

	coinChains, err := r.loadCoinChains(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load coin_chains: %w", err)
	}

	return chains, coins, coinChains, nil
}

func (r *DBRepository) loadChains(ctx context.Context) ([]*Chain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT code, name, network_family, COALESCE(chain_id, ''), native_symbol, COALESCE(explorer_url, ''), enabled, display_order
		 FROM chains WHERE enabled = true ORDER BY display_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chains []*Chain
	for rows.Next() {
		c := &Chain{}
		if err := rows.Scan(&c.Code, &c.Name, &c.NetworkFamily, &c.ChainID, &c.NativeSymbol, &c.ExplorerURL, &c.Enabled, &c.DisplayOrder); err != nil {
			return nil, err
		}
		chains = append(chains, c)
	}
	return chains, rows.Err()
}

func (r *DBRepository) loadCoins(ctx context.Context) ([]*Coin, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, symbol, name, is_stable, enabled, display_order
		 FROM coins WHERE enabled = true ORDER BY display_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var coins []*Coin
	for rows.Next() {
		c := &Coin{}
		if err := rows.Scan(&c.ID, &c.Symbol, &c.Name, &c.IsStable, &c.Enabled, &c.DisplayOrder); err != nil {
			return nil, err
		}
		coins = append(coins, c)
	}
	return coins, rows.Err()
}

func (r *DBRepository) loadCoinChains(ctx context.Context) ([]*CoinChain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, chain_code, coin_id, is_native, COALESCE(token_contract, ''), decimals,
		        safeheron_coin_key, min_deposit_amount, deposit_enabled, withdraw_enabled,
		        required_confirmations, display_order
		 FROM coin_chains WHERE deposit_enabled = true ORDER BY display_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ccs []*CoinChain
	for rows.Next() {
		cc := &CoinChain{}
		if err := rows.Scan(&cc.ID, &cc.ChainCode, &cc.CoinID, &cc.IsNative, &cc.TokenContract,
			&cc.Decimals, &cc.SafeheronCoinKey, &cc.MinDepositAmount, &cc.DepositEnabled,
			&cc.WithdrawEnabled, &cc.RequiredConfirmations, &cc.DisplayOrder); err != nil {
			return nil, err
		}
		ccs = append(ccs, cc)
	}
	return ccs, rows.Err()
}
