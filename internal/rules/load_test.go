package rules

import (
	"path/filepath"
	"testing"
)

func TestLoadRulesFile(t *testing.T) {
	cfg, err := LoadFile(filepath.Join("..", "..", "testdata", "rules", "valid.yml"))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "board1", cfg.BoardID)
	assertEqual(t, "BotName", cfg.AgentName)
	assertEqual(t, "https://example.test", cfg.APIURL)
	assertEqual(t, "codex", cfg.Executor)
	assertEqual(t, 2, len(cfg.Rules))
	assertEqual(t, "Explore", cfg.Rules[0].Name)
	assertEqual(t, "card_created", cfg.Rules[0].Events[0])
	assertEqual(t, "card_moved", cfg.Rules[0].Events[1])
	assertEqual(t, "claude-sonnet-4-5-20250929", cfg.Rules[0].ModelID())
	assertEqual(t, false, cfg.Rules[0].IsStop())
	assertEqual(t, true, cfg.Rules[1].IsStop())
	assertEqual(t, 1, len(cfg.Schedules))
	assertEqual(t, "Daily Summary", cfg.Schedules[0].Name)
	assertEqual(t, "claude-haiku-4-5-20251001", cfg.Schedules[0].ModelID())
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
