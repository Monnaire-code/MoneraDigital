package releasecontrol

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestStageWorkflowStructure(t *testing.T) {
	t.Parallel()
	root := repositoryRoot(t)
	document := parseWorkflow(t, filepath.Join(root, StageWorkflowPath))

	on := mappingValue(t, document, "on")
	push := mappingValue(t, on, "push")
	assertScalarSequence(t, mappingValue(t, push, "branches"), []string{"stage"})
	dispatch := mappingValue(t, on, "workflow_dispatch")
	inputs := mappingValue(t, dispatch, "inputs")
	assertMappingKeys(t, inputs, []string{"artifact_ref", "expected_migration_ceiling", "installed_server_sha", "mode", "run_id"})
	mode := mappingValue(t, inputs, "mode")
	assertScalarSequence(t, mappingValue(t, mode, "options"), []string{
		"migration-only", "workers-off-current", "server-dark", "workers-on-installed", "standard",
	})

	jobs := mappingValue(t, document, "jobs")
	assertMappingKeys(t, jobs, []string{"control-preflight", "deploy-stage"})
	control := mappingValue(t, jobs, "control-preflight")
	assertMappingKeys(t, mappingValue(t, control, "outputs"), []string{
		"artifact_ref", "baseline_digest", "control_token", "expected_migration_ceiling", "installed_server_sha", "mode", "run_id",
	})
	if mappingValueOptional(control, "environment") != nil {
		t.Fatal("control-preflight must not declare an environment")
	}
	assertControlHasNoSideEffects(t, mappingValue(t, control, "steps"))
	_, controlStep := namedStep(t, mappingValue(t, control, "steps"), "Validate event, ref, inputs, and repository lock")
	controlScript := scalarValue(t, mappingValue(t, controlStep, "run"))
	for _, required := range []string{"repository control token must be run_id@64hex", "workers-off-current requires installed_server_sha", "baseline_digest=", "control_token=", "installed_server_sha="} {
		if !strings.Contains(controlScript, required) {
			t.Errorf("control-preflight missing %q", required)
		}
	}

	deploy := mappingValue(t, jobs, "deploy-stage")
	assertScalar(t, mappingValue(t, deploy, "needs"), "control-preflight")
	assertScalar(t, mappingValue(t, deploy, "environment"), "stage")
	steps := mappingValue(t, deploy, "steps")
	revalidateIndex, revalidate := namedStep(t, steps, "Revalidate approved stage controls")
	checkoutIndex, checkout := namedStep(t, steps, "Checkout exact approved artifact")
	if revalidateIndex >= checkoutIndex {
		t.Fatal("approved controls must be revalidated before checkout")
	}
	assertScalar(t, mappingValue(t, checkout, "uses"), "actions/checkout@9f698171ed81b15d1823a05fc7211befd50c8ae0")
	with := mappingValue(t, checkout, "with")
	assertScalar(t, mappingValue(t, with, "ref"), "${{ needs.control-preflight.outputs.artifact_ref }}")
	revalidationScript := scalarValue(t, mappingValue(t, revalidate, "run"))
	revalidationEnv := mappingValue(t, revalidate, "env")
	assertScalar(t, mappingValue(t, revalidationEnv, "REPO_LOCK_AFTER_APPROVAL"), "${{ vars.COMPANY_FUND_STAGE_CUTOVER_LOCK_CONTROL }}")
	assertScalar(t, mappingValue(t, revalidationEnv, "ENV_STARTED"), "${{ vars.COMPANY_FUND_STAGE_CUTOVER_STARTED_AT }}")
	assertScalar(t, mappingValue(t, revalidationEnv, "PREFLIGHT_CONTROL_TOKEN"), "${{ needs.control-preflight.outputs.control_token }}")
	assertScalar(t, mappingValue(t, revalidationEnv, "PREFLIGHT_BASELINE_DIGEST"), "${{ needs.control-preflight.outputs.baseline_digest }}")
	for _, required := range []string{
		"REPO_LOCK_AFTER_APPROVAL",
		"ENV_STARTED",
		"[0-9]{4}-[0-9]{2}-[0-9]{2}T",
		"[0-9a-f]{64}",
		"compare/${ARTIFACT_REF}...${approved_stage_head}",
		"_FINAL",
		"repository control token changed after approval",
		"cutover baseline digest mismatch",
	} {
		if !strings.Contains(revalidationScript, required) {
			t.Errorf("approved-control step missing %q", required)
		}
	}

	_, copyPackage := namedStep(t, steps, "Copy approved stage package")
	copyCondition := scalarValue(t, mappingValue(t, copyPackage, "if"))
	if !strings.Contains(copyCondition, "workers-off-current") {
		t.Fatal("workers-off-current must receive the exact approved package runner for legacy stage bootstrap")
	}
	copyTarget := scalarValue(t, mappingValue(t, mappingValue(t, copyPackage, "with"), "target"))
	if !strings.Contains(copyTarget, "${{ github.run_id }}") || !strings.Contains(copyTarget, "${{ github.run_attempt }}") {
		t.Fatalf("stage package target is not unique to the exact workflow attempt: %q", copyTarget)
	}
	_, execute := namedStep(t, steps, "Execute stage release mode")
	executeScript := scalarValue(t, mappingValue(t, mappingValue(t, execute, "with"), "script"))
	for _, required := range []string{
		`case "$RELEASE_MODE" in`,
		`[[ -f "$DEPLOY_ROOT/release.tgz" ]]`,
		`rm -rf "$DEPLOY_SRC"`,
		`runner="$DEPLOY_SRC/deploy-remote.sh"`,
		`runner="$APP_DIR/deploy-remote.sh"`,
		`standard|migration-only|server-dark|workers-off-current)`,
		`workers-on-installed)`,
	} {
		if !strings.Contains(executeScript, required) {
			t.Errorf("stage execution does not fail closed on stale artifacts; missing %q", required)
		}
	}
	if strings.Contains(executeScript, `if [[ -f "$DEPLOY_SRC/deploy-remote.sh" ]]`) {
		t.Fatal("stage execution still falls back to an arbitrary residual /tmp runner")
	}
}

func TestWorkflowCompatibilityHashCoversInputSchemaAndControl(t *testing.T) {
	t.Parallel()
	document := parseWorkflow(t, filepath.Join(repositoryRoot(t), StageWorkflowPath))
	mainContract := workflowContractForNode(t, document)
	stageCopy := cloneNode(t, document)
	stageContract := workflowContractForNode(t, stageCopy)
	if mainContract != stageContract {
		t.Fatalf("identical main/stage workflows have different hashes: %+v != %+v", mainContract, stageContract)
	}

	inputs := mappingValue(t, mappingValue(t, mappingValue(t, stageCopy, "on"), "workflow_dispatch"), "inputs")
	artifact := mappingValue(t, inputs, "artifact_ref")
	mappingValue(t, artifact, "required").Value = "false"
	if changed, err := parseWorkflowContractNode(stageCopy); err == nil && changed.InputSchemaHash == mainContract.InputSchemaHash {
		t.Fatal("input schema drift did not change the compatibility hash")
	}

	controlCopy := cloneNode(t, document)
	control := mappingValue(t, mappingValue(t, controlCopy, "jobs"), "control-preflight")
	mappingValue(t, control, "timeout-minutes").Value = "6"
	if changed := workflowContractForNode(t, controlCopy); changed.ControlHash == mainContract.ControlHash {
		t.Fatal("control-preflight drift did not change the compatibility hash")
	}
}

func TestParseWorkflowContractRejectsStructuralDrift(t *testing.T) {
	t.Parallel()
	valid := bootstrapWorkflowFixture()
	tests := []struct {
		name string
		data string
	}{
		{"invalid-yaml", "["},
		{"non-mapping-root", "- item\n"},
		{"missing-on", strings.Replace(valid, "on:\n", "events:\n", 1)},
		{"missing-push", strings.Replace(valid, "  push:\n", "  pushes:\n", 1)},
		{"branches-not-sequence", strings.Replace(valid, "branches: [stage]", "branches: {only: stage}", 1)},
		{"wrong-push-branch", strings.Replace(valid, "branches: [stage]", "branches: [main]", 1)},
		{"missing-dispatch", strings.Replace(valid, "  workflow_dispatch:\n", "  manual_dispatch:\n", 1)},
		{"missing-inputs", strings.Replace(valid, "    inputs:\n", "    parameters:\n", 1)},
		{"inputs-not-mapping", strings.Replace(valid, "    inputs:\n      mode:", "    inputs: invalid\n    unused:\n      mode:", 1)},
		{"input-key-drift", strings.Replace(valid, "      run_id:\n", "      extra_input: {required: false, type: string}\n      run_id:\n", 1)},
		{"mode-options-not-sequence", strings.Replace(valid, "options: [migration-only, workers-off-current, server-dark, workers-on-installed, standard]", "options: standard", 1)},
		{"mode-options-wrong", strings.Replace(valid, "migration-only, workers-off-current, server-dark, workers-on-installed, standard", "standard", 1)},
		{"mode-required-wrong", strings.Replace(valid, "required: true\n        type: choice", "required: false\n        type: choice", 1)},
		{"mode-required-missing", strings.Replace(valid, "required: true\n        type: choice", "mandatory: true\n        type: choice", 1)},
		{"artifact-type-wrong", strings.Replace(valid, "artifact_ref:\n        required: true\n        type: string", "artifact_ref:\n        required: true\n        type: number", 1)},
		{"missing-jobs", strings.Replace(valid, "jobs:\n", "tasks:\n", 1)},
		{"job-key-drift", valid + "  unexpected-job: {}\n"},
		{"control-environment", strings.Replace(valid, "  control-preflight:\n", "  control-preflight:\n    environment: stage\n", 1)},
		{"deploy-needs-wrong", strings.Replace(valid, "needs: control-preflight", "needs: other", 1)},
		{"deploy-environment-wrong", strings.Replace(valid, "environment: stage", "environment: prod", 1)},
		{"input-alias", strings.Replace(strings.Replace(valid, "name: Stage release", "name: &shared Stage release", 1), "artifact_ref:\n        required: true", "artifact_ref:\n        description: *shared\n        required: true", 1)},
		{"control-alias", strings.Replace(strings.Replace(valid, "name: Stage release", "name: &shared Stage release", 1), "  control-preflight:\n", "  control-preflight:\n    alias: *shared\n", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseWorkflowContract([]byte(test.data)); err == nil {
				t.Fatal("invalid workflow accepted")
			}
		})
	}
}

func TestWorkflowContractHelperErrors(t *testing.T) {
	t.Parallel()
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Value: "value"}
	alias := &yaml.Node{Kind: yaml.AliasNode}
	if _, err := workflowMappingValue(nil, "missing"); err == nil {
		t.Fatal("missing mapping value accepted")
	}
	if workflowMappingValueOptional(scalar, "missing") != nil {
		t.Fatal("scalar exposed a mapping value")
	}
	if _, err := workflowScalarSequence(&yaml.Node{Kind: yaml.MappingNode}, "missing"); err == nil {
		t.Fatal("missing sequence accepted")
	}
	nonSequence := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "items"}, scalar,
	}}
	if _, err := workflowScalarSequence(nonSequence, "items"); err == nil {
		t.Fatal("scalar sequence accepted")
	}
	badItem := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "items"},
		{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}},
	}}
	if _, err := workflowScalarSequence(badItem, "items"); err == nil {
		t.Fatal("mapping sequence item accepted")
	}
	if err := requireWorkflowMappingKeys(scalar, nil); err == nil {
		t.Fatal("scalar mapping keys accepted")
	}
	if err := requireWorkflowMappingKeys(&yaml.Node{Kind: yaml.MappingNode}, []string{"missing"}); err == nil {
		t.Fatal("wrong mapping keys accepted")
	}
	if err := requireWorkflowScalar(&yaml.Node{Kind: yaml.MappingNode}, "missing", "value"); err == nil {
		t.Fatal("missing scalar accepted")
	}
	if err := requireWorkflowScalar(nonSequence, "items", "other"); err == nil {
		t.Fatal("wrong scalar accepted")
	}
	if _, err := workflowNodeHash(alias); err == nil {
		t.Fatal("alias node hashed")
	}
	badMapping := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "bad"}, alias,
	}}
	if _, err := canonicalWorkflowNode(badMapping); err == nil {
		t.Fatal("bad mapping canonicalized")
	}
	badSequence := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{alias}}
	if _, err := canonicalWorkflowNode(badSequence); err == nil {
		t.Fatal("bad sequence canonicalized")
	}
}

func workflowContractForNode(t *testing.T, document *yaml.Node) WorkflowContract {
	t.Helper()
	contract, err := parseWorkflowContractNode(document)
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func parseWorkflowContractNode(document *yaml.Node) (WorkflowContract, error) {
	data, err := yaml.Marshal(document)
	if err != nil {
		return WorkflowContract{}, err
	}
	return ParseWorkflowContract(data)
}

func assertControlHasNoSideEffects(t *testing.T, steps *yaml.Node) {
	t.Helper()
	if steps.Kind != yaml.SequenceNode {
		t.Fatalf("control steps kind = %d", steps.Kind)
	}
	for _, step := range steps.Content {
		if uses := mappingValueOptional(step, "uses"); uses != nil {
			t.Fatalf("control-preflight must not use actions: %s", scalarValue(t, uses))
		}
		if run := mappingValueOptional(step, "run"); run != nil {
			script := scalarValue(t, run)
			for _, forbidden := range []string{"systemctl", "monera-migrate", "scp", "ssh "} {
				if strings.Contains(script, forbidden) {
					t.Fatalf("control-preflight contains side effect %q", forbidden)
				}
			}
		}
	}
}

func namedStep(t *testing.T, steps *yaml.Node, name string) (int, *yaml.Node) {
	t.Helper()
	if steps.Kind != yaml.SequenceNode {
		t.Fatalf("steps kind = %d", steps.Kind)
	}
	for index, step := range steps.Content {
		nameNode := mappingValueOptional(step, "name")
		if nameNode != nil && scalarValue(t, nameNode) == name {
			return index, step
		}
	}
	t.Fatalf("step %q not found", name)
	return -1, nil
}

func assertMappingKeys(t *testing.T, node *yaml.Node, want []string) {
	t.Helper()
	if node.Kind != yaml.MappingNode {
		t.Fatalf("node kind = %d, want mapping", node.Kind)
	}
	got := make([]string, 0, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		got = append(got, node.Content[i].Value)
	}
	sort.Strings(got)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedWant)
	if !reflect.DeepEqual(got, sortedWant) {
		t.Fatalf("mapping keys = %v, want %v", got, sortedWant)
	}
}

func assertScalarSequence(t *testing.T, node *yaml.Node, want []string) {
	t.Helper()
	if node.Kind != yaml.SequenceNode {
		t.Fatalf("node kind = %d, want sequence", node.Kind)
	}
	got := make([]string, len(node.Content))
	for i, item := range node.Content {
		got[i] = scalarValue(t, item)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
}

func assertScalar(t *testing.T, node *yaml.Node, want string) {
	t.Helper()
	if got := scalarValue(t, node); got != want {
		t.Fatalf("scalar = %q, want %q", got, want)
	}
}

func scalarValue(t *testing.T, node *yaml.Node) string {
	t.Helper()
	if node.Kind != yaml.ScalarNode {
		t.Fatalf("node kind = %d, want scalar", node.Kind)
	}
	return node.Value
}

func mappingValue(t *testing.T, node *yaml.Node, key string) *yaml.Node {
	t.Helper()
	value := mappingValueOptional(node, key)
	if value == nil {
		t.Fatalf("mapping key %q not found", key)
	}
	return value
}

func mappingValueOptional(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func parseWorkflow(t *testing.T, path string) *yaml.Node {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		t.Fatal("workflow root must be a mapping")
	}
	return document.Content[0]
}

func cloneNode(t *testing.T, node *yaml.Node) *yaml.Node {
	t.Helper()
	data, err := yaml.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var clone yaml.Node
	if err := yaml.Unmarshal(data, &clone); err != nil {
		t.Fatal(err)
	}
	return clone.Content[0]
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
