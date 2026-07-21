package releasecontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"go.yaml.in/yaml/v3"
)

// WorkflowContract fingerprints the supported stage deploy surface.
// After cutover multi-mode removal, the contract is: push to stage → single
// standard deploy job with a controlled migration ceiling.
type WorkflowContract struct {
	DeployHash string `json:"deploy_hash"`
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
	if workflowMappingValueOptional(on, "workflow_dispatch") != nil {
		return WorkflowContract{}, errors.New("stage workflow must not expose multi-mode workflow_dispatch")
	}
	jobs, err := workflowMappingValue(root, "jobs")
	if err != nil {
		return WorkflowContract{}, err
	}
	if err := requireWorkflowMappingKeys(jobs, []string{"deploy-stage"}); err != nil {
		return WorkflowContract{}, fmt.Errorf("workflow jobs: %w", err)
	}
	if workflowMappingValueOptional(jobs, "control-preflight") != nil {
		return WorkflowContract{}, errors.New("stage workflow must not include cutover control-preflight")
	}
	deploy := workflowMappingValueOptional(jobs, "deploy-stage")
	if err := requireWorkflowScalar(deploy, "environment", "stage"); err != nil {
		return WorkflowContract{}, err
	}
	deployHash, err := workflowNodeHash(deploy)
	if err != nil {
		return WorkflowContract{}, err
	}
	return WorkflowContract{DeployHash: deployHash}, nil
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
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := canonicalWorkflowNode(child)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		return node.Value, nil
	case yaml.AliasNode:
		return nil, errors.New("workflow YAML aliases are not supported in contract hashing")
	default:
		return nil, fmt.Errorf("unsupported workflow YAML node kind %d", node.Kind)
	}
}

func workflowMappingValue(node *yaml.Node, key string) (*yaml.Node, error) {
	value := workflowMappingValueOptional(node, key)
	if value == nil {
		return nil, fmt.Errorf("missing mapping key %q", key)
	}
	return value, nil
}

func workflowMappingValueOptional(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
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
		return nil, fmt.Errorf("key %q must be a sequence", key)
	}
	values := make([]string, 0, len(sequence.Content))
	for _, child := range sequence.Content {
		if child.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("key %q must contain only scalars", key)
		}
		values = append(values, child.Value)
	}
	return values, nil
}

func requireWorkflowMappingKeys(node *yaml.Node, keys []string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return errors.New("expected a mapping")
	}
	seen := make(map[string]struct{}, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		seen[node.Content[index].Value] = struct{}{}
	}
	if len(seen) != len(keys) {
		return fmt.Errorf("expected keys %v", keys)
	}
	for _, key := range keys {
		if _, ok := seen[key]; !ok {
			return fmt.Errorf("missing key %q", key)
		}
	}
	return nil
}

func requireWorkflowScalar(node *yaml.Node, key, want string) error {
	value, err := workflowMappingValue(node, key)
	if err != nil {
		return err
	}
	if value.Kind != yaml.ScalarNode || value.Value != want {
		return fmt.Errorf("key %q must equal %q", key, want)
	}
	return nil
}
