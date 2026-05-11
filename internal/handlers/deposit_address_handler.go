package handlers

import (
	"context"
	"errors"
	"net/http"

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
// supported-chains endpoints.
type ChainsRegistry interface {
	ListEnabledCoinChainsByFamily(family string) []*walletconfig.CoinChain
	AllChains() []*walletconfig.Chain
	ListEnabledCoinChainsByChain(chainCode string) []*walletconfig.CoinChain
}

type supportedCoin struct {
	ChainCode  string `json:"chainCode"`
	Symbol     string `json:"symbol"`
	MinDeposit string `json:"minDeposit"`
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

	family := c.Query("network_family")
	if !allowedNetworkFamilies[family] {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_NETWORK_FAMILY",
			"message": "network_family must be EVM or TRON",
		})
		return
	}

	if h.PoolManager == nil || h.WalletRegistry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "WALLET_UNAVAILABLE",
			"message": "Safeheron wallet pool not initialized",
		})
		return
	}

	addr, err := h.PoolManager.GetOrAssign(c.Request.Context(), userID, family)
	if err != nil {
		if errors.Is(err, pool.ErrPoolEmpty) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":   "POOL_EMPTY",
				"message": "Deposit address pool temporarily exhausted, please retry shortly",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "ASSIGN_FAILED",
			"message": err.Error(),
		})
		return
	}

	coins := h.WalletRegistry.ListEnabledCoinChainsByFamily(family)
	supported := make([]supportedCoin, 0, len(coins))
	for _, cc := range coins {
		var symbol string
		if cc.Coin != nil {
			symbol = cc.Coin.Symbol
		}
		supported = append(supported, supportedCoin{
			ChainCode:  cc.ChainCode,
			Symbol:     symbol,
			MinDeposit: cc.MinDepositAmount,
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

	if h.WalletRegistry == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "REGISTRY_UNAVAILABLE",
			"message": "Wallet config registry not initialized",
		})
		return
	}

	chains := h.WalletRegistry.AllChains()
	out := make([]supportedChain, 0, len(chains))
	for _, ch := range chains {
		ccs := h.WalletRegistry.ListEnabledCoinChainsByChain(ch.Code)
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
