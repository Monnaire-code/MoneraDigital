package companyfund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

const maxCoinGeckoDefaultRateMappingsBytes = 16 << 10

var approvedCoinGeckoDefaultRateMappings = CoinGeckoDefaultRateMappings{
	Crypto: map[string]string{
		"BTC":  "bitcoin",
		"ETH":  "ethereum",
		"BNB":  "binancecoin",
		"USDT": "tether",
		"USDC": "usd-coin",
	},
	Fiat: []string{"CNY", "HKD", "SGD", "USD"},
}

// CoinGeckoDefaultRateMappings is the system-wide fallback for recognized
// assets with no explicit account-level valuation mapping. An enabled policy
// whose valuation mapping fields are all blank still permits this fallback.
// Crypto maps an internal currency code to an explicit CoinGecko coin ID;
// Fiat lists currencies priced through an auditable BTC cross. USD remains
// ledger parity and therefore never creates a provider request or cache key.
type CoinGeckoDefaultRateMappings struct {
	Crypto map[string]string `json:"crypto"`
	Fiat   []string          `json:"fiat"`
}

// ParseCoinGeckoDefaultRateMappingsJSON accepts one compact JSON environment
// value. Empty input uses the reviewed defaults; non-empty input is strict and
// does not infer provider IDs from currency symbols.
func ParseCoinGeckoDefaultRateMappingsJSON(raw []byte) (CoinGeckoDefaultRateMappings, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return normalizeCoinGeckoDefaultRateMappings(approvedCoinGeckoDefaultRateMappings)
	}
	if len(trimmed) > maxCoinGeckoDefaultRateMappingsBytes {
		return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default rate mappings exceed %d bytes", maxCoinGeckoDefaultRateMappingsBytes)
	}

	var decoded CoinGeckoDefaultRateMappings
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return CoinGeckoDefaultRateMappings{}, fmt.Errorf("decode CoinGecko default rate mappings: %w", err)
	}
	if err := ensureCoinGeckoDefaultMappingsEOF(decoder); err != nil {
		return CoinGeckoDefaultRateMappings{}, err
	}
	return normalizeCoinGeckoDefaultRateMappings(decoded)
}

func normalizeCoinGeckoDefaultRateMappings(input CoinGeckoDefaultRateMappings) (CoinGeckoDefaultRateMappings, error) {
	result := CoinGeckoDefaultRateMappings{Crypto: make(map[string]string, len(input.Crypto))}
	for rawCurrency, rawCoinID := range input.Crypto {
		currency := strings.ToUpper(strings.TrimSpace(rawCurrency))
		if !validCoinGeckoConfiguredFiatCode(currency) || currency == "USD" {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default crypto currency %q is invalid", rawCurrency)
		}
		coinID, err := normalizeCoinGeckoValue("default coin ID", rawCoinID, true)
		if err != nil {
			return CoinGeckoDefaultRateMappings{}, err
		}
		if existing, found := result.Crypto[currency]; found && existing != coinID {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default crypto currency %s is configured more than once", currency)
		}
		result.Crypto[currency] = coinID
	}

	fiatSet := make(map[string]struct{}, len(input.Fiat))
	for _, rawCurrency := range input.Fiat {
		currency := strings.ToUpper(strings.TrimSpace(rawCurrency))
		if !validCoinGeckoConfiguredFiatCode(currency) {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default fiat currency %q is invalid", rawCurrency)
		}
		if _, found := result.Crypto[currency]; found {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default currency %s cannot be both crypto and fiat", currency)
		}
		fiatSet[currency] = struct{}{}
	}
	if len(fiatSet) > 0 {
		if _, found := fiatSet["USD"]; !found {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default fiat mappings must include USD parity")
		}
	}
	if len(fiatSet) > 1 {
		if coinID := result.Crypto["BTC"]; coinID == "" {
			return CoinGeckoDefaultRateMappings{}, fmt.Errorf("CoinGecko default fiat mappings require an explicit BTC coin ID")
		}
	}
	result.Fiat = make([]string, 0, len(fiatSet))
	for currency := range fiatSet {
		result.Fiat = append(result.Fiat, currency)
	}
	sort.Strings(result.Fiat)
	return result, nil
}

func ensureCoinGeckoDefaultMappingsEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode CoinGecko default rate mappings: %w", err)
	}
	return fmt.Errorf("decode CoinGecko default rate mappings: multiple JSON values are not allowed")
}

// CoinGeckoQuoteCacheKeyForDefault returns the currency-scoped fallback key.
// Chain, provider key, and contract are deliberately excluded so one reviewed
// system mapping can price the same recognized currency across providers.
func CoinGeckoQuoteCacheKeyForDefault(asset AssetIdentity, mappings CoinGeckoDefaultRateMappings) (CoinGeckoQuoteCacheKey, bool) {
	currency := strings.ToUpper(strings.TrimSpace(asset.Currency))
	if currency == "" || currency == "USD" {
		return CoinGeckoQuoteCacheKey{}, false
	}
	assetKey := normalizeAssetIdentity(AssetIdentity{Currency: currency}).canonicalKey()
	if coinID := strings.ToLower(strings.TrimSpace(mappings.Crypto[currency])); coinID != "" {
		key := CoinGeckoQuoteCacheKey{
			Provider:         rateSnapshotCoinGeckoProvider,
			AssetIdentityKey: assetKey,
			CoinID:           coinID,
			QuoteCurrency:    "USD",
		}
		return key, key.validate() == nil
	}
	for _, fiat := range mappings.Fiat {
		if strings.EqualFold(strings.TrimSpace(fiat), currency) {
			key := CoinGeckoQuoteCacheKey{
				Provider:         rateSnapshotCoinGeckoProvider,
				AssetIdentityKey: assetKey,
				FiatCode:         currency,
				QuoteCurrency:    "USD",
			}
			return key, key.validate() == nil
		}
	}
	return CoinGeckoQuoteCacheKey{}, false
}
