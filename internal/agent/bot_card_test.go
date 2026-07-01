package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestBuildBotCardDescriptionIncludesRuntimeDetails(t *testing.T) {
	manager := newTestManager(t)
	manager.ExecutorType = "codex"
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "Explore", Events: []string{"card_created"}, Action: "/ke"}}}
	manager.Schedules = []rules.Schedule{{Name: "Daily", Cron: "0 9 * * 1-5", Action: "summarize"}}
	manager.StartTime = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

	description := manager.BuildBotCardDescription()
	assertContains(t, description, "| Agent | @coder |")
	assertContains(t, description, "| Executor | codex")
	assertContains(t, description, "| Board | `board1` |")
	assertContains(t, description, "| API | https://api.test |")
	assertContains(t, description, "| Timeout | 1h0m0s |")
	assertContains(t, description, "| Max concurrent | 3 |")
	assertContains(t, description, "| Rules | 1 |")
	assertContains(t, description, "| Schedules | 1 |")
	assertContains(t, description, "## Triggers")
	assertContains(t, description, "### Explore")
	assertContains(t, description, "## Schedules")
	assertContains(t, description, "### Daily")
}

func TestDiscoverSkillsUsesExecutorDirectories(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".agents", "skills", "ke"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(cwd, ".agents", "skills", "ke", "SKILL.md"), `---
name: Explore
description: Explore project context
---
# Ignored
`)
	if err := os.MkdirAll(filepath.Join(cwd, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeText(t, filepath.Join(cwd, ".codex", "skills", "ki.md"), "# Implement\n")

	manager := newTestManager(t)
	manager.CWD = cwd
	manager.ExecutorType = "codex"

	skills := manager.DiscoverSkills()
	assertEqual(t, 2, len(skills))
	assertEqual(t, "ke", skills[0].Command)
	assertEqual(t, "Explore", skills[0].Name)
	assertEqual(t, "Explore project context", skills[0].Description)
	assertEqual(t, "ki", skills[1].Command)
	assertEqual(t, "Implement", skills[1].Name)
}

func TestEnsureBotCardUpdatesExisting(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	client.board = rawJSON(t, map[string]any{
		"lists": []any{
			map[string]any{
				"id": "list1",
				"cards": []any{
					map[string]any{"id": "bot1", "title": "🤖 coder"},
				},
			},
		},
	})

	if err := manager.EnsureBotCard(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "bot1", manager.BotCardID)
	assertEqual(t, "bot1", client.updatedCardID)
	assertContains(t, client.updatedDescription, "| Agent | @coder |")
}

func TestEnsureWizardCardCreatesWhenRulesAreEmpty(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	client.board = rawJSON(t, map[string]any{
		"lists": []any{
			map[string]any{"id": "done", "name": "Done", "cards": []any{}},
			map[string]any{"id": "todo", "name": "Todo", "cards": []any{}},
		},
	})

	if err := manager.EnsureWizardCard(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "todo", client.createdListID)
	assertEqual(t, "Kardbrd.yml Workflow Generator", client.createdTitle)
	assertContains(t, client.comments[0].content, "@coder")
}

func writeText(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
