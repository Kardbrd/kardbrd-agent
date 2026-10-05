package rules

import (
	"strings"
	"testing"
)

func TestCommentExecutionConfig(t *testing.T) {
	base := "board_id: board1\nagent: coder\n"
	valid := `comment_execution:
  defaults:
    model: gpt-tested
    effort: high
  models:
    - id: gpt-tested
      efforts: [low, high]
  verification:
    source: operator_probe
    verified_at: "2026-10-05T16:00:00Z"
    executor_version: "codex 0.156.0"
`
	cfg, err := LoadValidatedBytes([]byte(base + valid))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CommentExecution == nil || cfg.CommentExecution.Defaults.Model != "gpt-tested" || cfg.CommentExecution.Defaults.Effort != "high" {
		t.Fatalf("comment defaults = %+v", cfg.CommentExecution)
	}

	for _, tc := range []struct{ name, yaml, want string }{
		{"unknown", "comment_execution:\n  surprise: true\n", "surprise"},
		{"null", "comment_execution: null\n", "comment_execution"},
		{"duplicate model", "comment_execution:\n  models:\n    - id: gpt-tested\n      efforts: [high]\n    - id: gpt-tested\n      efforts: [high]\n", "duplicate"},
		{"unknown effort", "comment_execution:\n  models:\n    - id: gpt-tested\n      efforts: [turbo]\n", "effort"},
		{"unlisted default", "comment_execution:\n  defaults:\n    model: other\n  models:\n    - id: gpt-tested\n      efforts: [high]\n", "default"},
		{"unverified", "comment_execution:\n  models:\n    - id: gpt-tested\n      efforts: [high]\n", "verification"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadValidatedBytes([]byte(base + tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
