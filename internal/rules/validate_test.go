package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRulesFileCollectsErrorsAndWarnings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yml")
	writeFile(t, path, `
board_id: board1
agent: Bot
unknown_top: value
rules:
  - event: card_created
    action: /ke
  - name: Bad Event
    event: not_real
    action: /ke
    unknown_field: value
  - name: Bad Assignee
    event: card_created
    action: /ke
    assignee: user1
schedules:
  - name: Bad Schedule
    cron: "* * *"
    action: summarize
`)

	result := ValidateFile(path)
	if result.IsValid() {
		t.Fatal("expected invalid result")
	}
	assertIssueContains(t, result.Errors, "Missing required field 'name'")
	assertIssueContains(t, result.Errors, "assignee must be a YAML list")
	assertIssueContains(t, result.Errors, "invalid cron expression")
	assertIssueContains(t, result.Warnings, "unknown top-level field 'unknown_top'")
	assertIssueContains(t, result.Warnings, "unknown field 'unknown_field'")
	assertIssueContains(t, result.Warnings, "unknown event 'not_real'")
}

func TestValidateRulesFileMissingAndEmpty(t *testing.T) {
	missing := ValidateFile(filepath.Join(t.TempDir(), "missing.yml"))
	assertIssueContains(t, missing.Errors, "File not found")

	emptyPath := filepath.Join(t.TempDir(), "empty.yml")
	writeFile(t, emptyPath, "")
	empty := ValidateFile(emptyPath)
	assertIssueContains(t, empty.Errors, "File is empty")
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertIssueContains(t *testing.T, issues []ValidationIssue, text string) {
	t.Helper()
	for _, issue := range issues {
		if strings.Contains(issue.Message, text) {
			return
		}
	}
	t.Fatalf("expected issue containing %q, got %#v", text, issues)
}
