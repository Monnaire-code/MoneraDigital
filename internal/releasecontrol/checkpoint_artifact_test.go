package releasecontrol

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func validateCheckpointFixtureArtifacts(ctx context.Context, repositoryPath, currentRef, aRef, v2Ref, bRef string) (CheckpointArtifactEvidence, error) {
	approved := controlledMigrationDigests{
		migration052: checkpointDigest([]byte(checkpointMigrationSource("052"))),
		migration053: checkpointDigest([]byte(checkpointMigrationSource("053"))),
	}
	return validateCheckpointArtifactsWithDigests(ctx, repositoryPath, currentRef, aRef, v2Ref, bRef, approved)
}

func TestValidateCheckpointArtifactsBuildsExactFourCommitSequence(t *testing.T) {
	repo, currentSHA, aSHA, v2SHA, bSHA := checkpointArtifactRepository(t)
	evidence, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, v2SHA, bSHA)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.CurrentSHA != currentSHA || evidence.ASHA != aSHA || evidence.V2SHA != v2SHA || evidence.BSHA != bSHA ||
		evidence.CurrentTreeSHA == "" || evidence.ATreeSHA == "" || evidence.V2TreeSHA == "" || evidence.BTreeSHA == "" ||
		evidence.CurrentMigrationCeiling != "051" || evidence.AMigrationCeiling != "052" ||
		evidence.V2MigrationCeiling != "052" || evidence.BMigrationCeiling != "053" ||
		strings.Join(evidence.CurrentMigrationVersions, ",") != "051" || strings.Join(evidence.AMigrationVersions, ",") != "051,052" ||
		strings.Join(evidence.V2MigrationVersions, ",") != "051,052" || strings.Join(evidence.BMigrationVersions, ",") != "051,052,053" ||
		evidence.CurrentPre052Digest == "" || evidence.CurrentPre052Digest != evidence.APre052Digest ||
		evidence.APre052Digest != evidence.V2Pre052Digest || evidence.V2Pre052Digest != evidence.BPre052Digest {
		t.Fatalf("evidence = %#v", evidence)
	}

	for name, refs := range map[string][4]string{
		"same A and v2":       {currentSHA, aSHA, aSHA, bSHA},
		"A not after current": {aSHA, currentSHA, v2SHA, bSHA},
		"v2 not after A":      {currentSHA, v2SHA, aSHA, bSHA},
		"B not after v2":      {currentSHA, aSHA, bSHA, v2SHA},
	} {
		t.Run(name, func(t *testing.T) {
			if _, validateErr := validateCheckpointFixtureArtifacts(context.Background(), repo, refs[0], refs[1], refs[2], refs[3]); validateErr == nil {
				t.Fatal("invalid checkpoint artifact graph accepted")
			}
		})
	}
}

func TestValidateCheckpointArtifactsRejectsWrongMigrationSplitAndCeiling(t *testing.T) {
	repo, currentSHA, aSHA, v2SHA, bSHA := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)

	git("checkout", "-q", aSHA)
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	git("add", ".")
	git("commit", "-qm", "combined-a")
	combinedA := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, "internal/companyfund/combined_v2.go", "package companyfund\n")
	git("add", ".")
	git("commit", "-qm", "combined-v2")
	combinedV2 := git("rev-parse", "HEAD")
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "combined-b")
	combinedB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, combinedA, combinedV2, combinedB); err == nil || !strings.Contains(err.Error(), "checkpoint A") {
		t.Fatalf("combined A tree = %v", err)
	}

	git("checkout", "-q", bSHA)
	if err := os.Remove(filepath.Join(repo, filepath.FromSlash(checkpoint053Path))); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-qm", "missing-b")
	missingB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, v2SHA, missingB); err == nil || !strings.Contains(err.Error(), "checkpoint B") {
		t.Fatalf("missing B tree = %v", err)
	}

	git("checkout", "-q", bSHA)
	writeCheckpointFile(t, repo, "internal/migration/migrations/054_future.go", "package migrations\n")
	git("add", ".")
	git("commit", "-qm", "future-migration")
	future := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, v2SHA, future); err == nil {
		t.Fatalf("future migration = %v", err)
	}
}

func TestValidateCheckpointArtifactsRejectsSourceClaimWithoutRunnerRegistration(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	writeCheckpointRunner(t, repo, "051")
	appendCheckpointFile(t, repo, "cmd/migrate/main.go", "\n// stale runner still declares ceiling 051\n")
	git("add", ".")
	git("commit", "-qm", "stale-runner")
	staleRunner := git("rev-parse", "HEAD")
	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "stale-v2")
	staleV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "stale-b")
	staleB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, staleRunner, staleV2, staleB); err == nil || !strings.Contains(err.Error(), "runner ceiling = 051, want 052") {
		t.Fatalf("stale source-declared runner accepted: %v", err)
	}
}

func TestValidateCheckpointArtifactsRejectsPlainMigrationWithoutControlledContract(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	writeCheckpointFile(t, repo, checkpoint052Path, "package migrations\n\nconst V052 = \"052\"\n")
	git("add", ".")
	git("commit", "-qm", "plain-migration-a")
	plainA := git("rev-parse", "HEAD")
	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "plain-v2")
	plainV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "plain-b")
	plainB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, plainA, plainV2, plainB); err == nil || !strings.Contains(err.Error(), "RequiredPreexistingVersion") {
		t.Fatalf("plain migration accepted: %v", err)
	}
}

func TestControlledMigrationASTRejectsNonFailClosedUpAndWrongUpTxSignature(t *testing.T) {
	source := checkpointMigrationSource("052")
	for name, mutated := range map[string]string{
		"direct Up returns nil": strings.Replace(source, `return fmt.Errorf("052 is controlled")`, `return nil`, 1),
		"UpTx takes DB":         strings.Replace(source, `UpTx(tx *sql.Tx)`, `UpTx(tx *sql.DB)`, 1),
		"UpTx is no-op":         strings.Replace(source, `return runMigration052(tx)`, `return nil`, 1),
		"run skips backfill":    strings.Replace(source, `if err := migration052Backfill(tx); err != nil { return err }`, ``, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateControlledMigrationSource([]byte(mutated), "ExpandCompanyFundOccurrenceAndManualValuation", "051", "052"); err == nil {
				t.Fatal("invalid controlled method set accepted")
			}
		})
	}
}

func TestControlledMigrationASTRejectsRunStageBypasses(t *testing.T) {
	source := checkpointMigrationSource("052")
	for name, mutated := range map[string]string{
		"referenced but not called": strings.Replace(source, `if err := migration052Backfill(tx); err != nil { return err }`, `_ = migration052Backfill`, 1),
		"hidden in dead branch":     strings.Replace(source, `if err := migration052Backfill(tx); err != nil { return err }`, `if false { if err := migration052Backfill(tx); err != nil { return err } }`, 1),
		"ignored error":             strings.Replace(source, `if err := migration052Backfill(tx); err != nil { return err }`, `_ = migration052Backfill(tx)`, 1),
		"reordered stages": strings.Replace(
			strings.Replace(
				strings.Replace(source, `if _, err := tx.ExecContext(ctx, migration052AddOccurrenceColumnsSQL); err != nil { return err }`, `__BACKFILL_STAGE__`, 1),
				`if err := migration052Backfill(tx); err != nil { return err }`,
				`if _, err := tx.ExecContext(ctx, migration052AddOccurrenceColumnsSQL); err != nil { return err }`,
				1,
			),
			`__BACKFILL_STAGE__`,
			`if err := migration052Backfill(tx); err != nil { return err }`,
			1,
		),
		"wrong transaction receiver": strings.Replace(
			strings.Replace(source, `ctx := context.Background()`, "ctx := context.Background()\n\totherTx := tx", 1),
			`tx.ExecContext(ctx, migration052SchemaDDL)`,
			`otherTx.ExecContext(ctx, migration052SchemaDDL)`,
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateControlledMigrationSource([]byte(mutated), "ExpandCompanyFundOccurrenceAndManualValuation", "051", "052"); err == nil {
				t.Fatal("run-stage bypass accepted")
			}
		})
	}
}

func TestControlledMigrationASTRejectsRunControlFlowAndPreflightBypasses(t *testing.T) {
	source052 := checkpointMigrationSource("052")
	source053 := checkpointMigrationSource("053")
	for name, testCase := range map[string]struct {
		source   string
		typeName string
		prior    string
		ceiling  string
	}{
		"early successful return": {
			source:   strings.Replace(source052, `ctx := context.Background()`, "ctx := context.Background()\n\treturn nil", 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"conditional early successful return": {
			source:   strings.Replace(source052, `ctx := context.Background()`, "ctx := context.Background()\n\tif true { return nil }", 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"returns unrelated error": {
			source: strings.Replace(
				source052,
				`if err := migration052Backfill(tx); err != nil { return err }`,
				`if err := migration052Backfill(tx); err != nil { return fmt.Errorf("unrelated") }`,
				1,
			),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"swallows current error": {
			source: strings.Replace(
				source052,
				`if err := migration052Backfill(tx); err != nil { return err }`,
				`if err := migration052Backfill(tx); err != nil { return swallow(err) }`,
				1,
			),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"052 missing-count guard removed": {
			source:   strings.Replace(source052, `if missing != 0 { return fmt.Errorf("missing occurrence tuples") }`, ``, 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"052 non-binary guard": {
			source:   strings.Replace(source052, `if missing != 0 { return fmt.Errorf("missing occurrence tuples") }`, `if preflightReady() { return fmt.Errorf("missing occurrence tuples") }`, 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"052 guard returns maybe-nil identifier": {
			source:   strings.Replace(source052, `if missing != 0 { return fmt.Errorf("missing occurrence tuples") }`, `if missing != 0 { return maybeNilError }`, 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"053 unsafe-state guard removed": {
			source:   strings.Replace(source053, `if preflight.unsafe() { return fmt.Errorf("unsafe occurrence tuples") }`, ``, 1),
			typeName: "EnforceSafeheronOccurrence", prior: "052", ceiling: "053",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateControlledMigrationSource([]byte(testCase.source), testCase.typeName, testCase.prior, testCase.ceiling); err == nil {
				t.Fatal("run control-flow bypass accepted")
			}
		})
	}
}

func TestApprovedControlledMigrationDigestsMatchProductionSources(t *testing.T) {
	approved := approvedControlledMigrationDigests()
	for _, testCase := range []struct {
		path, typeName, prior, ceiling, digest string
	}{
		{checkpoint052Path, "ExpandCompanyFundOccurrenceAndManualValuation", "051", "052", approved.migration052},
		{checkpoint053Path, "EnforceSafeheronOccurrence", "052", "053", approved.migration053},
	} {
		source := readProductionMigration(t, testCase.path)
		if actual := checkpointDigest(source); actual != testCase.digest {
			t.Fatalf("%s digest = %s, want %s", testCase.ceiling, actual, testCase.digest)
		}
		if err := validateApprovedControlledMigrationSource(source, testCase.typeName, testCase.prior, testCase.ceiling, approved); err != nil {
			t.Fatalf("production migration %s rejected: %v", testCase.ceiling, err)
		}
	}
}

func TestApprovedControlledMigrationDigestRejectsSemanticBypasses(t *testing.T) {
	approved := approvedControlledMigrationDigests()
	source052 := string(readProductionMigration(t, checkpoint052Path))
	source053 := string(readProductionMigration(t, checkpoint053Path))
	for name, testCase := range map[string]struct {
		source   string
		typeName string
		prior    string
		ceiling  string
	}{
		"swallowed stage error": {
			source: strings.Replace(source052,
				"if err := migration052Backfill(tx); err != nil {\n\t\treturn err\n\t}",
				"if err := migration052Backfill(tx); err != nil {\n\t\treturn swallow(err)\n\t}", 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"052 query result not bound to guard": {
			source: strings.Replace(source052,
				"missing, err := migration052Count(tx, migration052PreflightQuery)",
				"ignored, err := migration052Count(tx, migration052PreflightQuery)\n\tmissing := int64(0)\n\t_ = ignored", 1),
			typeName: "ExpandCompanyFundOccurrenceAndManualValuation", prior: "051", ceiling: "052",
		},
		"053 scan not bound to preflight": {
			source: strings.Replace(source053,
				".Scan(&preflight.missing, &preflight.wrongVersion, &preflight.duplicate, &preflight.invariant)",
				".Scan(new(int64), new(int64), new(int64), new(int64))", 1),
			typeName: "EnforceSafeheronOccurrence", prior: "052", ceiling: "053",
		},
		"053 unsafe guard is constant false": {
			source: strings.Replace(source053,
				"return result.missing != 0 || result.wrongVersion != 0 || result.duplicate != 0 || result.invariant != 0",
				"return false", 1),
			typeName: "EnforceSafeheronOccurrence", prior: "052", ceiling: "053",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateApprovedControlledMigrationSource([]byte(testCase.source), testCase.typeName, testCase.prior, testCase.ceiling, approved); err == nil {
				t.Fatal("semantic bypass matched approved migration blob")
			}
		})
	}
}

func readProductionMigration(t *testing.T, path string) []byte {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func TestValidateCheckpointArtifactsRejectsChangedMigration052Blob(t *testing.T) {
	repo, currentSHA, aSHA, v2SHA, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", v2SHA)
	writeCheckpointFile(t, repo, checkpoint052Path, strings.Replace(checkpointMigrationSource("052"), "public.company_fund_transactions", "public.company_fund_transactions /* changed */", 1))
	git("add", ".")
	git("commit", "-qm", "changed-052")
	changedV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "checkpoint-b-after-changed-052")
	changedB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, changedV2, changedB); err == nil {
		t.Fatalf("changed 052 blob accepted: %v", err)
	}
}

func TestValidateCheckpointArtifactsRejectsChangedPre052MigrationBlob(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	writeCheckpointFile(t, repo, "internal/migration/migrations/051_base.go", "package migrations\n\nconst V051 = \"051\" // changed after CURRENT\n")
	git("add", ".")
	git("commit", "-qm", "rewrite-051-in-a")
	changedA := git("rev-parse", "HEAD")
	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "changed-pre052-v2")
	changedV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "changed-pre052-b")
	changedB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, changedA, changedV2, changedB); err == nil {
		t.Fatal("changed pre-052 migration blob accepted")
	}
}

func TestValidateCheckpointArtifactsRejectsHiddenLowVersionRegistration(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	insertHiddenCheckpointRegistration(t, repo)
	git("add", ".")
	git("commit", "-qm", "hidden-low-version-a")
	hiddenA := git("rev-parse", "HEAD")
	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "hidden-registration-v2")
	hiddenV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	insertHiddenCheckpointRegistration(t, repo)
	git("add", ".")
	git("commit", "-qm", "hidden-registration-b")
	hiddenB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, hiddenA, hiddenV2, hiddenB); err == nil {
		t.Fatal("hidden low-version registration accepted")
	}
}

func TestValidateCheckpointArtifactsRejectsNonNumberedMigrationRegistrationSource(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	writeCheckpointFile(t, repo, "internal/migration/migrations/legacy_hidden.go", "package migrations\n\nconst V050Hidden = \"050\"\n")
	path := filepath.Join(repo, "cmd", "migrate", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(source), "registered := []string{", `registered := []string{migrations.V050Hidden, `, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-qm", "non-numbered-hidden-registration-a")
	badA := git("rev-parse", "HEAD")
	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "non-numbered-hidden-v2")
	badV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "non-numbered-hidden-b")
	badB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, badA, badV2, badB); err == nil {
		t.Fatal("non-numbered hidden migration registration source accepted")
	}
}

func TestValidateCheckpointArtifactsRejectsCheckpointAContainingV2BusinessCode(t *testing.T) {
	repo, currentSHA, _, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", currentSHA)
	writeCheckpointFile(t, repo, checkpoint052Path, checkpointMigrationSource("052"))
	writeCheckpointRunner(t, repo, "052")
	writeCheckpointFile(t, repo, "internal/companyfund/safeheron_runtime_resolvers.go", "package companyfund\n")
	git("add", ".")
	git("commit", "-qm", "checkpoint-a-with-v2-business")
	badA := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, "internal/companyfund/safeheron_webhook_eligibility.go", "package companyfund\n")
	git("add", ".")
	git("commit", "-qm", "remaining-v2")
	badV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "checkpoint-b")
	badB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, badA, badV2, badB); err == nil {
		t.Fatal("checkpoint A containing v2 business code accepted")
	}
}

func TestValidateCheckpointArtifactsRejectsEmptyV2Role(t *testing.T) {
	repo, currentSHA, aSHA, _, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", aSHA)
	git("commit", "--allow-empty", "-qm", "empty-v2")
	emptyV2 := git("rev-parse", "HEAD")
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "checkpoint-b")
	bSHA := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, emptyV2, bSHA); err == nil {
		t.Fatal("empty v2 role accepted")
	}
}

func TestValidateCheckpointArtifactsRejectsCheckpointBContainingBusinessCode(t *testing.T) {
	repo, currentSHA, aSHA, v2SHA, _ := checkpointArtifactRepository(t)
	git := checkpointGit(t, repo)
	git("checkout", "-q", v2SHA)
	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	appendCheckpointFile(t, repo, "internal/companyfund/valuation_runtime.go", "\n// unrelated business change in checkpoint B\n")
	git("add", ".")
	git("commit", "-qm", "checkpoint-b-with-business")
	badB := git("rev-parse", "HEAD")
	if _, err := validateCheckpointFixtureArtifacts(context.Background(), repo, currentSHA, aSHA, v2SHA, badB); err == nil {
		t.Fatal("checkpoint B containing business code accepted")
	}
}

func insertHiddenCheckpointRegistration(t *testing.T, repo string) {
	t.Helper()
	path := filepath.Join(repo, "cmd", "migrate", "main.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(source), "registered := []string{", `registered := []string{"050", `, 1)
	if updated == string(source) {
		t.Fatal("checkpoint fixture registration insertion point missing")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkpointArtifactRepository(t *testing.T) (string, string, string, string, string) {
	t.Helper()
	repo := t.TempDir()
	git := checkpointGit(t, repo)
	git("init", "-q")
	git("config", "user.name", "Checkpoint Test")
	git("config", "user.email", "checkpoint@example.test")
	writeCheckpointFile(t, repo, "go.mod", "module checkpoint-fixture\n\ngo 1.25\n")
	writeCheckpointFile(t, repo, "internal/migration/migrations/051_base.go", "package migrations\n\nconst V051 = \"051\"\n")
	writeCheckpointRunner(t, repo, "051")
	git("add", ".")
	git("commit", "-qm", "current")
	currentSHA := git("rev-parse", "HEAD")

	writeCheckpointFile(t, repo, checkpoint052Path, checkpointMigrationSource("052"))
	writeCheckpointRunner(t, repo, "052")
	git("add", ".")
	git("commit", "-qm", "checkpoint-a")
	aSHA := git("rev-parse", "HEAD")

	writeCheckpointV2Files(t, repo)
	git("add", ".")
	git("commit", "-qm", "v2-server")
	v2SHA := git("rev-parse", "HEAD")

	writeCheckpointFile(t, repo, checkpoint053Path, checkpointMigrationSource("053"))
	writeCheckpointRunner(t, repo, "053")
	git("add", ".")
	git("commit", "-qm", "checkpoint-b")
	return repo, currentSHA, aSHA, v2SHA, git("rev-parse", "HEAD")
}

func writeCheckpointV2Files(t *testing.T, repo string) {
	t.Helper()
	for _, path := range checkpointV2RequiredPaths {
		writeCheckpointFile(t, repo, path, "package companyfund\n")
	}
}

func checkpointMigrationSource(version string) string {
	typeName := "ExpandCompanyFundOccurrenceAndManualValuation"
	prior := "051"
	runBody := `func runMigration052(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration052TimeoutsSQL); err != nil { return err }
	missing, err := migration052Count(tx, migration052PreflightQuery)
	if err != nil { return err }
	if missing != 0 { return fmt.Errorf("missing occurrence tuples") }
	if _, err := tx.ExecContext(ctx, migration052AddOccurrenceColumnsSQL); err != nil { return err }
	if _, err := tx.ExecContext(ctx, migration052SchemaDDL); err != nil { return err }
	if err := migration052Backfill(tx); err != nil { return err }
	if err := migration052ValidateAliases(tx); err != nil { return err }
	return nil
}
func migration052Count(*sql.Tx, string) (int64, error) { return 0, nil }
func migration052Backfill(tx *sql.Tx) error { rows, err := tx.QueryContext(context.Background(), "SELECT 1"); if err == nil { _ = rows.Close() }; return err }
func migration052ValidateAliases(*sql.Tx) error { return nil }
const migration052TimeoutsSQL = "SET LOCAL search_path = pg_catalog, public"
const migration052PreflightQuery = "SELECT 1 FROM public.company_fund_transactions"
const migration052AddOccurrenceColumnsSQL = "ALTER TABLE public.company_fund_transactions ADD COLUMN fixture TEXT"
const migration052SchemaDDL = "ALTER TABLE public.company_fund_transactions ADD COLUMN fixture_two TEXT"
`
	if version == "053" {
		typeName = "EnforceSafeheronOccurrence"
		prior = "052"
		runBody = `func runMigration053(tx *sql.Tx) error {
	ctx := context.Background()
	if _, err := tx.ExecContext(ctx, migration053TimeoutsSQL); err != nil { return err }
	var preflight migration053Preflight
	if err := tx.QueryRowContext(ctx, migration053PreflightSQL).Scan(&preflight.missing); err != nil { return err }
	if preflight.unsafe() { return fmt.Errorf("unsafe occurrence tuples") }
	if _, err := tx.ExecContext(ctx, migration053AddConstraintSQL); err != nil { return err }
	if _, err := tx.ExecContext(ctx, migration053ValidateConstraintSQL); err != nil { return err }
	return nil
}
type migration053Preflight struct { missing int64 }
func (result migration053Preflight) unsafe() bool { return result.missing != 0 }
const migration053TimeoutsSQL = "SET LOCAL search_path = pg_catalog, public"
const migration053PreflightSQL = "SELECT 1 FROM public.company_fund_transactions"
const migration053AddConstraintSQL = "ALTER TABLE public.company_fund_transactions ADD CONSTRAINT fixture CHECK (true)"
const migration053ValidateConstraintSQL = "ALTER TABLE public.company_fund_transactions VALIDATE CONSTRAINT fixture"
`
	}
	return `package migrations

import (
	"context"
	"database/sql"
	"fmt"
)

const V` + version + ` = "` + version + `"

type ` + typeName + ` struct{}

func (*` + typeName + `) RequiredPreexistingVersion() string { return "` + prior + `" }
func (*` + typeName + `) RequiredExpectedCeiling() string { return "` + version + `" }
func (*` + typeName + `) Up(*sql.DB) error { return fmt.Errorf("` + version + ` is controlled") }
func (*` + typeName + `) UpTx(tx *sql.Tx) error { return runMigration` + version + `(tx) }
` + runBody
}

func writeCheckpointRunner(t *testing.T, repo, ceiling string) {
	t.Helper()
	registrations := "migrations.V051"
	if ceiling >= "052" {
		registrations += ", migrations.V052"
	}
	if ceiling >= "053" {
		registrations += ", migrations.V053"
	}
	writeCheckpointFile(t, repo, "cmd/migrate/main.go", `package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"checkpoint-fixture/internal/migration/migrations"
)

func main() {
	printCeiling := flag.Bool("print-ceiling", false, "print compiled migration ceiling")
	printVersions := flag.Bool("print-versions", false, "print compiled migration versions")
	flag.Parse()
	registered := []string{`+registrations+`}
	if *printVersions {
		_ = json.NewEncoder(os.Stdout).Encode(registered)
		return
	}
	if *printCeiling {
		fmt.Println(registered[len(registered)-1])
	}
}
`)
}

func checkpointGit(t *testing.T, repo string) func(...string) string {
	t.Helper()
	return func(args ...string) string {
		output, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
}

func writeCheckpointFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendCheckpointFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
