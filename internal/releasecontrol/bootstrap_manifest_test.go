package releasecontrol

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildBootstrapManifestFromRealGitRefs(t *testing.T) {
	t.Parallel()
	repo := newBootstrapGitRepository(t)
	input := BootstrapManifestInput{
		Repository:       repo.path,
		Phase:            BootstrapD0,
		BaseRef:          repo.baseSHA,
		HeadRef:          repo.bootstrapSHA,
		MainWorkflowRef:  "refs/heads/main-workflow",
		StageWorkflowRef: "refs/heads/stage-workflow",
	}
	manifest, err := BuildBootstrapManifest(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BaseSHA != repo.baseSHA || manifest.HeadSHA != repo.bootstrapSHA || len(manifest.TreeSHA) != 40 {
		t.Fatalf("manifest refs = %+v", manifest)
	}
	if len(manifest.ChangedFiles) != 1 || manifest.ChangedFiles[0] != StageWorkflowPath {
		t.Fatalf("changed files = %v", manifest.ChangedFiles)
	}
	if manifest.MainWorkflow.BlobSHA == "" || manifest.MainWorkflow.BlobSHA != manifest.StageWorkflow.BlobSHA {
		t.Fatalf("workflow blobs = %+v / %+v", manifest.MainWorkflow, manifest.StageWorkflow)
	}
	if manifest.HeadWorkflow.BlobSHA != manifest.MainWorkflow.BlobSHA {
		t.Fatalf("head workflow evidence = %+v", manifest.HeadWorkflow)
	}
	if manifest.MainWorkflow.DeployHash != manifest.StageWorkflow.DeployHash {
		t.Fatalf("workflow contract mismatch: %+v", manifest)
	}
	if !strings.Contains(strings.ToLower(manifest.PromotionWarning), "user") {
		t.Fatalf("promotion warning = %q", manifest.PromotionWarning)
	}

	input.Phase = BootstrapB0
	if _, err := BuildBootstrapManifest(context.Background(), input); err != nil {
		t.Fatalf("B0 rejected allowed real diff: %v", err)
	}
}

func TestBuildBootstrapManifestRejectsUnsafeRealDiff(t *testing.T) {
	t.Parallel()
	repo := newBootstrapGitRepository(t)
	writeGitFile(t, repo.path, "internal/companyfund/identity.go", "package companyfund\n")
	runGit(t, repo.path, "add", ".")
	runGit(t, repo.path, "commit", "-m", "unsafe business file")
	unsafeSHA := strings.TrimSpace(runGit(t, repo.path, "rev-parse", "HEAD"))
	_, err := BuildBootstrapManifest(context.Background(), BootstrapManifestInput{
		Repository: repo.path, Phase: BootstrapB0, BaseRef: repo.baseSHA, HeadRef: unsafeSHA,
		MainWorkflowRef: "refs/heads/main-workflow", StageWorkflowRef: "refs/heads/stage-workflow",
	})
	if err == nil || !strings.Contains(err.Error(), "outside the allowlist") {
		t.Fatalf("unsafe diff error = %v", err)
	}
}

func TestBuildBootstrapManifestRejectsWorkflowDriftAndMissingRefs(t *testing.T) {
	t.Parallel()
	repo := newBootstrapGitRepository(t)
	runGit(t, repo.path, "checkout", "-b", "stage-drift", repo.bootstrapSHA)
	workflow := strings.Replace(bootstrapWorkflowFixture(), "timeout-minutes: 25", "timeout-minutes: 26", 1)
	writeGitFile(t, repo.path, StageWorkflowPath, workflow)
	runGit(t, repo.path, "add", StageWorkflowPath)
	runGit(t, repo.path, "commit", "-m", "drift workflow input")

	input := BootstrapManifestInput{
		Repository: repo.path, Phase: BootstrapD0, BaseRef: repo.baseSHA, HeadRef: repo.bootstrapSHA,
		MainWorkflowRef: "refs/heads/main-workflow", StageWorkflowRef: "refs/heads/stage-drift",
	}
	if _, err := BuildBootstrapManifest(context.Background(), input); err == nil || !strings.Contains(err.Error(), "workflow contract") {
		t.Fatalf("workflow drift error = %v", err)
	}
	input.StageWorkflowRef = "refs/heads/missing"
	if _, err := BuildBootstrapManifest(context.Background(), input); err == nil || !strings.Contains(err.Error(), "resolve") {
		t.Fatalf("missing ref error = %v", err)
	}
}

func TestBuildBootstrapManifestRejectsInvalidWorkflowAtRef(t *testing.T) {
	t.Parallel()
	repo := newBootstrapGitRepository(t)
	runGit(t, repo.path, "checkout", "-b", "invalid-workflow", repo.bootstrapSHA)
	writeGitFile(t, repo.path, StageWorkflowPath, "invalid: [\n")
	runGit(t, repo.path, "add", StageWorkflowPath)
	runGit(t, repo.path, "commit", "-m", "invalid workflow")
	invalidSHA := strings.TrimSpace(runGit(t, repo.path, "rev-parse", "HEAD"))
	_, err := BuildBootstrapManifest(context.Background(), BootstrapManifestInput{
		Repository: repo.path, Phase: BootstrapD0, BaseRef: repo.baseSHA, HeadRef: repo.bootstrapSHA,
		MainWorkflowRef: invalidSHA, StageWorkflowRef: repo.bootstrapSHA,
	})
	if err == nil || !strings.Contains(err.Error(), "workflow contract") {
		t.Fatalf("invalid workflow error = %v", err)
	}
}

func TestBuildBootstrapManifestErrorPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := BuildBootstrapManifest(ctx, BootstrapManifestInput{}); err == nil {
		t.Fatal("empty repository accepted")
	}
	if _, err := BuildBootstrapManifest(ctx, BootstrapManifestInput{Repository: t.TempDir()}); err == nil {
		t.Fatal("non-git repository accepted")
	}
	repo := newBootstrapGitRepository(t)
	baseInput := BootstrapManifestInput{
		Repository: repo.path, Phase: BootstrapD0, BaseRef: repo.baseSHA, HeadRef: repo.bootstrapSHA,
		MainWorkflowRef: "refs/heads/main-workflow", StageWorkflowRef: "refs/heads/stage-workflow",
	}
	for _, mutate := range []func(*BootstrapManifestInput){
		func(input *BootstrapManifestInput) { input.BaseRef = "" },
		func(input *BootstrapManifestInput) { input.BaseRef = "missing" },
		func(input *BootstrapManifestInput) { input.HeadRef = "missing" },
		func(input *BootstrapManifestInput) { input.MainWorkflowRef = repo.baseSHA },
	} {
		input := baseInput
		mutate(&input)
		if _, err := BuildBootstrapManifest(ctx, input); err == nil {
			t.Fatal("invalid bootstrap manifest input accepted")
		}
	}
	if _, err := (gitRepository{path: repo.path}).resolveCommit(ctx, "test", ""); err == nil {
		t.Fatal("empty git ref accepted")
	}
	if _, err := (gitRepository{path: repo.path}).changedFiles(ctx, "missing", repo.bootstrapSHA); err == nil {
		t.Fatal("invalid git diff accepted")
	}
	if _, err := (gitRepository{path: repo.path}).workflowEvidence(ctx, repo.baseSHA); err == nil {
		t.Fatal("missing workflow evidence accepted")
	}

	runGit(t, repo.path, "checkout", "--orphan", "unrelated")
	runGit(t, repo.path, "rm", "-rf", ".")
	writeGitFile(t, repo.path, "README.md", "unrelated\n")
	runGit(t, repo.path, "add", ".")
	runGit(t, repo.path, "commit", "-m", "unrelated")
	unrelatedSHA := strings.TrimSpace(runGit(t, repo.path, "rev-parse", "HEAD"))
	input := baseInput
	input.HeadRef = unrelatedSHA
	if _, err := BuildBootstrapManifest(ctx, input); err == nil || !strings.Contains(err.Error(), "descend") {
		t.Fatalf("non-ancestor error = %v", err)
	}

	runGit(t, repo.path, "checkout", "-B", "delete-workflow", repo.bootstrapSHA)
	runGit(t, repo.path, "rm", StageWorkflowPath)
	runGit(t, repo.path, "commit", "-m", "delete workflow")
	deletedSHA := strings.TrimSpace(runGit(t, repo.path, "rev-parse", "HEAD"))
	input = baseInput
	input.BaseRef = repo.bootstrapSHA
	input.HeadRef = deletedSHA
	if _, err := BuildBootstrapManifest(ctx, input); err == nil || !strings.Contains(err.Error(), "head workflow") {
		t.Fatalf("deleted head workflow error = %v", err)
	}
}

type bootstrapGitRepository struct {
	path         string
	baseSHA      string
	bootstrapSHA string
}

func newBootstrapGitRepository(t *testing.T) bootstrapGitRepository {
	t.Helper()
	path := t.TempDir()
	runGit(t, path, "init", "-q")
	runGit(t, path, "config", "user.name", "Release Test")
	runGit(t, path, "config", "user.email", "release@example.test")
	writeGitFile(t, path, "README.md", "base\n")
	runGit(t, path, "add", ".")
	runGit(t, path, "commit", "-m", "base")
	baseSHA := strings.TrimSpace(runGit(t, path, "rev-parse", "HEAD"))
	writeGitFile(t, path, StageWorkflowPath, bootstrapWorkflowFixture())
	runGit(t, path, "add", StageWorkflowPath)
	runGit(t, path, "commit", "-m", "workflow bootstrap")
	bootstrapSHA := strings.TrimSpace(runGit(t, path, "rev-parse", "HEAD"))
	runGit(t, path, "update-ref", "refs/heads/main-workflow", bootstrapSHA)
	runGit(t, path, "update-ref", "refs/heads/stage-workflow", bootstrapSHA)
	return bootstrapGitRepository{path: path, baseSHA: baseSHA, bootstrapSHA: bootstrapSHA}
}

func writeGitFile(t *testing.T, repo, name, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repo}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func bootstrapWorkflowFixture() string {
	return `name: Deploy Backend to Stage
on:
  push:
    branches: [stage]
jobs:
  deploy-stage:
    environment: stage
    runs-on: ubuntu-latest
    timeout-minutes: 25
    env:
      EXPECTED_MIGRATION_CEILING: "060"
    steps:
      - name: Execute standard stage deploy
        run: echo standard
`
}
