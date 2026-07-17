package safeheron

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Safeheron/safeheron-api-sdk-go/safeheron/api"
)

type mockCoinAPI struct {
	listCoinFn func(*api.CoinResponse) error
}

func (m *mockCoinAPI) ListCoin(response *api.CoinResponse) error {
	return m.listCoinFn(response)
}

func TestClientListCoinMapsSDKMetadata(t *testing.T) {
	client := &Client{coin: &mockCoinAPI{listCoinFn: func(response *api.CoinResponse) error {
		return json.Unmarshal([]byte(`[{"coinKey":"ETHEREUM_USDT","coinName":"USDT","symbol":"USDT","coinDecimal":6,"feeCoinKey":"ETHEREUM_ETH","blockChain":"Ethereum","blockchainType":"EVM","network":"MAINNET","tokenIdentifier":"0xdac17f958d2ee523a2206206994597c13d831ec7"}]`), response)
	}}}

	coins, err := client.ListCoin(t.Context())
	if err != nil {
		t.Fatalf("ListCoin() error = %v", err)
	}
	want := Coin{
		CoinKey:         "ETHEREUM_USDT",
		CoinName:        "USDT",
		Symbol:          "USDT",
		CoinDecimal:     6,
		FeeCoinKey:      "ETHEREUM_ETH",
		BlockChain:      "Ethereum",
		BlockchainType:  "EVM",
		Network:         "MAINNET",
		TokenIdentifier: "0xdac17f958d2ee523a2206206994597c13d831ec7",
	}
	if len(coins) != 1 || coins[0] != want {
		t.Fatalf("ListCoin() = %#v, want %#v", coins, []Coin{want})
	}
}

func TestClientListCoinAllowsEmptyCatalog(t *testing.T) {
	client := &Client{coin: &mockCoinAPI{listCoinFn: func(response *api.CoinResponse) error {
		*response = api.CoinResponse{}
		return nil
	}}}

	coins, err := client.ListCoin(t.Context())
	if err != nil || len(coins) != 0 {
		t.Fatalf("ListCoin() = %#v, %v; want empty catalog", coins, err)
	}
}

func TestClientListCoinDoesNotExposeSDKErrorDetails(t *testing.T) {
	const secret = "safeheron-secret-api-key"
	client := &Client{coin: &mockCoinAPI{listCoinFn: func(*api.CoinResponse) error {
		return errors.New("request authorization failed for " + secret)
	}}}

	_, err := client.ListCoin(t.Context())
	if err == nil {
		t.Fatal("ListCoin() error = nil, want sanitized error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("ListCoin() error leaked credential: %v", err)
	}
}

func TestClientListCoinRejectsUnconfiguredCoinAPI(t *testing.T) {
	_, err := (&Client{}).ListCoin(t.Context())
	if err == nil {
		t.Fatal("ListCoin() error = nil, want configuration error")
	}
}
