package config

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

type AlertFunc func(level, title, message string)

type Registry struct {
	mu              sync.RWMutex
	chains          map[string]*Chain
	coins           map[string]*Coin
	coinsByID       map[int]*Coin
	coinChains      map[string]*CoinChain
	bySHKey         map[string]*CoinChain
	byChain         map[string][]*CoinChain
	byFamily        map[string][]*CoinChain
	repo            Repository
	refreshInterval time.Duration
	onAlert         AlertFunc
}

func NewRegistry(repo Repository, refreshInterval time.Duration) *Registry {
	if refreshInterval <= 0 {
		refreshInterval = 60 * time.Second
	}
	return &Registry{
		repo:            repo,
		refreshInterval: refreshInterval,
	}
}

func (r *Registry) SetAlertFunc(fn AlertFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onAlert = fn
}

func (r *Registry) Load(ctx context.Context) error {
	chains, coins, coinChains, err := r.repo.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("registry load: %w", err)
	}

	chainMap := make(map[string]*Chain, len(chains))
	for _, c := range chains {
		chainMap[c.Code] = c
	}

	coinMap := make(map[string]*Coin, len(coins))
	coinByIDMap := make(map[int]*Coin, len(coins))
	for _, c := range coins {
		coinMap[c.Symbol] = c
		coinByIDMap[c.ID] = c
	}

	ccMap := make(map[string]*CoinChain, len(coinChains))
	shKeyMap := make(map[string]*CoinChain, len(coinChains))
	byChainMap := make(map[string][]*CoinChain)
	byFamilyMap := make(map[string][]*CoinChain)

	for _, cc := range coinChains {
		cc.Chain = chainMap[cc.ChainCode]
		cc.Coin = coinByIDMap[cc.CoinID]

		var coinSymbol string
		if cc.Coin != nil {
			coinSymbol = cc.Coin.Symbol
		}
		key := cc.ChainCode + "|" + coinSymbol
		ccMap[key] = cc
		shKeyMap[cc.SafeheronCoinKey] = cc
		byChainMap[cc.ChainCode] = append(byChainMap[cc.ChainCode], cc)

		if cc.Chain != nil {
			byFamilyMap[cc.Chain.NetworkFamily] = append(byFamilyMap[cc.Chain.NetworkFamily], cc)
		}
	}

	r.mu.Lock()
	r.chains = chainMap
	r.coins = coinMap
	r.coinsByID = coinByIDMap
	r.coinChains = ccMap
	r.bySHKey = shKeyMap
	r.byChain = byChainMap
	r.byFamily = byFamilyMap
	r.mu.Unlock()

	log.Printf("Registry loaded: chains=%d coins=%d coin_chains=%d", len(chains), len(coins), len(coinChains))
	return nil
}

func (r *Registry) StartBackgroundRefresh(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(r.refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := r.Load(ctx); err != nil {
					log.Printf("Registry refresh failed: %v", err)
					r.mu.RLock()
					alertFn := r.onAlert
					r.mu.RUnlock()
					if alertFn != nil {
						alertFn("WARN", "Registry refresh failed", err.Error())
					}
				}
			}
		}
	}()
}

func (r *Registry) GetChain(code string) (*Chain, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.chains[code]
	return c, ok
}

func (r *Registry) GetCoin(symbol string) (*Coin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.coins[symbol]
	return c, ok
}

func (r *Registry) GetCoinByID(id int) (*Coin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.coinsByID[id]
	return c, ok
}

func (r *Registry) GetCoinChain(chainCode, coinSymbol string) (*CoinChain, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cc, ok := r.coinChains[chainCode+"|"+coinSymbol]
	return cc, ok
}

func (r *Registry) GetCoinChainBySafeheronKey(key string) (*CoinChain, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cc, ok := r.bySHKey[key]
	return cc, ok
}

func (r *Registry) ListEnabledCoinChainsByChain(chainCode string) []*CoinChain {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.byChain[chainCode]
	out := make([]*CoinChain, len(src))
	copy(out, src)
	return out
}

func (r *Registry) ListEnabledCoinChainsByFamily(family string) []*CoinChain {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.byFamily[family]
	out := make([]*CoinChain, len(src))
	copy(out, src)
	return out
}

func (r *Registry) SafeheronCoinKeysByFamily(family string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ccs := r.byFamily[family]
	keys := make([]string, 0, len(ccs))
	for _, cc := range ccs {
		keys = append(keys, cc.SafeheronCoinKey)
	}
	return keys
}

// AllChains returns the enabled chains. Disabled chains are filtered out so
// the supported-chains endpoint never advertises a chain the ops team has
// turned off (e.g. during incident response). T6-S-1.
func (r *Registry) AllChains() []*Chain {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*Chain, 0, len(r.chains))
	for _, c := range r.chains {
		if c.Enabled {
			result = append(result, c)
		}
	}
	return result
}
