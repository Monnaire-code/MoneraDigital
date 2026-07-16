package releasecontrol

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	checkpoint052Path          = "internal/migration/migrations/052_expand_company_fund_occurrence_and_manual_valuation.go"
	checkpoint053Path          = "internal/migration/migrations/053_enforce_safeheron_occurrence.go"
	approvedMigration052SHA256 = "ba1c352efbe273ae95cc92b78da901bd815e9d103f386796f7079c4c71b07a01"
	approvedMigration053SHA256 = "ecc427ebdd7a4ef47b4db246048dc07cc97adb88e5c206c8615b3c359269f867"
)

type controlledMigrationDigests struct {
	migration052 string
	migration053 string
}

func approvedControlledMigrationDigests() controlledMigrationDigests {
	return controlledMigrationDigests{
		migration052: approvedMigration052SHA256,
		migration053: approvedMigration053SHA256,
	}
}

var checkpointMigrationFilename = regexp.MustCompile(`^([0-9]{3})_.*\.go$`)
var checkpointMigrationVersion = regexp.MustCompile(`^[0-9]{3}$`)

var checkpointV2RequiredPaths = []string{
	"internal/companyfund/safeheron_coin_catalog.go",
	"internal/companyfund/safeheron_runtime_resolvers.go",
	"internal/companyfund/safeheron_webhook_eligibility.go",
	"internal/companyfund/valuation_runtime.go",
}

type CheckpointArtifactEvidence struct {
	CurrentSHA               string   `json:"current_sha"`
	ASHA                     string   `json:"a_sha"`
	V2SHA                    string   `json:"v2_sha"`
	BSHA                     string   `json:"b_sha"`
	CurrentTreeSHA           string   `json:"current_tree_sha"`
	ATreeSHA                 string   `json:"a_tree_sha"`
	V2TreeSHA                string   `json:"v2_tree_sha"`
	BTreeSHA                 string   `json:"b_tree_sha"`
	AMigration052BlobSHA256  string   `json:"a_migration_052_blob_sha256"`
	BMigration053BlobSHA256  string   `json:"b_migration_053_blob_sha256"`
	CurrentMigrationCeiling  string   `json:"current_migration_ceiling"`
	AMigrationCeiling        string   `json:"a_migration_ceiling"`
	V2MigrationCeiling       string   `json:"v2_migration_ceiling"`
	BMigrationCeiling        string   `json:"b_migration_ceiling"`
	CurrentMigrationVersions []string `json:"current_migration_versions"`
	AMigrationVersions       []string `json:"a_migration_versions"`
	V2MigrationVersions      []string `json:"v2_migration_versions"`
	BMigrationVersions       []string `json:"b_migration_versions"`
	CurrentPre052Digest      string   `json:"current_pre_052_digest"`
	APre052Digest            string   `json:"a_pre_052_digest"`
	V2Pre052Digest           string   `json:"v2_pre_052_digest"`
	BPre052Digest            string   `json:"b_pre_052_digest"`
}

type checkpointArtifact struct {
	label           string
	sha             string
	expectedCeiling string
	require052      bool
	require053      bool
	treeSHA         string
	runnerCeiling   string
	runnerVersions  []string
	fileVersions    []string
	pre052Manifest  map[string]string
	pre052Digest    string
}

func ValidateCheckpointArtifacts(ctx context.Context, repositoryPath, currentRef, aRef, v2Ref, bRef string) (CheckpointArtifactEvidence, error) {
	return validateCheckpointArtifactsWithDigests(ctx, repositoryPath, currentRef, aRef, v2Ref, bRef, approvedControlledMigrationDigests())
}

func validateCheckpointArtifactsWithDigests(ctx context.Context, repositoryPath, currentRef, aRef, v2Ref, bRef string, approved controlledMigrationDigests) (CheckpointArtifactEvidence, error) {
	if strings.TrimSpace(repositoryPath) == "" {
		return CheckpointArtifactEvidence{}, fmt.Errorf("checkpoint artifact repository is required")
	}
	repository := gitRepository{path: repositoryPath}
	refs := []struct {
		label string
		ref   string
	}{
		{label: "current", ref: currentRef},
		{label: "checkpoint A", ref: aRef},
		{label: "v2", ref: v2Ref},
		{label: "checkpoint B", ref: bRef},
	}
	resolved := make([]string, len(refs))
	for index, ref := range refs {
		sha, err := resolveExactCheckpointCommit(ctx, repository, ref.label, ref.ref)
		if err != nil {
			return CheckpointArtifactEvidence{}, err
		}
		resolved[index] = sha
	}
	if !allDistinctStrings(resolved...) {
		return CheckpointArtifactEvidence{}, fmt.Errorf("current, checkpoint A, v2, and checkpoint B must be distinct commits")
	}
	for index, relationship := range []string{"checkpoint A must descend from current artifact", "v2 must descend from checkpoint A", "checkpoint B must descend from v2"} {
		if err := repository.run(ctx, "merge-base", "--is-ancestor", resolved[index], resolved[index+1]); err != nil {
			return CheckpointArtifactEvidence{}, fmt.Errorf("%s", relationship)
		}
	}
	if err := validateCheckpointRoleChanges(ctx, repository, resolved[0], resolved[1], resolved[2], resolved[3]); err != nil {
		return CheckpointArtifactEvidence{}, err
	}

	artifacts := []checkpointArtifact{
		{label: "current", sha: resolved[0], expectedCeiling: "051"},
		{label: "checkpoint A", sha: resolved[1], expectedCeiling: "052", require052: true},
		{label: "v2", sha: resolved[2], expectedCeiling: "052", require052: true},
		{label: "checkpoint B", sha: resolved[3], expectedCeiling: "053", require052: true, require053: true},
	}
	for index := range artifacts {
		if err := inspectCheckpointArtifact(ctx, repository, &artifacts[index]); err != nil {
			return CheckpointArtifactEvidence{}, err
		}
	}
	if err := validateCheckpointRegistrationSequence(artifacts); err != nil {
		return CheckpointArtifactEvidence{}, err
	}
	if err := validatePre052MigrationManifests(artifacts); err != nil {
		return CheckpointArtifactEvidence{}, err
	}
	if err := validateCheckpointMigrationDelta(ctx, repository, artifacts[0], artifacts[1], checkpoint052Path); err != nil {
		return CheckpointArtifactEvidence{}, err
	}
	if err := validateCheckpointMigrationDelta(ctx, repository, artifacts[1], artifacts[2], ""); err != nil {
		return CheckpointArtifactEvidence{}, err
	}
	if err := validateCheckpointMigrationDelta(ctx, repository, artifacts[2], artifacts[3], checkpoint053Path); err != nil {
		return CheckpointArtifactEvidence{}, err
	}

	a052, err := checkpointFile(ctx, repository, artifacts[1].sha, checkpoint052Path)
	if err != nil {
		return CheckpointArtifactEvidence{}, fmt.Errorf("read checkpoint A migration 052: %w", err)
	}
	if err := validateApprovedControlledMigrationSource(a052, "ExpandCompanyFundOccurrenceAndManualValuation", "051", "052", approved); err != nil {
		return CheckpointArtifactEvidence{}, fmt.Errorf("checkpoint A migration 052: %w", err)
	}
	for _, artifact := range artifacts[2:] {
		candidate, readErr := checkpointFile(ctx, repository, artifact.sha, checkpoint052Path)
		if readErr != nil || !bytes.Equal(candidate, a052) {
			return CheckpointArtifactEvidence{}, fmt.Errorf("%s must retain the exact checkpoint A migration 052 blob", artifact.label)
		}
	}
	b053, err := checkpointFile(ctx, repository, artifacts[3].sha, checkpoint053Path)
	if err != nil {
		return CheckpointArtifactEvidence{}, fmt.Errorf("read checkpoint B migration 053: %w", err)
	}
	if err := validateApprovedControlledMigrationSource(b053, "EnforceSafeheronOccurrence", "052", "053", approved); err != nil {
		return CheckpointArtifactEvidence{}, fmt.Errorf("checkpoint B migration 053: %w", err)
	}

	return CheckpointArtifactEvidence{
		CurrentSHA: resolved[0], ASHA: resolved[1], V2SHA: resolved[2], BSHA: resolved[3],
		CurrentTreeSHA: artifacts[0].treeSHA, ATreeSHA: artifacts[1].treeSHA,
		V2TreeSHA: artifacts[2].treeSHA, BTreeSHA: artifacts[3].treeSHA,
		AMigration052BlobSHA256: checkpointDigest(a052), BMigration053BlobSHA256: checkpointDigest(b053),
		CurrentMigrationCeiling: artifacts[0].runnerCeiling, AMigrationCeiling: artifacts[1].runnerCeiling,
		V2MigrationCeiling: artifacts[2].runnerCeiling, BMigrationCeiling: artifacts[3].runnerCeiling,
		CurrentMigrationVersions: append([]string(nil), artifacts[0].runnerVersions...),
		AMigrationVersions:       append([]string(nil), artifacts[1].runnerVersions...),
		V2MigrationVersions:      append([]string(nil), artifacts[2].runnerVersions...),
		BMigrationVersions:       append([]string(nil), artifacts[3].runnerVersions...),
		CurrentPre052Digest:      artifacts[0].pre052Digest, APre052Digest: artifacts[1].pre052Digest,
		V2Pre052Digest: artifacts[2].pre052Digest, BPre052Digest: artifacts[3].pre052Digest,
	}, nil
}

func validateCheckpointRoleChanges(ctx context.Context, repository gitRepository, currentSHA, aSHA, v2SHA, bSHA string) error {
	roles := []struct {
		name     string
		base     string
		head     string
		allowed  func(string) bool
		required []string
	}{
		{name: "checkpoint A", base: currentSHA, head: aSHA, allowed: checkpointAPathAllowed, required: []string{checkpoint052Path, "cmd/migrate/main.go"}},
		{name: "v2", base: aSHA, head: v2SHA, allowed: checkpointV2PathAllowed, required: checkpointV2RequiredPaths},
		{name: "checkpoint B", base: v2SHA, head: bSHA, allowed: checkpointBPathAllowed, required: []string{checkpoint053Path, "cmd/migrate/main.go"}},
	}
	for _, role := range roles {
		files, err := repository.changedFiles(ctx, role.base, role.head)
		if err != nil {
			return fmt.Errorf("%s changed files: %w", role.name, err)
		}
		changed := make(map[string]bool, len(files))
		for _, path := range files {
			changed[path] = true
			if !role.allowed(path) {
				return fmt.Errorf("%s contains unapproved path %s", role.name, path)
			}
		}
		for _, path := range role.required {
			if !changed[path] {
				return fmt.Errorf("%s must contain approved role path %s", role.name, path)
			}
		}
	}
	return nil
}

func checkpointAPathAllowed(path string) bool {
	return path == checkpoint052Path || path == "cmd/migrate/main.go" || path == "cmd/migrate/main_test.go" ||
		path == "internal/migration/migrator.go" || strings.HasPrefix(path, "internal/migration/migrator_") ||
		path == "internal/migration/migrations/company_fund_postgres_integration_helpers_test.go" ||
		path == "internal/companyfund/safeheron_occurrence.go" || path == "internal/companyfund/safeheron_occurrence_test.go" ||
		(strings.HasPrefix(path, "internal/migration/migrations/052_") && strings.HasSuffix(path, "_test.go"))
}

func checkpointV2PathAllowed(path string) bool {
	for _, prefix := range []string{"internal/companyfund/", "internal/companyfundcontract/", "internal/container/", "internal/safeheron/", "internal/db/", "cmd/server/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return path == ".env.example"
}

func checkpointBPathAllowed(path string) bool {
	if path == checkpoint053Path || path == "cmd/migrate/main.go" || path == "cmd/migrate/main_test.go" ||
		path == ".github/workflows/deploy-backend-stage.yml" || path == "scripts/deploy-remote.sh" ||
		path == "scripts/db-promote/README.md" || path == "docs/company-fund-stage-release-control.md" {
		return true
	}
	for _, prefix := range []string{
		"internal/migration/migrations/053_", "internal/migration/migrations/company_fund_",
		"internal/releasecontrol/", "internal/buildinfo/", "cmd/company-fund-release/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func validateCheckpointRegistrationSequence(artifacts []checkpointArtifact) error {
	current := artifacts[0]
	if !equalCheckpointPaths(current.runnerVersions, current.fileVersions) || !strictlyIncreasingVersions(current.runnerVersions) {
		return fmt.Errorf("current runner registration manifest %v does not match production migration files %v", current.runnerVersions, current.fileVersions)
	}
	wantA := append(append([]string(nil), current.runnerVersions...), "052")
	wantB := append(append([]string(nil), wantA...), "053")
	for _, check := range []struct {
		artifact checkpointArtifact
		want     []string
	}{
		{artifact: artifacts[1], want: wantA},
		{artifact: artifacts[2], want: wantA},
		{artifact: artifacts[3], want: wantB},
	} {
		if !equalCheckpointPaths(check.artifact.runnerVersions, check.want) || !strictlyIncreasingVersions(check.artifact.runnerVersions) {
			return fmt.Errorf("%s runner registration manifest = %v, want %v", check.artifact.label, check.artifact.runnerVersions, check.want)
		}
	}
	return nil
}

func strictlyIncreasingVersions(versions []string) bool {
	if len(versions) == 0 {
		return false
	}
	for index, version := range versions {
		if !checkpointMigrationVersion.MatchString(version) || (index > 0 && versions[index-1] >= version) {
			return false
		}
	}
	return true
}

func validatePre052MigrationManifests(artifacts []checkpointArtifact) error {
	baseline := artifacts[0].pre052Manifest
	for _, artifact := range artifacts[1:] {
		if !equalStringMaps(artifact.pre052Manifest, baseline) {
			return fmt.Errorf("%s pre-052 production migration manifest differs from current", artifact.label)
		}
	}
	return nil
}

func equalStringMaps(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func validateCheckpointMigrationDelta(ctx context.Context, repository gitRepository, before, after checkpointArtifact, expectedAdded string) error {
	beforePaths, err := checkpointMigrationPaths(ctx, repository, before.sha)
	if err != nil {
		return err
	}
	afterPaths, err := checkpointMigrationPaths(ctx, repository, after.sha)
	if err != nil {
		return err
	}
	beforeSet := productionMigrationPathSet(beforePaths)
	afterSet := productionMigrationPathSet(afterPaths)
	added := make([]string, 0, 1)
	removed := make([]string, 0)
	for path := range afterSet {
		if !beforeSet[path] {
			added = append(added, path)
		}
	}
	for path := range beforeSet {
		if !afterSet[path] {
			removed = append(removed, path)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	expected := []string{}
	if expectedAdded != "" {
		expected = []string{expectedAdded}
	}
	if !equalCheckpointPaths(added, expected) || len(removed) != 0 {
		return fmt.Errorf("%s migration delta from %s added=%v removed=%v, want added=%v", after.label, before.label, added, removed, expected)
	}
	return nil
}

func productionMigrationPathSet(paths []string) map[string]bool {
	result := make(map[string]bool)
	for _, path := range paths {
		filename := filepath.Base(path)
		if checkpointMigrationFilename.MatchString(filename) && !strings.HasSuffix(filename, "_test.go") {
			result[path] = true
		}
	}
	return result
}

func equalCheckpointPaths(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func validateControlledMigrationSource(source []byte, typeName, prior, ceiling string) error {
	file, err := parser.ParseFile(token.NewFileSet(), "migration.go", source, 0)
	if err != nil {
		return fmt.Errorf("parse controlled migration: %w", err)
	}
	methods := make(map[string]*ast.FuncDecl)
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || len(function.Recv.List) != 1 || !receiverMatches(function.Recv.List[0].Type, typeName) {
			continue
		}
		methods[function.Name.Name] = function
	}
	if methodReturnedString(methods["RequiredPreexistingVersion"]) != prior {
		return fmt.Errorf("RequiredPreexistingVersion must return %q", prior)
	}
	if methodReturnedString(methods["RequiredExpectedCeiling"]) != ceiling {
		return fmt.Errorf("RequiredExpectedCeiling must return %q", ceiling)
	}
	runName := "runMigration" + ceiling
	if !methodAcceptsPointerAndReturnsError(methods["UpTx"], "sql", "Tx") || !methodDelegatesExactly(methods["UpTx"], runName) {
		return fmt.Errorf("UpTx(*sql.Tx) error method is required")
	}
	if !methodAcceptsPointerAndReturnsError(methods["Up"], "sql", "DB") || !methodReturnsNonNil(methods["Up"]) || !methodContainsString(methods["Up"], "controlled") {
		return fmt.Errorf("direct Up must fail closed as controlled")
	}
	publicSQLFound := false
	unsafeSQL := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, unquoteErr := strconv.Unquote(literal.Value)
		if unquoteErr != nil {
			return true
		}
		lower := strings.ToLower(value)
		if strings.Contains(lower, "public.company_fund_transactions") {
			publicSQLFound = true
		}
		for _, clause := range []string{"from company_fund_transactions", "update company_fund_transactions", "alter table company_fund_transactions", "on company_fund_transactions", "references company_fund_transactions"} {
			if strings.Contains(lower, clause) {
				unsafeSQL = true
			}
		}
		return true
	})
	if !publicSQLFound || unsafeSQL {
		return fmt.Errorf("migration SQL must explicitly target public.company_fund_transactions")
	}
	if err := validateControlledRunFunction(file, runName, ceiling); err != nil {
		return err
	}
	return nil
}

func validateApprovedControlledMigrationSource(source []byte, typeName, prior, ceiling string, approved controlledMigrationDigests) error {
	if err := validateControlledMigrationSource(source, typeName, prior, ceiling); err != nil {
		return err
	}
	expected := map[string]string{
		"052": approved.migration052,
		"053": approved.migration053,
	}[ceiling]
	if expected == "" {
		return fmt.Errorf("approved digest for controlled migration %s is required", ceiling)
	}
	actual := checkpointDigest(source)
	if actual != expected {
		return fmt.Errorf("controlled migration %s blob sha256 = %s, want approved %s", ceiling, actual, expected)
	}
	return nil
}

func methodDelegatesExactly(function *ast.FuncDecl, runName string) bool {
	if function == nil || function.Body == nil || function.Type.Params == nil || len(function.Type.Params.List) != 1 || len(function.Type.Params.List[0].Names) != 1 || len(function.Body.List) != 1 {
		return false
	}
	statement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return false
	}
	call, ok := statement.Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	callee, calleeOK := call.Fun.(*ast.Ident)
	argument, argumentOK := firstCallIdentifier(call)
	return calleeOK && callee.Name == runName && argumentOK && argument.Name == function.Type.Params.List[0].Names[0].Name && len(call.Args) == 1
}

func firstCallIdentifier(call *ast.CallExpr) (*ast.Ident, bool) {
	if call == nil || len(call.Args) == 0 {
		return nil, false
	}
	identifier, ok := call.Args[0].(*ast.Ident)
	return identifier, ok
}

func validateControlledRunFunction(file *ast.File, runName, ceiling string) error {
	var run *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Recv == nil && function.Name.Name == runName {
			run = function
			break
		}
	}
	if run == nil || run.Body == nil {
		return fmt.Errorf("%s implementation is required", runName)
	}
	expected := map[string][]string{
		"052": {"migration052TimeoutsSQL", "migration052PreflightQuery", "migration052AddOccurrenceColumnsSQL", "migration052SchemaDDL", "migration052Backfill", "migration052ValidateAliases"},
		"053": {"migration053TimeoutsSQL", "migration053PreflightSQL", "migration053AddConstraintSQL", "migration053ValidateConstraintSQL"},
	}[ceiling]
	if len(run.Type.Params.List) != 1 || len(run.Type.Params.List[0].Names) != 1 {
		return fmt.Errorf("%s must accept one named transaction", runName)
	}
	actual, err := controlledRunStages(run, run.Type.Params.List[0].Names[0].Name, expected)
	if err != nil {
		return fmt.Errorf("%s: %w", runName, err)
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%s stages = %v, want %v", runName, actual, expected)
	}
	for index := range expected {
		if actual[index] != expected[index] {
			return fmt.Errorf("%s stages = %v, want %v", runName, actual, expected)
		}
	}
	return nil
}

func controlledRunStages(run *ast.FuncDecl, transactionName string, expected []string) ([]string, error) {
	if len(run.Body.List) < 2 || !controlledContextAssignment(run.Body.List[0]) {
		return nil, errors.New("first statement must establish the migration context")
	}
	if !controlledSuccessfulReturn(run.Body.List[len(run.Body.List)-1]) {
		return nil, errors.New("final statement must be the sole successful return")
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, name := range expected {
		expectedSet[name] = true
	}
	stages := make([]string, 0, len(expected))
	preflightGuardSeen := false
	preflightDeclarationSeen := false
	for index := 1; index < len(run.Body.List)-1; index++ {
		statement := run.Body.List[index]
		ifStatement, isIf := statement.(*ast.IfStmt)
		if isIf && ifStatement.Init != nil {
			stage, errorName, found := controlledStageStatement(ifStatement.Init, transactionName, expectedSet)
			if !found {
				return nil, fmt.Errorf("unapproved conditional at statement %d", index+1)
			}
			if !controlledIfReturnsError(ifStatement, errorName) {
				return nil, fmt.Errorf("stage %s must return its error immediately", stage)
			}
			if err := appendControlledStage(&stages, expected, stage); err != nil {
				return nil, err
			}
			continue
		}

		stage, errorName, found := controlledStageStatement(statement, transactionName, expectedSet)
		if found {
			if index+1 >= len(run.Body.List)-1 {
				return nil, fmt.Errorf("stage %s ignores its error", stage)
			}
			next, ok := run.Body.List[index+1].(*ast.IfStmt)
			if !ok || next.Init != nil || !controlledIfReturnsError(next, errorName) {
				return nil, fmt.Errorf("stage %s must return its error immediately", stage)
			}
			if err := appendControlledStage(&stages, expected, stage); err != nil {
				return nil, err
			}
			index++
			continue
		}
		if isIf && !preflightGuardSeen && len(stages) == 2 && controlledPreflightGuard(ifStatement, expected[1]) {
			preflightGuardSeen = true
			continue
		}
		if !preflightDeclarationSeen && len(stages) == 1 && controlledPreflightDeclaration(statement, expected[1]) {
			preflightDeclarationSeen = true
			continue
		}
		return nil, fmt.Errorf("unapproved statement %T at position %d", statement, index+1)
	}
	if !preflightGuardSeen {
		return nil, fmt.Errorf("stage %s must be followed by its fail-closed state guard", expected[1])
	}
	if expected[1] == "migration053PreflightSQL" && !preflightDeclarationSeen {
		return nil, errors.New("migration053 preflight state declaration is required")
	}
	return stages, nil
}

func appendControlledStage(stages *[]string, expected []string, stage string) error {
	index := len(*stages)
	if index >= len(expected) || expected[index] != stage {
		return fmt.Errorf("stage %s is out of order; completed %v", stage, *stages)
	}
	*stages = append(*stages, stage)
	return nil
}

func controlledContextAssignment(statement ast.Stmt) bool {
	assignment, ok := statement.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	name, nameOK := assignment.Lhs[0].(*ast.Ident)
	call, callOK := assignment.Rhs[0].(*ast.CallExpr)
	if !nameOK || name.Name != "ctx" || !callOK || len(call.Args) != 0 {
		return false
	}
	selector, selectorOK := call.Fun.(*ast.SelectorExpr)
	if !selectorOK {
		return false
	}
	packageName, packageOK := selector.X.(*ast.Ident)
	return packageOK && packageName.Name == "context" && selector.Sel.Name == "Background"
}

func controlledSuccessfulReturn(statement ast.Stmt) bool {
	returned, ok := statement.(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return false
	}
	identifier, ok := returned.Results[0].(*ast.Ident)
	return ok && identifier.Name == "nil"
}

func controlledPreflightDeclaration(statement ast.Stmt, preflightStage string) bool {
	if preflightStage != "migration053PreflightSQL" {
		return false
	}
	declaration, ok := statement.(*ast.DeclStmt)
	if !ok {
		return false
	}
	general, ok := declaration.Decl.(*ast.GenDecl)
	if !ok || general.Tok != token.VAR || len(general.Specs) != 1 {
		return false
	}
	value, ok := general.Specs[0].(*ast.ValueSpec)
	typeName, typeOK := value.Type.(*ast.Ident)
	return ok && len(value.Names) == 1 && value.Names[0].Name == "preflight" && typeOK && typeName.Name == "migration053Preflight" && len(value.Values) == 0
}

func controlledPreflightGuard(statement *ast.IfStmt, preflightStage string) bool {
	if statement == nil || statement.Init != nil || statement.Else != nil || !controlledBodyReturnsNonNil(statement.Body) {
		return false
	}
	if preflightStage == "migration052PreflightQuery" {
		binary, ok := statement.Cond.(*ast.BinaryExpr)
		if !ok {
			return false
		}
		left, leftOK := binary.X.(*ast.Ident)
		right, rightOK := binary.Y.(*ast.BasicLit)
		return binary.Op == token.NEQ && leftOK && left.Name == "missing" && rightOK && right.Kind == token.INT && right.Value == "0"
	}
	call, ok := statement.Cond.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, receiverOK := selector.X.(*ast.Ident)
	return receiverOK && receiver.Name == "preflight" && selector.Sel.Name == "unsafe"
}

func controlledBodyReturnsNonNil(body *ast.BlockStmt) bool {
	if body == nil || len(body.List) != 1 {
		return false
	}
	returned, ok := body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return false
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Errorf" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "fmt"
}

func controlledStageStatement(statement ast.Stmt, transactionName string, expected map[string]bool) (string, string, bool) {
	var call *ast.CallExpr
	var left []ast.Expr
	switch typed := statement.(type) {
	case *ast.AssignStmt:
		if len(typed.Rhs) != 1 {
			return "", "", false
		}
		call, _ = typed.Rhs[0].(*ast.CallExpr)
		left = typed.Lhs
	case *ast.ExprStmt:
		call, _ = typed.X.(*ast.CallExpr)
	default:
		return "", "", false
	}
	stage := controlledStageCall(call, transactionName)
	if !expected[stage] {
		return "", "", false
	}
	return stage, assignedErrorName(left), true
}

func controlledStageCall(call *ast.CallExpr, transactionName string) string {
	if call == nil {
		return ""
	}
	if identifier, ok := call.Fun.(*ast.Ident); ok {
		argument, argumentOK := firstCallIdentifier(call)
		if argumentOK && argument.Name == transactionName {
			if identifier.Name == "migration052Backfill" || identifier.Name == "migration052ValidateAliases" {
				return identifier.Name
			}
			if identifier.Name == "migration052Count" && len(call.Args) == 2 {
				if query, ok := call.Args[1].(*ast.Ident); ok {
					return query.Name
				}
			}
		}
		return ""
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if selector.Sel.Name == "Scan" {
		queryCall, ok := selector.X.(*ast.CallExpr)
		if !ok {
			return ""
		}
		return controlledTransactionQueryStage(queryCall, transactionName)
	}
	if selector.Sel.Name != "ExecContext" {
		return ""
	}
	receiver, receiverOK := selector.X.(*ast.Ident)
	if !receiverOK || receiver.Name != transactionName || len(call.Args) < 2 {
		return ""
	}
	stage, _ := call.Args[1].(*ast.Ident)
	if stage == nil {
		return ""
	}
	return stage.Name
}

func controlledTransactionQueryStage(call *ast.CallExpr, transactionName string) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "QueryRowContext" || len(call.Args) < 2 {
		return ""
	}
	receiver, receiverOK := selector.X.(*ast.Ident)
	stage, stageOK := call.Args[1].(*ast.Ident)
	if !receiverOK || receiver.Name != transactionName || !stageOK {
		return ""
	}
	return stage.Name
}

func assignedErrorName(expressions []ast.Expr) string {
	for _, expression := range expressions {
		identifier, ok := expression.(*ast.Ident)
		if ok && identifier.Name != "_" && strings.Contains(strings.ToLower(identifier.Name), "err") {
			return identifier.Name
		}
	}
	return ""
}

func controlledIfReturnsError(statement *ast.IfStmt, errorName string) bool {
	if statement == nil || errorName == "" || statement.Else != nil || !conditionRejectsNonNilError(statement.Cond, errorName) || len(statement.Body.List) != 1 {
		return false
	}
	returned, ok := statement.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return false
	}
	if identifier, ok := returned.Results[0].(*ast.Ident); ok {
		return identifier.Name == errorName
	}
	call, ok := returned.Results[0].(*ast.CallExpr)
	if !ok {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Errorf" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "fmt" {
		return false
	}
	for _, argument := range call.Args {
		if identifier, ok := argument.(*ast.Ident); ok && identifier.Name == errorName {
			return true
		}
	}
	return false
}

func conditionRejectsNonNilError(expression ast.Expr, errorName string) bool {
	binary, ok := expression.(*ast.BinaryExpr)
	if !ok || binary.Op != token.NEQ {
		return false
	}
	left, leftOK := binary.X.(*ast.Ident)
	right, rightOK := binary.Y.(*ast.Ident)
	return leftOK && left.Name == errorName && rightOK && right.Name == "nil"
}

func methodAcceptsPointerAndReturnsError(function *ast.FuncDecl, packageName, typeName string) bool {
	if function == nil || function.Type == nil || function.Type.Params == nil || function.Type.Results == nil || len(function.Type.Params.List) != 1 || len(function.Type.Results.List) != 1 {
		return false
	}
	pointer, ok := function.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	selector, ok := pointer.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageIdentifier, packageOK := selector.X.(*ast.Ident)
	result, resultOK := function.Type.Results.List[0].Type.(*ast.Ident)
	return packageOK && packageIdentifier.Name == packageName && selector.Sel.Name == typeName && resultOK && result.Name == "error"
}

func methodReturnsNonNil(function *ast.FuncDecl) bool {
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return false
	}
	statement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return false
	}
	identifier, isIdentifier := statement.Results[0].(*ast.Ident)
	return !isIdentifier || identifier.Name != "nil"
}

func receiverMatches(expression ast.Expr, typeName string) bool {
	pointer, ok := expression.(*ast.StarExpr)
	if !ok {
		return false
	}
	identifier, ok := pointer.X.(*ast.Ident)
	return ok && identifier.Name == typeName
}

func methodReturnedString(function *ast.FuncDecl) string {
	if function == nil || function.Body == nil || len(function.Body.List) != 1 {
		return ""
	}
	statement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(statement.Results) != 1 {
		return ""
	}
	literal, ok := statement.Results[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return ""
	}
	return value
}

func methodContainsString(function *ast.FuncDecl, fragment string) bool {
	if function == nil || function.Body == nil {
		return false
	}
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil && strings.Contains(strings.ToLower(value), strings.ToLower(fragment)) {
			found = true
		}
		return true
	})
	return found
}

func inspectCheckpointArtifact(ctx context.Context, repository gitRepository, artifact *checkpointArtifact) error {
	paths, err := checkpointMigrationPaths(ctx, repository, artifact.sha)
	if err != nil {
		return fmt.Errorf("%s migration file set: %w", artifact.label, err)
	}
	versions := make(map[string]bool)
	artifact.fileVersions = productionMigrationVersions(paths)
	for _, path := range paths {
		filename := filepath.Base(path)
		match := checkpointMigrationFilename.FindStringSubmatch(filename)
		if len(match) != 2 || strings.HasSuffix(filename, "_test.go") {
			continue
		}
		version := match[1]
		versions[version] = true
		if version > artifact.expectedCeiling {
			return fmt.Errorf("%s must not contain migration %s above ceiling %s", artifact.label, version, artifact.expectedCeiling)
		}
	}
	if !versions[artifact.expectedCeiling] {
		return fmt.Errorf("%s migration file set must reach exact ceiling %s", artifact.label, artifact.expectedCeiling)
	}
	if versions["052"] != artifact.require052 || versions["053"] != artifact.require053 {
		return fmt.Errorf("%s migration file set does not match required 052/053 split", artifact.label)
	}

	tree, err := repository.output(ctx, "rev-parse", "--verify", artifact.sha+"^{tree}")
	if err != nil {
		return fmt.Errorf("resolve %s tree: %w", artifact.label, err)
	}
	artifact.treeSHA = strings.TrimSpace(tree)
	artifact.pre052Manifest, artifact.pre052Digest, err = checkpointPre052Manifest(ctx, repository, artifact.sha, paths)
	if err != nil {
		return fmt.Errorf("%s pre-052 migration manifest: %w", artifact.label, err)
	}
	runner, err := buildAndRunCheckpointMigrationEvidence(ctx, repository, artifact.sha)
	if err != nil {
		return fmt.Errorf("%s exact-SHA migration runner: %w", artifact.label, err)
	}
	if runner.ceiling != artifact.expectedCeiling {
		return fmt.Errorf("%s runner ceiling = %s, want %s", artifact.label, runner.ceiling, artifact.expectedCeiling)
	}
	artifact.runnerCeiling = runner.ceiling
	artifact.runnerVersions = runner.versions
	return nil
}

func productionMigrationVersions(paths []string) []string {
	versions := make([]string, 0, len(paths))
	for _, path := range paths {
		filename := filepath.Base(path)
		match := checkpointMigrationFilename.FindStringSubmatch(filename)
		if len(match) == 2 && !strings.HasSuffix(filename, "_test.go") {
			versions = append(versions, match[1])
		}
	}
	sort.Strings(versions)
	return versions
}

func checkpointPre052Manifest(ctx context.Context, repository gitRepository, commit string, paths []string) (map[string]string, string, error) {
	manifest := make(map[string]string)
	for _, path := range paths {
		filename := filepath.Base(path)
		match := checkpointMigrationFilename.FindStringSubmatch(filename)
		if len(match) != 2 || strings.HasSuffix(filename, "_test.go") || match[1] >= "052" {
			continue
		}
		contents, err := checkpointFile(ctx, repository, commit, path)
		if err != nil {
			return nil, "", err
		}
		manifest[path] = checkpointDigest(contents)
	}
	keys := make([]string, 0, len(manifest))
	for path := range manifest {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, path := range keys {
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(manifest[path]))
		_, _ = hash.Write([]byte{0})
	}
	return manifest, hex.EncodeToString(hash.Sum(nil)), nil
}

func checkpointMigrationPaths(ctx context.Context, repository gitRepository, commit string) ([]string, error) {
	output, err := repository.output(ctx, "ls-tree", "-r", "--name-only", commit, "--", "internal/migration/migrations")
	if err != nil {
		return nil, err
	}
	paths := strings.Fields(output)
	sort.Strings(paths)
	return paths, nil
}

type checkpointRunnerEvidence struct {
	ceiling  string
	versions []string
}

func buildAndRunCheckpointMigrationEvidence(ctx context.Context, repository gitRepository, commit string) (checkpointRunnerEvidence, error) {
	archive, err := repository.outputBytes(ctx, "archive", "--format=tar", commit)
	if err != nil {
		return checkpointRunnerEvidence{}, fmt.Errorf("archive commit: %w", err)
	}
	root, err := os.MkdirTemp("", "monera-checkpoint-artifact-")
	if err != nil {
		return checkpointRunnerEvidence{}, err
	}
	defer os.RemoveAll(root)
	if err := extractCheckpointArchive(archive, root); err != nil {
		return checkpointRunnerEvidence{}, err
	}
	binary := filepath.Join(root, "monera-migrate")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/migrate")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, buildErr := build.CombinedOutput(); buildErr != nil {
		return checkpointRunnerEvidence{}, fmt.Errorf("build: %w: %s", buildErr, strings.TrimSpace(string(output)))
	}
	run := func(argument string) ([]byte, error) {
		command := exec.CommandContext(ctx, binary, argument)
		command.Dir = root
		command.Env = append(os.Environ(), "APP_ENV=production")
		output, runErr := command.CombinedOutput()
		if runErr != nil {
			return nil, fmt.Errorf("run %s: %w: %s", argument, runErr, strings.TrimSpace(string(output)))
		}
		return output, nil
	}
	ceilingOutput, err := run("-print-ceiling")
	if err != nil {
		return checkpointRunnerEvidence{}, err
	}
	versionsOutput, err := run("-print-versions")
	if err != nil {
		return checkpointRunnerEvidence{}, err
	}
	var versions []string
	if err := json.Unmarshal(bytes.TrimSpace(versionsOutput), &versions); err != nil {
		return checkpointRunnerEvidence{}, fmt.Errorf("decode -print-versions: %w", err)
	}
	return checkpointRunnerEvidence{ceiling: strings.TrimSpace(string(ceilingOutput)), versions: versions}, nil
}

func extractCheckpointArchive(archive []byte, root string) error {
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(root, clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, openErr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if openErr != nil {
				return openErr
			}
			_, copyErr := io.Copy(file, reader)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue
		default:
			return fmt.Errorf("unsupported archive entry %q", header.Name)
		}
	}
}

func resolveExactCheckpointCommit(ctx context.Context, repository gitRepository, label, ref string) (string, error) {
	if err := ValidateFullSHA(ref); err != nil {
		return "", fmt.Errorf("%s ref: %w", label, err)
	}
	resolved, err := repository.resolveCommit(ctx, label, ref)
	if err != nil {
		return "", err
	}
	if resolved != ref {
		return "", fmt.Errorf("%s ref did not resolve to the exact approved commit", label)
	}
	return resolved, nil
}

func checkpointFile(ctx context.Context, repository gitRepository, commit, path string) ([]byte, error) {
	return repository.outputBytes(ctx, "show", commit+":"+path)
}

func checkpointDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
