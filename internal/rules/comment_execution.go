package rules

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func parseCommentExecution(data []byte) (*CommentExecutionConfig, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, nil
	}
	root, err := strictYAMLMap(document.Content[0], "root", nil)
	if err != nil {
		return nil, err
	}
	node, ok := root["comment_execution"]
	if !ok {
		return nil, nil
	}
	fields, err := strictYAMLMap(node, "comment_execution", set("defaults", "models", "verification"))
	if err != nil {
		return nil, err
	}
	cfg := &CommentExecutionConfig{}
	if value, ok := fields["defaults"]; ok {
		defaults, err := strictYAMLMap(value, "comment_execution.defaults", set("model", "effort"))
		if err != nil {
			return nil, err
		}
		if value, ok := defaults["model"]; ok {
			cfg.Defaults.Model, err = yamlString(value, "comment_execution.defaults.model")
			if err != nil {
				return nil, err
			}
		}
		if value, ok := defaults["effort"]; ok {
			cfg.Defaults.Effort, err = yamlString(value, "comment_execution.defaults.effort")
			if err != nil {
				return nil, err
			}
		}
		if cfg.Defaults.Effort != "" && !validReasoningEfforts[cfg.Defaults.Effort] {
			return nil, fmt.Errorf("comment_execution default effort %q is unsupported", cfg.Defaults.Effort)
		}
	}
	if value, ok := fields["models"]; ok {
		if value.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("comment_execution.models must be a list")
		}
		seen := map[string]bool{}
		for _, entry := range value.Content {
			modelFields, err := strictYAMLMap(entry, "comment_execution.models[]", set("id", "efforts"))
			if err != nil {
				return nil, err
			}
			id, err := yamlString(modelFields["id"], "comment_execution.models[].id")
			if err != nil {
				return nil, err
			}
			if seen[id] {
				return nil, fmt.Errorf("duplicate comment_execution model %q", id)
			}
			seen[id] = true
			effortsNode := modelFields["efforts"]
			if effortsNode == nil || effortsNode.Kind != yaml.SequenceNode || len(effortsNode.Content) == 0 {
				return nil, fmt.Errorf("comment_execution.models[].efforts must be a nonempty list")
			}
			model := CommentModel{ID: id}
			seenEffort := map[string]bool{}
			for _, effortNode := range effortsNode.Content {
				effort, err := yamlString(effortNode, "comment_execution.models[].efforts[]")
				if err != nil {
					return nil, err
				}
				if !validReasoningEfforts[effort] || seenEffort[effort] {
					return nil, fmt.Errorf("comment_execution effort %q is unsupported or duplicate", effort)
				}
				seenEffort[effort] = true
				model.Efforts = append(model.Efforts, effort)
			}
			cfg.Models = append(cfg.Models, model)
		}
	}
	if value, ok := fields["verification"]; ok {
		verification, err := strictYAMLMap(value, "comment_execution.verification", set("source", "verified_at", "executor_version"))
		if err != nil {
			return nil, err
		}
		cfg.Verification.Source, err = yamlString(verification["source"], "comment_execution.verification.source")
		if err != nil {
			return nil, err
		}
		cfg.Verification.VerifiedAt, err = yamlString(verification["verified_at"], "comment_execution.verification.verified_at")
		if err != nil {
			return nil, err
		}
		cfg.Verification.ExecutorVersion, err = yamlString(verification["executor_version"], "comment_execution.verification.executor_version")
		if err != nil {
			return nil, err
		}
		if cfg.Verification.Source != "operator_probe" && cfg.Verification.Source != "runtime_probe" {
			return nil, fmt.Errorf("comment_execution verification source must be operator_probe or runtime_probe")
		}
		at, err := time.Parse(time.RFC3339, cfg.Verification.VerifiedAt)
		if err != nil || at.After(time.Now().Add(time.Minute)) {
			return nil, fmt.Errorf("comment_execution verification verified_at must be a past RFC3339 timestamp")
		}
	}
	if cfg.Defaults.Model != "" {
		found := false
		for _, model := range cfg.Models {
			if model.ID == cfg.Defaults.Model {
				found = true
				if cfg.Defaults.Effort != "" {
					found = false
					for _, effort := range model.Efforts {
						if effort == cfg.Defaults.Effort {
							found = true
						}
					}
				}
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("comment_execution default model or effort is not in verified models")
		}
	}
	if len(cfg.Models) > 0 && cfg.Verification.Source == "" {
		return nil, fmt.Errorf("comment_execution models require verification")
	}
	return cfg, nil
}

func strictYAMLMap(node *yaml.Node, path string, allowed map[string]bool) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s must be a mapping", path)
	}
	result := make(map[string]*yaml.Node)
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("%s has duplicate field %q", path, key)
		}
		if allowed != nil && !allowed[key] {
			return nil, fmt.Errorf("%s has unknown field %q", path, key)
		}
		result[key] = node.Content[i+1]
	}
	return result, nil
}

func yamlString(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.TrimSpace(node.Value) == "" || strings.TrimSpace(node.Value) != node.Value {
		return "", fmt.Errorf("%s must be a nonempty string", path)
	}
	return node.Value, nil
}
