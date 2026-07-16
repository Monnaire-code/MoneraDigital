package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"monera-digital/internal/companyfund"
	"monera-digital/internal/migration/migrations"
	"monera-digital/internal/releasecontrol"
)

var exitProcess = os.Exit

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		exitProcess(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: company-fund-release <validate|plan|manifest-read|manifest-write|bootstrap-manifest|cutover-template|account-policy-export|freeze-verify|drain-verify|alias-scan|schema-fingerprint|fixture-seed|fixture-verify|fixture-mark-test|failure-matrix|evidence-validate>")
	}
	switch args[0] {
	case "validate":
		return validate(args[1:], output)
	case "plan":
		return printPlan(args[1:], output)
	case "manifest-read":
		return manifestRead(args[1:], output)
	case "manifest-write":
		return manifestWrite(args[1:], output)
	case "bootstrap-manifest":
		return bootstrapManifest(args[1:], output)
	case "cutover-template":
		return cutoverTemplate(args[1:], output)
	case "account-policy-export":
		return accountPolicyExport(args[1:], output)
	case "freeze-verify":
		return freezeVerify(args[1:], output)
	case "drain-verify":
		return drainVerify(args[1:], output)
	case "alias-scan":
		return aliasScan(args[1:], output)
	case "schema-fingerprint":
		return schemaFingerprint(args[1:], output)
	case "fixture-seed":
		return fixtureSeed(args[1:], output)
	case "fixture-verify":
		return fixtureVerify(args[1:], output)
	case "fixture-mark-test":
		return fixtureMarkTest(args[1:], output)
	case "failure-matrix":
		return printJSON(output, releasecontrol.CompanyFundReleaseFailureMatrix())
	case "evidence-validate":
		return evidenceValidate(args[1:], output)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func schemaFingerprint(args []string, output io.Writer) error {
	set := newFlagSet("schema-fingerprint")
	if err := set.Parse(args); err != nil {
		return err
	}
	if os.Getenv(migrations.CompanyFundSchemaFingerprintGate) != "1" {
		return fmt.Errorf("set %s=1 before reading DATABASE_URL or opening the live schema inspector", migrations.CompanyFundSchemaFingerprintGate)
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required for live company-fund schema fingerprint")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, err := migrations.InspectLiveCompanyFundSchema(ctx, db)
	if err != nil {
		return err
	}
	if err := printJSON(output, report); err != nil {
		return err
	}
	if report.State != migrations.CompanyFundSchemaStateB || !report.Migration052Recorded || !report.Migration053Recorded {
		return fmt.Errorf("live company-fund schema is %s with migration_052_recorded=%t migration_053_recorded=%t; final B evidence required", report.State, report.Migration052Recorded, report.Migration053Recorded)
	}
	return nil
}

func cutoverTemplate(args []string, output io.Writer) error {
	set := newFlagSet("cutover-template")
	repository := set.String("repo", "", "repository containing exact checkpoint commits")
	nowValue := set.String("utc", "", "exact UTC RFC3339 timestamp")
	entropy := set.String("entropy-hex", "", "optional deterministic 8-hex suffix")
	currentSHA := set.String("current-sha", "", "40-character current SHA")
	aSHA := set.String("a-sha", "", "40-character Migration A SHA")
	v2SHA := set.String("v2-sha", "", "40-character v2 server SHA")
	bSHA := set.String("b-sha", "", "40-character Migration B SHA")
	ceiling := set.String("expected-ceiling", "", "expected migration ceiling")
	evidenceDir := set.String("evidence-dir", "", "dedicated absolute evidence directory")
	fixtureTx := set.String("fixture-tx-key", "", "dedicated fixture transaction key")
	fixtureOccurrence := set.String("fixture-occurrence-key", "", "dedicated fixture occurrence key")
	if err := set.Parse(args); err != nil {
		return err
	}
	now, err := time.Parse(time.RFC3339, *nowValue)
	if err != nil {
		return fmt.Errorf("parse cutover UTC time: %w", err)
	}
	template, err := releasecontrol.BuildVerifiedCutoverRunTemplate(context.Background(), *repository, releasecontrol.CutoverRunTemplateInput{
		Now: now, EntropyHex: *entropy, CurrentSHA: *currentSHA, ASHA: *aSHA, V2SHA: *v2SHA, BSHA: *bSHA,
		ExpectedMigrationCeiling: *ceiling, EvidenceDir: *evidenceDir,
		Fixture: releasecontrol.CutoverFixtureIdentity{TransactionKey: *fixtureTx, OccurrenceKey: *fixtureOccurrence},
	})
	if err != nil {
		return err
	}
	return printJSON(output, template)
}

func accountPolicyExport(args []string, output io.Writer) error {
	set := newFlagSet("account-policy-export")
	input := set.String("input", "", "JSON account/policy records")
	if err := set.Parse(args); err != nil {
		return err
	}
	var records []releasecontrol.CanonicalAccountPolicyRecord
	if err := readJSONFile(*input, &records); err != nil {
		return err
	}
	exported, err := releasecontrol.BuildCanonicalAccountPolicyExport(records)
	if err != nil {
		return err
	}
	return printJSON(output, struct {
		CanonicalJSON json.RawMessage `json:"canonical_json"`
		SHA256        string          `json:"sha256"`
	}{CanonicalJSON: exported.JSON, SHA256: exported.SHA256})
}

func freezeVerify(args []string, output io.Writer) error {
	set := newFlagSet("freeze-verify")
	input := set.String("input", "", "JSON hash samples")
	windowValue := set.String("window", "", "minimum stable duration")
	if err := set.Parse(args); err != nil {
		return err
	}
	window, err := time.ParseDuration(*windowValue)
	if err != nil {
		return fmt.Errorf("parse freeze window: %w", err)
	}
	var samples []releasecontrol.CutoverHashSample
	if err := readJSONFile(*input, &samples); err != nil {
		return err
	}
	result, err := releasecontrol.ValidateStableAccountHash(samples, window)
	if err != nil {
		return err
	}
	return printJSON(output, result)
}

func drainVerify(args []string, output io.Writer) error {
	set := newFlagSet("drain-verify")
	input := set.String("input", "", "JSON drain config and evidence")
	if err := set.Parse(args); err != nil {
		return err
	}
	var request struct {
		Config   releasecontrol.CutoverDrainConfig   `json:"config"`
		Evidence releasecontrol.CutoverDrainEvidence `json:"evidence"`
	}
	if err := readJSONFile(*input, &request); err != nil {
		return err
	}
	result, err := releasecontrol.ValidateCutoverDrain(request.Config, request.Evidence)
	if err != nil {
		return err
	}
	return printJSON(output, result)
}

func aliasScan(args []string, output io.Writer) error {
	set := newFlagSet("alias-scan")
	inputPath := set.String("input", "", "JSON quiescence/provenance/freeze evidence")
	manifestPath := set.String("installed-manifest", "", "live installed release-manifest.json")
	environmentPath := set.String("environment-file", "", "live installed environment file")
	samplesPath := set.String("samples-artifact", "", "timestamped freeze/drain samples JSON")
	dryRun := set.Bool("dry-run", false, "scan the bounded A-schema window without updates")
	apply := set.Bool("apply", false, "apply the validated aliases atomically")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *dryRun && *apply {
		return errors.New("alias-scan accepts only one of --dry-run or --apply")
	}
	if os.Getenv(companyfund.SafeheronAliasRepairApplyGate) != "1" {
		return fmt.Errorf("set %s=1 before opening the Safeheron alias scanner database", companyfund.SafeheronAliasRepairApplyGate)
	}
	var request companyfund.SafeheronAliasRepairRequest
	if err := readJSONFile(*inputPath, &request); err != nil {
		return err
	}
	var samples struct {
		AccountHashStableFor time.Duration                           `json:"account_hash_stable_for"`
		AccountHashSamples   []companyfund.SafeheronAliasHashSample  `json:"account_hash_samples"`
		DrainSamples         []companyfund.SafeheronAliasDrainSample `json:"drain_samples"`
	}
	if err := readJSONFile(*samplesPath, &samples); err != nil {
		return err
	}
	request.Evidence.AccountHashStableFor = samples.AccountHashStableFor
	request.Evidence.AccountHashSamples = samples.AccountHashSamples
	request.Evidence.DrainSamples = samples.DrainSamples
	probe := companyfund.SafeheronAliasLiveProbe{ManifestPath: *manifestPath, EnvironmentPath: *environmentPath, ExpectedV2SHA: request.Evidence.V2ServerSHA}
	baseline, err := companyfund.CaptureSafeheronAliasLiveProbe(probe)
	if err != nil {
		return err
	}
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required for Safeheron alias scan")
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	db := stdlib.OpenDB(*config)
	defer db.Close()
	scanner := companyfund.NewDBSafeheronAliasRepairScanner(db, probe, baseline)
	var result companyfund.SafeheronAliasScanResult
	if *apply {
		result, err = scanner.ScanAndApplySafeheronAliasNull(context.Background(), request.Evidence, request.AfterID, request.Limit)
	} else {
		result, err = scanner.ScanSafeheronAliasNull(context.Background(), request.Evidence, request.AfterID, request.Limit)
	}
	if err != nil {
		return err
	}
	return printJSON(output, result)
}

func fixtureSeed(args []string, output io.Writer) error {
	set := newFlagSet("fixture-seed")
	runID := set.String("run-id", "", "cutover run id")
	txKey := set.String("tx-key", "", "fixture transaction key")
	occurrence := set.String("occurrence-key", "", "fixture occurrence key")
	eventOne := set.String("event-one-id", "", "first provider event ID")
	eventTwo := set.String("event-two-id", "", "second provider event ID")
	if err := set.Parse(args); err != nil {
		return err
	}
	seed, err := releasecontrol.BuildCutoverFixtureSeed(releasecontrol.CutoverFixtureSeedInput{RunID: *runID, TransactionKey: *txKey, OccurrenceKey: *occurrence, EventOneID: *eventOne, EventTwoID: *eventTwo})
	if err != nil {
		return err
	}
	return printJSON(output, seed)
}

func fixtureVerify(args []string, output io.Writer) error {
	set := newFlagSet("fixture-verify")
	seedPath := set.String("seed", "", "fixture seed JSON")
	observationPath := set.String("observation", "", "fixture observation JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	var seed releasecontrol.CutoverFixtureSeed
	var observation releasecontrol.CutoverFixtureObservation
	if err := readJSONFile(*seedPath, &seed); err != nil {
		return err
	}
	if err := readJSONFile(*observationPath, &observation); err != nil {
		return err
	}
	if err := releasecontrol.VerifyCutoverFixture(seed, observation); err != nil {
		return err
	}
	return printJSON(output, map[string]any{"scope": seed.Scope, "verified": true})
}

func fixtureMarkTest(args []string, output io.Writer) error {
	set := newFlagSet("fixture-mark-test")
	seedPath := set.String("seed", "", "fixture seed JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	var seed releasecontrol.CutoverFixtureSeed
	if err := readJSONFile(*seedPath, &seed); err != nil {
		return err
	}
	if err := releasecontrol.VerifyCutoverFixture(seed, releasecontrol.CutoverFixtureObservation{WorkersEnabled: false, PendingEvents: 2}); err != nil {
		return err
	}
	return printJSON(output, releasecontrol.MarkCutoverFixtureTest(seed))
}

func evidenceValidate(args []string, output io.Writer) error {
	set := newFlagSet("evidence-validate")
	input := set.String("input", "", "release evidence JSON")
	if err := set.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		return err
	}
	if err := releasecontrol.ValidateReleaseEvidenceJSON(data); err != nil {
		return err
	}
	return printJSON(output, map[string]bool{"valid": true})
}

func readJSONFile(path string, destination any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("input JSON path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func bootstrapManifest(args []string, output io.Writer) error {
	set := newFlagSet("bootstrap-manifest")
	repository := set.String("repo", "", "path to the Git repository")
	phase := set.String("phase", "", "bootstrap phase D0 or B0")
	baseRef := set.String("base-ref", "", "exact bootstrap base ref")
	headRef := set.String("head-ref", "", "exact bootstrap head ref")
	mainWorkflowRef := set.String("main-workflow-ref", "", "exact ref containing the main workflow")
	stageWorkflowRef := set.String("stage-workflow-ref", "", "exact ref containing the stage workflow")
	if err := set.Parse(args); err != nil {
		return err
	}
	manifest, err := releasecontrol.BuildBootstrapManifest(context.Background(), releasecontrol.BootstrapManifestInput{
		Repository: *repository, Phase: releasecontrol.BootstrapPhase(*phase),
		BaseRef: *baseRef, HeadRef: *headRef,
		MainWorkflowRef: *mainWorkflowRef, StageWorkflowRef: *stageWorkflowRef,
	})
	if err != nil {
		return err
	}
	return printJSON(output, manifest)
}

func validate(args []string, output io.Writer) error {
	set := newFlagSet("validate")
	event := set.String("event", "", "GitHub event name")
	ref := set.String("ref", "", "Git ref")
	modeValue := set.String("mode", "", "release mode")
	sha := set.String("artifact-sha", "", "full artifact SHA")
	runID := set.String("run-id", "", "manual run id")
	lock := set.String("control-lock", "", "repository run_id@baseline_digest control token")
	ceiling := set.String("migration-ceiling", "", "expected migration ceiling")
	stageHead := set.String("stage-head", "", "full stage HEAD")
	artifactReachable := set.Bool("artifact-reachable", false, "artifact is a stage commit or ancestor")
	if err := set.Parse(args); err != nil {
		return err
	}
	mode, err := releasecontrol.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	control, err := releasecontrol.ValidateControl(releasecontrol.ControlInput{
		EventName: *event, Ref: *ref, Mode: mode, ArtifactSHA: *sha,
		RunID: *runID, ControlLock: *lock, MigrationCeiling: *ceiling,
		StageHead: *stageHead, ArtifactReachable: *artifactReachable,
	})
	if err != nil {
		return err
	}
	return printJSON(output, control)
}

func printPlan(args []string, output io.Writer) error {
	set := newFlagSet("plan")
	modeValue := set.String("mode", "", "release mode")
	if err := set.Parse(args); err != nil {
		return err
	}
	mode, err := releasecontrol.ParseMode(*modeValue)
	if err != nil {
		return err
	}
	plan, _ := releasecontrol.PlanForMode(mode)
	return printJSON(output, plan)
}

func manifestRead(args []string, output io.Writer) error {
	set := newFlagSet("manifest-read")
	path := set.String("path", "", "manifest path")
	if err := set.Parse(args); err != nil {
		return err
	}
	manifest, err := releasecontrol.ReadManifest(*path)
	if err != nil {
		return err
	}
	return printJSON(output, manifest)
}

func manifestWrite(args []string, output io.Writer) error {
	set := newFlagSet("manifest-write")
	path := set.String("path", "", "manifest path")
	sha := set.String("server-sha", "", "full server SHA")
	if err := set.Parse(args); err != nil {
		return err
	}
	manifest := releasecontrol.Manifest{ServerSHA: *sha}
	if err := releasecontrol.WriteManifest(*path, manifest); err != nil {
		return err
	}
	return printJSON(output, manifest)
}

func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

func printJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
