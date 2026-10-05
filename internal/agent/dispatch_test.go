package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestParseMentionDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, input, content, model, effort, errorText string
	}{
		{"plain", "@coder Fix model=foo exactly", "@coder Fix model=foo exactly", "", "", ""},
		{"selected", "@CoDeR [dispatch model=gpt-6.1-sol effort=high]\nFix this.\n> @coder [dispatch model=bad effort=low]", "@CoDeR\nFix this.\n> @coder [dispatch model=bad effort=low]", "gpt-6.1-sol", "high", ""},
		{"quoted", "> @coder [dispatch model=bad effort=low]\n@coder explain", "> @coder [dispatch model=bad effort=low]\n@coder explain", "", "", ""},
		{"duplicate", "@coder [dispatch model=gpt-6.1-sol model=gpt-6-sol effort=high]\nTask", "", "", "", "duplicate model"},
		{"unknown", "@coder [dispatch model=gpt-6.1-sol effort=high region=us]\nTask", "", "", "", "unknown dispatch field"},
		{"unsupported", "@coder [dispatch model=made-up effort=high]\nTask", "", "", "", "unsupported model"},
		{"bad effort", "@coder [dispatch model=gpt-6.1-sol effort=none]\nTask", "", "", "", "unsupported effort"},
		{"missing", "@coder [dispatch model=gpt-6.1-sol]\nTask", "", "", "", "requires model and effort"},
		{"unclosed", "@coder [dispatch model=gpt-6.1-sol effort=high\nTask", "", "", "", "close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMentionDispatch(tc.input, "@coder")
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Content != tc.content || got.Model != tc.model || got.ReasoningEffort != tc.effort {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestMentionDispatchSelectionAndRejection(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	content := "@coder [dispatch model=gpt-6.1-sol effort=high]\nKeep model=xhigh literal."
	if err := m.ProcessMention(context.Background(), "card1", "comment1", content, "Paul"); err != nil {
		t.Fatal(err)
	}
	e := m.Executor.(*fakeExecutor)
	if e.lastPromptRequest.CommentContent != "@coder\nKeep model=xhigh literal." {
		t.Fatalf("prompt content = %q", e.lastPromptRequest.CommentContent)
	}
	if e.lastPromptRequest.Command != "Keep model=xhigh literal." {
		t.Fatalf("command text = %q", e.lastPromptRequest.Command)
	}
	if e.lastExecuteRequest.Model != "gpt-6.1-sol" || e.lastExecuteRequest.ReasoningEffort != "high" {
		t.Fatalf("request = %+v", e.lastExecuteRequest)
	}
	if err := m.ProcessMention(context.Background(), "card2", "comment2", "@coder [dispatch model=bad effort=high]\nTask", "Paul"); err != nil {
		t.Fatal(err)
	}
	if e.executeCount != 1 {
		t.Fatalf("invalid selection dispatched")
	}
	if !strings.Contains(m.Client.(*fakeBoardClient).comments[1].content, "unsupported model") {
		t.Fatalf("no actionable error")
	}
}

func TestRuleReasoningAndResumeKeepSelection(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	e := m.Executor.(*fakeExecutor)
	e.result = executor.Result{Success: true, SessionID: "session1"}
	if err := m.ProcessRule(context.Background(), "card1", rules.Rule{Name: "test", Action: "inspect", Model: "gpt-6.1-sol", Reasoning: "high"}, nil); err != nil {
		t.Fatal(err)
	}
	if e.executeCount != 2 {
		t.Fatalf("expected resume, got %d calls", e.executeCount)
	}
	if e.lastExecuteRequest.Model != "gpt-6.1-sol" || e.lastExecuteRequest.ReasoningEffort != "high" || e.lastExecuteRequest.ResumeSessionID != "session1" {
		t.Fatalf("resume = %+v", e.lastExecuteRequest)
	}
	if e.requests[0].Model != e.requests[1].Model || e.requests[0].ReasoningEffort != e.requests[1].ReasoningEffort {
		t.Fatalf("selection changed on resume: %+v", e.requests)
	}
}

func TestScheduleReasoningReachesExecutor(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	if err := m.ProcessSchedule(context.Background(), "card1", rules.Schedule{Name: "daily", Action: "inspect", Model: "gpt-6.1-sol", Reasoning: "high"}); err != nil {
		t.Fatal(err)
	}
	req := m.Executor.(*fakeExecutor).lastExecuteRequest
	if req.Model != "gpt-6.1-sol" || req.ReasoningEffort != "high" {
		t.Fatalf("schedule request = %+v", req)
	}
}

func TestUnsupportedRuleEffortIsVisibleWithoutRunning(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "goose"
	if err := m.ProcessRule(context.Background(), "card1", rules.Rule{Name: "bad", Action: "inspect", Reasoning: "high"}, nil); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("unsupported rule ran")
	}
	if !strings.Contains(m.Client.(*fakeBoardClient).commentsSnapshot()[0].content, "reasoning effort requires") {
		t.Fatal("missing selection error")
	}
}

func TestMentionResumeKeepsSelection(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	e := m.Executor.(*fakeExecutor)
	e.results = []executor.Result{{Success: true, SessionID: "thread-1"}, {Success: true, ResultText: "summary"}}
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder [dispatch model=gpt-6.1-sol effort=high]\nTask", "Paul"); err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 2 {
		t.Fatalf("requests = %d", len(e.requests))
	}
	if e.requests[1].ResumeSessionID != "thread-1" || e.requests[1].Model != "gpt-6.1-sol" || e.requests[1].ReasoningEffort != "high" {
		t.Fatalf("resume = %+v", e.requests[1])
	}
}

func TestQueuedFollowUpKeepsOwnSelection(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- m.ProcessMention(context.Background(), "card1", "comment1", "@coder first", "Paul")
	}()
	select {
	case <-e.started:
	case <-time.After(time.Second):
		t.Fatal("first execution did not start")
	}
	if err := m.ProcessMention(context.Background(), "card1", "comment2", "@coder [dispatch model=gpt-6.1-sol effort=high]\nFollow up", "Paul"); err != nil {
		t.Fatal(err)
	}
	if e.executionCount() != 1 {
		t.Fatal("follow-up ran before active session finished")
	}
	close(e.blockUntilRelease)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first execution did not finish")
	}
	waitFor(t, time.Second, func() bool { return e.executionCount() == 2 })
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.requests[0].Model != "" || e.requests[1].Model != "gpt-6.1-sol" || e.requests[1].ReasoningEffort != "high" {
		t.Fatalf("queued selection = %+v", e.requests)
	}
}

func TestMentionSelectionWaitsInQueueAndDoesNotLeak(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	for i := 0; i < cap(m.sem); i++ {
		m.sem <- struct{}{}
	}
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		done <- m.ProcessMention(context.Background(), "card1", "comment1", "@coder [dispatch model=gpt-6.1-sol effort=high]\nWork", "Paul")
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("queued request finished before slot released: %v", err)
	default:
	}
	<-m.sem
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for i := 1; i < cap(m.sem); i++ {
		<-m.sem
	}
	if err := m.ProcessMention(context.Background(), "card2", "comment2", "@coder plain request", "Paul"); err != nil {
		t.Fatal(err)
	}
	e := m.Executor.(*fakeExecutor)
	if len(e.requests) != 2 {
		t.Fatalf("requests = %d", len(e.requests))
	}
	if e.requests[0].Model != "gpt-6.1-sol" || e.requests[0].ReasoningEffort != "high" {
		t.Fatalf("queued request = %+v", e.requests[0])
	}
	if e.requests[1].Model != "" || e.requests[1].ReasoningEffort != "" {
		t.Fatalf("selection leaked: %+v", e.requests[1])
	}
}

func TestSelectedExecutorFailureIsReported(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.Executor.(*fakeExecutor).result = executor.Result{Success: false, Error: "model not supported by account"}
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder [dispatch model=gpt-6.1-sol effort=high]\nWork", "Paul"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Client.(*fakeBoardClient).comments[0].content, "model not supported by account") {
		t.Fatal("provider failure was hidden")
	}
}

func TestAddressedCommentTakesPrecedenceOverCommentRule(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "All comments", Events: []string{"comment_created"}, Action: "extra"}}}
	if err := m.HandleBoardEvent(context.Background(), map[string]any{"event_type": "comment_created", "card_id": "card1", "comment_id": "comment1", "content": "@coder [dispatch model=gpt-6.1-sol effort=high]\nWork", "author_name": "Paul"}); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executeCount != 1 {
		t.Fatal("comment rule also dispatched")
	}
}

func TestUnsupportedExecutorRejectsDirectSelection(t *testing.T) {
	m := newTestManager(t)
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder [dispatch model=gpt-6.1-sol effort=high]\nWork", "Paul"); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executeCount != 0 {
		t.Fatal("unsupported executor ran")
	}
	if !strings.Contains(m.Client.(*fakeBoardClient).comments[0].content, "requires the codex executor") {
		t.Fatal("missing executor error")
	}
}
