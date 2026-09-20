package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	stream := &fakeStream{}
	manager.Active["card1"] = &ActiveSession{CardID: "card1", Stream: stream}

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
	assertEqual(t, true, stream.closed)
}

func TestDoneCleanupRunsBeforeWorktreeRemovalAndSuppressesRegularDoneRules(t *testing.T) {
	manager := newTestManager(t)
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{
		"list": map[string]any{"name": "Done"},
	})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{
		{
			Name:           "Retire preview",
			Events:         []string{"card_moved"},
			List:           "Done",
			CleanupCommand: cleanupHelperCommand(outputFile, "success"),
		},
		{
			Name:   "Legacy Done automation",
			Events: []string{"card_moved"},
			List:   "Done",
			Action: "/implement",
		},
	}}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "card_moved",
		"card_id":    "card1",
		"list_name":  "Done",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("cleanup command did not run: %v", err)
	}
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
	assertEqual(t, 0, manager.Executor.(*fakeExecutor).executionCount())
}

func TestReserveCleanupCancelsActiveSessionAndDiscardsPendingWork(t *testing.T) {
	manager := newTestManager(t)
	cancelled := make(chan struct{})
	activeStream := &fakeStream{}
	manager.Active["card1"] = &ActiveSession{
		CardID: "card1",
		Cancel: func() { close(cancelled) },
		Stream: activeStream,
	}
	manager.pending["card1"] = pendingMention{cardID: "card1", commentID: "follow-up"}

	cleanup := manager.reserveCleanup(context.Background(), "card1")

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("active session was not cancelled")
	}
	assertEqual(t, true, activeStream.closed)
	assertEqual(t, true, cleanup.Cleanup)
	if manager.Active["card1"] != cleanup {
		t.Fatal("cleanup did not retain ownership of the card")
	}
	assertEqual(t, 0, len(manager.pending))
}

func TestRunCleanupCommandPreservesSourceAndPassesCanonicalCardID(t *testing.T) {
	source := t.TempDir()
	sourceFile := filepath.Join(source, "source.txt")
	if err := os.WriteFile(sourceFile, []byte("dirty source remains"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager := newTestManager(t)
	manager.CWD = source
	manager.Token = "tok_secret"
	t.Setenv("KARDBRD_TOKEN", "environment_secret")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{
		"list": map[string]any{"name": "Done"},
	})
	rule := rules.Rule{
		Name:           "Retire preview",
		List:           "Done",
		CleanupCommand: cleanupHelperCommand(outputFile, "success"),
	}

	if err := manager.runCleanup(context.Background(), "card-canonical", rule); err != nil {
		t.Fatal(err)
	}

	gotSource, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "dirty source remains", string(gotSource))
	output, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "card-canonical|card-canonical|", string(output))
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
	assertEqual(t, 0, manager.Executor.(*fakeExecutor).executionCount())
}

func cleanupHelperCommand(outputPath, mode string) []string {
	return []string{os.Args[0], "-test.run=^TestCleanupCommandHelper$", "--", outputPath, mode}
}

func TestCleanupCommandHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "--" {
			marker = i
			break
		}
	}
	if marker == -1 {
		return
	}
	args := os.Args[marker+1:]
	if len(args) != 3 {
		fmt.Fprint(os.Stderr, "invalid cleanup helper arguments")
		os.Exit(2)
	}
	if err := os.WriteFile(args[0], []byte(strings.Join([]string{args[2], os.Getenv("KARDBRD_CARD_ID"), os.Getenv("KARDBRD_TOKEN")}, "|")), 0o600); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(2)
	}
	if args[1] == "fail" {
		fmt.Fprint(os.Stderr, "cleanup failed with tok_secret")
		os.Exit(7)
	}
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

func TestProcessRuleReservesCardBeforeWorktreeCreate(t *testing.T) {
	manager := newTestManager(t)
	worktrees := manager.Worktree.(*fakeWorktree)
	firstCreateStarted := make(chan struct{})
	releaseFirstCreate := make(chan struct{})
	var blockFirstCreate sync.Once
	worktrees.onCreate = func(string) {
		blockFirstCreate.Do(func() {
			close(firstCreateStarted)
			<-releaseFirstCreate
		})
	}
	rule := rules.Rule{Name: "Auto", Action: "summarize"}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- manager.ProcessRule(context.Background(), "card1", rule, nil)
	}()
	select {
	case <-firstCreateStarted:
	case <-time.After(time.Second):
		t.Fatal("first rule did not start worktree creation")
	}

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- manager.ProcessRule(context.Background(), "card1", rule, nil)
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second rule did not return while the card was reserved")
	}

	close(releaseFirstCreate)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first rule did not finish")
	}

	assertEqual(t, 1, manager.Executor.(*fakeExecutor).executionCount())
}
