package currency

import "strings"

// Supported currencies following token_network format
// 注意: USDC_BEP20 使用长格式，其他使用短格式
const (
	USDT_ERC20        = "USDT_ERC20"
	USDT_TRC20        = "USDT_TRC20"
	USDT_BEP20        = "USDT_BEP20"
	USDT_TRON_TESTNET = "USDT_TRON_TESTNET"
	USDC_ERC20        = "USDC_ERC20"
	USDC_TRC20        = "USDC_TRC20"
	USDC_BEP20        = "USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET" // 特例：长格式
	USDC_TRON_TESTNET = "USDC_TRON_TESTNET"
)

// ShortFormatToFull maps short currency codes to their full backend format
// 注意：只有 USDC_BEP20 需要映射到长格式
var ShortFormatToFull = map[string]string{
	"USDT_ERC20":        USDT_ERC20,
	"USDT_TRC20":        USDT_TRC20,
	"USDT_BEP20":        USDT_BEP20,
	"USDT_TRON_TESTNET": USDT_TRON_TESTNET,
	"USDC_ERC20":        USDC_ERC20,
	"USDC_TRC20":        USDC_TRC20,
	"USDC_BEP20":        USDC_BEP20, // 映射到长格式
	"USDC_TRON_TESTNET": USDC_TRON_TESTNET,
}

// SupportedNetworks contains all valid network identifiers
var SupportedNetworks = []string{
	"ERC20",
	"TRC20",
	"BEP20",
	"TRON_TESTNET",
	"TRX(SHASTA)_TRON_TESTNET",
}

// NetworkAliasMap maps common aliases to standard network names
var NetworkAliasMap = map[string]string{
	"TRON":                     "TRC20",
	"BSC":                      "BEP20",
	"ETH":                      "ERC20",
	"TRX(SHASTA)_TRON_TESTNET": "TRX(SHASTA)_TRON_TESTNET",
	// Aliases with spaces and hyphens
	"TRX (SHASTA) - TRON Testnet": "TRX(SHASTA)_TRON_TESTNET",
}

// NormalizeNetwork converts network aliases to standard names
func NormalizeNetwork(network string) string {
	if normalized, ok := NetworkAliasMap[network]; ok {
		return normalized
	}
	return network
}

// SupportedCurrencies contains all valid currency tokens
var SupportedCurrencies = []string{
	"USDT",
	"USDC",
}

// AllSupportedCurrencies returns all valid currency strings (DB存储格式)
var AllSupportedCurrencies = []string{
	USDT_ERC20,
	USDT_TRC20,
	USDT_BEP20,
	USDT_TRON_TESTNET,
	USDC_ERC20,
	USDC_TRC20,
	USDC_BEP20, // 长格式
	USDC_TRON_TESTNET,
}

// CurrencyLabelMap provides display labels for currencies (使用DB存储格式作为key)
var CurrencyLabelMap = map[string]string{
	USDT_ERC20:        "USDT (ERC20)",
	USDT_TRC20:        "USDT (TRC20)",
	USDT_BEP20:        "USDT (BEP20)",
	USDT_TRON_TESTNET: "USDT (TRON Testnet)",
	USDC_ERC20:        "USDC (ERC20)",
	USDC_TRC20:        "USDC (TRC20)",
	USDC_BEP20:        "USDC (BEP20)",
	USDC_TRON_TESTNET: "USDC (TRON Testnet)",
}

// ShortFormatLabelMap provides display labels for short currency codes
var ShortFormatLabelMap = map[string]string{
	"USDT_ERC20":        "USDT (ERC20)",
	"USDT_TRC20":        "USDT (TRC20)",
	"USDT_BEP20":        "USDT (BEP20)",
	"USDT_TRON_TESTNET": "USDT (TRON Testnet)",
	"USDC_ERC20":        "USDC (ERC20)",
	"USDC_TRC20":        "USDC (TRC20)",
	"USDC_BEP20":        "USDC (BEP20)",
	"USDC_TRON_TESTNET": "USDC (TRON Testnet)",
}

// NetworkFromCurrency extracts the network part from currency (e.g., "ERC20" from "USDT_ERC20")
// For USDC_BEP20_BINANCE_SMART_CHAIN_MAINNET, returns "BEP20"
func NetworkFromCurrency(currency string) string {
	// 特殊处理 USDC_BEP20 长格式
	if currency == USDC_BEP20 {
		return "BEP20"
	}
	parts := strings.SplitN(currency, "_", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// TokenFromCurrency extracts the token part from currency (e.g., "USDT" from "USDT_ERC20")
func TokenFromCurrency(currency string) string {
	// 特殊处理 USDC_BEP20 长格式
	if currency == USDC_BEP20 {
		return "USDC"
	}
	parts := strings.SplitN(currency, "_", 2)
	if len(parts) >= 1 {
		return parts[0]
	}
	return currency
}

// IsValid checks if the currency format is valid
// 接受DB存储格式（USDC_BEP20为长格式，其他为短格式）
func IsValid(currency string) bool {
	if currency == "" {
		return false
	}

	// Check if it's in the supported list (DB存储格式)
	for _, c := range AllSupportedCurrencies {
		if c == currency {
			return true
		}
	}

	// Also accept short format for USDC_BEP20 (will be converted to full format)
	if currency == "USDC_BEP20" {
		return true
	}

	return false
}

// ToFullFormat converts short currency format to full backend format
// 只有 USDC_BEP20 需要转换，其他返回原值
func ToFullFormat(currency string) string {
	if full, ok := ShortFormatToFull[currency]; ok {
		return full
	}
	return currency
}

// ToShortFormat converts full backend format to short format
// 只有 USDC_BEP20 长格式需要转换
func ToShortFormat(currency string) string {
	// Check if it's a full format and convert back to short
	for short, full := range ShortFormatToFull {
		if full == currency {
			return short
		}
	}
	return currency
}

// BuildCurrency creates a currency string from token and network
// 返回短格式，后续通过 ToFullFormat 转换 USDC_BEP20
func BuildCurrency(token, network string) string {
	// Special case: TRX(SHASTA)_TRON_TESTNET
	if network == "TRX(SHASTA)_TRON_TESTNET" {
		if token == "" || token == "TRX" {
			return network
		}
		return token + "_TRON_TESTNET"
	}
	return token + "_" + network
}

// FormatForDisplay returns a human-readable label for the currency
func FormatForDisplay(currency string) string {
	// Try DB format first
	if label, ok := CurrencyLabelMap[currency]; ok {
		return label
	}
	// Try short format
	if label, ok := ShortFormatLabelMap[currency]; ok {
		return label
	}
	return currency
}

// SupportedCurrenciesOptions returns frontend options for currency selection
func SupportedCurrenciesOptions() []struct {
	Value string
	Label string
} {
	options := make([]struct {
		Value string
		Label string
	}, len(AllSupportedCurrencies))

	for i, c := range AllSupportedCurrencies {
		options[i] = struct {
			Value string
			Label string
		}{
			Value: c,
			Label: CurrencyLabelMap[c],
		}
	}
	return options
}
