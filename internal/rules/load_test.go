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

func TestLoadRulesKeepsExistingValidationScopeForUnrelatedSchedules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-schedule.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
schedules:
  - name: Legacy schedule
    cron: not-a-cron
    action: inspect
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile changed unrelated schedule behavior: %v", err)
	}
	assertEqual(t, "not-a-cron", cfg.Schedules[0].Cron)
}

func TestLoadDoneCleanupCommandRejectsEveryInvalidCleanupForm(t *testing.T) {
	for _, tt := range []struct {
		name string
		rule string
	}{
		{name: "empty argv", rule: "cleanup_command: []"},
		{name: "blank argv", rule: "cleanup_command:\n      - ''"},
		{name: "wrong event", rule: "event: card_created\n    cleanup_command:\n      - /usr/local/bin/retire-preview"},
		{name: "wrong list", rule: "list: In Progress\n    cleanup_command:\n      - /usr/local/bin/retire-preview"},
		{name: "action", rule: "action: /implement\n    cleanup_command:\n      - /usr/local/bin/retire-preview"},
		{name: "sudo", rule: "cleanup_command:\n      - sudo\n      - retire-preview"},
		{name: "shell wrapper", rule: "cleanup_command:\n      - /bin/sh\n      - -c\n      - retire-preview"},
		{name: "env sudo wrapper", rule: "cleanup_command:\n      - /usr/bin/env\n      - sudo\n      - retire-preview"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-cleanup-command.yml")
			content := "board_id: board1\nagent: BotName\nrules:\n  - name: Retire preview\n    event: card_moved\n    list: Done\n    " + tt.rule + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path); err == nil {
				t.Fatal("expected invalid cleanup command to be rejected")
			}
		})
	}
}

func TestLoadLifecycleNormalizesReviewedDefaultsAndCommandPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lifecycle.yml")
	if err := os.WriteFile(path, []byte(`
board_id: board1
agent: BotName
worktree:
  helpers:
    stable_root: /stable
  prepare:
    argv: [/stable/prepare]
rules:
  - name: Stop preview
    event: comment_created
    comment_command: /down
    execution: existing_or_base
    action: /down
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worktree == nil {
		t.Fatal("expected opt-in lifecycle configuration")
	}
	assertEqual(t, "origin", cfg.Worktree.Base.Remote)
	assertEqual(t, "", cfg.Worktree.Base.Ref)
	assertEqual(t, CheckoutFull, cfg.Worktree.Checkout.Mode)
	assertEqual(t, 900, cfg.Worktree.Prepare.TimeoutSeconds)
	assertEqual(t, true, cfg.Worktree.Prepare.OnCreate)
	assertEqual(t, false, cfg.Worktree.Prepare.OnReuse)
	assertEqual(t, SharingEnvDisabled, cfg.Worktree.Sharing.Env)
	assertEqual(t, SharingSkillsFallback, cfg.Worktree.Sharing.Skills)
	assertEqual(t, "/down", cfg.Rules[0].CommentCommand)
	assertEqual(t, ExecutionExistingOrBase, cfg.Rules[0].Execution)
}

func TestLoadLifecycleRejectsNullUnknownAndCoercedFields(t *testing.T) {
	for _, lifecycle := range []string{
		"worktree: null",
		"worktree:\n  unknown: value",
		"worktree:\n  checkout:\n    mode: delegated",
		"worktree:\n  prepare:\n    argv: [/stable/prepare]\n    on_create: 'true'",
	} {
		t.Run(lifecycle, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-lifecycle.yml")
			content := "board_id: board1\nagent: BotName\n" + lifecycle + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path); err == nil {
				t.Fatal("expected strict lifecycle load failure")
			}
		})
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
