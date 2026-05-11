package safeheron

import "context"

// SafeheronClient defines the business interface for Safeheron SDK operations.
// Note: ctx is accepted for interface consistency but not propagated to the
// Safeheron SDK, which does not support context-based cancellation.
type SafeheronClient interface {
	CreateAssetWallet(ctx context.Context, customerRefID string, coinKeyList []string) (*Wallet, error)
	AddCoin(ctx context.Context, accountKey string, coinKeyList []string) (*Wallet, error)
	ListAccountCoin(ctx context.Context, accountKey string) ([]AccountCoin, error)
	GetAccountByAddress(ctx context.Context, address string) (*Account, error)
	WebhookConvert(rawBody []byte) (*WebhookEvent, error)
	Close() error
}

var _ SafeheronClient = (*Client)(nil)
