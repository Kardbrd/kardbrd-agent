package rules

import (
	"strings"
	"testing"
)

func TestRuleAndScheduleReasoningLoadsAndValidates(t *testing.T) {
	data := []byte("board_id: board1\nagent: Bot\nrules:\n  - name: Explore\n    event: card_created\n    action: inspect\n    model: gpt-6.1-sol\n    reasoning: high\nschedules:\n  - name: Daily\n    cron: '0 * * * *'\n    action: inspect\n    reasoning: xhigh\n")
	if validation := ValidateBytes(data); !validation.IsValid() {
		t.Fatalf("validation errors: %+v", validation.Errors)
	}
	cfg, err := LoadValidatedBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Rules[0].Reasoning != "high" || cfg.Schedules[0].Reasoning != "xhigh" {
		t.Fatalf("selection lost: %+v", cfg)
	}
}

func TestRuleReasoningRejectsUnsupportedValues(t *testing.T) {
	data := []byte("board_id: board1\nagent: Bot\nrules:\n  - name: Bad\n    event: card_created\n    action: inspect\n    reasoning: none\n")
	if validation := ValidateBytes(data); validation.IsValid() {
		t.Fatal("invalid reasoning passed validation")
	}
	if _, err := LoadBytes(data); err == nil || !strings.Contains(err.Error(), "unsupported reasoning effort") {
		t.Fatalf("load error = %v", err)
	}
}
