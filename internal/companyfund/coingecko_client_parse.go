package companyfund

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

func normalizeCoinGeckoValues(label string, values []string, lowercase bool) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("coingecko %s must not be empty", label)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeCoinGeckoValue(label, value, lowercase)
		if err != nil {
			return nil, err
		}
		identity := normalized
		if !lowercase {
			identity = strings.ToLower(normalized)
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeCoinGeckoValue(label, value string, lowercase bool) (string, error) {
	normalized := strings.TrimSpace(value)
	if lowercase {
		normalized = strings.ToLower(normalized)
	}
	if normalized == "" || strings.ContainsAny(normalized, ",/?#") {
		return "", fmt.Errorf("coingecko %s contains an invalid identifier", label)
	}
	return normalized, nil
}

func readCoinGeckoResponseBody(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxCoinGeckoResponseBytes+1)
	contents, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read coingecko response: %w", err)
	}
	if len(contents) > maxCoinGeckoResponseBytes {
		return nil, ErrCoinGeckoResponseTooLarge
	}
	return contents, nil
}

func decodeCoinGeckoPricePayload(body []byte) (map[string]map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var decoded map[string]map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, ErrCoinGeckoMalformedResponse
	}
	if err := ensureCoinGeckoJSONEOF(decoder); err != nil {
		return nil, err
	}
	payload := make(map[string]map[string]any, len(decoded))
	for asset, fields := range decoded {
		normalizedFields := make(map[string]any, len(fields))
		for field, value := range fields {
			normalizedFields[strings.ToLower(field)] = value
		}
		payload[strings.ToLower(asset)] = normalizedFields
	}
	return payload, nil
}

func ensureCoinGeckoJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrCoinGeckoMalformedResponse
	}
	return nil
}

func parseCoinGeckoDecimal(value any) (decimal.Decimal, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = typed
	default:
		return decimal.Zero, false
	}
	price, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, false
	}
	return price, true
}

func parseCoinGeckoUnixTime(value any) (time.Time, bool) {
	var text string
	switch typed := value.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = typed
	default:
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(text, 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0).UTC(), true
}

func parseCoinGeckoRetryAfter(value string, now time.Time) *time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64(maxCoinGeckoRetryAfterDuration/time.Second) {
			return nil
		}
		duration := time.Duration(seconds) * time.Second
		return &duration
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return nil
	}
	duration := when.Sub(now)
	if duration < 0 {
		duration = 0
	}
	if duration > maxCoinGeckoRetryAfterDuration {
		return nil
	}
	return &duration
}

const maxCoinGeckoRetryAfterDuration = 15 * time.Minute

type coinGeckoExchangeRatesPayload struct {
	Rates map[string]coinGeckoExchangeRatePayload `json:"rates"`
}

type coinGeckoExchangeRatePayload struct {
	Unit  string      `json:"unit"`
	Type  string      `json:"type"`
	Value json.Number `json:"value"`
}

// decodeCoinGeckoExchangeRatesPayload preserves provider numeric literals as
// json.Number and converts them straight to decimal. The response's BTC
// relative values are retained exactly; derived USD-per-fiat happens later
// only when USD and the explicitly configured fiat code are both present.
func decodeCoinGeckoExchangeRatesPayload(body []byte) (map[string]CoinGeckoExchangeRate, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var payload coinGeckoExchangeRatesPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, ErrCoinGeckoMalformedResponse
	}
	if err := ensureCoinGeckoJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(payload.Rates) == 0 {
		return nil, ErrCoinGeckoMalformedResponse
	}
	rates := make(map[string]CoinGeckoExchangeRate, len(payload.Rates))
	for providerCode, raw := range payload.Rates {
		code, ok := normalizeCoinGeckoExchangeRateCode(providerCode)
		if !ok {
			return nil, ErrCoinGeckoMalformedResponse
		}
		value, ok := parseCoinGeckoDecimal(raw.Value)
		if !ok || !value.GreaterThan(decimal.Zero) {
			return nil, ErrCoinGeckoMalformedResponse
		}
		unit := strings.TrimSpace(raw.Unit)
		rateType := strings.ToLower(strings.TrimSpace(raw.Type))
		if unit == "" || rateType == "" {
			return nil, ErrCoinGeckoMalformedResponse
		}
		if _, exists := rates[code]; exists {
			return nil, ErrCoinGeckoMalformedResponse
		}
		rates[code] = CoinGeckoExchangeRate{
			Code: code, Unit: unit, Type: rateType, Value: value,
		}
	}
	return rates, nil
}

func normalizeCoinGeckoExchangeRateCode(value string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if len(normalized) < 2 || len(normalized) > 16 {
		return "", false
	}
	for _, character := range normalized {
		if character < 'A' || character > 'Z' {
			return "", false
		}
	}
	return normalized, true
}
