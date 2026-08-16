package agent

import (
	"context"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestRuleDispatchFetchesLabelsWhenMissing(t *testing.T) {
	manager := newTestManager(t)
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{
		Name:         "Needs Label",
		Events:       []string{"card_moved"},
		RequireLabel: "Ready",
		Action:       "summarize",
	}}}
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{
		"comments": []any{},
		"labels":   []any{map[string]any{"name": "Ready"}},
	})

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "card_moved",
		"card_id":    "card1",
		"list_name":  "Doing",
	}); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 1, manager.Executor.(*fakeExecutor).executeCount)
}

func TestStopReactionRemovesActiveSessionAndPostsConfirmation(t *testing.T) {
	manager := newTestManager(t)
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{
		Name:   "Stop",
		Events: []string{"reaction_added"},
		Emoji:  "🛑",
		Action: rules.StopAction,
	}}}
	manager.Active["card1"] = &ActiveSession{CardID: "card1", CommentID: "comment1"}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "reaction_added",
		"card_id":    "card1",
		"comment_id": "comment1",
		"emoji":      "🛑",
	}); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 0, len(manager.Active))
	assertContains(t, manager.Client.(*fakeBoardClient).comments[0].content, "Agent stopped")
}

func TestCardMovedToDoneRemovesWorktree(t *testing.T) {
	manager := newTestManager(t)
	manager.Active["card1"] = &ActiveSession{CardID: "card1"}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "card_moved",
		"card_id":    "card1",
		"list_name":  "Done",
	}); err != nil {
		t.Fatal(err)
	}

	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "card1", worktrees.removedCard)
	assertEqual(t, false, worktrees.forced)
}

func TestRuleDispatchPostsAuthError(t *testing.T) {
	manager := newTestManager(t)
	manager.Executor = &fakeExecutor{auth: executor.AuthStatus{Authenticated: false, Error: "login required"}}
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{
		Name:   "Auto",
		Events: []string{"card_created"},
		Action: "summarize",
	}}}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "card_created",
		"card_id":    "card1",
	}); err != nil {
		t.Fatal(err)
	}

	assertContains(t, manager.Client.(*fakeBoardClient).comments[0].content, "login required")
}

func TestRuleDispatchPublishesTerminalSummaryWithoutResume(t *testing.T) {
	manager := newTestManager(t)
	manager.Executor.(*fakeExecutor).result = executor.Result{
		Success:    true,
		ResultText: "automation terminal response",
		SessionID:  "session1",
	}
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{
		Name:   "Auto",
		Events: []string{"card_created"},
		Action: "summarize",
	}}}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "card_created",
		"card_id":    "card1",
	}); err != nil {
		t.Fatal(err)
	}

	exec := manager.Executor.(*fakeExecutor)
	assertEqual(t, 1, exec.executionCount())
	comments := manager.Client.(*fakeBoardClient).commentsSnapshot()
	assertEqual(t, 1, len(comments))
	assertContains(t, comments[0].content, "automation terminal response")
	assertContains(t, comments[0].content, "@automation")
}
