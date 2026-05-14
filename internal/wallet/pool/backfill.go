package pool

import (
	"context"
	"fmt"
	"sort"

	"monera-digital/internal/safeheron"
)

// BackfillClient is the minimal Safeheron surface needed by BackfillAddCoin.
// Defined as an interface so tests can mock without standing up the full Client.
type BackfillClient interface {
	ListAccountCoin(ctx context.Context, accountKey string) ([]safeheron.AccountCoin, error)
	AddCoin(ctx context.Context, accountKey string, coinKeys []string) (*safeheron.Wallet, error)
}

// AccountTarget describes one address_pool row to be backfilled.
type AccountTarget struct {
	AccountKey    string
	NetworkFamily string
	Address       string // for logging only
}

// FamilyExpectations returns the coinKeys that should be registered on every
// account belonging to the given network family — typically the current
// coin_chains seed via Registry.SafeheronCoinKeysByFamily.
type FamilyExpectations func(family string) []string

// BackfillResult is the per-account outcome of a backfill pass.
type BackfillResult struct {
	AccountKey   string
	Address      string
	Family       string
	CurrentCoins []string // sorted snapshot of what Safeheron currently has
	AddedCoins   []string // sorted; in dry-run this is what *would* be added
	Skipped      bool     // true when account already has every expected coin
	Error        error    // non-nil = this account failed; loop continues
}

// BackfillAddCoin diff each target's current coin registration against the
// expected set and calls AddCoin for the missing keys. Per-account failures
// are captured in BackfillResult.Error rather than aborting the batch — a
// single 5xx on one account must not block the rest.
//
// dryRun=true skips the AddCoin call but still computes and reports the
// would-be diff, so operators can inspect the plan before mutating Safeheron.
func BackfillAddCoin(
	ctx context.Context,
	client BackfillClient,
	targets []AccountTarget,
	expectations FamilyExpectations,
	dryRun bool,
) []BackfillResult {
	results := make([]BackfillResult, 0, len(targets))
	for _, t := range targets {
		results = append(results, processOne(ctx, client, t, expectations, dryRun))
	}
	return results
}

func processOne(
	ctx context.Context,
	client BackfillClient,
	t AccountTarget,
	expectations FamilyExpectations,
	dryRun bool,
) BackfillResult {
	result := BackfillResult{
		AccountKey: t.AccountKey,
		Address:    t.Address,
		Family:     t.NetworkFamily,
	}

	coins, err := client.ListAccountCoin(ctx, t.AccountKey)
	if err != nil {
		result.Error = fmt.Errorf("list account coin: %w", err)
		return result
	}

	currentSet := make(map[string]struct{}, len(coins))
	currentList := make([]string, 0, len(coins))
	for _, c := range coins {
		currentSet[c.CoinKey] = struct{}{}
		currentList = append(currentList, c.CoinKey)
	}
	sort.Strings(currentList)
	result.CurrentCoins = currentList

	missing := make([]string, 0)
	for _, want := range expectations(t.NetworkFamily) {
		if _, ok := currentSet[want]; !ok {
			missing = append(missing, want)
		}
	}
	sort.Strings(missing)

	if len(missing) == 0 {
		result.Skipped = true
		return result
	}

	result.AddedCoins = missing
	if !dryRun {
		if _, err := client.AddCoin(ctx, t.AccountKey, missing); err != nil {
			result.Error = fmt.Errorf("add coin: %w", err)
		}
	}
	return result
}
