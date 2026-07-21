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
	if mappingValueOptional(on, "workflow_dispatch") != nil {
		t.Fatal("stage workflow must not expose cutover workflow_dispatch modes")
	}

	jobs := mappingValue(t, document, "jobs")
	assertMappingKeys(t, jobs, []string{"deploy-stage"})
	if mappingValueOptional(jobs, "control-preflight") != nil {
		t.Fatal("stage workflow must not include cutover control-preflight")
	}
	deploy := mappingValue(t, jobs, "deploy-stage")
	assertScalar(t, mappingValue(t, deploy, "environment"), "stage")

	env := mappingValue(t, deploy, "env")
	assertScalar(t, mappingValue(t, env, "EXPECTED_MIGRATION_CEILING"), "060")

	steps := mappingValue(t, deploy, "steps")
	_, execute := namedStep(t, steps, "Execute standard stage deploy")
	executeScript := scalarValue(t, mappingValue(t, mappingValue(t, execute, "with"), "script"))
	for _, required := range []string{
		`--release-mode standard`,
		`--expected-migration-ceiling`,
		`EXPECTED_MIGRATION_CEILING`,
		`deploy-remote.sh`,
	} {
		if !strings.Contains(executeScript, required) {
			t.Errorf("standard stage deploy missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"workers-off-current",
		"server-dark",
		"workers-on-installed",
		"migration-only",
		"CUTOVER_LOCK",
	} {
		if strings.Contains(executeScript, forbidden) {
			t.Errorf("standard stage deploy still contains multi-mode surface %q", forbidden)
		}
	}

	contract, err := ParseWorkflowContract(mustRead(t, filepath.Join(root, StageWorkflowPath)))
	if err != nil {
		t.Fatal(err)
	}
	if contract.DeployHash == "" || len(contract.DeployHash) != 64 {
		t.Fatalf("deploy hash = %#v", contract)
	}
}

func TestWorkflowCompatibilityHashCoversDeployJob(t *testing.T) {
	t.Parallel()
	document := parseWorkflow(t, filepath.Join(repositoryRoot(t), StageWorkflowPath))
	mainContract := workflowContractForNode(t, document)
	stageCopy := cloneNode(t, document)
	stageContract := workflowContractForNode(t, stageCopy)
	if mainContract != stageContract {
		t.Fatalf("identical workflows have different hashes: %+v != %+v", mainContract, stageContract)
	}

	deployCopy := cloneNode(t, document)
	deploy := mappingValue(t, mappingValue(t, deployCopy, "jobs"), "deploy-stage")
	mappingValue(t, deploy, "timeout-minutes").Value = "99"
	if changed := workflowContractForNode(t, deployCopy); changed.DeployHash == mainContract.DeployHash {
		t.Fatal("deploy-stage drift did not change the compatibility hash")
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
		{"wrong-push-branch", strings.Replace(valid, "branches: [stage]", "branches: [main]", 1)},
		{"dispatch-present", strings.Replace(valid, "branches: [stage]\n", "branches: [stage]\n  workflow_dispatch: {}\n", 1)},
		{"control-preflight-present", strings.Replace(valid, "jobs:\n  deploy-stage:", "jobs:\n  control-preflight: {}\n  deploy-stage:", 1)},
		{"missing-deploy", strings.Replace(valid, "  deploy-stage:\n", "  other:\n", 1)},
		{"deploy-environment-wrong", strings.Replace(valid, "environment: stage", "environment: prod", 1)},
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
	if err := requireWorkflowMappingKeys(scalar, nil); err == nil {
		t.Fatal("scalar mapping keys accepted")
	}
	if err := requireWorkflowMappingKeys(&yaml.Node{Kind: yaml.MappingNode}, []string{"missing"}); err == nil {
		t.Fatal("wrong mapping keys accepted")
	}
	if err := requireWorkflowScalar(&yaml.Node{Kind: yaml.MappingNode}, "missing", "value"); err == nil {
		t.Fatal("missing scalar accepted")
	}
	if _, err := workflowNodeHash(alias); err == nil {
		t.Fatal("alias node hashed")
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
	if len(clone.Content) != 1 {
		t.Fatal("clone root unexpected")
	}
	return clone.Content[0]
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
