package releasecontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type BootstrapManifestInput struct {
	Repository       string
	Phase            BootstrapPhase
	BaseRef          string
	HeadRef          string
	MainWorkflowRef  string
	StageWorkflowRef string
}

type WorkflowEvidence struct {
	Ref           string `json:"ref"`
	CommitSHA     string `json:"commit_sha"`
	BlobSHA       string `json:"blob_sha"`
	ContentSHA256 string `json:"content_sha256"`
	DeployHash    string `json:"deploy_hash"`
}

type BootstrapManifest struct {
	Repository       string           `json:"repository"`
	Phase            BootstrapPhase   `json:"phase"`
	BaseSHA          string           `json:"base_sha"`
	HeadSHA          string           `json:"head_sha"`
	TreeSHA          string           `json:"tree_sha"`
	ChangedFiles     []string         `json:"changed_files"`
	HeadWorkflow     WorkflowEvidence `json:"head_workflow"`
	MainWorkflow     WorkflowEvidence `json:"main_workflow"`
	StageWorkflow    WorkflowEvidence `json:"stage_workflow"`
	PromotionWarning string           `json:"promotion_warning"`
}

func BuildBootstrapManifest(ctx context.Context, input BootstrapManifestInput) (BootstrapManifest, error) {
	if input.Repository == "" {
		return BootstrapManifest{}, errors.New("repository path is required")
	}
	repository, err := filepath.Abs(input.Repository)
	if err != nil {
		return BootstrapManifest{}, err
	}
	git := gitRepository{path: repository}
	if _, err := git.output(ctx, "rev-parse", "--show-toplevel"); err != nil {
		return BootstrapManifest{}, fmt.Errorf("open repository: %w", err)
	}
	baseSHA, err := git.resolveCommit(ctx, "base", input.BaseRef)
	if err != nil {
		return BootstrapManifest{}, err
	}
	headSHA, err := git.resolveCommit(ctx, "head", input.HeadRef)
	if err != nil {
		return BootstrapManifest{}, err
	}
	if err := git.run(ctx, "merge-base", "--is-ancestor", baseSHA, headSHA); err != nil {
		return BootstrapManifest{}, errors.New("bootstrap head must descend from the exact base")
	}
	treeSHA, err := git.output(ctx, "rev-parse", "--verify", headSHA+"^{tree}")
	if err != nil {
		return BootstrapManifest{}, fmt.Errorf("resolve head tree: %w", err)
	}
	changedFiles, err := git.changedFiles(ctx, baseSHA, headSHA)
	if err != nil {
		return BootstrapManifest{}, err
	}
	if err := ValidateBootstrapChangedFiles(input.Phase, changedFiles); err != nil {
		return BootstrapManifest{}, err
	}
	headWorkflow, err := git.workflowEvidence(ctx, headSHA)
	if err != nil {
		return BootstrapManifest{}, fmt.Errorf("head workflow: %w", err)
	}
	mainWorkflow, err := git.workflowEvidence(ctx, input.MainWorkflowRef)
	if err != nil {
		return BootstrapManifest{}, fmt.Errorf("main workflow: %w", err)
	}
	stageWorkflow, err := git.workflowEvidence(ctx, input.StageWorkflowRef)
	if err != nil {
		return BootstrapManifest{}, fmt.Errorf("stage workflow: %w", err)
	}
	if mainWorkflow.DeployHash != stageWorkflow.DeployHash || headWorkflow.DeployHash != mainWorkflow.DeployHash {
		return BootstrapManifest{}, errors.New("main/stage workflow contract hashes do not match")
	}
	return BootstrapManifest{
		Repository: repository, Phase: input.Phase, BaseSHA: baseSHA, HeadSHA: headSHA,
		TreeSHA: strings.TrimSpace(treeSHA), ChangedFiles: changedFiles,
		HeadWorkflow: headWorkflow, MainWorkflow: mainWorkflow, StageWorkflow: stageWorkflow,
		PromotionWarning: "User approval is required before promoting D0 to main or B0 to stage; this manifest performs no promotion.",
	}, nil
}

type gitRepository struct {
	path string
}

func (repository gitRepository) resolveCommit(ctx context.Context, label, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("resolve %s ref: ref is required", label)
	}
	sha, err := repository.output(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve %s ref %q: %w", label, ref, err)
	}
	return strings.TrimSpace(sha), nil
}

func (repository gitRepository) changedFiles(ctx context.Context, baseSHA, headSHA string) ([]string, error) {
	output, err := repository.outputBytes(ctx, "diff", "--name-only", "-z", baseSHA, headSHA, "--")
	if err != nil {
		return nil, fmt.Errorf("list bootstrap changed files: %w", err)
	}
	parts := bytes.Split(output, []byte{0})
	files := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) > 0 {
			files = append(files, filepath.ToSlash(string(part)))
		}
	}
	sort.Strings(files)
	return files, nil
}

func (repository gitRepository) workflowEvidence(ctx context.Context, ref string) (WorkflowEvidence, error) {
	commitSHA, err := repository.resolveCommit(ctx, "workflow", ref)
	if err != nil {
		return WorkflowEvidence{}, err
	}
	lsTree, err := repository.output(ctx, "ls-tree", commitSHA, "--", StageWorkflowPath)
	if err != nil {
		return WorkflowEvidence{}, fmt.Errorf("read workflow tree entry: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(lsTree))
	if len(fields) < 3 || fields[1] != "blob" {
		return WorkflowEvidence{}, fmt.Errorf("workflow %s is missing at %s", StageWorkflowPath, ref)
	}
	content, err := repository.outputBytes(ctx, "show", commitSHA+":"+StageWorkflowPath)
	if err != nil {
		return WorkflowEvidence{}, fmt.Errorf("read workflow content: %w", err)
	}
	contract, err := ParseWorkflowContract(content)
	if err != nil {
		return WorkflowEvidence{}, fmt.Errorf("workflow contract: %w", err)
	}
	contentHash := sha256.Sum256(content)
	return WorkflowEvidence{
		Ref: ref, CommitSHA: commitSHA, BlobSHA: fields[2], ContentSHA256: hex.EncodeToString(contentHash[:]),
		DeployHash: contract.DeployHash,
	}, nil
}

func (repository gitRepository) run(ctx context.Context, args ...string) error {
	_, err := repository.outputBytes(ctx, args...)
	return err
}

func (repository gitRepository) output(ctx context.Context, args ...string) (string, error) {
	output, err := repository.outputBytes(ctx, args...)
	return string(output), err
}

func (repository gitRepository) outputBytes(ctx context.Context, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", repository.path}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
