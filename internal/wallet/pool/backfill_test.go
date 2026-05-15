package pool

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"monera-digital/internal/safeheron"
)

type addCoinCall struct {
	accountKey string
	coinKeys   []string
}

type mockBackfillClient struct {
	listFn       func(ctx context.Context, accountKey string) ([]safeheron.AccountCoin, error)
	addFn        func(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error)
	addCoinCalls []addCoinCall
}

func (m *mockBackfillClient) ListAccountCoin(ctx context.Context, accountKey string) ([]safeheron.AccountCoin, error) {
	return m.listFn(ctx, accountKey)
}

func (m *mockBackfillClient) AddCoin(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error) {
	m.addCoinCalls = append(m.addCoinCalls, addCoinCall{accountKey, append([]string(nil), coinKeys...)})
	if m.addFn != nil {
		return m.addFn(ctx, accountKey, coinKeys)
	}
	return &safeheron.Wallet{AccountKey: accountKey}, nil
}

func evmExpectations(keys ...string) FamilyExpectations {
	return func(family string) []string {
		if family == "EVM" {
			return keys
		}
		return nil
	}
}

func TestBackfillAddCoin_SkipsAccountsWithAllExpectedCoins(t *testing.T) {
	client := &mockBackfillClient{
		listFn: func(_ context.Context, _ string) ([]safeheron.AccountCoin, error) {
			return []safeheron.AccountCoin{{CoinKey: "ETH"}, {CoinKey: "BNB_BSC"}}, nil
		},
	}
	targets := []AccountTarget{{AccountKey: "acct-1", NetworkFamily: "EVM"}}

	results := BackfillAddCoin(context.Background(), client, targets, evmExpectations("ETH", "BNB_BSC"), false)

	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected 1 skipped result, got %+v", results)
	}
	if len(results[0].AddedCoins) != 0 {
		t.Errorf("AddedCoins should be empty when skipped, got %v", results[0].AddedCoins)
	}
	if len(client.addCoinCalls) != 0 {
		t.Errorf("AddCoin should not be called, got %v", client.addCoinCalls)
	}
}

func TestBackfillAddCoin_AddsMissingCoinsSortedAndUnique(t *testing.T) {
	client := &mockBackfillClient{
		listFn: func(_ context.Context, _ string) ([]safeheron.AccountCoin, error) {
			return []safeheron.AccountCoin{{CoinKey: "ETH"}}, nil
		},
	}
	targets := []AccountTarget{{AccountKey: "acct-2", NetworkFamily: "EVM", Address: "0xabc"}}

	results := BackfillAddCoin(
		context.Background(),
		client,
		targets,
		evmExpectations("USDT_BEP20", "ETH", "BNB_BSC"), // unsorted on purpose
		false,
	)

	wantMissing := []string{"BNB_BSC", "USDT_BEP20"}
	if len(client.addCoinCalls) != 1 {
		t.Fatalf("expected 1 AddCoin call, got %d", len(client.addCoinCalls))
	}
	got := client.addCoinCalls[0]
	if got.accountKey != "acct-2" || !reflect.DeepEqual(got.coinKeys, wantMissing) {
		t.Errorf("AddCoin call = %+v, want acct-2 / %v", got, wantMissing)
	}
	if !reflect.DeepEqual(results[0].AddedCoins, wantMissing) {
		t.Errorf("result.AddedCoins = %v, want %v", results[0].AddedCoins, wantMissing)
	}
	if results[0].Skipped {
		t.Error("Skipped should be false when there were missing coins")
	}
}

func TestBackfillAddCoin_DryRunReportsButDoesNotMutate(t *testing.T) {
	client := &mockBackfillClient{
		listFn: func(_ context.Context, _ string) ([]safeheron.AccountCoin, error) {
			return []safeheron.AccountCoin{{CoinKey: "ETH"}}, nil
		},
	}
	targets := []AccountTarget{{AccountKey: "acct-3", NetworkFamily: "EVM"}}

	results := BackfillAddCoin(context.Background(), client, targets, evmExpectations("ETH", "BNB_BSC"), true)

	if len(client.addCoinCalls) != 0 {
		t.Errorf("dry-run must not call AddCoin, got %v", client.addCoinCalls)
	}
	if !reflect.DeepEqual(results[0].AddedCoins, []string{"BNB_BSC"}) {
		t.Errorf("dry-run still reports planned additions, got %v", results[0].AddedCoins)
	}
	if results[0].Error != nil {
		t.Errorf("no error expected, got %v", results[0].Error)
	}
}

func TestBackfillAddCoin_ListFailureIsolatedToOneAccount(t *testing.T) {
	listErr := errors.New("api timeout")
	calls := 0
	client := &mockBackfillClient{
		listFn: func(_ context.Context, _ string) ([]safeheron.AccountCoin, error) {
			calls++
			if calls == 1 {
				return nil, listErr
			}
			return []safeheron.AccountCoin{{CoinKey: "ETH"}}, nil
		},
	}
	targets := []AccountTarget{
		{AccountKey: "acct-fail", NetworkFamily: "EVM"},
		{AccountKey: "acct-ok", NetworkFamily: "EVM"},
	}

	results := BackfillAddCoin(context.Background(), client, targets, evmExpectations("ETH"), false)

	if len(results) != 2 {
		t.Fatalf("expected 2 results (failure must not abort batch), got %d", len(results))
	}
	if !errors.Is(results[0].Error, listErr) {
		t.Errorf("first result should wrap list error, got %v", results[0].Error)
	}
	if !results[1].Skipped {
		t.Errorf("second account should be skipped (already has ETH), got %+v", results[1])
	}
}

func TestBackfillAddCoin_AddCoinFailureRecordedNotPropagated(t *testing.T) {
	addErr := errors.New("safeheron 5xx")
	client := &mockBackfillClient{
		listFn: func(_ context.Context, accountKey string) ([]safeheron.AccountCoin, error) {
			// 第一个 account 缺 BNB_BSC（会触发 AddCoin 失败）；第二个全齐（skip）
			if accountKey == "acct-bad" {
				return []safeheron.AccountCoin{{CoinKey: "ETH"}}, nil
			}
			return []safeheron.AccountCoin{{CoinKey: "ETH"}, {CoinKey: "BNB_BSC"}}, nil
		},
		addFn: func(_ context.Context, _ string, _ []string) (*safeheron.Wallet, error) {
			return nil, addErr
		},
	}
	targets := []AccountTarget{
		{AccountKey: "acct-bad", NetworkFamily: "EVM"},
		{AccountKey: "acct-good", NetworkFamily: "EVM"},
	}

	results := BackfillAddCoin(context.Background(), client, targets, evmExpectations("ETH", "BNB_BSC"), false)

	if !errors.Is(results[0].Error, addErr) {
		t.Errorf("first account should carry AddCoin error, got %v", results[0].Error)
	}
	if !results[1].Skipped {
		t.Errorf("second account already had every coin → should be Skipped, got %+v", results[1])
	}
	if results[1].Error != nil {
		t.Errorf("second account should not carry an error, got %v", results[1].Error)
	}
}

func TestBackfillAddCoin_EmptyExpectationsSkipsAll(t *testing.T) {
	client := &mockBackfillClient{
		listFn: func(_ context.Context, _ string) ([]safeheron.AccountCoin, error) {
			return []safeheron.AccountCoin{{CoinKey: "ETH"}}, nil
		},
	}
	targets := []AccountTarget{{AccountKey: "acct", NetworkFamily: "TRON"}} // family with no config

	results := BackfillAddCoin(context.Background(), client, targets, func(string) []string { return nil }, false)

	if !results[0].Skipped {
		t.Errorf("empty expectations should skip the account, got %+v", results[0])
	}
	if len(client.addCoinCalls) != 0 {
		t.Errorf("AddCoin should not be called, got %v", client.addCoinCalls)
	}
}
