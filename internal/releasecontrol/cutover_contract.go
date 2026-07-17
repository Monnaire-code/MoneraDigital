package releasecontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"monera-digital/internal/companyfundcontract"
)

const cutoverSampleInterval = 5 * time.Second

type CutoverFixtureIdentity struct {
	RunID          string `json:"run_id"`
	TransactionKey string `json:"transaction_key"`
	OccurrenceKey  string `json:"occurrence_key"`
	PermanentTest  bool   `json:"permanent_test"`
}

type CutoverRunTemplateInput struct {
	Now                      time.Time
	EntropyHex               string
	CurrentSHA               string
	ASHA                     string
	V2SHA                    string
	BSHA                     string
	ExpectedMigrationCeiling string
	Fixture                  CutoverFixtureIdentity
	EvidenceDir              string
}

type CutoverRunTemplate struct {
	RunID                    string                      `json:"run_id"`
	CurrentSHA               string                      `json:"current_sha"`
	ASHA                     string                      `json:"a_sha"`
	V2SHA                    string                      `json:"v2_sha"`
	BSHA                     string                      `json:"b_sha"`
	ExpectedMigrationCeiling string                      `json:"expected_migration_ceiling"`
	Fixture                  CutoverFixtureIdentity      `json:"fixture"`
	EvidenceDir              string                      `json:"evidence_dir"`
	CheckpointArtifacts      *CheckpointArtifactEvidence `json:"checkpoint_artifacts,omitempty"`
}

func BuildVerifiedCutoverRunTemplate(ctx context.Context, repository string, input CutoverRunTemplateInput) (CutoverRunTemplate, error) {
	evidence, err := ValidateCheckpointArtifacts(ctx, repository, input.CurrentSHA, input.ASHA, input.V2SHA, input.BSHA)
	if err != nil {
		return CutoverRunTemplate{}, err
	}
	template, err := BuildCutoverRunTemplate(input)
	if err != nil {
		return CutoverRunTemplate{}, err
	}
	template.CheckpointArtifacts = &evidence
	return template, nil
}

func BuildCutoverRunTemplate(input CutoverRunTemplateInput) (CutoverRunTemplate, error) {
	if input.Now.IsZero() || input.Now.Location() != time.UTC {
		return CutoverRunTemplate{}, fmt.Errorf("cutover run time must be explicit UTC")
	}
	for label, value := range map[string]string{"CURRENT_SHA": input.CurrentSHA, "A_SHA": input.ASHA, "V2_SHA": input.V2SHA, "B_SHA": input.BSHA} {
		if err := ValidateFullSHA(value); err != nil {
			return CutoverRunTemplate{}, fmt.Errorf("%s: %w", label, err)
		}
	}
	if input.ExpectedMigrationCeiling != "052" {
		return CutoverRunTemplate{}, fmt.Errorf("checkpoint A requires expected migration ceiling 052")
	}
	if !allDistinctStrings(input.CurrentSHA, input.ASHA, input.V2SHA, input.BSHA) {
		return CutoverRunTemplate{}, fmt.Errorf("current, checkpoint A, v2, and checkpoint B artifacts must be distinct commits")
	}
	entropy := strings.TrimSpace(input.EntropyHex)
	if entropy == "" {
		var bytes [4]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return CutoverRunTemplate{}, fmt.Errorf("generate cutover entropy: %w", err)
		}
		entropy = hex.EncodeToString(bytes[:])
	}
	if len(entropy) != 8 || !validLowerHexString(entropy) {
		return CutoverRunTemplate{}, fmt.Errorf("cutover entropy must be exactly 8 lowercase hex characters")
	}
	if strings.TrimSpace(input.Fixture.TransactionKey) == "" || strings.TrimSpace(input.Fixture.OccurrenceKey) == "" {
		return CutoverRunTemplate{}, fmt.Errorf("cutover fixture transaction and occurrence identities are required")
	}
	if !filepath.IsAbs(input.EvidenceDir) || filepath.Clean(input.EvidenceDir) == "/" {
		return CutoverRunTemplate{}, fmt.Errorf("cutover evidence directory must be a dedicated absolute path")
	}
	runID := "CF_CUTOVER_" + input.Now.Format("20060102T150405Z") + "_" + entropy
	fixture := input.Fixture
	fixture.RunID = runID
	fixture.PermanentTest = true
	return CutoverRunTemplate{
		RunID: runID, CurrentSHA: input.CurrentSHA, ASHA: input.ASHA, V2SHA: input.V2SHA, BSHA: input.BSHA,
		ExpectedMigrationCeiling: input.ExpectedMigrationCeiling,
		Fixture:                  fixture, EvidenceDir: filepath.Clean(input.EvidenceDir),
	}, nil
}

func allDistinctStrings(values ...string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

type CanonicalAccountPolicyRecord = companyfundcontract.CanonicalAccountPolicyRecord
type CanonicalAccountPolicyExport = companyfundcontract.CanonicalAccountPolicyExport

func BuildCanonicalAccountPolicyExport(records []CanonicalAccountPolicyRecord) (CanonicalAccountPolicyExport, error) {
	return companyfundcontract.BuildCanonicalAccountPolicyExport(records)
}

type CutoverHashSample struct {
	At     time.Time `json:"at"`
	SHA256 string    `json:"sha256"`
}

type StableAccountHashResult struct {
	SHA256      string        `json:"sha256"`
	StableSince time.Time     `json:"stable_since"`
	StableFor   time.Duration `json:"stable_for"`
}

func ValidateStableAccountHash(samples []CutoverHashSample, minimumWindow time.Duration) (StableAccountHashResult, error) {
	if minimumWindow < cutoverSampleInterval || len(samples) < 3 {
		return StableAccountHashResult{}, fmt.Errorf("account hash freeze requires at least three samples and a five-second window")
	}
	stableSince := samples[0].At
	current := samples[0].SHA256
	if samples[0].At.IsZero() || !validHexLength(current, 64) {
		return StableAccountHashResult{}, fmt.Errorf("account hash sample is invalid")
	}
	for index := 1; index < len(samples); index++ {
		if samples[index].At.Sub(samples[index-1].At) < cutoverSampleInterval || !validHexLength(samples[index].SHA256, 64) {
			return StableAccountHashResult{}, fmt.Errorf("account hash samples must be valid and at least five seconds apart")
		}
		if samples[index].SHA256 != current {
			current = samples[index].SHA256
			stableSince = samples[index].At
		}
	}
	stableFor := samples[len(samples)-1].At.Sub(stableSince)
	if stableFor < minimumWindow {
		return StableAccountHashResult{}, fmt.Errorf("account hash stable window is incomplete")
	}
	return StableAccountHashResult{SHA256: current, StableSince: stableSince, StableFor: stableFor}, nil
}

type CutoverDrainConfig struct {
	ProviderLeaseWindow time.Duration `json:"provider_lease_window"`
	SyncLeaseWindow     time.Duration `json:"sync_lease_window"`
	InFlightWindow      time.Duration `json:"in_flight_window"`
}

type CutoverDrainSample struct {
	At             time.Time `json:"at"`
	ProviderLeases int       `json:"provider_leases"`
	SyncLeases     int       `json:"sync_leases"`
	InFlight       int       `json:"in_flight"`
	OldAppSessions int       `json:"old_app_sessions"`
	AccountHash    string    `json:"account_hash"`
}

type CutoverDrainEvidence struct {
	OldWorkersWereEnabled bool                 `json:"old_workers_were_enabled"`
	OldMainPID            string               `json:"old_main_pid"`
	OldInvocationID       string               `json:"old_invocation_id"`
	OldWorkersExited      bool                 `json:"old_workers_exited"`
	OldWorkersExitedAt    time.Time            `json:"old_workers_exited_at"`
	FrozenAccountHash     string               `json:"frozen_account_hash"`
	Samples               []CutoverDrainSample `json:"samples"`
}

type CutoverDrainResult struct {
	MaxWindow           time.Duration `json:"max_window"`
	TerminalZeroSamples int           `json:"terminal_zero_samples"`
}

func ValidateCutoverDrain(config CutoverDrainConfig, evidence CutoverDrainEvidence) (CutoverDrainResult, error) {
	maxWindow, err := config.effectiveMaxWindow()
	if err != nil {
		return CutoverDrainResult{}, err
	}
	if !evidence.OldWorkersWereEnabled || strings.TrimSpace(evidence.OldMainPID) == "" || strings.TrimSpace(evidence.OldInvocationID) == "" || !evidence.OldWorkersExited || evidence.OldWorkersExitedAt.IsZero() {
		return CutoverDrainResult{}, fmt.Errorf("drain requires the old workers=true MainPID and InvocationID to exit")
	}
	if !validHexLength(evidence.FrozenAccountHash, 64) || len(evidence.Samples) < 3 {
		return CutoverDrainResult{}, fmt.Errorf("drain requires a frozen account hash and samples")
	}
	for index, sample := range evidence.Samples {
		if sample.At.Before(evidence.OldWorkersExitedAt) || sample.AccountHash != evidence.FrozenAccountHash {
			return CutoverDrainResult{}, fmt.Errorf("drain sample violates the frozen account state")
		}
		if index > 0 && sample.At.Sub(evidence.Samples[index-1].At) < cutoverSampleInterval {
			return CutoverDrainResult{}, fmt.Errorf("drain samples must be at least five seconds apart")
		}
	}
	last := evidence.Samples[len(evidence.Samples)-1]
	if last.At.Sub(evidence.OldWorkersExitedAt) < maxWindow {
		return CutoverDrainResult{}, fmt.Errorf("drain did not observe the complete effective maximum window")
	}
	terminal := evidence.Samples[len(evidence.Samples)-3:]
	for _, sample := range terminal {
		if sample.ProviderLeases != 0 || sample.SyncLeases != 0 || sample.InFlight != 0 || sample.OldAppSessions != 0 {
			return CutoverDrainResult{}, fmt.Errorf("drain requires three terminal zero samples")
		}
	}
	return CutoverDrainResult{MaxWindow: maxWindow, TerminalZeroSamples: 3}, nil
}

func (config CutoverDrainConfig) effectiveMaxWindow() (time.Duration, error) {
	windows := []time.Duration{config.ProviderLeaseWindow, config.SyncLeaseWindow, config.InFlightWindow}
	for index, window := range windows {
		if window < 0 {
			return 0, fmt.Errorf("drain window cannot be negative")
		}
		if window == 0 {
			windows[index] = 5 * time.Minute
		}
	}
	maxWindow := windows[0]
	for _, window := range windows[1:] {
		if window > maxWindow {
			maxWindow = window
		}
	}
	return maxWindow, nil
}

type CutoverFixtureSeedInput struct {
	RunID          string
	TransactionKey string
	OccurrenceKey  string
	EventOneID     string
	EventTwoID     string
}

type CutoverFixtureSeed struct {
	RunID          string `json:"run_id"`
	TransactionKey string `json:"transaction_key"`
	OccurrenceKey  string `json:"occurrence_key"`
	EventOneID     string `json:"event_one_id"`
	EventTwoID     string `json:"event_two_id"`
	Scope          string `json:"scope"`
	PermanentTest  bool   `json:"permanent_test"`
}

type CutoverFixtureObservation struct {
	WorkersEnabled bool `json:"workers_enabled"`
	PendingEvents  int  `json:"pending_events"`
	ClaimedEvents  int  `json:"claimed_events"`
	Transactions   int  `json:"transactions"`
	Movements      int  `json:"movements"`
}

type CutoverFixtureMark struct {
	Scope         string `json:"scope"`
	PermanentTest bool   `json:"permanent_test"`
	AllowDelete   bool   `json:"allow_delete"`
	AllowMerge    bool   `json:"allow_merge"`
}

func BuildCutoverFixtureSeed(input CutoverFixtureSeedInput) (CutoverFixtureSeed, error) {
	if !isCutoverRunID(input.RunID) || strings.TrimSpace(input.TransactionKey) == "" || strings.TrimSpace(input.OccurrenceKey) == "" || strings.TrimSpace(input.EventOneID) == "" || strings.TrimSpace(input.EventTwoID) == "" || input.EventOneID == input.EventTwoID {
		return CutoverFixtureSeed{}, fmt.Errorf("fixture requires one cutover run, exact TxKey/occurrence and two distinct event IDs")
	}
	return CutoverFixtureSeed{
		RunID: input.RunID, TransactionKey: input.TransactionKey, OccurrenceKey: input.OccurrenceKey,
		EventOneID: input.EventOneID, EventTwoID: input.EventTwoID,
		Scope: input.RunID + "/" + input.TransactionKey + "/" + input.OccurrenceKey, PermanentTest: true,
	}, nil
}

func VerifyCutoverFixture(seed CutoverFixtureSeed, observation CutoverFixtureObservation) error {
	if !seed.PermanentTest || seed.Scope != seed.RunID+"/"+seed.TransactionKey+"/"+seed.OccurrenceKey {
		return fmt.Errorf("fixture scope is not exact or permanently marked TEST")
	}
	if !observation.WorkersEnabled {
		if observation.PendingEvents != 2 || observation.ClaimedEvents != 0 || observation.Transactions != 0 || observation.Movements != 0 {
			return fmt.Errorf("workers=false fixture must remain two PENDING events with zero claims and movements")
		}
		return nil
	}
	if observation.PendingEvents != 0 || observation.ClaimedEvents != 2 || observation.Transactions != 1 || observation.Movements != 1 {
		return fmt.Errorf("same-SHA workers=true fixture must converge to exactly one movement")
	}
	return nil
}

func MarkCutoverFixtureTest(seed CutoverFixtureSeed) CutoverFixtureMark {
	return CutoverFixtureMark{Scope: seed.Scope, PermanentTest: true, AllowDelete: false, AllowMerge: false}
}

func ValidateCutoverFixtureRetry(previousRunID, nextRunID string) error {
	if !isCutoverRunID(previousRunID) || !isCutoverRunID(nextRunID) || previousRunID == nextRunID {
		return fmt.Errorf("fixture retry requires a new valid cutover run id")
	}
	return nil
}

type ReleaseFailureContract struct {
	ID                    string `json:"id"`
	Mode                  Mode   `json:"mode"`
	ServerUnchanged       bool   `json:"server_unchanged"`
	EnvironmentUnchanged  bool   `json:"environment_unchanged"`
	WorkersRemainFalse    bool   `json:"workers_remain_false"`
	RestorePreCallState   bool   `json:"restore_pre_call_state"`
	InstalledSHAUnchanged bool   `json:"installed_sha_unchanged"`
	InvocationUnchanged   bool   `json:"invocation_unchanged"`
	SchemaARetained       bool   `json:"schema_a_retained"`
	SameV2Retained        bool   `json:"same_v2_retained"`
	ServiceStopped        bool   `json:"service_stopped"`
	WorkersQuiesced       bool   `json:"workers_quiesced"`
	RetainControlLock     bool   `json:"retain_control_lock"`
	ManualQuiesceRequired bool   `json:"manual_quiesce_required"`
}

func CompanyFundReleaseFailureMatrix() []ReleaseFailureContract {
	return []ReleaseFailureContract{
		{ID: "migration-a", Mode: ModeMigrationOnly, ServerUnchanged: true, EnvironmentUnchanged: true},
		{ID: "workers-off", Mode: ModeWorkersOffCurrent, RestorePreCallState: true, InstalledSHAUnchanged: true},
		{ID: "server-dark", Mode: ModeServerDark, WorkersRemainFalse: true},
		{ID: "workers-on-health", Mode: ModeWorkersOnInstalled, WorkersRemainFalse: true, InstalledSHAUnchanged: true},
		{ID: "migration-b-atomic", Mode: ModeMigrationOnly, ServerUnchanged: true, EnvironmentUnchanged: true, InvocationUnchanged: true, SchemaARetained: true, SameV2Retained: true, RetainControlLock: true},
		{ID: "migration-b-non-atomic", Mode: ModeMigrationOnly, ServerUnchanged: true, ServiceStopped: true, WorkersQuiesced: true, InvocationUnchanged: true, SameV2Retained: true, RetainControlLock: true, ManualQuiesceRequired: true},
	}
}

func ValidateReleaseEvidenceJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decode release evidence: %w", err)
	}
	return validateEvidenceValue(value)
}

func validateEvidenceValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			for _, forbidden := range []string{"token", "webhook", "signature", "credential", "private_key", "secret", "password"} {
				if strings.Contains(normalized, forbidden) {
					return fmt.Errorf("release evidence contains forbidden field %q", key)
				}
			}
			if err := validateEvidenceValue(nested); err != nil {
				return err
			}
		}
	case []any:
		for _, nested := range typed {
			if err := validateEvidenceValue(nested); err != nil {
				return err
			}
		}
	case string:
		lower := strings.ToLower(typed)
		if strings.Contains(lower, "postgres://") || strings.Contains(lower, "postgresql://") || strings.Contains(lower, "begin private key") {
			return fmt.Errorf("release evidence contains credential material")
		}
	}
	return nil
}

func isCutoverRunID(value string) bool {
	parts := strings.Split(value, "_")
	if len(parts) != 4 || parts[0] != "CF" || parts[1] != "CUTOVER" || len(parts[2]) != 16 || len(parts[3]) != 8 || !validLowerHexString(parts[3]) {
		return false
	}
	_, err := time.Parse("20060102T150405Z", parts[2])
	return err == nil
}

func validHexLength(value string, length int) bool {
	return len(value) == length && validLowerHexString(value)
}

func validLowerHexString(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
