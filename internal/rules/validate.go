package rules

import (
	"fmt"
	"os"
	"strings"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v3"
)

type Severity string

const (
	ErrorSeverity   Severity = "error"
	WarningSeverity Severity = "warning"
)

type ValidationIssue struct {
	Severity  Severity
	RuleIndex *int
	RuleName  string
	Message   string
}

type ValidationResult struct {
	Errors   []ValidationIssue
	Warnings []ValidationIssue
}

func (r ValidationResult) IsValid() bool {
	return len(r.Errors) == 0
}

var knownEvents = set(
	"card_created", "card_moved", "card_archived", "card_unarchived", "card_deleted",
	"comment_created", "comment_deleted", "reaction_added",
	"checklist_created", "checklist_deleted",
	"todo_item_created", "todo_item_completed", "todo_item_reopened", "todo_item_deleted", "todo_item_assigned", "todo_item_unassigned",
	"attachment_created", "attachment_deleted",
	"card_link_created", "card_link_deleted",
	"label_added", "label_removed",
	"list_created", "list_deleted",
)

var knownTopFields = set("board_id", "agent", "api_url", "executor", "rules", "schedules")
var knownRuleFields = set("name", "event", "action", "model", "list", "title", "label", "content_contains", "exclude_label", "require_label", "emoji", "require_user", "assignee", "comment_author")
var knownScheduleFields = set("name", "card_id", "cron", "action", "model", "assignee", "list", "publish_result")

func ValidateFile(path string) ValidationResult {
	var result ValidationResult
	data, err := os.ReadFile(path)
	if err != nil {
		result.addError("File not found: " + path)
		return result
	}
	if strings.TrimSpace(string(data)) == "" {
		result.addError("File is empty")
		return result
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		result.addError("Invalid YAML syntax: " + err.Error())
		return result
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		result.addError(fmt.Sprintf("File must be a YAML dict, got %s", doc.ShortTag()))
		return result
	}

	top := mapping(doc)
	for key := range top {
		if !knownTopFields[key] {
			result.addWarning("unknown top-level field '" + key + "'")
		}
	}
	if scalar(top["board_id"]) == "" {
		result.addError("Missing required field 'board_id'")
	}
	if scalar(top["agent"]) == "" {
		result.addError("Missing required field 'agent'")
	}

	if rulesNode, ok := top["rules"]; ok {
		validateRulesNode(&result, rulesNode)
	}
	if schedulesNode, ok := top["schedules"]; ok {
		validateSchedulesNode(&result, schedulesNode)
	}
	return result
}

func validateRulesNode(result *ValidationResult, node *yaml.Node) {
	if node.Kind != yaml.SequenceNode {
		result.addError("'rules' must be a list")
		return
	}
	for i, entry := range node.Content {
		if entry.Kind != yaml.MappingNode {
			result.addRuleError(i, "", "Rule must be a mapping")
			continue
		}
		fields := mapping(entry)
		name := scalar(fields["name"])
		for key := range fields {
			if !knownRuleFields[key] {
				result.addRuleWarning(i, name, "unknown field '"+key+"'")
			}
		}
		if name == "" {
			result.addRuleError(i, name, "Missing required field 'name'")
		}
		events := parseEventNode(fields["event"])
		if len(events) == 0 {
			result.addRuleError(i, name, "Missing required field 'event'")
		}
		for _, event := range events {
			if !knownEvents[event] {
				result.addRuleWarning(i, name, "unknown event '"+event+"'")
			}
		}
		if scalar(fields["action"]) == "" {
			result.addRuleError(i, name, "Missing required field 'action'")
		}
		if assignee, ok := fields["assignee"]; ok && assignee.Kind != yaml.SequenceNode {
			result.addRuleError(i, name, "assignee must be a YAML list")
		}
	}
}

func validateSchedulesNode(result *ValidationResult, node *yaml.Node) {
	if node.Kind != yaml.SequenceNode {
		result.addError("'schedules' must be a list")
		return
	}
	for i, entry := range node.Content {
		if entry.Kind != yaml.MappingNode {
			result.addRuleError(i, "", "Schedule must be a mapping")
			continue
		}
		fields := mapping(entry)
		name := scalar(fields["name"])
		for key := range fields {
			if !knownScheduleFields[key] {
				result.addRuleWarning(i, name, "unknown schedule field '"+key+"'")
			}
		}
		if name == "" {
			result.addRuleError(i, name, "Schedule missing required field 'name'")
		}
		if scalar(fields["action"]) == "" {
			result.addRuleError(i, name, "Schedule missing required field 'action'")
		}
		cron := scalar(fields["cron"])
		if cron == "" {
			result.addRuleError(i, name, "Schedule missing required field 'cron'")
		} else if !validCron(cron) {
			result.addRuleError(i, name, "invalid cron expression '"+cron+"'")
		}
		if publishResult, ok := fields["publish_result"]; ok && publishResult.Tag != "!!bool" {
			result.addRuleError(i, name, "publish_result must be a boolean")
		}
	}
}

func parseEventNode(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.ScalarNode {
		return []string{node.Value}
	}
	if node.Kind == yaml.SequenceNode {
		events := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			if item.Kind == yaml.ScalarNode {
				events = append(events, item.Value)
			}
		}
		return events
	}
	return nil
}

func validCron(expr string) bool {
	_, err := standardCronParser().Parse(expr)
	return err == nil
}

func standardCronParser() cron.Parser {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
}

func mapping(node *yaml.Node) map[string]*yaml.Node {
	out := make(map[string]*yaml.Node)
	for i := 0; i+1 < len(node.Content); i += 2 {
		out[node.Content[i].Value] = node.Content[i+1]
	}
	return out
}

func scalar(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}

func (r *ValidationResult) addError(message string) {
	r.Errors = append(r.Errors, ValidationIssue{Severity: ErrorSeverity, Message: message})
}

func (r *ValidationResult) addWarning(message string) {
	r.Warnings = append(r.Warnings, ValidationIssue{Severity: WarningSeverity, Message: message})
}

func (r *ValidationResult) addRuleError(index int, name string, message string) {
	r.Errors = append(r.Errors, ValidationIssue{Severity: ErrorSeverity, RuleIndex: &index, RuleName: name, Message: message})
}

func (r *ValidationResult) addRuleWarning(index int, name string, message string) {
	r.Warnings = append(r.Warnings, ValidationIssue{Severity: WarningSeverity, RuleIndex: &index, RuleName: name, Message: message})
}

func set(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
