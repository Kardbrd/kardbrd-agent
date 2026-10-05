package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func structuredEvent(request, defaults string) map[string]any {
	return map[string]any{"event_type": "comment_created", "card_id": "card1", "comment_id": "comment1", "content": "@coder [dispatch model=gpt-6-sol effort=low]\nFix this.", "author_name": "Paul", "execution_request_raw": json.RawMessage(request), "accepted_defaults_raw": json.RawMessage(defaults)}
}

func TestStructuredEventUsesFrozenSelectionAndDeduplicates(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	exec := m.Executor.(*fakeExecutor)
	if exec.executionCount() != 1 || exec.lastExecuteRequest.Model != "gpt-6-sol" || exec.lastExecuteRequest.ReasoningEffort != "high" {
		t.Fatalf("requests = %+v", exec.requests)
	}
	data, err := os.ReadFile(m.claimPath("card1", "comment1"))
	if err != nil {
		t.Fatal(err)
	}
	var claim mentionClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.ModelSource != "directive" || claim.EffortSource != "explicit" || claim.State != "terminal" {
		t.Fatalf("claim = %+v", claim)
	}
}

func TestInvalidStructuredEventCannotFallThroughToCommand(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "command", Events: []string{"comment_created"}, CommentCommand: "/down", Action: "/down"}}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":null}`, `{"model":null,"effort":null}`)
	event["content"] = "@coder /down"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("invalid request executed")
	}
	comments := m.Client.(*fakeBoardClient).commentsSnapshot()
	if len(comments) == 0 || !strings.Contains(comments[0].content, "execution_request.model") {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestStructuredWrongBotIsIgnored(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-2","capability_revision":"rev-1"}`, `{"model":null,"effort":null}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("wrong bot executed")
	}
}

func TestPlainMentionUsesOnlyCommentExecutionDefaults(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "verified-model", Effort: "high"}}
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder fix", "Paul"); err != nil {
		t.Fatal(err)
	}
	req := m.Executor.(*fakeExecutor).lastExecuteRequest
	if req.Model != "verified-model" || req.ReasoningEffort != "high" {
		t.Fatalf("request = %+v", req)
	}
}

func TestClaudeCommentDefaultsReachExecutor(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "claude"
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "claude-verified", Effort: "high"}}
	if err := m.ProcessMention(context.Background(), "card1", "comment1", "@coder fix", "Paul"); err != nil {
		t.Fatal(err)
	}
	req := m.Executor.(*fakeExecutor).lastExecuteRequest
	if req.Model != "claude-verified" || req.ReasoningEffort != "high" {
		t.Fatalf("request = %+v", req)
	}
}

func TestAcceptedStructuredClaimRecoversAfterRegistration(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high", "low"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"old-rev","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("stale claim executed")
	}
	m.SetCapabilityRevision("new-rev")
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 {
		t.Fatal("accepted claim did not recover")
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 {
		t.Fatal("terminal claim replayed")
	}
}

func TestStructuredQueueKeepsEveryAcceptedComment(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	first := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	done := make(chan error, 1)
	go func() { done <- m.HandleBoardEvent(context.Background(), first) }()
	<-e.started
	for _, id := range []string{"comment2", "comment3"} {
		follow := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
		follow["comment_id"] = id
		if err := m.HandleBoardEvent(context.Background(), follow); err != nil {
			t.Fatal(err)
		}
	}
	close(e.blockUntilRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return e.executionCount() == 3 })
	if e.requests[0].Model != "gpt-6-sol" || e.requests[1].Model != "gpt-6-sol" || e.requests[2].ReasoningEffort != "high" {
		t.Fatalf("requests = %+v", e.requests)
	}
}

func TestStructuredEventUsesRealCodexArgv(t *testing.T) {
	paths := configureManagerCodex(t, "final")
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high", "low"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	worktree := t.TempDir()
	m.Worktree = &fakeWorktree{path: worktree}
	m.Executor = executor.NewCodex(executor.Config{CWD: worktree, Timeout: 5 * time.Second, APIURL: "https://api.test", Token: "tok"})
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	args := readManagerFile(t, filepath.Join(paths.argsDir, "1"))
	if !strings.Contains(args, "--model\ngpt-6-sol\n") || !strings.Contains(args, "--config\nmodel_reasoning_effort=\"high\"\n") {
		t.Fatalf("argv = %q", args)
	}
}

func TestStructuredPerFieldPrecedence(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	request, err := parseExecutionRequest([]byte(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":"gpt-6-astra"}`))
	if err != nil {
		t.Fatal(err)
	}
	model, effort := "gpt-6-luna", "medium"
	selection, err := m.resolveStructuredSelection("@coder [dispatch model=gpt-6-sol effort=high]\nTask", request, acceptedDefaults{Model: &model, Effort: &effort})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "gpt-6-astra" || selection.ReasoningEffort != "high" || selection.ModelSource != "explicit" || selection.EffortSource != "directive" {
		t.Fatalf("selection = %+v", selection)
	}
	selection, err = m.resolveStructuredSelection("@coder Task", request, acceptedDefaults{Model: &model, Effort: &effort})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Model != "gpt-6-astra" || selection.ReasoningEffort != "medium" || selection.EffortSource != "accepted_defaults" {
		t.Fatalf("selection = %+v", selection)
	}
}

func TestStructuredRequestHoldsOnWrongLiveInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"data":{"version":1,"instance_id":"other-socket","executor":"codex","board_id":"board1","revision":"rev-1","expires_at":"2030-01-01T00:00:00Z"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer server.Close()
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.Client = api.NewClient(server.URL, "tok")
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("wrong live instance executed")
	}
	data, err := os.ReadFile(m.claimPath("card1", "comment1"))
	if err != nil {
		t.Fatal(err)
	}
	var claim mentionClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.State != "accepted" {
		t.Fatalf("claim = %+v", claim)
	}
}

func TestQueuedStructuredClaimRechecksRegistrationBeforeExecution(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	done := make(chan error, 1)
	go func() { done <- m.ProcessMention(context.Background(), "card1", "legacy", "@coder first", "Paul") }()
	<-e.started
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	m.SetCapabilityRevision("")
	close(e.blockUntilRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(m.ActiveCardIDs()) == 0 })
	if e.executionCount() != 1 {
		t.Fatalf("stale queued request ran: %+v", e.requests)
	}
	data, err := os.ReadFile(m.claimPath("card1", "comment1"))
	if err != nil {
		t.Fatal(err)
	}
	var claim mentionClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		t.Fatal(err)
	}
	if claim.State != "accepted" {
		t.Fatalf("claim = %+v", claim)
	}
}

func TestStructuredContinuationKeepsFrozenSelection(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high", "low"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	e := m.Executor.(*fakeExecutor)
	e.results = []executor.Result{{Success: true, SessionID: "thread-1"}, {Success: true, ResultText: "final"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":"low"}`)
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(e.requests) != 2 || e.requests[1].ResumeSessionID != "thread-1" || e.requests[0].Model != e.requests[1].Model || e.requests[0].ReasoningEffort != e.requests[1].ReasoningEffort {
		t.Fatalf("requests = %+v", e.requests)
	}
}
