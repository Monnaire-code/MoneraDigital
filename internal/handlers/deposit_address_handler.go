package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"

	walletconfig "monera-digital/internal/wallet/config"
	"monera-digital/internal/wallet/pool"
)

// DepositPoolManager is the narrow interface the deposit-address handler needs
// from the address pool. Defined here for testability.
type DepositPoolManager interface {
	GetOrAssign(ctx context.Context, userID int, networkFamily string) (*pool.Address, error)
}

// ChainsRegistry is the narrow Registry view used by deposit-address /
// supported-chains / deposit-coins endpoints.
type ChainsRegistry interface {
	ListEnabledCoinChainsByFamily(family string) []*walletconfig.CoinChain
	AllChains() []*walletconfig.Chain
	ListEnabledCoinChainsByChain(chainCode string) []*walletconfig.CoinChain
	AllEnabledCoinChains() []*walletconfig.CoinChain
}

// supportedCoin mirrors the TS `SupportedCoin` shape in src/lib/wallet-service.ts.
// coinKey + decimals are required: the frontend uses `${chainCode}-${coinKey}`
// as a React row key (deduplicates multiple coins on the same chain) and reads
// decimals to format on-chain amounts.
type supportedCoin struct {
	ChainCode  string `json:"chainCode"`
	Symbol     string `json:"symbol"`
	CoinKey    string `json:"coinKey"`
	MinDeposit string `json:"minDeposit"`
	Decimals   int    `json:"decimals"`
}

type depositAddressResponse struct {
	Address        string          `json:"address"`
	NetworkFamily  string          `json:"networkFamily"`
	SupportedCoins []supportedCoin `json:"supportedCoins"`
}

type chainCoin struct {
	Symbol     string `json:"symbol"`
	IsNative   bool   `json:"isNative"`
	MinDeposit string `json:"minDeposit"`
	Decimals   int    `json:"decimals"`
}

type supportedChain struct {
	Code          string      `json:"code"`
	Name          string      `json:"name"`
	NetworkFamily string      `json:"networkFamily"`
	NativeSymbol  string      `json:"nativeSymbol"`
	ExplorerURL   string      `json:"explorerUrl,omitempty"`
	Coins         []chainCoin `json:"coins"`
}

type supportedChainsResponse struct {
	Chains []supportedChain `json:"chains"`
}

var allowedNetworkFamilies = map[string]bool{
	"EVM":  true,
	"TRON": true,
}

// GetDepositAddress assigns (or returns existing) a Safeheron-backed deposit
// address for the authenticated user.
func (h *Handler) GetDepositAddress(c *gin.Context) {
	userID, err := h.getUserID(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	// R2-C-1: query param is camelCase per CLAUDE.md JSON naming convention.
	// Frontend (wallet-service.ts) sends `?networkFamily=EVM`; reading snake_case
	// here would always 400 in production while unit tests passed.
	family := c.Query("networkFamily")
	if !allowedNetworkFamilies[family] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_NETWORK_FAMILY",
			"message": "networkFamily must be EVM or TRON",
		})
		return
	}

	if h.poolManager == nil || h.walletRegistry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "WALLET_UNAVAILABLE",
			"message": "Safeheron wallet pool not initialized",
		})
		return
	}

	addr, err := h.poolManager.GetOrAssign(c.Request.Context(), userID, family)
	if err != nil {
		if errors.Is(err, pool.ErrPoolEmpty) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "POOL_EMPTY",
				"message": "Deposit address pool temporarily exhausted, please retry shortly",
			})
			return
		}
		// T6-I-3: never echo raw err.Error() — DB errors / SQL fragments must
		// stay in server logs, not response bodies.
		log.Printf("deposit-address assign failed userId=%d family=%s: %v", userID, family, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "ASSIGN_FAILED",
			"message": "Failed to assign deposit address, please retry shortly",
		})
		return
	}

	coins := h.walletRegistry.ListEnabledCoinChainsByFamily(family)
	supported := make([]supportedCoin, 0, len(coins))
	for _, cc := range coins {
		var symbol string
		if cc.Coin != nil {
			symbol = cc.Coin.Symbol
		}
		supported = append(supported, supportedCoin{
			ChainCode:  cc.ChainCode,
			Symbol:     symbol,
			CoinKey:    cc.SafeheronCoinKey,
			MinDeposit: cc.MinDepositAmount,
			Decimals:   cc.Decimals,
		})
	}

	c.JSON(http.StatusOK, depositAddressResponse{
		Address:        addr.Address,
		NetworkFamily:  addr.NetworkFamily,
		SupportedCoins: supported,
	})
}

// GetSupportedChains returns all enabled chains grouped with their enabled
// coin_chains for the deposit UI.
func (h *Handler) GetSupportedChains(c *gin.Context) {
	if _, err := h.getUserID(c); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	if h.walletRegistry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "REGISTRY_UNAVAILABLE",
			"message": "Wallet config registry not initialized",
		})
		return
	}

	chains := h.walletRegistry.AllChains()
	out := make([]supportedChain, 0, len(chains))
	for _, ch := range chains {
		ccs := h.walletRegistry.ListEnabledCoinChainsByChain(ch.Code)
		coins := make([]chainCoin, 0, len(ccs))
		for _, cc := range ccs {
			var symbol string
			if cc.Coin != nil {
				symbol = cc.Coin.Symbol
			}
			coins = append(coins, chainCoin{
				Symbol:     symbol,
				IsNative:   cc.IsNative,
				MinDeposit: cc.MinDepositAmount,
				Decimals:   cc.Decimals,
			})
		}
		out = append(out, supportedChain{
			Code:          ch.Code,
			Name:          ch.Name,
			NetworkFamily: ch.NetworkFamily,
			NativeSymbol:  ch.NativeSymbol,
			ExplorerURL:   ch.ExplorerURL,
			Coins:         coins,
		})
	}

	c.JSON(http.StatusOK, supportedChainsResponse{Chains: out})
}

// DeprecatedWalletEndpoint returns HTTP 410 Gone with a pointer to the new
// deposit-address endpoint. Used to retire the legacy Core-API wallet flow.
func DeprecatedWalletEndpoint(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"error":   "DEPRECATED",
		"message": "Use GET /api/wallet/deposit-address instead",
	})
}

type coinNetwork struct {
	ChainCode               string  `json:"chainCode"`
	ChainName               string  `json:"chainName"`
	NetworkFamily           string  `json:"networkFamily"`
	ShortName               string  `json:"shortName"`
	TokenStandard           string  `json:"tokenStandard"`
	IsNative                bool    `json:"isNative"`
	TokenContract           *string `json:"tokenContract"`
	Decimals                int     `json:"decimals"`
	MinDeposit              string  `json:"minDeposit"`
	RequiredConfirmations   int     `json:"requiredConfirmations"`
	EstimatedArrivalMinutes int     `json:"estimatedArrivalMinutes"`
	ExplorerURL             string  `json:"explorerUrl"`
}

type depositCoin struct {
	Symbol   string        `json:"symbol"`
	Name     string        `json:"name"`
	IsStable bool          `json:"isStable"`
	Networks []coinNetwork `json:"networks"`
}

type depositCoinsResponse struct {
	Coins []depositCoin `json:"coins"`
}

func (h *Handler) GetDepositCoins(c *gin.Context) {
	if _, err := h.getUserID(c); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	if h.walletRegistry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "REGISTRY_UNAVAILABLE",
			"message": "Wallet config registry not initialized",
		})
		return
	}

	allCC := h.walletRegistry.AllEnabledCoinChains()

	type netWithOrder struct {
		net   coinNetwork
		order int
	}
	type coinEntry struct {
		coin     *walletconfig.Coin
		networks []netWithOrder
		order    int
	}
	bySymbol := make(map[string]*coinEntry)
	var symbolOrder []string

	for _, cc := range allCC {
		if cc.Coin == nil || cc.Chain == nil {
			continue
		}
		sym := cc.Coin.Symbol

		shortName := cc.Chain.ShortName
		if shortName == "" {
			shortName = cc.Chain.Code
		}

		var contract *string
		if cc.TokenContract != "" {
			s := cc.TokenContract
			contract = &s
		}

		net := coinNetwork{
			ChainCode:               cc.ChainCode,
			ChainName:               cc.Chain.Name,
			NetworkFamily:           cc.Chain.NetworkFamily,
			ShortName:               shortName,
			TokenStandard:           cc.TokenStandard,
			IsNative:                cc.IsNative,
			TokenContract:           contract,
			Decimals:                cc.Decimals,
			MinDeposit:              cc.MinDepositAmount,
			RequiredConfirmations:   cc.RequiredConfirmations,
			EstimatedArrivalMinutes: cc.EstimatedArrivalMinutes,
			ExplorerURL:             cc.Chain.ExplorerURL,
		}

		entry, exists := bySymbol[sym]
		if !exists {
			entry = &coinEntry{coin: cc.Coin, order: cc.Coin.DisplayOrder}
			bySymbol[sym] = entry
			symbolOrder = append(symbolOrder, sym)
		}
		entry.networks = append(entry.networks, netWithOrder{net: net, order: cc.DisplayOrder})
	}

	sort.Slice(symbolOrder, func(i, j int) bool {
		return bySymbol[symbolOrder[i]].order < bySymbol[symbolOrder[j]].order
	})

	coins := make([]depositCoin, 0, len(symbolOrder))
	for _, sym := range symbolOrder {
		e := bySymbol[sym]
		sort.Slice(e.networks, func(i, j int) bool {
			return e.networks[i].order < e.networks[j].order
		})
		nets := make([]coinNetwork, len(e.networks))
		for i, nwo := range e.networks {
			nets[i] = nwo.net
		}
		coins = append(coins, depositCoin{
			Symbol:   e.coin.Symbol,
			Name:     e.coin.Name,
			IsStable: e.coin.IsStable,
			Networks: nets,
		})
	}

	c.JSON(http.StatusOK, depositCoinsResponse{Coins: coins})
}
