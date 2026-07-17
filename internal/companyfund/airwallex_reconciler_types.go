package companyfund

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// AirwallexFinancialTransactionsSyncKind is the durable reconciliation kind
	// recorded by the sync-run adapter. It is distinct from webhook deliveries:
	// webhooks may trigger a run but do not provide Financial Transactions facts.
	AirwallexFinancialTransactionsSyncKind = "AIRWALLEX_FINANCIAL_TRANSACTIONS"

	// AirwallexFinancialTransactionsLateStatusSyncKind is the independent
	// rolling overlap pass used to observe terminal corrections that arrived
	// after the primary daily window completed.
	AirwallexFinancialTransactionsLateStatusSyncKind = "AIRWALLEX_FT_LATE_STATUS"

	// AirwallexFinancialTransactionSnapshotEventType identifies one encrypted
	// Financial Transactions API item in the provider-event inbox.
	AirwallexFinancialTransactionSnapshotEventType = "FINANCIAL_TRANSACTION_SNAPSHOT"

	maxAirwallexReconcilerVersionBytes = 64
)

// AirwallexFinancialTransactionsClient is the exact read surface used by the
// reconciler. PinnedAPIVersion prevents a caller from pairing a versioned sync
// input with a client whose outgoing business requests use another contract.
type AirwallexFinancialTransactionsClient interface {
	PinnedAPIVersion() string
	// PinnedLoginAsScope is the exact x-login-as identity used by this
	// client. Financial Transactions responses do not prove their account
	// ownership, so reconciliation may use only this configured scope.
	PinnedLoginAsScope() string
	ListFinancialTransactions(ctx context.Context, input AirwallexFinancialTransactionsRequest) (AirwallexFinancialTransactionsPage, error)
}

// AirwallexOwnedProviderEventIngestor is satisfied by
// OwnedProviderPayloadService. It is the only mutation performed by this
// reconciler: raw API item bytes are encrypted and inserted into the durable
// provider-event inbox for a later worker to normalize.
type AirwallexOwnedProviderEventIngestor interface {
	Ingest(ctx context.Context, input OwnedProviderPayloadInput) (ProviderEventInsertResult, error)
}

// AirwallexFinancialTransactionsSyncRunStore is intentionally narrow. The
// schema/repository layer owns leases and durable sync-run SQL; the reconciler
// needs only an opened run, durable checkpoints, and completion recording.
type AirwallexFinancialTransactionsSyncRunStore interface {
	OpenAirwallexFinancialTransactionsSyncRun(ctx context.Context, input AirwallexFinancialTransactionsSyncRunInput) (AirwallexFinancialTransactionsSyncRun, error)
	CheckpointAirwallexFinancialTransactionsSyncRun(ctx context.Context, runID int64, checkpoint AirwallexFinancialTransactionsSyncCheckpoint) error
	CompleteAirwallexFinancialTransactionsSyncRun(ctx context.Context, runID int64, checkpoint AirwallexFinancialTransactionsSyncCheckpoint) error
}

// AirwallexFinancialTransactionsReconcilerConfig pins the parser boundary and
// owned-payload policy. It has no scheduler, route, or database dependency.
type AirwallexFinancialTransactionsReconcilerConfig struct {
	APIVersion        string
	SchemaVersion     string
	EventVersion      string
	PageSize          int
	MaxPages          int
	PayloadKeyVersion string
	PayloadRetention  time.Duration
}

// AirwallexFinancialTransactionsReconcileInput supplies a pre-resolved,
// configured company account. It deliberately carries no webhook account_id
// or org_id because neither can decide which company account to ingest.
type AirwallexFinancialTransactionsReconcileInput struct {
	Account            CompanyFundAccount
	ProviderAccountKey string
	WindowStart        time.Time
	WindowEnd          time.Time
	APIVersion         string
	SchemaVersion      string
	EventVersion       string
	// SyncKind and WindowKey optionally override the normal daily durable
	// identity. They are used only for bounded late-status overlap scans; blank
	// values retain the standard account/window/version-derived key.
	SyncKind  string
	WindowKey string
}

// AirwallexFinancialTransactionsSyncRunInput is the adapter-facing immutable
// key for one reconciliation window. WindowKey includes the provider account,
// all parser/API pins, and page size, so a changed request contract never
// silently reuses an older run.
type AirwallexFinancialTransactionsSyncRunInput struct {
	Channel              Channel
	SyncKind             string
	WindowKey            string
	CompanyFundAccountID int64
	ProviderAccountKey   string
	WindowStart          time.Time
	WindowEnd            time.Time
	APIVersion           string
	SchemaVersion        string
	EventVersion         string
	PageSize             int
}

// AirwallexFinancialTransactionsSyncCheckpoint supports safe retry of a
// partially ingested page. InFlightEventIDs are exact deterministic event IDs,
// not provider source IDs: an item with a corrected raw digest is re-ingested.
type AirwallexFinancialTransactionsSyncCheckpoint struct {
	NextPageNum      int
	InFlightPageNum  *int
	InFlightEventIDs []string
	CandidatesSeen   int
	EventsCreated    int
	EventsExisting   int
}

// AirwallexFinancialTransactionsSyncRun is a leased/opened durable sync run
// represented without SQL concerns. The store returns its last checkpoint for
// a retry after an item or page failure.
type AirwallexFinancialTransactionsSyncRun struct {
	ID           int64
	AttemptCount int
	Checkpoint   AirwallexFinancialTransactionsSyncCheckpoint
}

// AirwallexFinancialTransactionsReconcileResult exposes only durable work
// counts. It does not claim that a later worker has normalized or upserted a
// financial movement yet.
type AirwallexFinancialTransactionsReconcileResult struct {
	RunID          int64
	AttemptCount   int
	PagesFetched   int
	CandidatesSeen int
	EventsCreated  int
	EventsExisting int
	Checkpoint     AirwallexFinancialTransactionsSyncCheckpoint
}

// AirwallexFinancialTransactionsReconciliationContract is the fixed
// provider/parser version tuple used by background scans. It deliberately
// carries no credentials or webhook payload metadata.
type AirwallexFinancialTransactionsReconciliationContract struct {
	APIVersion    string
	SchemaVersion string
	EventVersion  string
	LoginAsScope  string
}

func (contract AirwallexFinancialTransactionsReconciliationContract) validate() error {
	if _, err := parseAirwallexAPIVersion(contract.APIVersion); err != nil {
		return err
	}
	if _, err := normalizeAirwallexReconcilerVersion("Airwallex financial transactions schema version", contract.SchemaVersion); err != nil {
		return err
	}
	if _, err := normalizeAirwallexReconcilerVersion("Airwallex financial transactions event version", contract.EventVersion); err != nil {
		return err
	}
	if _, err := normalizeAirwallexReconcilerAccountKey(contract.LoginAsScope); err != nil {
		return fmt.Errorf("Airwallex financial transactions login scope is invalid: %w", err)
	}
	return nil
}

// ResolveAirwallexSingleAccountScope returns the one enabled Airwallex
// company account that a single x-login-as client is permitted to reconcile.
// The Financial Transactions API response has no account-ownership proof, so
// multi-account, blank, or mismatched settings fail closed instead of
// attempting to attribute a provider line to an arbitrary account.
func ResolveAirwallexSingleAccountScope(snapshot *AccountRegistrySnapshot, loginAsScope string) (CompanyFundAccount, bool) {
	if loginAsScope == "" || loginAsScope != strings.TrimSpace(loginAsScope) {
		return CompanyFundAccount{}, false
	}

	var selected CompanyFundAccount
	found := false
	for _, account := range snapshot.Accounts() {
		if account.Channel != ChannelAirwallex || !account.Enabled {
			continue
		}
		if found {
			return CompanyFundAccount{}, false
		}
		if account.ProviderAccountKey == "" || account.ProviderAccountKey != strings.TrimSpace(account.ProviderAccountKey) {
			return CompanyFundAccount{}, false
		}
		selected = account
		found = true
	}
	if !found || selected.ProviderAccountKey != loginAsScope {
		return CompanyFundAccount{}, false
	}
	return selected, true
}

// AirwallexFinancialTransactionsReconciler performs complete official
// page_num/page_size/has_more reads and materializes raw item facts into the
// encrypted provider-event inbox. It never creates ledger movements itself.
type AirwallexFinancialTransactionsReconciler struct {
	client   AirwallexFinancialTransactionsClient
	ingester AirwallexOwnedProviderEventIngestor
	syncRuns AirwallexFinancialTransactionsSyncRunStore
	config   AirwallexFinancialTransactionsReconcilerConfig
}

// NewAirwallexFinancialTransactionsReconciler validates dependencies and the
// pinned contract before any HTTP or provider-event mutation can occur.
func NewAirwallexFinancialTransactionsReconciler(
	client AirwallexFinancialTransactionsClient,
	ingester AirwallexOwnedProviderEventIngestor,
	syncRuns AirwallexFinancialTransactionsSyncRunStore,
	config AirwallexFinancialTransactionsReconcilerConfig,
) (*AirwallexFinancialTransactionsReconciler, error) {
	if client == nil || ingester == nil || syncRuns == nil {
		return nil, fmt.Errorf("Airwallex financial transactions reconciler dependencies are required")
	}
	normalizedConfig, err := normalizeAirwallexFinancialTransactionsReconcilerConfig(config)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(client.PinnedAPIVersion()) != normalizedConfig.APIVersion {
		return nil, fmt.Errorf("Airwallex financial transactions client API version does not match reconciler pin")
	}
	if _, err := normalizeAirwallexReconcilerAccountKey(client.PinnedLoginAsScope()); err != nil {
		return nil, fmt.Errorf("Airwallex financial transactions client login scope is invalid")
	}
	return &AirwallexFinancialTransactionsReconciler{
		client:   client,
		ingester: ingester,
		syncRuns: syncRuns,
		config:   normalizedConfig,
	}, nil
}

// ReconciliationContract returns the immutable API/schema/event pins that
// were validated at construction. A runtime reads this rather than duplicating
// provider versions in a separate scheduler configuration.
func (r *AirwallexFinancialTransactionsReconciler) ReconciliationContract() AirwallexFinancialTransactionsReconciliationContract {
	if r == nil {
		return AirwallexFinancialTransactionsReconciliationContract{}
	}
	return AirwallexFinancialTransactionsReconciliationContract{
		APIVersion:    r.config.APIVersion,
		SchemaVersion: r.config.SchemaVersion,
		EventVersion:  r.config.EventVersion,
		LoginAsScope:  r.client.PinnedLoginAsScope(),
	}
}

func normalizeAirwallexFinancialTransactionsReconcilerConfig(config AirwallexFinancialTransactionsReconcilerConfig) (AirwallexFinancialTransactionsReconcilerConfig, error) {
	apiVersion, err := parseAirwallexAPIVersion(config.APIVersion)
	if err != nil {
		return AirwallexFinancialTransactionsReconcilerConfig{}, err
	}
	schemaVersion, err := normalizeAirwallexReconcilerVersion("Airwallex financial transactions schema version", config.SchemaVersion)
	if err != nil {
		return AirwallexFinancialTransactionsReconcilerConfig{}, err
	}
	eventVersion, err := normalizeAirwallexReconcilerVersion("Airwallex financial transactions event version", config.EventVersion)
	if err != nil {
		return AirwallexFinancialTransactionsReconcilerConfig{}, err
	}
	if config.PageSize <= 0 || config.PageSize > maxAirwallexFinancialTransactionPageSize {
		return AirwallexFinancialTransactionsReconcilerConfig{}, fmt.Errorf("Airwallex financial transactions reconciliation page size is outside configured bounds")
	}
	if config.MaxPages <= 0 || config.MaxPages > maxAirwallexFinancialTransactionPageNumber+1 {
		return AirwallexFinancialTransactionsReconcilerConfig{}, fmt.Errorf("Airwallex financial transactions reconciliation maximum pages is outside configured bounds")
	}
	payloadKeyVersion, err := normalizeAirwallexReconcilerVersion("Airwallex owned payload key version", config.PayloadKeyVersion)
	if err != nil {
		return AirwallexFinancialTransactionsReconcilerConfig{}, err
	}
	if config.PayloadRetention <= 0 || config.PayloadRetention.Microseconds() <= 0 {
		return AirwallexFinancialTransactionsReconcilerConfig{}, fmt.Errorf("Airwallex owned payload retention must be positive")
	}
	config.APIVersion = apiVersion
	config.SchemaVersion = schemaVersion
	config.EventVersion = eventVersion
	config.PayloadKeyVersion = payloadKeyVersion
	return config, nil
}

func normalizeAirwallexReconcilerVersion(label, value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || len(value) > maxAirwallexReconcilerVersionBytes {
		return "", fmt.Errorf("%s must be a non-blank bounded version", label)
	}
	return value, nil
}
