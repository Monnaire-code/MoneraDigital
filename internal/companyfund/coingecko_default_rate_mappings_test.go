package companyfund

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCoinGeckoDefaultRateMappingsJSON_UsesApprovedDefaultsForEmptyInput(t *testing.T) {
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatalf("ParseCoinGeckoDefaultRateMappingsJSON() error = %v", err)
	}

	wantCrypto := map[string]string{
		"BTC":  "bitcoin",
		"ETH":  "ethereum",
		"BNB":  "binancecoin",
		"USDT": "tether",
		"USDC": "usd-coin",
	}
	if !reflect.DeepEqual(mappings.Crypto, wantCrypto) {
		t.Fatalf("default crypto mappings = %#v, want %#v", mappings.Crypto, wantCrypto)
	}
	if want := []string{"CNY", "HKD", "SGD", "USD"}; !reflect.DeepEqual(mappings.Fiat, want) {
		t.Fatalf("default fiat mappings = %#v, want %#v", mappings.Fiat, want)
	}
	if _, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{Currency: "USD"}, mappings); ok {
		t.Fatal("USD parity must not create a provider cache key")
	}
}

func TestParseCoinGeckoDefaultRateMappingsJSON_NormalizesFriendlyStrictConfig(t *testing.T) {
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON([]byte(`{
		"crypto":{"btc":"Bitcoin","USDT":"tether","ETH":"ethereum"},
		"fiat":["usd","sgd","CNY","sgd"]
	}`))
	if err != nil {
		t.Fatalf("ParseCoinGeckoDefaultRateMappingsJSON() error = %v", err)
	}
	if want := map[string]string{"BTC": "bitcoin", "ETH": "ethereum", "USDT": "tether"}; !reflect.DeepEqual(mappings.Crypto, want) {
		t.Fatalf("normalized crypto mappings = %#v, want %#v", mappings.Crypto, want)
	}
	if want := []string{"CNY", "SGD", "USD"}; !reflect.DeepEqual(mappings.Fiat, want) {
		t.Fatalf("normalized fiat mappings = %#v, want %#v", mappings.Fiat, want)
	}

	for _, raw := range []string{
		`{"crypto":{"BTC":"bitcoin"},"fiat":["USD"],"unknown":true}`,
		`{"crypto":{"USD":"usd"},"fiat":["USD"]}`,
		`{"crypto":{"BTC":"bitcoin"},"fiat":["JPY"]}`,
		`{"crypto":{"ETH":"ethereum"},"fiat":["USD","SGD"]}`,
		`{"crypto":{"BTC":"bitcoin","btc":"wrapped-bitcoin"},"fiat":["USD"]}`,
		`{"crypto":{"BTC":"bitcoin","SGD":"sgd-token"},"fiat":["USD","SGD"]}`,
		`{"crypto":{"BTC":"bit/coin"},"fiat":["USD"]}`,
		`{"crypto":{"BTC":"bitcoin"},"fiat":["US-D"]}`,
		`{"crypto":{"BTC":"bitcoin"},"fiat":["USD"]} {}`,
		`{"crypto":{"BTC":"bitcoin"},"fiat":["USD"]} ]`,
		strings.Repeat("x", maxCoinGeckoDefaultRateMappingsBytes+1),
	} {
		if _, err := ParseCoinGeckoDefaultRateMappingsJSON([]byte(raw)); err == nil {
			t.Fatalf("ParseCoinGeckoDefaultRateMappingsJSON(%s) error = nil, want strict rejection", raw)
		}
	}
}

func TestCoinGeckoQuoteCacheKeyForDefault_IsCurrencyScoped(t *testing.T) {
	mappings, err := ParseCoinGeckoDefaultRateMappingsJSON(nil)
	if err != nil {
		t.Fatal(err)
	}

	first, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{
		Currency: "usdt", ChainCode: "BINANCE_SMART_CHAIN", ProviderAssetKey: "USDT_BEP20",
	}, mappings)
	if !ok || first.CoinID != "tether" || first.QuoteCurrency != "USD" {
		t.Fatalf("BSC USDT default key = %#v, %v", first, ok)
	}
	second, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{
		Currency: "USDT", ChainCode: "ETHEREUM", ProviderAssetKey: "USDT_ERC20",
	}, mappings)
	if !ok || first != second {
		t.Fatalf("default currency keys differ by provider identity: %#v != %#v", first, second)
	}
	if _, ok := CoinGeckoQuoteCacheKeyForDefault(AssetIdentity{Currency: "DOGE"}, mappings); ok {
		t.Fatal("unconfigured currency must not receive an inferred mapping")
	}
}
