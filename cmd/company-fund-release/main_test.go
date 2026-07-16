package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"monera-digital/internal/companyfund"
	"monera-digital/internal/migration/migrations"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestMainEntryPoint(t *testing.T) {
	originalArgs, originalStdout, originalStderr, originalExit := os.Args, os.Stdout, os.Stderr, exitProcess
	t.Cleanup(func() {
		os.Args, os.Stdout, os.Stderr, exitProcess = originalArgs, originalStdout, originalStderr, originalExit
	})
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout, os.Stderr = stdout, stderr
	exitCode := 0
	exitProcess = func(code int) { exitCode = code }
	os.Args = []string{"company-fund-release", "unknown"}
	main()
	if exitCode != 1 {
		t.Fatalf("exit code = %d", exitCode)
	}
	exitCode = 0
	os.Args = []string{"company-fund-release", "plan", "--mode", "standard"}
	main()
	if exitCode != 0 {
		t.Fatalf("successful main exit code = %d", exitCode)
	}
}

func TestRunCommands(t *testing.T) {
	t.Parallel()
	sha := "0123456789abcdef0123456789abcdef01234567"
	controlToken := "run-1@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tmp := t.TempDir()
	manifest := filepath.Join(tmp, "manifest.json")
	repo, baseRef, headRef := bootstrapCLIRepository(t)
	tests := [][]string{
		{"plan", "--mode", "server-dark"},
		{"validate", "--event", "push", "--ref", "refs/heads/stage", "--mode", "standard", "--artifact-sha", sha},
		{"validate", "--event", "workflow_dispatch", "--ref", "refs/heads/stage", "--mode", "server-dark", "--artifact-sha", sha, "--run-id", "run-1", "--control-lock", controlToken, "--stage-head", sha, "--artifact-reachable"},
		{"manifest-write", "--path", manifest, "--server-sha", sha},
		{"manifest-read", "--path", manifest},
		{"bootstrap-manifest", "--repo", repo, "--phase", "D0", "--base-ref", baseRef, "--head-ref", headRef, "--main-workflow-ref", headRef, "--stage-workflow-ref", headRef},
	}
	for _, args := range tests {
		var output bytes.Buffer
		if err := run(args, &output); err != nil {
			t.Fatalf("run(%v) error = %v", args, err)
		}
		if !strings.Contains(output.String(), sha) && args[0] != "plan" && args[0] != "bootstrap-manifest" {
			t.Fatalf("run(%v) output = %q", args, output.String())
		}
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal(err)
	}
}

func bootstrapCLIRepository(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("config", "user.name", "CLI Test")
	git("config", "user.email", "cli@example.test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	workflowPath := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(workflowPath, 0o755); err != nil {
		t.Fatal(err)
	}
	workflow := `name: Stage
on:
  push:
    branches: [stage]
  workflow_dispatch:
    inputs:
      mode: {required: true, type: choice, options: [migration-only, workers-off-current, server-dark, workers-on-installed, standard]}
      artifact_ref: {required: true, type: string}
      run_id: {required: true, type: string}
      expected_migration_ceiling: {required: false, type: string}
jobs:
  control-preflight:
    runs-on: ubuntu-latest
    steps: [{run: "echo ok"}]
  deploy-stage:
    needs: control-preflight
    environment: stage
    runs-on: ubuntu-latest
    steps: []
`
	if err := os.WriteFile(filepath.Join(workflowPath, "deploy-backend-stage.yml"), []byte(workflow), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "workflow")
	return repo, base, git("rev-parse", "HEAD")
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		nil,
		{"unknown"},
		{"plan", "--bad"},
		{"plan", "--mode", "unknown"},
		{"validate", "--bad"},
		{"validate", "--mode", "unknown"},
		{"validate", "--mode", "standard"},
		{"manifest-read", "--bad"},
		{"manifest-read", "--path", "/missing"},
		{"manifest-write", "--bad"},
		{"manifest-write", "--path", "/missing/path", "--server-sha", "short"},
		{"bootstrap-manifest"},
		{"bootstrap-manifest", "--bad"},
	} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("run(%v) unexpectedly succeeded", args)
		}
	}
	if err := run([]string{"plan", "--mode", "standard"}, failingWriter{}); err == nil {
		t.Fatal("writer error ignored")
	}
}

func TestCutoverAndAliasCommandsArePureByDefault(t *testing.T) {
	repo, shaCurrent, shaA, shaV2, shaB := cutoverCLIRepository(t)
	var output bytes.Buffer
	if err := run([]string{
		"cutover-template", "--utc", "2026-07-15T04:05:06Z", "--entropy-hex", "12ab34cd",
		"--repo", repo,
		"--current-sha", shaCurrent, "--a-sha", shaA, "--v2-sha", shaV2, "--b-sha", shaB, "--expected-ceiling", "052",
		"--evidence-dir", filepath.Join(t.TempDir(), "evidence"), "--fixture-tx-key", "fixture-tx", "--fixture-occurrence-key", "fixture-occ",
	}, &output); err != nil || !strings.Contains(output.String(), "CF_CUTOVER_20260715T040506Z_12ab34cd") {
		t.Fatalf("cutover-template = %q, %v", output.String(), err)
	}

	request := aliasCLIRequest()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "alias.json")
	if err := os.WriteFile(input, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(companyfund.SafeheronAliasRepairApplyGate, "")
	t.Setenv("DATABASE_URL", "postgresql://must-not-be-read.invalid/db")
	for _, mode := range []string{"--dry-run", "--apply"} {
		if err := run([]string{"alias-scan", "--input", input, mode}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), companyfund.SafeheronAliasRepairApplyGate) {
			t.Fatalf("default %s gate error = %v", mode, err)
		}
	}
	output.Reset()
	if err := run([]string{"failure-matrix"}, &output); err != nil || !strings.Contains(output.String(), "migration-b-non-atomic") {
		t.Fatalf("failure matrix = %q, %v", output.String(), err)
	}
}

func cutoverCLIRepository(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	write := func(name, content string) {
		path := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "user.name", "CLI Checkpoint Test")
	git("config", "user.email", "checkpoint-cli@example.test")
	write("go.mod", "module monera-digital\n\ngo 1.25\n\nrequire github.com/shopspring/decimal v0.0.0\nreplace github.com/shopspring/decimal => ./third_party/decimal\n")
	write("third_party/decimal/go.mod", "module github.com/shopspring/decimal\n\ngo 1.25\n")
	write("third_party/decimal/decimal.go", "package decimal\n\ntype Decimal struct{}\nfunc NewFromString(string) (Decimal, error) { return Decimal{}, nil }\n")
	write("internal/migration/migration.go", `package migration
import "database/sql"
type Migration interface { Version() string; Description() string; Up(*sql.DB) error; Down(*sql.DB) error }
type ControlledMigration interface { Migration; RequiredPreexistingVersion() string; RequiredExpectedCeiling() string; UpTx(*sql.Tx) error }
`)
	write("internal/companyfund/stub.go", `package companyfund
import "github.com/shopspring/decimal"
type MovementKind string
type TransferMode string
type SafeheronOccurrenceInput struct { ProviderTransactionKey string; MovementKind MovementKind; RawCoinKey string; NormalizedSource string; NormalizedDestination string; Amount decimal.Decimal; TransferMode TransferMode; MovementIndex int }
type SafeheronOccurrence struct { Key string; AlgorithmVersion string }
func NormalizeSafeheronOccurrenceAddress(string, string) (string, error) { return "", nil }
func BuildSafeheronOccurrence(SafeheronOccurrenceInput) (SafeheronOccurrence, error) { return SafeheronOccurrence{}, nil }
`)
	write("internal/migration/migrations/051_base.go", "package migrations\n\nconst ( V051 = \"051\"; V052 = \"052\"; V053 = \"053\" )\n")
	write("cmd/migrate/main.go", checkpointCLIRunnerSource("051"))
	git("add", ".")
	git("commit", "-qm", "current")
	current := git("rev-parse", "HEAD")
	write("internal/migration/migrations/052_expand_company_fund_occurrence_and_manual_valuation.go", productionCLIMigrationSource(t, "052"))
	write("cmd/migrate/main.go", checkpointCLIRunnerSource("052"))
	git("add", ".")
	git("commit", "-qm", "checkpoint-a")
	a := git("rev-parse", "HEAD")
	for _, path := range []string{
		"internal/companyfund/safeheron_coin_catalog.go",
		"internal/companyfund/safeheron_runtime_resolvers.go",
		"internal/companyfund/safeheron_webhook_eligibility.go",
		"internal/companyfund/valuation_runtime.go",
	} {
		write(path, "package companyfund\n")
	}
	git("add", ".")
	git("commit", "-qm", "v2-server")
	v2 := git("rev-parse", "HEAD")
	write("internal/migration/migrations/053_enforce_safeheron_occurrence.go", productionCLIMigrationSource(t, "053"))
	write("cmd/migrate/main.go", checkpointCLIRunnerSource("053"))
	git("add", ".")
	git("commit", "-qm", "checkpoint-b")
	return repo, current, a, v2, git("rev-parse", "HEAD")
}

func productionCLIMigrationSource(t *testing.T, version string) string {
	t.Helper()
	filename := map[string]string{
		"052": "052_expand_company_fund_occurrence_and_manual_valuation.go",
		"053": "053_enforce_safeheron_occurrence.go",
	}[version]
	source, err := os.ReadFile(filepath.Join("..", "..", "internal", "migration", "migrations", filename))
	if err != nil {
		t.Fatal(err)
	}
	return string(source)
}

func checkpointCLIRunnerSource(ceiling string) string {
	versions := "migrations.V051"
	if ceiling >= "052" {
		versions += ", migrations.V052"
	}
	if ceiling >= "053" {
		versions += ", migrations.V053"
	}
	return `package main
import ("encoding/json"; "flag"; "fmt"; "os"; "monera-digital/internal/migration/migrations")
func main() { printCeiling := flag.Bool("print-ceiling", false, ""); printVersions := flag.Bool("print-versions", false, ""); flag.Parse(); versions := []string{` + versions + `}; if *printVersions { _ = json.NewEncoder(os.Stdout).Encode(versions); return }; if *printCeiling { fmt.Println(versions[len(versions)-1]) } }
`
}

func TestAliasScanRequiresLiveEvidenceBeforeDatabaseConfiguration(t *testing.T) {
	dir := t.TempDir()
	request := aliasCLIRequest()
	input := filepath.Join(dir, "alias.json")
	requestData, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC)
	hash := request.Evidence.FrozenAccountHash
	samples := struct {
		AccountHashStableFor time.Duration                           `json:"account_hash_stable_for"`
		AccountHashSamples   []companyfund.SafeheronAliasHashSample  `json:"account_hash_samples"`
		DrainSamples         []companyfund.SafeheronAliasDrainSample `json:"drain_samples"`
	}{
		AccountHashStableFor: 10 * time.Second,
		AccountHashSamples:   []companyfund.SafeheronAliasHashSample{{At: start, SHA256: hash}, {At: start.Add(5 * time.Second), SHA256: hash}, {At: start.Add(10 * time.Second), SHA256: hash}},
		DrainSamples:         []companyfund.SafeheronAliasDrainSample{{At: start, AccountHash: hash}, {At: start.Add(5 * time.Second), AccountHash: hash}, {At: start.Add(10 * time.Second), AccountHash: hash}},
	}
	samplesPath := filepath.Join(dir, "samples.json")
	samplesData, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(samplesPath, samplesData, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "release-manifest.json")
	environment := filepath.Join(dir, ".env")
	args := []string{"alias-scan", "--input", input, "--samples-artifact", samplesPath, "--installed-manifest", manifest, "--environment-file", environment, "--dry-run"}
	t.Setenv(companyfund.SafeheronAliasRepairApplyGate, "1")
	t.Setenv("DATABASE_URL", "://invalid")
	if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "read installed release manifest") {
		t.Fatalf("missing manifest did not block before database configuration: %v", err)
	}
	if err := os.WriteFile(manifest, []byte(`{"server_sha":"`+request.Evidence.V2ServerSHA+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "read installed environment") {
		t.Fatalf("missing environment did not block before database configuration: %v", err)
	}
	if err := os.WriteFile(environment, []byte("COMPANY_FUND_START_BACKGROUND_WORKERS=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "parse DATABASE_URL") {
		t.Fatalf("valid live evidence did not reach database configuration last: %v", err)
	}
}

func TestSchemaFingerprintCommandGatesBeforeDatabaseConfiguration(t *testing.T) {
	t.Setenv(migrations.CompanyFundSchemaFingerprintGate, "")
	t.Setenv("DATABASE_URL", "://must-not-be-read")
	if err := run([]string{"schema-fingerprint"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), migrations.CompanyFundSchemaFingerprintGate) {
		t.Fatalf("default live fingerprint gate = %v", err)
	}
	t.Setenv(migrations.CompanyFundSchemaFingerprintGate, "1")
	if err := run([]string{"schema-fingerprint"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "parse DATABASE_URL") {
		t.Fatalf("enabled live fingerprint did not reach database configuration: %v", err)
	}
	if err := run([]string{"schema-fingerprint", "--bad"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid schema-fingerprint flag accepted")
	}
}

func aliasCLIRequest() companyfund.SafeheronAliasRepairRequest {
	sha := strings.Repeat("a", 40)
	hash := strings.Repeat("b", 64)
	return companyfund.SafeheronAliasRepairRequest{
		Evidence: companyfund.SafeheronAliasRepairEvidence{
			V2ServerSHA: sha, FrozenAccountHash: hash,
		},
		AfterID: 1, Limit: 1,
		Rows: []companyfund.SafeheronAliasNullRow{{TransactionID: 2, MovementKey: "v1:legacy", IdentityAlgorithmVersion: companyfund.MovementIdentityAlgorithmVersion}},
		FactsByTransactionID: map[int64][]companyfund.SafeheronAliasOccurrenceFact{2: {{
			TransactionID: 2,
			Occurrence: companyfund.SafeheronOccurrenceInput{
				ProviderTransactionKey: "tx", MovementKind: companyfund.MovementKindPrincipal, RawCoinKey: "ETHEREUM_ETH",
				NormalizedSource: "0xfrom", NormalizedDestination: "0xto", Amount: decimal.NewFromInt(1),
				TransferMode: companyfund.TransferModeSingle,
			},
		}}},
		ExistingOccurrenceOwners: map[string]int64{},
	}
}
