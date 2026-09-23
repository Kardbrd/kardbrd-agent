package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMentionPreparationFailureRepliesAndReleasesCard(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	m.Token = "private-board-token"
	w := &fakeLifecycleWorktree{fakeWorktree: &fakeWorktree{}, prepareErr: errors.New("migration failed private-board-token")}
	m.Worktree = w
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder help", "Andy"); err == nil {
		t.Fatal("expected preparation failure")
	}
	assertEqual(t, 1, len(client.comments))
	message := client.comments[0].content
	if !strings.Contains(message, "preparing the workspace") || !strings.Contains(message, "@Andy") || strings.Contains(message, m.Token) {
		t.Fatalf("unexpected failure reply: %s", message)
	}
	assertEqual(t, "🛑", client.reactions[len(client.reactions)-1].emoji)
	assertEqual(t, 0, len(m.ActiveCardIDs()))
	assertEqual(t, 0, m.Executor.(*fakeExecutor).executionCount())
	// A repaired workspace must accept the next comment.
	w.prepareErr = nil
	if err := m.ProcessMention(context.Background(), "card1", "comment2", "@coder retry", "Andy"); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, m.Executor.(*fakeExecutor).executionCount())
}

func TestMentionCardReadFailureReplies(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	client.markdownErr = errors.New("card service unavailable")
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder help", "Andy"); err == nil {
		t.Fatal("expected card read failure")
	}
	assertEqual(t, 1, len(client.comments))
	if !strings.Contains(client.comments[0].content, "loading the card") {
		t.Fatal(client.comments[0].content)
	}
}

func TestQueuedMentionPreparationFailureReplies(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	m.Worktree = &fakeLifecycleWorktree{fakeWorktree: &fakeWorktree{}, prepareErr: errors.New("migration failed")}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	active := &ActiveSession{CardID: "card1", Cancel: cancel, Done: make(chan struct{})}
	m.Active["card1"] = active
	if err := m.ProcessMention(context.Background(), "card1", "queued", "@coder help", "Andy"); err != nil {
		t.Fatal(err)
	}
	m.finishActiveSession(active)
	waitFor(t, time.Second, func() bool { return len(m.ActiveCardIDs()) == 0 })
	client.mu.Lock()
	defer client.mu.Unlock()
	assertEqual(t, 1, len(client.comments))
	assertEqual(t, "queued", client.reactions[len(client.reactions)-1].commentID)
	assertEqual(t, "🛑", client.reactions[len(client.reactions)-1].emoji)
}

func TestMentionDeadlineRepliesAfterExecutionContextExpires(t *testing.T) {
	m := newTestManager(t)
	m.Timeout = 10 * time.Millisecond
	m.Executor.(*fakeExecutor).blockUntilCancel = true
	client := m.Client.(*fakeBoardClient)
	client.onAddComment = func(_, content string) {
		if !strings.Contains(content, "timed out") {
			t.Errorf("unexpected reply: %s", content)
		}
	}
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder help", "Andy"); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, len(client.comments))
	assertEqual(t, "🛑", client.reactions[len(client.reactions)-1].emoji)
}
