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
	// KytReport 查询交易的 KYT/AML 筛查报告。
	// 调用时机：(1) ProcessOne 初查 COMPLETED+CONFIRMED 时 (2) 超时兜底扫描 KYT_PENDING 超时后。
	// ctx 在接口层保留但不传给 SDK（SDK 方法不接受 ctx）。
	KytReport(ctx context.Context, txKey string) (*KytReportResponse, error)
	WebhookConvert(rawBody []byte) (*WebhookEvent, error)
	Close() error
}

var _ SafeheronClient = (*Client)(nil)
