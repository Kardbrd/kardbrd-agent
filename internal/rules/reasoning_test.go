package rules

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledMBPBotRulesUseRequestedHighEffort(t *testing.T) {
	path := filepath.Join("..", "..", "kardbrd.yml")
	cfg, err := LoadValidatedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentName != "MBPBot" {
		t.Fatalf("bundled agent = %q, want MBPBot", cfg.AgentName)
	}
	want := map[string]string{
		"Explore and plan new cards":       "gpt-5.6-terra",
		"Implement on move to In Progress": "gpt-5.6-terra",
		"Ship card via approval":           "gpt-5.6-terra",
	}
	for _, rule := range cfg.Rules {
		model, ok := want[rule.Name]
		if !ok {
			continue
		}
		if rule.Model != model || rule.Reasoning != "high" {
			t.Errorf("rule %q has model=%q reasoning=%q; want model=%q reasoning=high", rule.Name, rule.Model, rule.Reasoning, model)
		}
		delete(want, rule.Name)
	}
	for name := range want {
		t.Errorf("bundled MBPBot rule %q is missing", name)
	}
}

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
