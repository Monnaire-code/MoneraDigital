package releasecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"go.yaml.in/yaml/v3"
)

type WorkflowContract struct {
	InputSchemaHash string `json:"input_schema_hash"`
	ControlHash     string `json:"control_hash"`
}

func ParseWorkflowContract(data []byte) (WorkflowContract, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return WorkflowContract{}, fmt.Errorf("parse workflow YAML: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return WorkflowContract{}, errors.New("workflow root must be a mapping")
	}
	root := document.Content[0]
	on, err := workflowMappingValue(root, "on")
	if err != nil {
		return WorkflowContract{}, err
	}
	push, err := workflowMappingValue(on, "push")
	if err != nil {
		return WorkflowContract{}, err
	}
	branches, err := workflowScalarSequence(push, "branches")
	if err != nil || !reflect.DeepEqual(branches, []string{"stage"}) {
		return WorkflowContract{}, errors.New("workflow push branches must be exactly [stage]")
	}
	dispatch, err := workflowMappingValue(on, "workflow_dispatch")
	if err != nil {
		return WorkflowContract{}, err
	}
	inputs, err := workflowMappingValue(dispatch, "inputs")
	if err != nil {
		return WorkflowContract{}, err
	}
	if err := requireWorkflowMappingKeys(inputs, []string{"artifact_ref", "expected_migration_ceiling", "installed_server_sha", "mode", "run_id"}); err != nil {
		return WorkflowContract{}, fmt.Errorf("workflow_dispatch inputs: %w", err)
	}
	mode := workflowMappingValueOptional(inputs, "mode")
	options, err := workflowScalarSequence(mode, "options")
	if err != nil || !reflect.DeepEqual(options, []string{"migration-only", "workers-off-current", "server-dark", "workers-on-installed", "standard"}) {
		return WorkflowContract{}, errors.New("workflow mode options do not match the release contract")
	}
	for _, input := range []struct {
		name     string
		required string
		typeName string
	}{
		{"mode", "true", "choice"},
		{"artifact_ref", "true", "string"},
		{"run_id", "true", "string"},
		{"installed_server_sha", "false", "string"},
		{"expected_migration_ceiling", "false", "string"},
	} {
		definition := workflowMappingValueOptional(inputs, input.name)
		if err := requireWorkflowScalar(definition, "required", input.required); err != nil {
			return WorkflowContract{}, err
		}
		if err := requireWorkflowScalar(definition, "type", input.typeName); err != nil {
			return WorkflowContract{}, err
		}
	}
	jobs, err := workflowMappingValue(root, "jobs")
	if err != nil {
		return WorkflowContract{}, err
	}
	if err := requireWorkflowMappingKeys(jobs, []string{"control-preflight", "deploy-stage"}); err != nil {
		return WorkflowContract{}, fmt.Errorf("workflow jobs: %w", err)
	}
	control := workflowMappingValueOptional(jobs, "control-preflight")
	deploy := workflowMappingValueOptional(jobs, "deploy-stage")
	if environment := workflowMappingValueOptional(control, "environment"); environment != nil {
		return WorkflowContract{}, errors.New("control-preflight must not declare an environment")
	}
	if err := requireWorkflowScalar(deploy, "needs", "control-preflight"); err != nil {
		return WorkflowContract{}, err
	}
	if err := requireWorkflowScalar(deploy, "environment", "stage"); err != nil {
		return WorkflowContract{}, err
	}
	inputHash, err := workflowNodeHash(inputs)
	if err != nil {
		return WorkflowContract{}, err
	}
	controlHash, err := workflowNodeHash(control)
	if err != nil {
		return WorkflowContract{}, err
	}
	return WorkflowContract{InputSchemaHash: inputHash, ControlHash: controlHash}, nil
}

func workflowNodeHash(node *yaml.Node) (string, error) {
	canonical, err := canonicalWorkflowNode(node)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalWorkflowNode(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		result := make(map[string]any, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			value, err := canonicalWorkflowNode(node.Content[index+1])
			if err != nil {
				return nil, err
			}
			result[node.Content[index].Value] = value
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]any, len(node.Content))
		for index, child := range node.Content {
			value, err := canonicalWorkflowNode(child)
			if err != nil {
				return nil, err
			}
			result[index] = value
		}
		return result, nil
	case yaml.ScalarNode:
		return map[string]string{"tag": node.Tag, "value": node.Value}, nil
	default:
		return nil, fmt.Errorf("unsupported workflow YAML node kind %d", node.Kind)
	}
}

func workflowMappingValue(node *yaml.Node, key string) (*yaml.Node, error) {
	value := workflowMappingValueOptional(node, key)
	if value == nil {
		return nil, fmt.Errorf("workflow mapping key %q is missing", key)
	}
	return value, nil
}

func workflowMappingValueOptional(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func workflowScalarSequence(node *yaml.Node, key string) ([]string, error) {
	sequence, err := workflowMappingValue(node, key)
	if err != nil {
		return nil, err
	}
	if sequence.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("workflow %s must be a sequence", key)
	}
	values := make([]string, len(sequence.Content))
	for index, item := range sequence.Content {
		if item.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("workflow %s item must be a scalar", key)
		}
		values[index] = item.Value
	}
	return values, nil
}

func requireWorkflowMappingKeys(node *yaml.Node, expected []string) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("value must be a mapping")
	}
	actual := make([]string, 0, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		actual = append(actual, node.Content[index].Value)
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		return fmt.Errorf("keys are %v, want %v", actual, want)
	}
	return nil
}

func requireWorkflowScalar(node *yaml.Node, key, expected string) error {
	value, err := workflowMappingValue(node, key)
	if err != nil {
		return err
	}
	if value.Kind != yaml.ScalarNode || value.Value != expected {
		return fmt.Errorf("workflow %s must equal %q", key, expected)
	}
	return nil
}
