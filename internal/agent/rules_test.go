package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func TestDoneCleanupPreservesCleanDirtyAndMissingGitWorktrees(t *testing.T) {
	for _, tt := range []struct {
		name          string
		dirtySource   bool
		worktree      bool
		dirtyWorktree bool
	}{
		{name: "clean source"},
		{name: "dirty source", dirtySource: true},
		{name: "clean worktree", worktree: true},
		{name: "dirty worktree", worktree: true, dirtyWorktree: true},
		{name: "missing worktree"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := newTemporaryGitRepo(t)
			sourceFile := filepath.Join(source, "source.txt")
			if tt.dirtySource {
				if err := os.WriteFile(sourceFile, []byte("dirty source"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			worktreePath := filepath.Join(t.TempDir(), "card-worktree")
			if tt.worktree {
				runGit(t, source, "worktree", "add", "--detach", "--quiet", worktreePath, "HEAD")
				if tt.dirtyWorktree {
					if err := os.WriteFile(filepath.Join(worktreePath, "source.txt"), []byte("dirty worktree"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			sourceBefore := readFile(t, sourceFile)
			worktreeBefore := ""
			if tt.worktree {
				worktreeBefore = readFile(t, filepath.Join(worktreePath, "source.txt"))
			}

			outputFile := filepath.Join(t.TempDir(), "cleanup-output")
			manager := newTestManager(t)
			manager.CWD = source
			manager.Worktree.(*fakeWorktree).path = worktreePath
			manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
			manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "success")}}

			if err := manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")); err != nil {
				t.Fatal(err)
			}

			assertEqual(t, sourceBefore, readFile(t, sourceFile))
			if tt.worktree {
				assertEqual(t, worktreeBefore, readFile(t, filepath.Join(worktreePath, "source.txt")))
			}
			worktrees := manager.Worktree.(*fakeWorktree)
			assertEqual(t, "", worktrees.createdCard)
			assertEqual(t, "", worktrees.removedCard)
		})
	}
}

func TestDoneCleanupRepeatedEventRunsIdempotentCommand(t *testing.T) {
	manager := newTestManager(t)
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "append")}}

	for range 2 {
		if err := manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")); err != nil {
			t.Fatal(err)
		}
	}

	assertEqual(t, "card1\ncard1\n", readFile(t, outputFile))
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
}

func TestDoneCleanupSkipsCardReopenedWhileWaitingForSlot(t *testing.T) {
	manager := newTestManager(t)
	manager.sem = make(chan struct{}, 1)
	manager.sem <- struct{}{}
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	client := manager.Client.(*fakeBoardClient)
	client.card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "success")}}

	done := make(chan error, 1)
	go func() { done <- manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")) }()
	waitForCleanupReservation(t, manager, "card1")
	client.card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
	<-manager.sem

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup did not finish after releasing the slot")
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Fatalf("cleanup ran after card reopened: %v", err)
	}
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
}

func TestDoneCleanupCancelsActiveWorkAndDropsPendingMention(t *testing.T) {
	manager := newTestManager(t)
	manager.sem = make(chan struct{}, 1)
	executor := &fakeExecutor{
		auth:             executor.AuthStatus{Authenticated: true},
		result:           executor.Result{Success: true, ResultText: "Done"},
		blockUntilCancel: true,
		started:          make(chan struct{}),
		cancelled:        make(chan struct{}),
	}
	manager.Executor = executor
	started := make(chan error, 1)
	go func() {
		started <- manager.ProcessRule(context.Background(), "card1", rules.Rule{Name: "Work", Action: "/implement"}, nil)
	}()
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("active work did not start")
	}
	manager.Worktree.(*fakeWorktree).createdCard = ""
	manager.queueMentionIfActive(context.Background(), "card1", "follow-up", "@coder continue", "Paul")
	outputFile := filepath.Join(t.TempDir(), "cleanup-output")
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(outputFile, "success")}}

	if err := manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-executor.cancelled:
	case <-time.After(time.Second):
		t.Fatal("active work was not cancelled")
	}
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled work did not finish")
	}
	assertEqual(t, "card1|card1|", readFile(t, outputFile))
	assertEqual(t, 1, executor.executionCount())
	assertEqual(t, 0, len(manager.pending))
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
}

func TestDoneCleanupReportsNonzeroCommandFailureWithoutSecrets(t *testing.T) {
	manager := newTestManager(t)
	manager.Token = "tok_secret"
	manager.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	manager.Rules = &rules.Engine{Rules: []rules.Rule{doneCleanupRule(filepath.Join(t.TempDir(), "cleanup-output"), "fail")}}

	err := manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1"))
	if err == nil {
		t.Fatal("expected cleanup command failure")
	}
	assertContains(t, err.Error(), "exit status 7")
	comments := manager.Client.(*fakeBoardClient).commentsSnapshot()
	assertEqual(t, 1, len(comments))
	assertContains(t, comments[0].content, "Cleanup Error")
	assertContains(t, comments[0].content, "[REDACTED]")
	assertNotContains(t, comments[0].content, "tok_secret")
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "", worktrees.createdCard)
	assertEqual(t, "", worktrees.removedCard)
}

func TestLimitedCleanupOutputBoundsCapturedBytes(t *testing.T) {
	output := newLimitedCleanupOutput(4)
	n, err := output.Write([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 6, n)
	assertEqual(t, "abcd\n... (output truncated)", output.String())
}

func TestDoneWithoutCleanupRetainsDefaultWorktreeLifecycle(t *testing.T) {
	manager := newTestManager(t)
	manager.Rules = &rules.Engine{Rules: []rules.Rule{{
		Name:   "Legacy Done automation",
		Events: []string{"card_moved"},
		List:   "Done",
		Action: "/implement",
	}}}

	if err := manager.HandleBoardEvent(context.Background(), doneCardMovedEvent("card1")); err != nil {
		t.Fatal(err)
	}
	worktrees := manager.Worktree.(*fakeWorktree)
	assertEqual(t, "card1", worktrees.removedCard)
	assertEqual(t, "card1", worktrees.createdCard)
	assertEqual(t, 1, manager.Executor.(*fakeExecutor).executionCount())
}

func doneCleanupRule(outputPath, mode string) rules.Rule {
	return rules.Rule{
		Name:           "Retire preview",
		Events:         []string{"card_moved"},
		List:           "Done",
		CleanupCommand: cleanupHelperCommand(outputPath, mode),
	}
}

func doneCardMovedEvent(cardID string) map[string]any {
	return map[string]any{"event_type": "card_moved", "card_id": cardID, "list_name": "Done"}
}

func newTemporaryGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("clean source"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "source.txt")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--quiet", "-m", "initial")
	return repo
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

func waitForCleanupReservation(t *testing.T, manager *Manager, cardID string) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		manager.mu.Lock()
		session := manager.Active[cardID]
		reserved := session != nil && session.Cleanup
		manager.mu.Unlock()
		if reserved {
			return
		}
		select {
		case <-deadline:
			t.Fatal("cleanup did not reserve the card")
		case <-ticker.C:
		}
	}
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
	if args[1] == "append" {
		file, err := os.OpenFile(args[0], os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(2)
		}
		_, err = fmt.Fprintln(file, args[2])
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			fmt.Fprint(os.Stderr, "unable to append cleanup output")
			os.Exit(2)
		}
		return
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
