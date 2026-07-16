package companyfund

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"monera-digital/internal/companyfundcontract"
)

const selectSafeheronOccurrenceOwnerSQL = `
SELECT id
FROM company_fund_transactions
WHERE provider_occurrence_key = $1
	AND channel = 'SAFEHERON'
ORDER BY id`

const selectSafeheronAliasSchemaAReadySQL = `
SELECT COUNT(*) = 2
FROM information_schema.columns
WHERE table_schema = current_schema()
	AND table_name = 'company_fund_transactions'
	AND column_name IN ('provider_occurrence_key', 'provider_occurrence_algorithm_version')`

const selectSafeheronAliasQuiescenceSQL = `
SELECT
	(SELECT COUNT(*) FROM company_fund_provider_events WHERE event_state = 'LEASED' AND lease_expires_at > NOW()),
	(SELECT COUNT(*) FROM company_fund_sync_runs WHERE status = 'LEASED' AND lease_expires_at > NOW()),
	(SELECT COUNT(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname = current_database()
		AND application_name LIKE 'monera-digital/%' AND state IS DISTINCT FROM 'idle'),
	(SELECT COUNT(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND datname = current_database()
		AND application_name LIKE 'monera-digital/%'
		AND application_name NOT LIKE ('monera-digital/' || left($1, 12) || '/%'))`

const selectCanonicalAccountPolicyRecordsSQL = `
SELECT account.id,
	account.channel,
	COALESCE(account.provider_account_key, ''),
	COALESCE(account.normalized_address, ''),
	COALESCE(account.network_family, ''),
	account.is_enabled,
	COALESCE(policy.provider_asset_key, policy.currency, ''),
	COALESCE(policy.is_enabled, false)
FROM company_fund_accounts AS account
LEFT JOIN company_fund_account_asset_policies AS policy
	ON policy.company_fund_account_id = account.id
ORDER BY account.id, policy.id`

type DBSafeheronAliasRepairScanner struct {
	db           *sql.DB
	probe        SafeheronAliasLiveProbe
	baseline     SafeheronAliasLiveProbeSnapshot
	beforeCommit func()
	now          func() time.Time
}

func NewDBSafeheronAliasRepairScanner(db *sql.DB, probe SafeheronAliasLiveProbe, baseline SafeheronAliasLiveProbeSnapshot) *DBSafeheronAliasRepairScanner {
	return &DBSafeheronAliasRepairScanner{db: db, probe: probe, baseline: baseline, now: time.Now}
}

func (scanner *DBSafeheronAliasRepairScanner) ScanSafeheronAliasNull(ctx context.Context, evidence SafeheronAliasRepairEvidence, afterID int64, limit int) (SafeheronAliasScanResult, error) {
	if scanner == nil || scanner.db == nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("Safeheron alias repair database is not configured")
	}
	if err := scanner.validateLiveProbe(); err != nil {
		return SafeheronAliasScanResult{}, err
	}
	if err := scanner.validateEvidenceFreshness(evidence); err != nil {
		return SafeheronAliasScanResult{}, err
	}
	tx, err := scanner.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
	if err != nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("begin Safeheron alias scan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := scanSafeheronAliasNullTx(ctx, tx, evidence, afterID, limit, scanner.trustedNow())
	if err != nil {
		return result, err
	}
	if scanner.beforeCommit != nil {
		scanner.beforeCommit()
	}
	if err := scanner.validateEvidenceFreshness(evidence); err != nil {
		return result, err
	}
	if err := scanner.validateLiveProbe(); err != nil {
		return result, err
	}
	if err := revalidateSafeheronAliasSafetyTx(ctx, tx, evidence, scanner.trustedNow()); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("commit Safeheron alias scan: %w", err)
	}
	return result, nil
}

func (scanner *DBSafeheronAliasRepairScanner) ScanAndApplySafeheronAliasNull(ctx context.Context, evidence SafeheronAliasRepairEvidence, afterID int64, limit int) (SafeheronAliasScanResult, error) {
	if scanner == nil || scanner.db == nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("Safeheron alias repair database is not configured")
	}
	if err := scanner.validateLiveProbe(); err != nil {
		return SafeheronAliasScanResult{}, err
	}
	if err := scanner.validateEvidenceFreshness(evidence); err != nil {
		return SafeheronAliasScanResult{}, err
	}
	tx, err := scanner.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("begin Safeheron alias repair: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := scanSafeheronAliasNullTx(ctx, tx, evidence, afterID, limit, scanner.trustedNow())
	if err != nil {
		return result, err
	}
	for _, patch := range result.Plan.Patches {
		if err := applySafeheronAliasPatchTx(ctx, tx, patch); err != nil {
			return result, err
		}
		result.Applied++
	}
	if scanner.beforeCommit != nil {
		scanner.beforeCommit()
	}
	if err := scanner.validateEvidenceFreshness(evidence); err != nil {
		return result, err
	}
	if err := scanner.validateLiveProbe(); err != nil {
		return result, err
	}
	if err := revalidateSafeheronAliasSafetyTx(ctx, tx, evidence, scanner.trustedNow()); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit Safeheron alias repair: %w", err)
	}
	committed = true
	return result, nil
}

func (scanner *DBSafeheronAliasRepairScanner) validateLiveProbe() error {
	current, err := CaptureSafeheronAliasLiveProbe(scanner.probe)
	if err != nil {
		return err
	}
	return ValidateSafeheronAliasLiveProbeUnchanged(scanner.baseline, current)
}

func (scanner *DBSafeheronAliasRepairScanner) trustedNow() time.Time {
	if scanner.now == nil {
		return time.Time{}
	}
	return scanner.now().UTC()
}

func (scanner *DBSafeheronAliasRepairScanner) validateEvidenceFreshness(evidence SafeheronAliasRepairEvidence) error {
	if err := evidence.validate(); err != nil {
		return err
	}
	return validateSafeheronAliasEvidenceFreshness(evidence, scanner.trustedNow())
}

func scanSafeheronAliasNullTx(ctx context.Context, tx *sql.Tx, evidence SafeheronAliasRepairEvidence, afterID int64, limit int, trustedNow time.Time) (SafeheronAliasScanResult, error) {
	request := SafeheronAliasRepairRequest{Evidence: evidence, AfterID: afterID, Limit: limit, FactsByTransactionID: make(map[int64][]SafeheronAliasOccurrenceFact), ExistingOccurrenceOwners: make(map[string]int64)}
	if afterID < 0 || limit < 1 || limit > maxSafeheronAliasRepairWindow {
		return SafeheronAliasScanResult{}, fmt.Errorf("Safeheron alias scan window is invalid")
	}
	if err := revalidateSafeheronAliasSafetyTx(ctx, tx, evidence, trustedNow); err != nil {
		return SafeheronAliasScanResult{}, err
	}
	rows, err := tx.QueryContext(ctx, selectSafeheronAliasNullRepairFactsSQL, afterID, limit)
	if err != nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("query Safeheron alias-null window: %w", err)
	}
	defer rows.Close()
	seenRows := make(map[int64]struct{})
	for rows.Next() {
		var row SafeheronAliasNullRow
		var factID sql.NullInt64
		var providerTransactionKey, movementKind, rawCoinKey, source, destination, amountText, transferMode sql.NullString
		var movementIndex sql.NullInt64
		if err := rows.Scan(&row.TransactionID, &row.MovementKey, &row.IdentityAlgorithmVersion, &factID, &providerTransactionKey, &movementKind, &rawCoinKey, &source, &destination, &amountText, &transferMode, &movementIndex); err != nil {
			return SafeheronAliasScanResult{}, fmt.Errorf("scan Safeheron alias-null fact: %w", err)
		}
		if _, found := seenRows[row.TransactionID]; !found {
			request.Rows = append(request.Rows, row)
			seenRows[row.TransactionID] = struct{}{}
		}
		if !factID.Valid {
			continue
		}
		amount, err := decimal.NewFromString(amountText.String)
		if err != nil || !providerTransactionKey.Valid || !movementKind.Valid || !rawCoinKey.Valid || !transferMode.Valid || !movementIndex.Valid {
			request.FactsByTransactionID[row.TransactionID] = append(request.FactsByTransactionID[row.TransactionID], SafeheronAliasOccurrenceFact{TransactionID: row.TransactionID})
			continue
		}
		request.FactsByTransactionID[row.TransactionID] = append(request.FactsByTransactionID[row.TransactionID], SafeheronAliasOccurrenceFact{TransactionID: row.TransactionID, Occurrence: SafeheronOccurrenceInput{
			ProviderTransactionKey: providerTransactionKey.String, MovementKind: MovementKind(movementKind.String), RawCoinKey: rawCoinKey.String,
			NormalizedSource: source.String, NormalizedDestination: destination.String, Amount: amount,
			TransferMode: TransferMode(transferMode.String), MovementIndex: int(movementIndex.Int64),
		}})
	}
	if err := rows.Err(); err != nil {
		return SafeheronAliasScanResult{}, fmt.Errorf("iterate Safeheron alias-null facts: %w", err)
	}
	return buildSafeheronAliasScanResult(ctx, tx, request, afterID, limit)
}

func revalidateSafeheronAliasSafetyTx(ctx context.Context, tx *sql.Tx, evidence SafeheronAliasRepairEvidence, trustedNow time.Time) error {
	if err := evidence.validate(); err != nil {
		return err
	}
	if err := validateSafeheronAliasEvidenceFreshness(evidence, trustedNow); err != nil {
		return err
	}
	var schemaAReady bool
	if err := tx.QueryRowContext(ctx, selectSafeheronAliasSchemaAReadySQL).Scan(&schemaAReady); err != nil {
		return fmt.Errorf("verify Migration A schema: %w", err)
	}
	if !schemaAReady {
		return fmt.Errorf("Safeheron alias scanner requires Migration A schema")
	}
	var providerLeases, syncLeases, inFlight, oldSessions int
	if err := tx.QueryRowContext(ctx, selectSafeheronAliasQuiescenceSQL, evidence.V2ServerSHA).Scan(&providerLeases, &syncLeases, &inFlight, &oldSessions); err != nil {
		return fmt.Errorf("revalidate Safeheron alias quiescence: %w", err)
	}
	if providerLeases != 0 || syncLeases != 0 || inFlight != 0 || oldSessions != 0 {
		return fmt.Errorf("Safeheron alias scanner observed active work: provider=%d sync=%d in_flight=%d old_sessions=%d", providerLeases, syncLeases, inFlight, oldSessions)
	}
	currentAccountHash, err := loadCanonicalAccountPolicyHash(ctx, tx)
	if err != nil {
		return err
	}
	if currentAccountHash != evidence.FrozenAccountHash || evidence.AccountHashSamples[len(evidence.AccountHashSamples)-1].SHA256 != currentAccountHash || evidence.DrainSamples[len(evidence.DrainSamples)-1].AccountHash != currentAccountHash {
		return fmt.Errorf("current account/policy canonical hash does not match frozen evidence")
	}
	return nil
}

func buildSafeheronAliasScanResult(ctx context.Context, tx *sql.Tx, request SafeheronAliasRepairRequest, afterID int64, limit int) (SafeheronAliasScanResult, error) {
	result := SafeheronAliasScanResult{AfterID: afterID, LastID: afterID, Limit: limit, AliasNull: len(request.Rows)}
	planned := make(map[string]int64)
	for _, row := range request.Rows {
		result.LastID = row.TransactionID
		facts := request.FactsByTransactionID[row.TransactionID]
		if len(facts) == 0 || (len(facts) == 1 && strings.TrimSpace(facts[0].Occurrence.ProviderTransactionKey) == "") {
			result.Missing++
			continue
		}
		if len(facts) != 1 {
			result.Ambiguous++
			continue
		}
		occurrence, err := BuildSafeheronOccurrence(facts[0].Occurrence)
		if err != nil {
			result.Missing++
			continue
		}
		var owner int64
		err = tx.QueryRowContext(ctx, selectSafeheronOccurrenceOwnerSQL, occurrence.Key).Scan(&owner)
		if err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("query Safeheron occurrence owner: %w", err)
		}
		if err == nil {
			request.ExistingOccurrenceOwners[occurrence.Key] = owner
		}
		if err == nil && owner != row.TransactionID {
			result.Duplicate++
			continue
		}
		if owner, found := planned[occurrence.Key]; found && owner != row.TransactionID {
			result.Duplicate++
			continue
		}
		planned[occurrence.Key] = row.TransactionID
	}
	if result.Missing != 0 || result.Duplicate != 0 || result.Ambiguous != 0 {
		return result, fmt.Errorf("Safeheron alias scan hard stop: missing=%d duplicate=%d ambiguous=%d", result.Missing, result.Duplicate, result.Ambiguous)
	}
	plan, err := PlanSafeheronAliasRepair(request)
	if err != nil {
		return result, err
	}
	result.Plan = plan
	result.Repairable = plan.Repairable
	return result, nil
}

func loadCanonicalAccountPolicyHash(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, selectCanonicalAccountPolicyRecordsSQL)
	if err != nil {
		return "", fmt.Errorf("query canonical account/policy records: %w", err)
	}
	defer rows.Close()
	records := make([]companyfundcontract.CanonicalAccountPolicyRecord, 0)
	for rows.Next() {
		var record companyfundcontract.CanonicalAccountPolicyRecord
		if err := rows.Scan(&record.AccountID, &record.Channel, &record.ProviderAccountKey, &record.Address, &record.NetworkFamily, &record.AccountEnabled, &record.AssetKey, &record.PolicyEnabled); err != nil {
			return "", fmt.Errorf("scan canonical account/policy record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate canonical account/policy records: %w", err)
	}
	exported, err := companyfundcontract.BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		return "", err
	}
	return exported.SHA256, nil
}

func applySafeheronAliasPatchTx(ctx context.Context, tx *sql.Tx, patch SafeheronAliasPatch) error {
	if patch.TransactionID <= 0 || patch.OccurrenceKey == "" || patch.OccurrenceAlgorithmVersion != SafeheronOccurrenceAlgorithmVersion || patch.expectedMovementKey == "" {
		return fmt.Errorf("Safeheron alias patch is incomplete")
	}
	var updatedID int64
	if err := tx.QueryRowContext(ctx, updateSafeheronOccurrenceAliasSQL, patch.TransactionID, patch.OccurrenceKey, patch.OccurrenceAlgorithmVersion).Scan(&updatedID); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("Safeheron alias transaction %d changed before apply", patch.TransactionID)
		}
		return fmt.Errorf("update Safeheron alias transaction %d: %w", patch.TransactionID, err)
	}
	input := TransactionUpsertInput{MovementKey: patch.expectedMovementKey, IdentityAlgorithmVersion: SafeheronMovementIdentityAlgorithmVersion, ProviderOccurrenceKey: patch.OccurrenceKey, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}
	resolved, found, err := loadSafeheronCompanyFundTransactionForUpdate(ctx, tx, input)
	if err != nil || !found || resolved.ID != patch.TransactionID {
		return fmt.Errorf("authoritative resolver rejected applied Safeheron alias %d: %w", patch.TransactionID, err)
	}
	return nil
}
