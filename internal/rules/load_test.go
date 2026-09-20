package rules

import (
	"os"
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

func TestLoadScheduleCardID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixed-card.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
schedules:
  - name: Renamed watcher
    card_id: card-fixed
    cron: "0 * * * *"
    action: inspect
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, len(cfg.Schedules))
	assertEqual(t, "card-fixed", cfg.Schedules[0].CardID)
}

func TestLoadSchedulePublishResultDefaultsToTrueAndAcceptsFalse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "publish-result.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
schedules:
  - name: Legacy schedule
    cron: "0 * * * *"
    action: inspect
  - name: Script-owned schedule
    cron: "15 * * * *"
    action: inspect
    publish_result: false
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, true, cfg.Schedules[0].PublishesResult())
	assertEqual(t, false, cfg.Schedules[1].PublishesResult())
}

func TestLoadDoneCleanupCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanup-command.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
rules:
  - name: Retire preview
    event: card_moved
    list: Done
    cleanup_command:
      - /usr/local/bin/retire-preview
      - --quiet
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, len(cfg.Rules))
	assertEqual(t, true, cfg.Rules[0].IsCleanup())
	assertEqual(t, "/usr/local/bin/retire-preview", cfg.Rules[0].CleanupCommand[0])
	assertEqual(t, "--quiet", cfg.Rules[0].CleanupCommand[1])
}

func TestLoadDoneCleanupCommandRejectsNonStringArgument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-cleanup-command.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
rules:
  - name: Retire preview
    event: card_moved
    list: Done
    cleanup_command:
      - /usr/local/bin/retire-preview
      - true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected non-string cleanup argument to be rejected")
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
