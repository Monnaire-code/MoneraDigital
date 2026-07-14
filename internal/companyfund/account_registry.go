package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const defaultAccountRegistryRefreshInterval = time.Minute

// AccountRegistryLoader is intentionally small so SQL repositories, fixtures,
// and future admin-side adapters can supply one consistent account/policy view.
// Callers must not retain or mutate the returned slices after Load returns.
type AccountRegistryLoader interface {
	LoadCompanyFundAccounts(ctx context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error)
}

// AccountRegistryDatabase opens the one read-only, consistent transaction
// used to build an account snapshot. It is intentionally narrower than the
// general DBRepository because this cache never writes business data.
type AccountRegistryDatabase interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

type accountRegistryQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// PostgresAccountRegistryLoader reads enabled company-fund account settings
// and asset policies from PostgreSQL. It scans NUMERIC dust thresholds through
// text so no float can enter the registry snapshot.
type PostgresAccountRegistryLoader struct {
	db AccountRegistryDatabase
}

func NewPostgresAccountRegistryLoader(db AccountRegistryDatabase) *PostgresAccountRegistryLoader {
	return &PostgresAccountRegistryLoader{db: db}
}

const selectEnabledCompanyFundAccountsSQL = `
SELECT id, channel, COALESCE(provider_account_key, ''), COALESCE(wallet_address, ''),
       COALESCE(normalized_address, ''), COALESCE(network_family, ''),
       COALESCE(company_entity, ''), COALESCE(fund_account_name, ''),
       COALESCE(sub_account_name, ''), COALESCE(account_type, ''), account_name,
       COALESCE(account_role, ''), is_enabled
FROM company_fund_accounts
WHERE is_enabled = true
ORDER BY id`

const selectEnabledCompanyFundAccountAssetPoliciesSQL = `
SELECT id, company_fund_account_id, currency, COALESCE(chain_code, ''),
       COALESCE(provider_asset_key, ''), COALESCE(asset_contract, ''),
       dust_detection_enabled, dust_threshold::TEXT, auto_exclude_dust_from_summary,
       COALESCE(valuation_provider_asset_id, ''), COALESCE(valuation_provider_platform_id, ''),
       COALESCE(valuation_contract_address, ''), is_enabled
FROM company_fund_account_asset_policies
WHERE is_enabled = true
ORDER BY id`

func (l *PostgresAccountRegistryLoader) LoadCompanyFundAccounts(ctx context.Context) ([]CompanyFundAccount, []AccountAssetPolicy, error) {
	if l == nil || l.db == nil {
		return nil, nil, fmt.Errorf("company-fund account registry database is not configured")
	}
	tx, err := l.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, nil, fmt.Errorf("begin company-fund account registry consistent read: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	accounts, err := loadRegistryAccounts(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	policies, err := loadRegistryAssetPolicies(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit company-fund account registry consistent read: %w", err)
	}
	committed = true
	return accounts, policies, nil
}

func loadRegistryAccounts(ctx context.Context, queryer accountRegistryQueryer) ([]CompanyFundAccount, error) {
	rows, err := queryer.QueryContext(ctx, selectEnabledCompanyFundAccountsSQL)
	if err != nil {
		return nil, fmt.Errorf("query enabled company-fund accounts: %w", err)
	}
	defer rows.Close()

	accounts := make([]CompanyFundAccount, 0)
	for rows.Next() {
		var account CompanyFundAccount
		var channel string
		if err := rows.Scan(
			&account.ID,
			&channel,
			&account.ProviderAccountKey,
			&account.WalletAddress,
			&account.NormalizedAddress,
			&account.NetworkFamily,
			&account.CompanyEntity,
			&account.FundAccountName,
			&account.SubAccountName,
			&account.AccountType,
			&account.AccountName,
			&account.AccountRole,
			&account.Enabled,
		); err != nil {
			return nil, fmt.Errorf("scan enabled company-fund account: %w", err)
		}
		account.Channel = Channel(channel)
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled company-fund accounts: %w", err)
	}
	return accounts, nil
}

func loadRegistryAssetPolicies(ctx context.Context, queryer accountRegistryQueryer) ([]AccountAssetPolicy, error) {
	rows, err := queryer.QueryContext(ctx, selectEnabledCompanyFundAccountAssetPoliciesSQL)
	if err != nil {
		return nil, fmt.Errorf("query enabled company-fund account asset policies: %w", err)
	}
	defer rows.Close()

	policies := make([]AccountAssetPolicy, 0)
	for rows.Next() {
		var policy AccountAssetPolicy
		var thresholdText sql.NullString
		if err := rows.Scan(
			&policy.ID,
			&policy.AccountID,
			&policy.Asset.Currency,
			&policy.Asset.ChainCode,
			&policy.Asset.ProviderAssetKey,
			&policy.Asset.ContractAddress,
			&policy.Dust.Enabled,
			&thresholdText,
			&policy.AutoExcludeDustFromSummary,
			&policy.CoinGeckoID,
			&policy.CoinGeckoPlatformID,
			&policy.CoinGeckoContractAddress,
			&policy.Enabled,
		); err != nil {
			return nil, fmt.Errorf("scan enabled company-fund account asset policy: %w", err)
		}
		policy.Dust.ID = policy.ID
		if thresholdText.Valid && thresholdText.String != "" {
			threshold, err := decimal.NewFromString(thresholdText.String)
			if err != nil {
				return nil, fmt.Errorf("parse company-fund policy %d dust threshold: %w", policy.ID, err)
			}
			policy.Dust.Threshold = &threshold
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate enabled company-fund account asset policies: %w", err)
	}
	return policies, nil
}

// AccountRegistryStatus exposes cache freshness and the latest refresh error
// without exposing mutable snapshot maps.
type AccountRegistryStatus struct {
	LastSuccessfulRefreshAt time.Time
	LastRefreshError        error
	LastRefreshErrorAt      time.Time
	Age                     time.Duration
}

// AccountRegistrySnapshot is immutable after construction. Every event or
// reconciliation run should retain one Snapshot pointer for its whole work
// unit so a periodic refresh cannot change account matching midway through it.
type AccountRegistrySnapshot struct {
	loadedAt                     time.Time
	accountsByID                 map[int64]CompanyFundAccount
	safeheronByAddress           map[string]CompanyFundAccount
	safeheronProviderAccountKeys map[string]struct{}
	airwallexByAccountKey        map[string]CompanyFundAccount
	policiesByAccount            map[int64][]AccountAssetPolicy
}

func newEmptyAccountRegistrySnapshot(loadedAt time.Time) *AccountRegistrySnapshot {
	return &AccountRegistrySnapshot{
		loadedAt:                     loadedAt,
		accountsByID:                 make(map[int64]CompanyFundAccount),
		safeheronByAddress:           make(map[string]CompanyFundAccount),
		safeheronProviderAccountKeys: make(map[string]struct{}),
		airwallexByAccountKey:        make(map[string]CompanyFundAccount),
		policiesByAccount:            make(map[int64][]AccountAssetPolicy),
	}
}

// LoadedAt returns the instant at which this immutable snapshot was built.
func (s *AccountRegistrySnapshot) LoadedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.loadedAt
}

// Accounts returns detached enabled account settings in stable ID order. It
// is for bounded reconciliation work; callers cannot enumerate the mutable
// registry maps while a refresh may atomically publish a replacement snapshot.
func (s *AccountRegistrySnapshot) Accounts() []CompanyFundAccount {
	if s == nil || len(s.accountsByID) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(s.accountsByID))
	for id := range s.accountsByID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	accounts := make([]CompanyFundAccount, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, cloneCompanyFundAccount(s.accountsByID[id]))
	}
	return accounts
}

// LookupSafeheron resolves only enabled Safeheron accounts by network family
// and address. EVM addresses are normalized to lowercase; non-EVM families
// preserve case so case-sensitive chains are never silently remapped.
func (s *AccountRegistrySnapshot) LookupSafeheron(networkFamily, address string) (CompanyFundAccount, bool) {
	if s == nil {
		return CompanyFundAccount{}, false
	}
	account, ok := s.safeheronByAddress[safeheronAddressKey(networkFamily, address)]
	return account, ok
}

// HasSafeheronProviderAccountKey is an exact, case-sensitive membership
// check. A custody account may safely map to several configured addresses, so
// this deliberately proves only configured account-key membership rather than
// selecting an arbitrary wallet address.
func (s *AccountRegistrySnapshot) HasSafeheronProviderAccountKey(providerAccountKey string) bool {
	if s == nil || providerAccountKey == "" || providerAccountKey != strings.TrimSpace(providerAccountKey) {
		return false
	}
	_, ok := s.safeheronProviderAccountKeys[providerAccountKey]
	return ok
}

// LookupAirwallex resolves only enabled Airwallex accounts by the provider's
// account identifier. Provider account identifiers are not case-folded.
func (s *AccountRegistrySnapshot) LookupAirwallex(providerAccountKey string) (CompanyFundAccount, bool) {
	if s == nil {
		return CompanyFundAccount{}, false
	}
	account, ok := s.airwallexByAccountKey[strings.TrimSpace(providerAccountKey)]
	return account, ok
}

// LookupAssetPolicy returns the most-specific enabled asset policy belonging
// to the enabled account. Currency is required; exact chain/provider-asset/
// contract constraints outrank progressively broader policies.
func (s *AccountRegistrySnapshot) LookupAssetPolicy(accountID int64, asset AssetIdentity) (AccountAssetPolicy, bool) {
	if s == nil {
		return AccountAssetPolicy{}, false
	}
	candidates := s.policiesByAccount[accountID]
	var best AccountAssetPolicy
	bestScore := -1
	found := false
	for _, candidate := range candidates {
		score, matches := accountAssetPolicyMatchScore(candidate, asset)
		if !matches {
			continue
		}
		if !found || score > bestScore || (score == bestScore && candidate.ID < best.ID) {
			best = cloneAccountAssetPolicy(candidate)
			bestScore = score
			found = true
		}
	}
	return best, found
}

// LookupAssetPolicyFields is a convenience form for adapters whose provider
// payload has not yet been converted into AssetIdentity.
func (s *AccountRegistrySnapshot) LookupAssetPolicyFields(accountID int64, currency, chainCode, providerAssetKey, contractAddress string) (AccountAssetPolicy, bool) {
	return s.LookupAssetPolicy(accountID, AssetIdentity{
		Currency:         currency,
		ChainCode:        chainCode,
		ProviderAssetKey: providerAssetKey,
		ContractAddress:  contractAddress,
	})
}

// AssetPolicies returns detached enabled policy snapshots in a stable order.
// It is deliberately a list rather than the registry's internal map so rate
// refreshers can build one coherent request plan without being able to mutate
// the live account cache.
func (s *AccountRegistrySnapshot) AssetPolicies() []AccountAssetPolicy {
	if s == nil {
		return nil
	}
	policies := make([]AccountAssetPolicy, 0)
	for _, source := range s.policiesByAccount {
		for _, policy := range source {
			policies = append(policies, cloneAccountAssetPolicy(policy))
		}
	}
	sort.Slice(policies, func(left, right int) bool {
		if policies[left].AccountID != policies[right].AccountID {
			return policies[left].AccountID < policies[right].AccountID
		}
		return policies[left].ID < policies[right].ID
	})
	return policies
}

// AccountRegistry owns one atomically-swapped immutable company-fund account
// snapshot. It deliberately uses only the Go standard library and does not
// introduce Redis or another cache component.
type AccountRegistry struct {
	mu              sync.RWMutex
	refreshMu       sync.Mutex
	loader          AccountRegistryLoader
	refreshInterval time.Duration
	snapshot        *AccountRegistrySnapshot
	lastSuccessAt   time.Time
	lastRefreshErr  error
	lastErrorAt     time.Time
	running         bool
	runCancel       context.CancelFunc
	runDone         chan struct{}
}

func NewAccountRegistry(loader AccountRegistryLoader, refreshInterval time.Duration) *AccountRegistry {
	if refreshInterval <= 0 {
		refreshInterval = defaultAccountRegistryRefreshInterval
	}
	return &AccountRegistry{
		loader:          loader,
		refreshInterval: refreshInterval,
		snapshot:        newEmptyAccountRegistrySnapshot(time.Time{}),
	}
}

// NewCompanyFundAccountRegistry is a descriptive alias retained for callers
// that prefer the feature-qualified constructor name.
func NewCompanyFundAccountRegistry(loader AccountRegistryLoader, refreshInterval time.Duration) *AccountRegistry {
	return NewAccountRegistry(loader, refreshInterval)
}

func (r *AccountRegistry) RefreshInterval() time.Duration {
	if r == nil {
		return defaultAccountRegistryRefreshInterval
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.refreshInterval
}

// Snapshot returns the current immutable snapshot pointer. It is safe to hold
// that pointer across an entire event/reconciliation run.
func (r *AccountRegistry) Snapshot() *AccountRegistrySnapshot {
	if r == nil {
		return newEmptyAccountRegistrySnapshot(time.Time{})
	}
	r.mu.RLock()
	snapshot := r.snapshot
	r.mu.RUnlock()
	return snapshot
}

func (r *AccountRegistry) Status() AccountRegistryStatus {
	if r == nil {
		return AccountRegistryStatus{}
	}
	r.mu.RLock()
	status := AccountRegistryStatus{
		LastSuccessfulRefreshAt: r.lastSuccessAt,
		LastRefreshError:        r.lastRefreshErr,
		LastRefreshErrorAt:      r.lastErrorAt,
	}
	r.mu.RUnlock()
	if !status.LastSuccessfulRefreshAt.IsZero() {
		status.Age = time.Since(status.LastSuccessfulRefreshAt)
		if status.Age < 0 {
			status.Age = 0
		}
	}
	return status
}

func (r *AccountRegistry) LookupSafeheron(networkFamily, address string) (CompanyFundAccount, bool) {
	return r.Snapshot().LookupSafeheron(networkFamily, address)
}

func (r *AccountRegistry) HasSafeheronProviderAccountKey(providerAccountKey string) bool {
	return r.Snapshot().HasSafeheronProviderAccountKey(providerAccountKey)
}

func (r *AccountRegistry) LookupAirwallex(providerAccountKey string) (CompanyFundAccount, bool) {
	return r.Snapshot().LookupAirwallex(providerAccountKey)
}

func (r *AccountRegistry) LookupAssetPolicy(accountID int64, asset AssetIdentity) (AccountAssetPolicy, bool) {
	return r.Snapshot().LookupAssetPolicy(accountID, asset)
}

func (r *AccountRegistry) LookupAssetPolicyFields(accountID int64, currency, chainCode, providerAssetKey, contractAddress string) (AccountAssetPolicy, bool) {
	return r.Snapshot().LookupAssetPolicyFields(accountID, currency, chainCode, providerAssetKey, contractAddress)
}

// Refresh builds a complete detached snapshot outside the registry lock, then
// atomically swaps it. A loader failure preserves the exact prior snapshot.
func (r *AccountRegistry) Refresh(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("company-fund account registry is not configured")
	}
	// Do not hold mu during I/O; refreshMu only orders complete refreshes so a
	// slow older loader result cannot publish after a newer refresh request.
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.RLock()
	loader := r.loader
	r.mu.RUnlock()
	if loader == nil {
		err := fmt.Errorf("company-fund account registry loader is not configured")
		r.recordRefreshFailure(err)
		return err
	}

	accounts, policies, err := loader.LoadCompanyFundAccounts(ctx)
	if err == nil {
		var snapshot *AccountRegistrySnapshot
		snapshot, err = buildAccountRegistrySnapshot(accounts, policies, time.Now().UTC())
		if err == nil {
			r.mu.Lock()
			r.snapshot = snapshot
			r.lastSuccessAt = snapshot.loadedAt
			r.lastRefreshErr = nil
			r.lastErrorAt = time.Time{}
			r.mu.Unlock()
			return nil
		}
	}

	wrapped := fmt.Errorf("refresh company-fund account registry: %w", err)
	r.recordRefreshFailure(wrapped)
	return wrapped
}

// Load is an explicit synonym for Refresh for callers following the existing
// wallet-config registry convention.
func (r *AccountRegistry) Load(ctx context.Context) error {
	return r.Refresh(ctx)
}

func (r *AccountRegistry) recordRefreshFailure(err error) {
	r.mu.Lock()
	r.lastRefreshErr = err
	r.lastErrorAt = time.Now().UTC()
	r.mu.Unlock()
}

// Start begins at most one periodic refresh loop. It does not synchronously
// refresh; callers that require an initial snapshot should call Refresh first.
func (r *AccountRegistry) Start(parent context.Context) {
	if r == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	r.running = true
	r.runCancel = cancel
	r.runDone = done
	interval := r.refreshInterval
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			if r.runDone == done {
				r.running = false
				r.runCancel = nil
				r.runDone = nil
			}
			r.mu.Unlock()
			close(done)
		}()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.Refresh(ctx)
			}
		}
	}()
}

// Stop cancels and waits for the single background refresh loop, if one is
// running. It is safe to call repeatedly.
func (r *AccountRegistry) Stop() {
	if r == nil {
		return
	}
	r.mu.RLock()
	cancel := r.runCancel
	done := r.runDone
	r.mu.RUnlock()
	if cancel == nil || done == nil {
		return
	}
	cancel()
	<-done
}

func buildAccountRegistrySnapshot(accounts []CompanyFundAccount, policies []AccountAssetPolicy, loadedAt time.Time) (*AccountRegistrySnapshot, error) {
	snapshot := newEmptyAccountRegistrySnapshot(loadedAt)
	for _, source := range accounts {
		if !source.Enabled {
			continue
		}
		account := cloneCompanyFundAccount(source)
		if account.ID <= 0 {
			return nil, fmt.Errorf("enabled company-fund account must have a positive ID")
		}
		if _, exists := snapshot.accountsByID[account.ID]; exists {
			return nil, fmt.Errorf("duplicate enabled company-fund account ID %d", account.ID)
		}
		snapshot.accountsByID[account.ID] = account

		switch account.Channel {
		case ChannelSafeheron:
			address := account.NormalizedAddress
			if strings.TrimSpace(address) == "" {
				address = account.WalletAddress
			}
			key := safeheronAddressKey(account.NetworkFamily, address)
			if key == "" {
				delete(snapshot.accountsByID, account.ID)
				return nil, fmt.Errorf("enabled Safeheron account %d requires network family and normalized address", account.ID)
			}
			if _, exists := snapshot.safeheronByAddress[key]; exists {
				return nil, fmt.Errorf("duplicate enabled Safeheron account identity %q", key)
			}
			snapshot.safeheronByAddress[key] = account
			providerAccountKey := strings.TrimSpace(account.ProviderAccountKey)
			if providerAccountKey != "" {
				if providerAccountKey != account.ProviderAccountKey {
					return nil, fmt.Errorf("enabled Safeheron account %d provider account key must not have surrounding whitespace", account.ID)
				}
				snapshot.safeheronProviderAccountKeys[providerAccountKey] = struct{}{}
			}
		case ChannelAirwallex:
			key := strings.TrimSpace(account.ProviderAccountKey)
			if key == "" {
				delete(snapshot.accountsByID, account.ID)
				return nil, fmt.Errorf("enabled Airwallex account %d requires provider account key", account.ID)
			}
			if _, exists := snapshot.airwallexByAccountKey[key]; exists {
				return nil, fmt.Errorf("duplicate enabled Airwallex account key %q", key)
			}
			snapshot.airwallexByAccountKey[key] = account
		default:
			delete(snapshot.accountsByID, account.ID)
			return nil, fmt.Errorf("enabled company-fund account %d has unsupported channel %q", account.ID, account.Channel)
		}
	}

	for _, source := range policies {
		if !source.Enabled {
			continue
		}
		if _, exists := snapshot.accountsByID[source.AccountID]; !exists {
			continue
		}
		policy := cloneAccountAssetPolicy(source)
		if strings.TrimSpace(policy.Asset.Currency) == "" {
			return nil, fmt.Errorf("enabled company-fund asset policy %d requires currency", policy.ID)
		}
		snapshot.policiesByAccount[policy.AccountID] = append(snapshot.policiesByAccount[policy.AccountID], policy)
	}
	return snapshot, nil
}

func cloneCompanyFundAccount(source CompanyFundAccount) CompanyFundAccount {
	return source
}

func cloneAccountAssetPolicy(source AccountAssetPolicy) AccountAssetPolicy {
	clone := source
	if source.Dust.Threshold != nil {
		threshold := *source.Dust.Threshold
		clone.Dust.Threshold = &threshold
	}
	return clone
}

func safeheronAddressKey(networkFamily, address string) string {
	family := normalizeNetworkFamily(networkFamily)
	if family == "" {
		return ""
	}
	normalizedAddress := normalizeSafeheronAddress(family, address)
	if normalizedAddress == "" {
		return ""
	}
	return family + "\x00" + normalizedAddress
}

func normalizeNetworkFamily(networkFamily string) string {
	return strings.ToUpper(strings.TrimSpace(networkFamily))
}

func normalizeSafeheronAddress(networkFamily, address string) string {
	normalized := strings.TrimSpace(address)
	if normalizeNetworkFamily(networkFamily) == "EVM" {
		return strings.ToLower(normalized)
	}
	return normalized
}

func accountAssetPolicyMatchScore(policy AccountAssetPolicy, input AssetIdentity) (int, bool) {
	policyCurrency := strings.ToUpper(strings.TrimSpace(policy.Asset.Currency))
	inputCurrency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if policyCurrency == "" || policyCurrency != inputCurrency {
		return 0, false
	}

	policyChain := strings.ToUpper(strings.TrimSpace(policy.Asset.ChainCode))
	inputChain := strings.ToUpper(strings.TrimSpace(input.ChainCode))
	policyProviderAsset := strings.TrimSpace(policy.Asset.ProviderAssetKey)
	inputProviderAsset := strings.TrimSpace(input.ProviderAssetKey)
	policyContract := normalizeAssetContract(policy.Asset.ContractAddress)
	inputContract := normalizeAssetContract(input.ContractAddress)

	score := 0
	if policyChain != "" {
		if policyChain != inputChain {
			return 0, false
		}
		score += 4
	}
	if policyProviderAsset != "" {
		if policyProviderAsset != inputProviderAsset {
			return 0, false
		}
		score += 8
	}
	if policyContract != "" {
		if policyContract != inputContract {
			return 0, false
		}
		score += 16
	}
	return score, true
}

func normalizeAssetContract(contract string) string {
	trimmed := strings.TrimSpace(contract)
	if !isEVMHexAddress(trimmed) {
		return trimmed
	}
	return strings.ToLower(trimmed)
}

func isEVMHexAddress(value string) bool {
	if len(value) != 42 || !strings.HasPrefix(value, "0x") {
		return false
	}
	for _, character := range value[2:] {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
