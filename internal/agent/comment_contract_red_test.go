package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
)

func TestREDManagerRejectsOverlongTerminalWithoutClipping(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	m.Executor.(*fakeExecutor).result = executor.Result{Success: true, ResultText: strings.Repeat("evidence ", 250)}
	if err := m.ProcessMention(context.Background(), "card1", "trigger", "@coder investigate", "Paul"); err != nil {
		t.Fatal(err)
	}
	for _, comment := range client.comments {
		if strings.Contains(comment.content, strings.Repeat("evidence ", 100)) || strings.Contains(comment.content, "output truncated") {
			t.Fatalf("published overlong or clipped terminal draft: %.100q", comment.content)
		}
	}
	if len(client.comments) == 0 {
		t.Fatal("expected a concise, actionable failure notice after bounded revision")
	}
}

func TestREDOverlongRevisionMustStayBoundedAndNonempty(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision string
	}{
		{"empty", "  \n"},
		{"still overlong", strings.Repeat("revised ", 251)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			client := m.Client.(*fakeBoardClient)
			exec := m.Executor.(*fakeExecutor)
			exec.results = []executor.Result{
				{Success: true, SessionID: "session1", ResultText: strings.Repeat("draft ", 250)},
				{Success: true, ResultText: tc.revision},
			}
			if err := m.ProcessMention(context.Background(), "card1", "trigger", "@coder investigate", "Paul"); err != nil {
				t.Fatal(err)
			}
			if exec.executionCount() != 2 {
				t.Fatalf("expected one revision, got %d executions", exec.executionCount())
			}
			if len(client.comments) != 1 || strings.Contains(client.comments[0].content, "draft draft") ||
				strings.Contains(client.comments[0].content, "revised revised") || strings.Contains(client.comments[0].content, "output truncated") {
				t.Fatalf("expected one concise failure notice, got %#v", client.comments)
			}
		})
	}
}

func TestREDRecentProgressDoesNotSuppressTerminalOutcome(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	client.card = rawJSON(t, map[string]any{"comments": []any{map[string]any{
		"author": map[string]any{"is_bot": true},
	}}})
	m.Executor.(*fakeExecutor).result = executor.Result{Success: true, ResultText: "Decision: ready for review. Evidence: tests passed."}
	if err := m.ProcessMention(context.Background(), "card1", "trigger", "@coder investigate", "Paul"); err != nil {
		t.Fatal(err)
	}
	if len(client.comments) != 1 || !strings.Contains(client.comments[0].content, "ready for review") {
		t.Fatalf("terminal outcome suppressed by unrelated progress: %#v", client.comments)
	}
}

func TestREDPublicationFailureIsNotMarkedSuccessful(t *testing.T) {
	m := newTestManager(t)
	client := m.Client.(*fakeBoardClient)
	client.addCommentErrors = []error{errors.New("response lost after write")}
	m.Executor.(*fakeExecutor).result = executor.Result{Success: true, ResultText: "Decision: retry safely after checking the card."}
	if err := m.ProcessMention(context.Background(), "card1", "trigger", "@coder investigate", "Paul"); err != nil {
		t.Fatal(err)
	}
	if len(client.commentAttempts) != 1 {
		t.Fatalf("uncertain write must not trigger another comment; got %d attempts", len(client.commentAttempts))
	}
	for _, reaction := range client.reactions {
		if reaction.emoji == "✅" {
			t.Fatal("uncertain publication must not be marked successful")
		}
	}
}
