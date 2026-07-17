package companyfund

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

const maxSafeheronAliasRepairWindow = 1000
const SafeheronAliasRepairApplyGate = "RUN_COMPANY_FUND_SAFEHERON_ALIAS_REPAIR"

// SafeheronAliasEvidenceFreshnessWindow is the maximum trusted-clock age of
// the final account-freeze and drain samples at every repair safety check.
const SafeheronAliasEvidenceFreshnessWindow = 10 * time.Second

type SafeheronAliasRepairEvidence struct {
	V2ServerSHA          string                      `json:"v2_server_sha"`
	FrozenAccountHash    string                      `json:"frozen_account_hash"`
	AccountHashSamples   []SafeheronAliasHashSample  `json:"account_hash_samples"`
	DrainSamples         []SafeheronAliasDrainSample `json:"drain_samples"`
	AccountHashStableFor time.Duration               `json:"account_hash_stable_for"`
}

type SafeheronAliasHashSample struct {
	At     time.Time `json:"at"`
	SHA256 string    `json:"sha256"`
}

type SafeheronAliasDrainSample struct {
	At             time.Time `json:"at"`
	ProviderLeases int       `json:"provider_leases"`
	SyncLeases     int       `json:"sync_leases"`
	InFlight       int       `json:"in_flight"`
	OldAppSessions int       `json:"old_app_sessions"`
	AccountHash    string    `json:"account_hash"`
}

type SafeheronAliasLiveProbe struct {
	ManifestPath    string
	EnvironmentPath string
	ExpectedV2SHA   string
}

type SafeheronAliasLiveProbeSnapshot struct {
	ManifestSHA       string
	ManifestDigest    string
	EnvironmentDigest string
}

func CaptureSafeheronAliasLiveProbe(probe SafeheronAliasLiveProbe) (SafeheronAliasLiveProbeSnapshot, error) {
	if strings.TrimSpace(probe.ManifestPath) == "" || strings.TrimSpace(probe.EnvironmentPath) == "" || !validLowerHex(probe.ExpectedV2SHA, 40) {
		return SafeheronAliasLiveProbeSnapshot{}, fmt.Errorf("Safeheron alias live probe paths and exact v2 SHA are required")
	}
	manifest, err := os.ReadFile(probe.ManifestPath)
	if err != nil {
		return SafeheronAliasLiveProbeSnapshot{}, fmt.Errorf("read installed release manifest: %w", err)
	}
	manifestSHA, err := decodeExactReleaseManifestSHA(manifest)
	if err != nil || manifestSHA != probe.ExpectedV2SHA {
		return SafeheronAliasLiveProbeSnapshot{}, fmt.Errorf("installed release manifest does not prove the exact v2 SHA")
	}
	environment, err := os.ReadFile(probe.EnvironmentPath)
	if err != nil {
		return SafeheronAliasLiveProbeSnapshot{}, fmt.Errorf("read installed environment: %w", err)
	}
	assignments := 0
	exact := 0
	for _, line := range strings.Split(string(environment), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}
		if strings.HasPrefix(trimmed, "COMPANY_FUND_START_BACKGROUND_WORKERS") && strings.Contains(trimmed, "=") {
			assignments++
			if line == "COMPANY_FUND_START_BACKGROUND_WORKERS=false" {
				exact++
			}
		}
	}
	if assignments != 1 || exact != 1 {
		return SafeheronAliasLiveProbeSnapshot{}, fmt.Errorf("installed environment must contain one normalized workers=false assignment")
	}
	manifestDigest := sha256.Sum256(manifest)
	environmentDigest := sha256.Sum256(environment)
	return SafeheronAliasLiveProbeSnapshot{ManifestSHA: manifestSHA, ManifestDigest: hex.EncodeToString(manifestDigest[:]), EnvironmentDigest: hex.EncodeToString(environmentDigest[:])}, nil
}

func decodeExactReleaseManifestSHA(data []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return "", fmt.Errorf("release manifest must be an object")
	}
	sha := ""
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "server_sha" || sha != "" {
			return "", fmt.Errorf("release manifest must contain only one server_sha")
		}
		if err := decoder.Decode(&sha); err != nil || sha == "" {
			return "", fmt.Errorf("release manifest server_sha must be a string")
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') || sha == "" {
		return "", fmt.Errorf("release manifest is incomplete")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", fmt.Errorf("release manifest contains trailing data")
	}
	return sha, nil
}

func ValidateSafeheronAliasLiveProbeUnchanged(before, after SafeheronAliasLiveProbeSnapshot) error {
	if before.ManifestSHA == "" || before != after {
		return fmt.Errorf("Safeheron alias live manifest/environment changed during scan")
	}
	return nil
}

type SafeheronAliasNullRow struct {
	TransactionID            int64  `json:"transaction_id"`
	MovementKey              string `json:"movement_key"`
	IdentityAlgorithmVersion string `json:"identity_algorithm_version"`
}

type SafeheronAliasOccurrenceFact struct {
	TransactionID int64                    `json:"transaction_id"`
	Occurrence    SafeheronOccurrenceInput `json:"occurrence"`
}

type SafeheronAliasRepairRequest struct {
	Evidence                 SafeheronAliasRepairEvidence             `json:"evidence"`
	AfterID                  int64                                    `json:"after_id"`
	Limit                    int                                      `json:"limit"`
	Rows                     []SafeheronAliasNullRow                  `json:"rows"`
	FactsByTransactionID     map[int64][]SafeheronAliasOccurrenceFact `json:"facts_by_transaction_id"`
	ExistingOccurrenceOwners map[string]int64                         `json:"existing_occurrence_owners"`
}

// SafeheronAliasPatch deliberately exposes only the alias update. Legacy
// movement identity, provider facts and transaction rows are never returned as
// repair output and are never candidates for merge or deletion.
type SafeheronAliasPatch struct {
	TransactionID              int64  `json:"transaction_id"`
	OccurrenceKey              string `json:"provider_occurrence_key"`
	OccurrenceAlgorithmVersion string `json:"provider_occurrence_algorithm_version"`
	expectedMovementKey        string
}

type SafeheronAliasRepairPlan struct {
	AfterID    int64                 `json:"after_id"`
	LastID     int64                 `json:"last_id"`
	Limit      int                   `json:"limit"`
	Scanned    int                   `json:"scanned"`
	Repairable int                   `json:"repairable"`
	Patches    []SafeheronAliasPatch `json:"patches"`
}

type SafeheronAliasScanResult struct {
	AfterID    int64                    `json:"after_id"`
	LastID     int64                    `json:"last_id"`
	Limit      int                      `json:"limit"`
	AliasNull  int                      `json:"alias_null"`
	Missing    int                      `json:"missing"`
	Duplicate  int                      `json:"duplicate"`
	Ambiguous  int                      `json:"ambiguous"`
	Repairable int                      `json:"repairable"`
	Applied    int                      `json:"applied"`
	Plan       SafeheronAliasRepairPlan `json:"-"`
}

func PlanSafeheronAliasRepair(request SafeheronAliasRepairRequest) (SafeheronAliasRepairPlan, error) {
	if err := request.Evidence.validate(); err != nil {
		return SafeheronAliasRepairPlan{}, err
	}
	if request.AfterID < 0 {
		return SafeheronAliasRepairPlan{}, fmt.Errorf("Safeheron alias repair cursor must be non-negative")
	}
	if request.Limit < 1 || request.Limit > maxSafeheronAliasRepairWindow {
		return SafeheronAliasRepairPlan{}, fmt.Errorf("Safeheron alias repair limit must be between 1 and %d", maxSafeheronAliasRepairWindow)
	}
	if len(request.Rows) > request.Limit {
		return SafeheronAliasRepairPlan{}, fmt.Errorf("Safeheron alias repair rows exceed the bounded window")
	}
	rows := append([]SafeheronAliasNullRow(nil), request.Rows...)
	sort.Slice(rows, func(left, right int) bool { return rows[left].TransactionID < rows[right].TransactionID })
	plan := SafeheronAliasRepairPlan{AfterID: request.AfterID, Limit: request.Limit, Scanned: len(rows), Patches: make([]SafeheronAliasPatch, 0, len(rows))}
	plannedOwners := make(map[string]int64, len(rows))
	lastID := request.AfterID
	for _, row := range rows {
		if row.TransactionID <= lastID {
			return SafeheronAliasRepairPlan{}, fmt.Errorf("Safeheron alias repair window is not strictly after the cursor")
		}
		lastID = row.TransactionID
		patch, err := planSafeheronAliasPatch(row, request.FactsByTransactionID[row.TransactionID], request.ExistingOccurrenceOwners, plannedOwners, resolveSafeheronPersistedIdentityPair)
		if err != nil {
			return SafeheronAliasRepairPlan{}, err
		}
		plannedOwners[patch.OccurrenceKey] = row.TransactionID
		plan.Patches = append(plan.Patches, patch)
	}
	plan.LastID = lastID
	plan.Repairable = len(plan.Patches)
	return plan, nil
}

type safeheronPersistedIdentityResolver func([]persistedCompanyFundTransaction, TransactionUpsertInput) (persistedCompanyFundTransaction, bool, error)

func planSafeheronAliasPatch(row SafeheronAliasNullRow, facts []SafeheronAliasOccurrenceFact, existingOwners, plannedOwners map[string]int64, resolve safeheronPersistedIdentityResolver) (SafeheronAliasPatch, error) {
	if strings.TrimSpace(row.MovementKey) == "" || row.IdentityAlgorithmVersion != MovementIdentityAlgorithmVersion {
		return SafeheronAliasPatch{}, fmt.Errorf("Safeheron alias repair accepts only complete legacy v1 identity rows")
	}
	if len(facts) != 1 || facts[0].TransactionID != row.TransactionID {
		return SafeheronAliasPatch{}, fmt.Errorf("Safeheron alias repair transaction %d requires exactly one complete occurrence fact", row.TransactionID)
	}
	occurrence, err := BuildSafeheronOccurrence(facts[0].Occurrence)
	if err != nil {
		return SafeheronAliasPatch{}, fmt.Errorf("build Safeheron occurrence for transaction %d: %w", row.TransactionID, err)
	}
	if owner, found := existingOwners[occurrence.Key]; found && owner != row.TransactionID {
		return SafeheronAliasPatch{}, fmt.Errorf("Safeheron occurrence alias already belongs to transaction %d", owner)
	}
	if owner, found := plannedOwners[occurrence.Key]; found && owner != row.TransactionID {
		return SafeheronAliasPatch{}, fmt.Errorf("Safeheron occurrence alias is ambiguous across repair rows")
	}
	movement := buildSafeheronMovementIdentity(occurrence.Input)
	candidate := persistedCompanyFundTransaction{ID: row.TransactionID, MovementKey: row.MovementKey, IdentityAlgorithmVersion: MovementIdentityAlgorithmVersion, ProviderOccurrenceKey: occurrence.Key, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}
	input := TransactionUpsertInput{MovementKey: movement.Key, IdentityAlgorithmVersion: SafeheronMovementIdentityAlgorithmVersion, ProviderOccurrenceKey: occurrence.Key, ProviderOccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion}
	resolved, found, err := resolve([]persistedCompanyFundTransaction{candidate}, input)
	if err != nil || !found || resolved.ID != row.TransactionID {
		return SafeheronAliasPatch{}, fmt.Errorf("authoritative Safeheron resolver rejected transaction %d: %w", row.TransactionID, err)
	}
	return SafeheronAliasPatch{TransactionID: row.TransactionID, OccurrenceKey: occurrence.Key, OccurrenceAlgorithmVersion: SafeheronOccurrenceAlgorithmVersion, expectedMovementKey: movement.Key}, nil
}

func (evidence SafeheronAliasRepairEvidence) validate() error {
	switch {
	case !validLowerHex(evidence.V2ServerSHA, 40):
		return fmt.Errorf("Safeheron alias repair requires the exact v2 SHA")
	case !validLowerHex(evidence.FrozenAccountHash, 64):
		return fmt.Errorf("Safeheron alias repair requires a frozen account hash")
	case len(evidence.AccountHashSamples) < 3:
		return fmt.Errorf("Safeheron alias repair requires at least three stable account hash samples")
	case len(evidence.DrainSamples) < 3:
		return fmt.Errorf("Safeheron alias repair requires at least three terminal drain samples")
	case evidence.AccountHashStableFor < 10*time.Second:
		return fmt.Errorf("Safeheron alias repair requires a measured stable account hash window")
	}
	for index, sample := range evidence.AccountHashSamples {
		if sample.At.IsZero() || sample.SHA256 != evidence.FrozenAccountHash || (index > 0 && sample.At.Sub(evidence.AccountHashSamples[index-1].At) < 5*time.Second) {
			return fmt.Errorf("Safeheron alias repair account hash changed during quiescence")
		}
	}
	if evidence.AccountHashSamples[len(evidence.AccountHashSamples)-1].At.Sub(evidence.AccountHashSamples[0].At) < evidence.AccountHashStableFor {
		return fmt.Errorf("Safeheron alias repair account hash stable window is incomplete")
	}
	for index, sample := range evidence.DrainSamples {
		if sample.At.IsZero() || sample.AccountHash != evidence.FrozenAccountHash || sample.ProviderLeases != 0 || sample.SyncLeases != 0 || sample.InFlight != 0 || sample.OldAppSessions != 0 || (index > 0 && sample.At.Sub(evidence.DrainSamples[index-1].At) < 5*time.Second) {
			return fmt.Errorf("Safeheron alias repair drain evidence is not terminal and stable")
		}
	}
	if evidence.DrainSamples[len(evidence.DrainSamples)-1].At.Sub(evidence.DrainSamples[0].At) < evidence.AccountHashStableFor {
		return fmt.Errorf("Safeheron alias repair drain stable window is incomplete")
	}
	return nil
}

func validateSafeheronAliasEvidenceFreshness(evidence SafeheronAliasRepairEvidence, trustedNow time.Time) error {
	if trustedNow.IsZero() || len(evidence.AccountHashSamples) == 0 || len(evidence.DrainSamples) == 0 {
		return fmt.Errorf("Safeheron alias repair requires a trusted current time and complete samples")
	}
	for label, sampledAt := range map[string]time.Time{
		"account hash": evidence.AccountHashSamples[len(evidence.AccountHashSamples)-1].At,
		"drain":        evidence.DrainSamples[len(evidence.DrainSamples)-1].At,
	} {
		if sampledAt.After(trustedNow) {
			return fmt.Errorf("Safeheron alias repair %s evidence is in the future", label)
		}
		if trustedNow.Sub(sampledAt) > SafeheronAliasEvidenceFreshnessWindow {
			return fmt.Errorf("Safeheron alias repair %s evidence is stale", label)
		}
	}
	return nil
}

func validLowerHex(value string, bytes int) bool {
	if len(value) != bytes {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

const selectSafeheronAliasNullRepairFactsSQL = `
WITH alias_window AS (
	SELECT movement.*
	FROM company_fund_transactions AS movement
	WHERE movement.channel = 'SAFEHERON'
		AND movement.identity_algorithm_version = 'v1'
		AND movement.provider_occurrence_key IS NULL
		AND movement.id > $1
	ORDER BY movement.id
	LIMIT $2
)
SELECT movement.id,
	movement.movement_key,
	movement.identity_algorithm_version,
	fact.id,
	movement.provider_transaction_id,
	movement.movement_kind,
	fact.provider_extras ->> 'coinKey' AS raw_coin_key,
	COALESCE(movement.from_address_or_account, ''),
	COALESCE(movement.to_address_or_account, ''),
	movement.amount::TEXT,
	movement.transfer_mode,
	movement.movement_index
FROM alias_window AS movement
LEFT JOIN company_fund_provider_transaction_facts AS fact
	ON fact.channel = movement.channel
	AND fact.provider_account_key = movement.provider_account_key
	AND fact.provider_transaction_id = movement.provider_transaction_id
ORDER BY movement.id, fact.id`

const updateSafeheronOccurrenceAliasSQL = `
UPDATE company_fund_transactions
SET provider_occurrence_key = $2,
	provider_occurrence_algorithm_version = $3
WHERE id = $1
	AND channel = 'SAFEHERON'
	AND identity_algorithm_version = 'v1'
	AND provider_occurrence_key IS NULL
RETURNING id`
