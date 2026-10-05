package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type failFirstWorktree struct{ fakeWorktree }

func (w *failFirstWorktree) Prepare(_ context.Context, cardID, _ string) (string, error) {
	if cardID == "card1" {
		return "", errors.New("persistent workspace failure")
	}
	return w.path, nil
}

func (w *failFirstWorktree) ExistingOrBase(ctx context.Context, cardID, boardID string) (string, error) {
	return w.Prepare(ctx, cardID, boardID)
}

func replayEvent(t *testing.T, cardID, commentID, content string) json.RawMessage {
	t.Helper()
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["card_id"], event["comment_id"], event["content"] = cardID, commentID, content
	event["execution_request"] = event["execution_request_raw"]
	event["accepted_defaults"] = event["accepted_defaults_raw"]
	event["accepted_models"] = event["accepted_models_raw"]
	return rawJSON(t, event)
}

func replayManager(t *testing.T) *Manager {
	t.Helper()
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	return m
}

func TestReplayRejectsMalformedDirectiveOnceAndContinuesPages(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	client.loseCommentResponse = true
	cursor := "bad"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":    {Requests: []json.RawMessage{replayEvent(t, "card1", "bad", "@coder [dispatch model=oops]\nFix")}, NextAfter: &cursor},
		"bad": {Requests: []json.RawMessage{replayEvent(t, "card2", "good2", "@coder fix"), replayEvent(t, "card3", "good3", "@coder fix")}},
	}
	for range 2 {
		if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
			t.Fatal(err)
		}
		_ = m.RecoverAcceptedMentions(context.Background())
	}
	claim, err := m.readMentionClaim("card1", "bad")
	if err != nil || claim.State != "rejected" {
		t.Fatalf("rejection claim=%+v err=%v", claim, err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("executions=%d", got)
	}
	if got := len(client.commentsSnapshot()); got != 3 {
		t.Fatalf("comments=%d", got)
	}
}

func TestReplayPreparationFailureDoesNotBlockNextCard(t *testing.T) {
	m := replayManager(t)
	m.Worktree = &failFirstWorktree{fakeWorktree{path: "/tmp/test-worktree"}}
	client := m.Client.(*fakeBoardClient)
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{replayEvent(t, "card1", "first", "@coder fix"), replayEvent(t, "card2", "second", "@coder fix")}}}
	for range 2 {
		if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := m.RecoverAcceptedMentions(context.Background()); err == nil {
			t.Fatal("persistent preparation failure was hidden")
		}
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("executions=%d", got)
	}
	if claim, err := m.readMentionClaim("card1", "first"); err != nil || claim.State != "accepted" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if got := len(client.commentsSnapshot()); got != 2 {
		t.Fatalf("comments=%d", got)
	}
}

func TestReplayRetryAfterPageFailureStartsFromDurableItems(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	cursor := "first"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":     {Requests: []json.RawMessage{replayEvent(t, "card1", "first", "@coder fix")}, NextAfter: &cursor},
		cursor: {Requests: []json.RawMessage{replayEvent(t, "card2", "second", "@coder fix")}},
	}
	client.requestPageErrors = map[string]error{cursor: errors.New("temporary page failure")}
	if err := m.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("missing page failure")
	}
	if claim, err := m.readMentionClaim("card1", "first"); err != nil || claim.State != "accepted" {
		t.Fatalf("first claim=%+v err=%v", claim, err)
	}
	// A new manager represents a process restart. It scans from the beginning;
	// the first durable item must not execute twice and the second is ingested.
	restarted := replayManager(t)
	restarted.ClaimDir = m.ClaimDir
	restarted.Client = client
	if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount() + restarted.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("executions=%d", got)
	}
	if claim, err := restarted.readMentionClaim("card2", "second"); err != nil || claim.State != "terminal" {
		t.Fatalf("second claim=%+v err=%v", claim, err)
	}
}

func TestReplayRecordsLaterIntakeBeforeEarlierExecutionFinishes(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{replayEvent(t, "card1", "first", "@coder fix"), replayEvent(t, "card2", "second", "@coder fix")}}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	done := make(chan error, 1)
	go func() {
		if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
			done <- err
			return
		}
		done <- m.RecoverAcceptedMentions(context.Background())
	}()
	<-e.started
	defer func() { close(e.blockUntilRelease); <-done }()
	deadline := time.After(time.Second)
	for {
		if claim, err := m.readMentionClaim("card2", "second"); err == nil && claim.State != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("later replay item was not durably ingested while first executor ran")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestCanceledStructuredExecutorKeepsReturnedSuccess(t *testing.T) {
	for _, kind := range []string{"deadline", "socket"} {
		t.Run(kind, func(t *testing.T) {
			m := replayManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := m.Executor.(*fakeExecutor)
			if kind == "deadline" {
				m.Timeout = time.Millisecond
				e.onExecute = func() { time.Sleep(10 * time.Millisecond) }
			} else {
				e.onExecute = cancel
			}
			raw := replayEvent(t, "card1", "comment1", "@coder fix")
			_ = m.HandleBoardEventRaw(ctx, raw)
			claim, err := m.readMentionClaim("card1", "comment1")
			if err != nil || claim.State != "terminal" || claim.OutcomeStatus != "completed" || e.executionCount() != 1 {
				t.Fatalf("claim=%+v executions=%d err=%v", claim, e.executionCount(), err)
			}
			if err := m.HandleBoardEventRaw(context.Background(), raw); err != nil {
				t.Fatal(err)
			}
			if got := len(m.Client.(*fakeBoardClient).commentsSnapshot()); got != 1 {
				t.Fatalf("comments=%d", got)
			}
		})
	}
}

func TestCanceledStructuredExecutorPersistsKnownResult(t *testing.T) {
	for _, kind := range []string{"deadline", "socket"} {
		for _, lost := range []string{"none", "comment", "receipt"} {
			t.Run(kind+"/"+lost, func(t *testing.T) {
				m := replayManager(t)
				m.Timeout = 20 * time.Millisecond
				client := m.Client.(*fakeBoardClient)
				client.loseCommentResponse = lost == "comment"
				client.loseReceiptResponse = lost == "receipt"
				e := m.Executor.(*fakeExecutor)
				e.blockUntilCancel = true
				e.started = make(chan struct{})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if kind == "socket" {
					m.Timeout = time.Hour
				}
				done := make(chan error, 1)
				raw := replayEvent(t, "card1", "comment1", "@coder fix")
				go func() { done <- m.HandleBoardEventRaw(ctx, raw) }()
				<-e.started
				if kind == "socket" {
					cancel()
				}
				_ = <-done
				claim, err := m.readMentionClaim("card1", "comment1")
				if err != nil || (claim.State != "outcome" && claim.State != "terminal") || claim.OutcomeStatus != "failed" || claim.OwnerID != "socket-1" {
					t.Fatalf("claim=%+v err=%v", claim, err)
				}
				if err := m.HandleBoardEventRaw(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				if err := m.HandleBoardEventRaw(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				claim, err = m.readMentionClaim("card1", "comment1")
				if err != nil || claim.State != "terminal" || e.executionCount() != 1 || len(client.commentsSnapshot()) != 1 || client.receiptCalls < 1 {
					t.Fatalf("claim=%+v executions=%d comments=%v receipts=%d err=%v", claim, e.executionCount(), client.commentsSnapshot(), client.receiptCalls, err)
				}
				for _, receipt := range client.receiptRequests {
					if receipt.InstanceID != "socket-1" || receipt.Status != "failed" {
						t.Fatalf("receipt=%+v", receipt)
					}
				}
			})
		}
	}
}

func TestCanceledStructuredExecutorDoesNotResumeEmptyResult(t *testing.T) {
	m := replayManager(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := m.Executor.(*fakeExecutor)
	e.result = executor.Result{Success: true, SessionID: "session-1"}
	e.onExecute = cancel
	raw := replayEvent(t, "card1", "comment1", "@coder fix")
	_ = m.HandleBoardEventRaw(ctx, raw)
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "terminal" || claim.OutcomeStatus != "failed" || e.executionCount() != 1 {
		t.Fatalf("claim=%+v executions=%d err=%v", claim, e.executionCount(), err)
	}
}

func structuredEvent(request, defaults string) map[string]any {
	return map[string]any{"event_type": "comment_created", "board_id": "board1", "card_id": "card1", "comment_id": "comment1", "content": "@coder [dispatch model=gpt-6-sol effort=low]\nFix this.", "author_name": "Paul", "execution_request_raw": json.RawMessage(request), "accepted_defaults_raw": json.RawMessage(defaults), "accepted_models_raw": json.RawMessage(`[{"id":"gpt-6-sol","efforts":["low","high"]},{"id":"m","efforts":["high"]}]`)}
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

func TestQueuedPlainMentionKeepsDefaultsAcrossReload(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "original", Effort: "high"}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	done := make(chan error, 1)
	go func() { done <- m.ProcessMention(context.Background(), "card1", "first", "@coder first", "Paul") }()
	<-e.started
	if err := m.ProcessMention(context.Background(), "card1", "second", "@coder second", "Paul"); err != nil {
		t.Fatal(err)
	}
	m.CommentExecution = &rules.CommentExecutionConfig{Defaults: rules.CommentDefaults{Model: "reloaded", Effort: "low"}}
	close(e.blockUntilRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return e.executionCount() == 2 })
	if e.requests[1].Model != "original" || e.requests[1].ReasoningEffort != "high" {
		t.Fatalf("queued request = %+v", e.requests[1])
	}
}

func TestPlainMentionWithoutCommentScopeUsesCLIDefaults(t *testing.T) {
	m := newTestManager(t)
	if err := m.ProcessMention(context.Background(), "card1", "plain", "@coder task", "Paul"); err != nil {
		t.Fatal(err)
	}
	request := m.Executor.(*fakeExecutor).lastExecuteRequest
	if request.Model != "" || request.ReasoningEffort != "" {
		t.Fatalf("request = %+v", request)
	}
}

func TestStructuredClaimIsRequiredBeforeExecutor(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "m", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	client := m.Client.(*fakeBoardClient)
	client.claimErr = &api.APIError{StatusCode: 409, Code: "CLAIM_UNCERTAIN", Message: "other owner"}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"m","effort":"high"}`)
	event["content"] = "@coder task"
	_ = m.HandleBoardEvent(context.Background(), event)
	if client.claimCalls != 1 || m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatalf("claim calls=%d executor calls=%d", client.claimCalls, m.Executor.(*fakeExecutor).executionCount())
	}
}

func TestStructuredOutcomeRetryDoesNotRerunExecutor(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	client := m.Client.(*fakeBoardClient)
	client.addCommentErrors = []error{context.DeadlineExceeded}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder task"
	if err := m.HandleBoardEvent(context.Background(), event); err == nil {
		t.Fatal("lost outcome response must stay pending")
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "outcome" || claim.OutcomeBody == "" || claim.OwnerID != "socket-1" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 || client.receiptCalls != 1 {
		t.Fatalf("executor=%d receipts=%d", m.Executor.(*fakeExecutor).executionCount(), client.receiptCalls)
	}
	claim, err = m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "terminal" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
}

func TestExecutionRequestPagingUsesDurableEventPath(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	client := m.Client.(*fakeBoardClient)
	first := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	first["content"] = "@coder first"
	first["author_id"] = "author-1"
	first["author_is_bot"] = false
	second := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	second["comment_id"] = "comment2"
	second["content"] = "@coder second"
	for _, event := range []map[string]any{first, second} {
		event["execution_request"] = event["execution_request_raw"]
		event["accepted_defaults"] = event["accepted_defaults_raw"]
		event["accepted_models"] = event["accepted_models_raw"]
		delete(event, "execution_request_raw")
		delete(event, "accepted_defaults_raw")
		delete(event, "accepted_models_raw")
	}
	cursor := "comment1"
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{rawJSON(t, first)}, NextAfter: &cursor}, cursor: {Requests: []json.RawMessage{rawJSON(t, second)}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 2 || len(client.requestCursors) != 2 || client.requestCursors[1] != cursor {
		t.Fatalf("executions=%d cursors=%v", m.Executor.(*fakeExecutor).executionCount(), client.requestCursors)
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.AuthorID != "author-1" || claim.AuthorName != "Paul" {
		t.Fatalf("replayed author claim=%+v err=%v", claim, err)
	}
}

func TestReplayRejectsCursorThatCouldSkipUnrecordedItems(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder task"
	event["execution_request"] = event["execution_request_raw"]
	event["accepted_defaults"] = event["accepted_defaults_raw"]
	event["accepted_models"] = event["accepted_models_raw"]
	wrong := "unseen-comment"
	client := m.Client.(*fakeBoardClient)
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{rawJSON(t, event)}, NextAfter: &wrong}}
	if err := m.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("cursor mismatch was accepted")
	}
	if len(client.requestCursors) != 1 {
		t.Fatalf("request cursors=%v", client.requestCursors)
	}
	if _, err := m.readMentionClaim("card1", "comment1"); err != nil {
		t.Fatalf("processed page item was not recorded: %v", err)
	}
}

func TestMatchedDoneCleanupDrainsStructuredQueue(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	done := make(chan error, 1)
	go func() { done <- m.ProcessMention(context.Background(), "card1", "first", "@coder first", "Paul") }()
	<-e.started
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder queued"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	cleanup, _ := m.reserveCleanup(context.Background(), "card1")
	if cleanup == nil {
		t.Fatal("cleanup did not reserve card")
	}
	close(e.blockUntilRelease)
	<-done
	cleanup.Cancel()
	m.finishActiveSession(cleanup)
	if err := m.ProcessMention(context.Background(), "card1", "third", "@coder new work", "Paul"); err != nil {
		t.Fatal(err)
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "needs_review" || e.executionCount() != 2 {
		t.Fatalf("claim=%+v executions=%d err=%v", claim, e.executionCount(), err)
	}
}

func TestStructuredPreparationRevalidatesBeforeClaim(t *testing.T) {
	for _, change := range []string{"disconnect", "expiry", "withdraw"} {
		t.Run(change, func(t *testing.T) {
			m := newTestManager(t)
			m.ExecutorType = "codex"
			m.ClaimDir = t.TempDir()
			m.SetConnectedBot("bot-1", "socket-1")
			m.SetCapabilityRegistration("socket-1", "rev-1", time.Now().Add(time.Hour))
			m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
			client := m.Client.(*fakeBoardClient)
			client.markdownStarted = make(chan struct{})
			client.markdownRelease = make(chan struct{})
			event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
			event["content"] = "@coder task"
			done := make(chan error, 1)
			go func() { done <- m.HandleBoardEvent(context.Background(), event) }()
			<-client.markdownStarted
			switch change {
			case "disconnect":
				m.SetConnectedBot("", "")
			case "expiry":
				m.SetCapabilityRegistration("socket-1", "rev-1", time.Now().Add(-time.Second))
			case "withdraw":
				m.mu.Lock()
				m.suppressedPairs["gpt-6-sol\x00high"] = true
				m.mu.Unlock()
			}
			close(client.markdownRelease)
			<-done
			if m.Executor.(*fakeExecutor).executionCount() != 0 || client.claimCalls != 0 {
				t.Fatalf("executor=%d claim=%d", m.Executor.(*fakeExecutor).executionCount(), client.claimCalls)
			}
		})
	}
}

func TestCanceledStructuredSemaphoreWaitCanRecover(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	for range cap(m.sem) {
		m.sem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder task"
	done := make(chan error, 1)
	go func() { done <- m.HandleBoardEvent(ctx, event) }()
	waitFor(t, time.Second, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.structuredInFlight["comment1"] })
	cancel()
	<-done
	m.mu.Lock()
	stranded := m.structuredInFlight["comment1"]
	m.mu.Unlock()
	if stranded {
		t.Fatal("canceled request kept its reservation")
	}
	for range cap(m.sem) {
		<-m.sem
	}
	m.SetConnectedBot("bot-1", "socket-2")
	m.SetCapabilityRevision("rev-2")
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 {
		t.Fatal("accepted request did not recover")
	}
}

func TestOrphanLockRecoveryDistinguishesAcceptedAndStarted(t *testing.T) {
	for _, state := range []string{"accepted", "started"} {
		t.Run(state, func(t *testing.T) {
			m := newTestManager(t)
			m.ExecutorType = "codex"
			m.ClaimDir = t.TempDir()
			m.SetConnectedBot("bot-1", "socket-1")
			m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
			event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
			event["content"] = "@coder task"
			if err := m.HandleBoardEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			claim, err := m.readMentionClaim("card1", "comment1")
			if err != nil {
				t.Fatal(err)
			}
			if state == "started" {
				claim.OwnerID = "old-socket"
				if err := m.setMentionClaimState(claim, "started"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(m.claimPath("card1", "comment1")+".lock", []byte("orphan"), 0o600); err != nil {
				t.Fatal(err)
			}
			m.SetCapabilityRevision("rev-1")
			if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 0
			if state == "accepted" {
				want = 1
			} else {
				if err := m.HandleBoardEvent(context.Background(), event); err != nil {
					t.Fatal(err)
				}
				if err := m.HandleBoardEvent(context.Background(), event); err != nil {
					t.Fatal(err)
				}
				if got := len(m.Client.(*fakeBoardClient).commentsSnapshot()); got != 2 {
					t.Fatalf("expected one initial hold plus one idempotent uncertainty, got %d comments", got)
				}
			}
			if got := m.Executor.(*fakeExecutor).executionCount(); got != want {
				t.Fatalf("state=%s executions=%d want=%d", state, got, want)
			}
		})
	}
}

func TestNullableAcceptedDefaultsUseUnresolvedCLIFlags(t *testing.T) {
	for _, defaults := range []string{`{"model":null,"effort":null}`, `{"model":"gpt-6-sol","effort":null}`, `{"model":null,"effort":"high"}`} {
		t.Run(defaults, func(t *testing.T) {
			m := newTestManager(t)
			m.ExecutorType = "codex"
			m.ClaimDir = t.TempDir()
			m.SetConnectedBot("bot-1", "socket-1")
			m.SetCapabilityRevision("rev-1")
			m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
			event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, defaults)
			event["content"] = "@coder task"
			if err := m.HandleBoardEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if m.Executor.(*fakeExecutor).executionCount() != 1 {
				t.Fatal("nullable default was rejected")
			}
			claim, err := m.readMentionClaim("card1", "comment1")
			if err != nil {
				t.Fatal(err)
			}
			if claim.Model == "" && claim.ModelSource != "cli_unknown" || claim.Effort == "" && claim.EffortSource != "cli_unknown" {
				t.Fatalf("claim=%+v", claim)
			}
		})
	}
}

func TestLostStructuredOutcomeAndReceiptResponsesRetrySafely(t *testing.T) {
	for _, failure := range []bool{false, true} {
		for _, lost := range []string{"comment", "receipt"} {
			t.Run(fmt.Sprintf("failure=%t/%s", failure, lost), func(t *testing.T) {
				m := newTestManager(t)
				m.ExecutorType = "codex"
				m.ClaimDir = t.TempDir()
				m.SetConnectedBot("bot-1", "socket-1")
				m.SetCapabilityRevision("rev-1")
				m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
				if failure {
					m.Executor.(*fakeExecutor).result = executor.Result{Success: false, Error: "provider failed"}
				}
				client := m.Client.(*fakeBoardClient)
				client.loseCommentResponse = lost == "comment"
				client.loseReceiptResponse = lost == "receipt"
				event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
				event["content"] = "@coder task"
				if err := m.HandleBoardEvent(context.Background(), event); err == nil {
					t.Fatal("lost response should retain outbox")
				}
				if err := m.HandleBoardEvent(context.Background(), event); err != nil {
					t.Fatal(err)
				}
				claim, err := m.readMentionClaim("card1", "comment1")
				if err != nil || claim.State != "terminal" || m.Executor.(*fakeExecutor).executionCount() != 1 || len(client.commentsSnapshot()) != 1 {
					t.Fatalf("claim=%+v executions=%d comments=%v err=%v", claim, m.Executor.(*fakeExecutor).executionCount(), client.commentsSnapshot(), err)
				}
			})
		}
	}
}

func TestWebClaimRetryInspectsStatusAndUncertainty(t *testing.T) {
	for _, status := range []string{"completed", "failed", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			m := newTestManager(t)
			m.ExecutorType = "codex"
			m.ClaimDir = t.TempDir()
			m.SetConnectedBot("bot-1", "socket-1")
			m.SetCapabilityRevision("rev-1")
			m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
			client := m.Client.(*fakeBoardClient)
			if status == "uncertain" {
				client.claimErr = &api.APIError{StatusCode: 409, Code: "CLAIM_UNCERTAIN", Message: "other instance owns request"}
			} else {
				model, effort := "gpt-6-sol", "high"
				client.claimResult = api.ExecutionClaim{BoardID: "board1", CardID: "card1", CommentID: "comment1", InstanceID: "socket-1", Effective: api.ExecutionSelection{Model: &model, Effort: &effort}, Status: status}
			}
			event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
			event["content"] = "@coder task"
			if err := m.HandleBoardEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			if err := m.HandleBoardEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			claim, err := m.readMentionClaim("card1", "comment1")
			if err != nil || m.Executor.(*fakeExecutor).executionCount() != 0 || client.claimCalls != 1 || claim.State == "accepted" {
				t.Fatalf("claim=%+v calls=%d executions=%d err=%v", claim, client.claimCalls, m.Executor.(*fakeExecutor).executionCount(), err)
			}
		})
	}
}

func TestStructuredClaimPersistsAcceptedSnapshotAndAuthor(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, `{"model":"gpt-6-sol","effort":null}`)
	event["content"] = "@coder task"
	event["author_id"] = "author-1"
	event["created_at"] = "2026-10-05T18:00:00Z"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.AuthorID != "author-1" || claim.CreatedAt != "2026-10-05T18:00:00Z" || !strings.Contains(string(claim.Request), `"effort":"high"`) || !strings.Contains(string(claim.AcceptedDefaults), `"effort":null`) || claim.AcceptedRevision != "rev-1" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
}

func TestStructuredResolvedPairMustMatchAcceptedSnapshot(t *testing.T) {
	for _, test := range []struct {
		name, request, content, models string
	}{
		{"structured-model-directive-effort", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":"gpt-6-sol"}`, "@coder [dispatch model=gpt-6-sol effort=high]\nTask", `[{"id":"gpt-6-sol","efforts":["low"]}]`},
		{"directive-model-structured-effort", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`, "@coder [dispatch model=gpt-6-astra effort=low]\nTask", `[{"id":"gpt-6-sol","efforts":["high"]}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestManager(t)
			m.ExecutorType = "codex"
			m.ClaimDir = t.TempDir()
			m.SetConnectedBot("bot-1", "socket-1")
			m.SetCapabilityRevision("rev-1")
			m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"low", "high"}}, {ID: "gpt-6-astra", Efforts: []string{"low", "high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
			event := structuredEvent(test.request, `{"model":"gpt-6-sol","effort":"low"}`)
			event["content"] = test.content
			event["accepted_models_raw"] = json.RawMessage(test.models)
			if err := m.HandleBoardEvent(context.Background(), event); err != nil {
				t.Fatal(err)
			}
			claim, err := m.readMentionClaim("card1", "comment1")
			if err != nil || claim.State != "rejected" || m.Executor.(*fakeExecutor).executionCount() != 0 || m.Client.(*fakeBoardClient).claimCalls != 0 {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
		})
	}
}

func TestMalformedDirectiveRejectsStructuredOverride(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":"gpt-6-sol"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder [dispatch model=gpt-6-sol effort=broken]\nTask"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 || m.Client.(*fakeBoardClient).claimCalls != 0 {
		t.Fatal("malformed directive bypassed structured validation")
	}
}

func TestMissingAcceptedModelsCannotClaim(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":null,"effort":null}`)
	delete(event, "accepted_models_raw")
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 0 || m.Client.(*fakeBoardClient).claimCalls != 0 {
		t.Fatal("request without server snapshot was dispatched")
	}
}

func TestStaleWebClaimHoldsAcceptedRequestForRetry(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	client := m.Client.(*fakeBoardClient)
	client.claimErr = &api.APIError{StatusCode: 409, Code: "CAPABILITIES_STALE", Message: "reader changed"}
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder task"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "accepted" || m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	client.claimErr = nil
	m.SetCapabilityRevision("rev-2")
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 {
		t.Fatal("accepted request did not recover after fresh registration")
	}
}

func TestConcurrentStructuredRetryClaimsOnce(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder task"
	done := make(chan error, 1)
	go func() { done <- m.HandleBoardEvent(context.Background(), event) }()
	<-e.started
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	close(e.blockUntilRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e.executionCount() != 1 || m.Client.(*fakeBoardClient).claimCalls != 1 {
		t.Fatalf("executions=%d claims=%d", e.executionCount(), m.Client.(*fakeBoardClient).claimCalls)
	}
}

func TestCanceledQueuedStructuredSemaphoreWaitReleasesReservation(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	selection := mentionDispatch{Content: "@coder task", Model: "gpt-6-sol", ReasoningEffort: "high"}
	claim, _, err := m.createOrReadMentionClaim("card1", "comment1", "Paul", selection, structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.Active["card1"] = &ActiveSession{CardID: "card1", Done: make(chan struct{})}
	m.structuredQueue["card1"] = []pendingMention{{ctx: ctx, cardID: "card1", commentID: "comment1", content: selection.Content, authorName: "Paul", frozen: &selection, claim: &claim}}
	m.structuredInFlight["comment1"] = true
	m.mu.Unlock()
	for range cap(m.sem) {
		m.sem <- struct{}{}
	}
	m.finishActiveSession(m.Active["card1"])
	cancel()
	waitFor(t, time.Second, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return !m.structuredInFlight["comment1"] })
	waitFor(t, time.Second, func() bool { return len(m.ActiveCardIDs()) == 0 })
	for range cap(m.sem) {
		<-m.sem
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Executor.(*fakeExecutor).executionCount() != 1 {
		t.Fatal("queued canceled claim did not recover")
	}
}

func TestPausedMatchedDoneStillDrainsStructuredQueue(t *testing.T) {
	m := newTestManager(t)
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.Paused = true
	m.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "Done cleanup", Events: []string{"card_moved"}, List: "Done", CleanupCommand: []string{"/bin/true"}}}}
	selection := mentionDispatch{Content: "@coder task"}
	claim, _, err := m.createOrReadMentionClaim("card1", "comment1", "Paul", selection, structuredSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.structuredQueue["card1"] = []pendingMention{{cardID: "card1", commentID: "comment1", claim: &claim}}
	m.structuredInFlight["comment1"] = true
	m.mu.Unlock()
	if err := m.HandleBoardEvent(context.Background(), map[string]any{"event_type": "card_moved", "card_id": "card1", "list_name": "Done"}); err != nil {
		t.Fatal(err)
	}
	claim, err = m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "needs_review" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
}

func TestMatchedDoneEventDrainsStructuredBeforeLaterCoding(t *testing.T) {
	m := newTestManager(t)
	m.ExecutorType = "codex"
	m.ClaimDir = t.TempDir()
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	m.CommentExecution = &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe"}}
	m.Rules = &rules.Engine{Rules: []rules.Rule{{Name: "Done cleanup", Events: []string{"card_moved"}, List: "Done", CleanupCommand: []string{"/bin/true"}}}}
	e := m.Executor.(*fakeExecutor)
	e.started = make(chan struct{})
	e.blockUntilRelease = make(chan struct{})
	e.blockOnExecute = 1
	done := make(chan error, 1)
	go func() { done <- m.ProcessMention(context.Background(), "card1", "first", "@coder first", "Paul") }()
	<-e.started
	event := structuredEvent(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, `{"model":"gpt-6-sol","effort":"high"}`)
	event["content"] = "@coder queued"
	if err := m.HandleBoardEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	client := m.Client.(*fakeBoardClient)
	client.card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Done"}})
	if err := m.HandleBoardEvent(context.Background(), map[string]any{"event_type": "card_moved", "card_id": "card1", "list_name": "Done"}); err != nil {
		t.Fatal(err)
	}
	close(e.blockUntilRelease)
	<-done
	client.card = rawJSON(t, map[string]any{"list": map[string]any{"name": "Coding"}})
	if err := m.ProcessMention(context.Background(), "card1", "third", "@coder later", "Paul"); err != nil {
		t.Fatal(err)
	}
	claim, err := m.readMentionClaim("card1", "comment1")
	if err != nil || claim.State != "needs_review" || e.executionCount() != 2 {
		t.Fatalf("claim=%+v executions=%d err=%v", claim, e.executionCount(), err)
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
	waitFor(t, time.Second, func() bool { return len(m.ActiveCardIDs()) == 0 })
	for _, id := range []string{"comment1", "comment2", "comment3"} {
		claim, err := m.readMentionClaim("card1", id)
		if err != nil || claim.State != "terminal" {
			t.Fatalf("claim %s = %+v, err = %v", id, claim, err)
		}
	}
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

func TestRecoveryUsesAuthenticatedBotAcrossSharedDirectoryAndDisconnect(t *testing.T) {
	for _, state := range []string{"rejected", "outcome"} {
		t.Run(state, func(t *testing.T) {
			owner := replayManager(t)
			owner.ClaimDir = t.TempDir()
			selection := mentionDispatch{Content: "@coder fix", Model: "gpt-6-sol", ReasoningEffort: "high"}
			claim, _, err := owner.createOrReadMentionClaimWithDisposition("card1", "comment1", "Paul", selection, structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}}, state, "outcome body")
			if err != nil {
				t.Fatal(err)
			}
			if state == "outcome" {
				claim.OwnerID = "socket-1"
				claim.OutcomeStatus = "completed"
				claim.OutcomeMessage = "completed"
				if err := owner.saveMentionClaim(claim); err != nil {
					t.Fatal(err)
				}
			}
			other := replayManager(t)
			other.ClaimDir = owner.ClaimDir
			other.SetConnectedBot("bot-2", "socket-2")
			other.SetCapabilityRevision("rev-2")
			for range 2 {
				_ = other.RecoverAcceptedMentions(context.Background())
			}
			otherClient := other.Client.(*fakeBoardClient)
			if got := len(otherClient.commentsSnapshot()); got != 0 || otherClient.receiptCalls != 0 || other.Executor.(*fakeExecutor).executionCount() != 0 {
				t.Fatalf("other bot acted: comments=%d receipts=%d executions=%d", got, otherClient.receiptCalls, other.Executor.(*fakeExecutor).executionCount())
			}
			owner.SetConnectedBot("", "")
			for range 2 {
				if err := owner.RecoverAcceptedMentions(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			ownerClient := owner.Client.(*fakeBoardClient)
			wantReceipts := 0
			if state == "outcome" {
				wantReceipts = 1
			}
			if got := len(ownerClient.commentsSnapshot()); got != 1 || ownerClient.receiptCalls != wantReceipts || owner.Executor.(*fakeExecutor).executionCount() != 0 {
				t.Fatalf("owner reconciliation: comments=%d receipts=%d executions=%d", got, ownerClient.receiptCalls, owner.Executor.(*fakeExecutor).executionCount())
			}
		})
	}
}

func TestRecoveryPublishesLegacyOutcomeDespiteSameCardTerminal(t *testing.T) {
	for _, state := range []string{"rejected", "outcome"} {
		t.Run(state, func(t *testing.T) {
			owner := replayManager(t)
			owner.ClaimDir = t.TempDir()
			selection := mentionDispatch{Content: "@coder fix", Model: "gpt-6-sol", ReasoningEffort: "high"}
			snapshot := structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}}
			if _, _, err := owner.createOrReadMentionClaimWithDisposition("card1", "old", "Paul", selection, snapshot, "terminal", ""); err != nil {
				t.Fatal(err)
			}
			claim, _, err := owner.createOrReadMentionClaimWithDisposition("card1", "pending", "Paul", selection, snapshot, state, "outcome body")
			if err != nil {
				t.Fatal(err)
			}
			if state == "outcome" {
				claim.OwnerID = "socket-1"
				claim.OutcomeStatus = "completed"
				claim.OutcomeMessage = "completed"
				if err := owner.saveMentionClaim(claim); err != nil {
					t.Fatal(err)
				}
			}
			other := replayManager(t)
			other.ClaimDir = owner.ClaimDir
			other.SetConnectedBot("bot-2", "socket-2")
			for range 2 {
				if err := other.RecoverAcceptedMentions(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			otherClient := other.Client.(*fakeBoardClient)
			if len(otherClient.commentsSnapshot()) != 0 || otherClient.receiptCalls != 0 || other.Executor.(*fakeExecutor).executionCount() != 0 {
				t.Fatal("foreign bot acted on legacy claims")
			}
			owner.SetConnectedBot("", "")
			for range 2 {
				if err := owner.RecoverAcceptedMentions(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			ownerClient := owner.Client.(*fakeBoardClient)
			wantReceipts := 0
			if state == "outcome" {
				wantReceipts = 1
			}
			if got := len(ownerClient.commentsSnapshot()); got != 1 || ownerClient.receiptCalls != wantReceipts || owner.Executor.(*fakeExecutor).executionCount() != 0 {
				t.Fatalf("owner reconciliation: comments=%d receipts=%d executions=%d", got, ownerClient.receiptCalls, owner.Executor.(*fakeExecutor).executionCount())
			}
		})
	}
}

type orderingExecutor struct{ *fakeExecutor }

func (e *orderingExecutor) BuildPrompt(req executor.PromptRequest) string {
	return req.CommentContent
}

func TestReplaySameCardKeepsServerTieOrderAcrossPagesAndRestart(t *testing.T) {
	intake := replayManager(t)
	client := intake.Client.(*fakeBoardClient)
	first := replayEvent(t, "card1", "comment1", "@coder first")
	second := replayEvent(t, "card1", "comment2", "@coder second")
	for _, raw := range []*json.RawMessage{&first, &second} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = "2026-10-05T18:00:00Z"
		*raw = rawJSON(t, event)
	}
	cursor := "comment1"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":     {Requests: []json.RawMessage{first}, NextAfter: &cursor},
		cursor: {Requests: []json.RawMessage{second}},
	}
	if err := intake.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := replayManager(t)
	restarted.ClaimDir = intake.ClaimDir
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := underlying
	if got := e.executionCount(); got != 2 {
		t.Fatalf("executions=%d", got)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !strings.Contains(e.requests[0].Prompt, "first") || !strings.Contains(e.requests[1].Prompt, "second") {
		t.Fatalf("executor order: %q then %q", e.requests[0].Prompt, e.requests[1].Prompt)
	}
}

func TestReplayMutableCollectionDiscoversLaterRequestAcrossRestart(t *testing.T) {
	intake := replayManager(t)
	client := intake.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	for _, raw := range []*json.RawMessage{&a, &b, &c} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = "2026-10-05T18:00:00Z"
		*raw = rawJSON(t, event)
	}
	afterA := "a"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{a}, NextAfter: &afterA},
		"a": {Requests: []json.RawMessage{b}},
	}
	if err := intake.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A is deleted or moved off board. Its accepted local claim survives, while
	// Web now returns B at the first position and C on a later page.
	afterB := "b"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{b}, NextAfter: &afterB},
		"b": {Requests: []json.RawMessage{c}},
	}
	client.claimErrors = map[string]error{"a": &api.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "request no longer on board"}}
	restarted := replayManager(t)
	restarted.ClaimDir = intake.ClaimDir
	restarted.Client = client
	if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if claim, err := restarted.readMentionClaim("card1", "c"); err != nil || claim.State != "accepted" {
		t.Fatalf("C was not durably discovered: claim=%+v err=%v", claim, err)
	}
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	for range 2 {
		if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil && !strings.Contains(err.Error(), "request no longer on board") {
			t.Fatal(err)
		}
	}
	if got := underlying.executionCount(); got != 2 {
		t.Fatalf("executions=%d, want B and C exactly once", got)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	for i, want := range []string{"second", "third"} {
		if !strings.Contains(underlying.requests[i].Prompt, want) {
			t.Fatalf("request %d prompt=%q, want %q", i, underlying.requests[i].Prompt, want)
		}
	}
}

func TestReplayResetsDeletedCursorWithoutLosingIntake(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	for _, raw := range []*json.RawMessage{&a, &b, &c} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = "2026-10-05T18:00:00Z"
		*raw = rawJSON(t, event)
	}
	afterA := "a"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{a}, NextAfter: &afterA},
		"a": {Requests: []json.RawMessage{b}},
	}
	client.requestPageErrors = map[string]error{"a": &api.APIError{StatusCode: 400, Code: "VALIDATION_ERROR", Message: "cursor no longer exists"}}
	// The first page changes after Web rejects the now deleted cursor. A
	// retry must begin at the start and discover the later C request.
	afterB := "b"
	client.requestPagesOnError = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{b}, NextAfter: &afterB},
		"b": {Requests: []json.RawMessage{c}},
	}
	client.claimErrors = map[string]error{"a": &api.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "request no longer on board"}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	cursors := append([]string(nil), client.requestCursors...)
	client.mu.Unlock()
	if got, want := strings.Join(cursors, ","), ",a,,b"; got != want {
		t.Fatalf("cursor calls=%q, want %q", got, want)
	}
	if claim, err := m.readMentionClaim("card1", "c"); err != nil || claim.State != "accepted" {
		t.Fatalf("C claim=%+v err=%v", claim, err)
	}
	restarted := replayManager(t)
	restarted.ClaimDir = m.ClaimDir
	restarted.Client = client
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	for range 2 {
		if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil && !strings.Contains(err.Error(), "request no longer on board") {
			t.Fatal(err)
		}
	}
	if got := underlying.executionCount(); got != 2 {
		t.Fatalf("executions=%d, want B and C once", got)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if !strings.Contains(underlying.requests[0].Prompt, "second") || !strings.Contains(underlying.requests[1].Prompt, "third") {
		t.Fatalf("executor order: %+v", underlying.requests)
	}
}

func TestReplayPersistsIdentityBeforeAcceptedClaimOnInterruptedPage(t *testing.T) {
	intake := replayManager(t)
	client := intake.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	for _, raw := range []*json.RawMessage{&a, &b, &c} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = "2026-10-05T18:00:00Z"
		*raw = rawJSON(t, event)
	}
	// The invalid second item interrupts this page after A's acceptance,
	// before the page-level order checkpoint. A process crash has the same
	// durable state at this boundary.
	client.requestPages = map[string]api.ExecutionRequestPage{"": {
		Requests: []json.RawMessage{a, json.RawMessage("{")},
	}}
	if err := intake.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("interrupted page was accepted")
	}
	claim, err := intake.readMentionClaim("card1", "a")
	if err != nil || claim.State != "accepted" {
		t.Fatalf("A claim=%+v err=%v", claim, err)
	}
	sequence, err := intake.readReplaySequence("bot-1")
	if err != nil || len(sequence.Items) != 1 || sequence.Items[0].CommentID != "a" {
		t.Fatalf("accepted A has no durable order: sequence=%+v err=%v", sequence, err)
	}
	// Restart before recovery. A is deleted from Web, so its authoritative
	// claim fails with 404; B and C remain eligible in their tied server order.
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{b, c}}}
	client.claimErrors = map[string]error{"a": &api.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "request no longer on board"}}
	restarted := replayManager(t)
	restarted.ClaimDir = intake.ClaimDir
	restarted.Client = client
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	for range 2 {
		if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if len(underlying.requests) != 2 {
		t.Fatalf("executions=%d, want B and C once", len(underlying.requests))
	}
	for i, want := range []string{"second", "third"} {
		request := underlying.requests[i]
		if !strings.Contains(request.Prompt, want) || request.Model != "gpt-6-sol" || request.ReasoningEffort != "high" {
			t.Fatalf("request %d=%+v, want frozen %s", i, request, want)
		}
	}
	if claim, err := restarted.readMentionClaim("card1", "a"); err != nil || claim.State != "accepted" {
		t.Fatalf("deleted A claim=%+v err=%v", claim, err)
	}
}

func TestReplayPageFailureDoesNotHoldDurableNewRequest(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterB := "b"
	c := replayEvent(t, "card1", "c", "@coder third")
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{a, b}, NextAfter: &afterB},
		"b": {Requests: []json.RawMessage{c}},
	}
	for range 2 {
		client.requestPageErrors = map[string]error{"b": &api.APIError{StatusCode: 500, Code: "SERVER_ERROR", Message: "temporary page failure"}}
		if err := m.ReconcileExecutionRequests(context.Background()); err == nil {
			t.Fatal("expected later-page failure")
		}
		if claim, err := m.readMentionClaim("card1", "b"); err != nil || claim.State == "" {
			t.Fatalf("B intake claim=%+v err=%v", claim, err)
		}
		if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("executions=%d, want A and B once despite page failure", got)
	}
}

func TestReplayCrashBeforePageOrderDoesNotBlockLaterPrefix(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	for _, raw := range []*json.RawMessage{&a, &b, &c} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = "2026-10-05T18:00:00Z"
		*raw = rawJSON(t, event)
	}
	// Simulate a crash after the claim for A was durable but before its page
	// order was written. A is then deleted before the restarted scan.
	if err := m.HandleBoardEventRaw(context.WithValue(context.Background(), replayIntakeKey{}, true), a); err != nil {
		t.Fatal(err)
	}
	afterC := "c"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{b, c}, NextAfter: &afterC},
		"c": {Requests: []json.RawMessage{replayEvent(t, "card2", "later", "@coder later")}},
	}
	client.requestPageErrors = map[string]error{"c": &api.APIError{StatusCode: 500, Code: "SERVER_ERROR", Message: "later page failed"}}
	if err := m.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("expected later page failure")
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("executions=%d, want B and C despite unobserved A", got)
	}
	if claim, err := m.readMentionClaim("card1", "a"); err != nil || claim.State != "accepted" {
		t.Fatalf("unobserved A claim=%+v err=%v", claim, err)
	}
}

func TestReplayPartialPrefixKeepsOmittedOldClaimRecoverable(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	// A's card moves off board while B and C remain on another card. A can
	// later return; deletion would not permit that transition.
	a := replayEvent(t, "card2", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterC := "c"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{b, c}, NextAfter: &afterC},
		"c": {Requests: []json.RawMessage{replayEvent(t, "card3", "later", "@coder later")}},
	}
	client.claimErrors = map[string]error{"a": &api.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "moved off board"}}
	for range 2 {
		client.requestPageErrors = map[string]error{"c": &api.APIError{StatusCode: 500, Code: "SERVER_ERROR", Message: "later page failed"}}
		if err := m.ReconcileExecutionRequests(context.Background()); err == nil {
			t.Fatal("expected later page failure")
		}
		if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("executions=%d, want B and C once", got)
	}
	if client.claimCalls != 2 || len(client.commentsSnapshot()) != 2 {
		t.Fatalf("claims=%d comments=%d, omitted A must make no cross-board request", client.claimCalls, len(client.commentsSnapshot()))
	}
	for _, reaction := range client.reactionsSnapshot() {
		if reaction.commentID == "a" {
			t.Fatalf("omitted A received cross-board reaction: %+v", reaction)
		}
	}
	if claim, err := m.readMentionClaim("card2", "a"); err != nil || claim.State != "accepted" {
		t.Fatalf("omitted A claim=%+v err=%v", claim, err)
	}
	delete(client.claimErrors, "a")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b, c}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 3 {
		t.Fatalf("executions after A returned=%d, want A once", got)
	}
	if got := len(client.commentsSnapshot()); got != 3 {
		t.Fatalf("comments after A returned=%d", got)
	}
}

func TestReplayPartialScanHoldsUnobservedPriorTailUntilComplete(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Both claims are durable, but the process restarts before local recovery.
	restarted := replayManager(t)
	restarted.ClaimDir = m.ClaimDir
	restarted.Client = client
	afterA := "a"
	client.requestPages = map[string]api.ExecutionRequestPage{
		"":  {Requests: []json.RawMessage{a}, NextAfter: &afterA},
		"a": {Requests: []json.RawMessage{b}},
	}
	client.requestPageErrors = map[string]error{"a": &api.APIError{StatusCode: 500, Code: "SERVER_ERROR", Message: "later page failed"}}
	if err := restarted.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("expected later page failure")
	}
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := underlying.executionCount(); got != 1 {
		t.Fatalf("partial scan executions=%d, want only observed A", got)
	}
	if claim, err := restarted.readMentionClaim("card1", "b"); err != nil || claim.State != "accepted" {
		t.Fatalf("unobserved B claim=%+v err=%v", claim, err)
	}
	delete(client.requestPageErrors, "a")
	if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if len(underlying.requests) != 2 || !strings.Contains(underlying.requests[0].Prompt, "first") || !strings.Contains(underlying.requests[1].Prompt, "second") {
		t.Fatalf("tail did not resume in original order: %+v", underlying.requests)
	}
}

func TestReplayFirstPageFailureHoldsOldAcceptedClaim(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := replayManager(t)
	restarted.ClaimDir = m.ClaimDir
	restarted.Client = client
	client.requestPageErrors = map[string]error{"": &api.APIError{StatusCode: 500, Code: "SERVER_ERROR", Message: "first page failed"}}
	if err := restarted.ReconcileExecutionRequests(context.Background()); err == nil {
		t.Fatal("expected first page failure")
	}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := restarted.Executor.(*fakeExecutor).executionCount(); got != 0 {
		t.Fatalf("stale collection executed %d requests", got)
	}
	if claim, err := restarted.readMentionClaim("card1", "a"); err != nil || claim.State != "accepted" {
		t.Fatalf("held claim=%+v err=%v", claim, err)
	}
	delete(client.requestPageErrors, "")
	if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := restarted.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("recovered executions=%d", got)
	}
}

func TestReplayObservedRequestMovedAfterScanDoesNotReactOrReject(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	client.claimErrors = map[string]error{"a": &api.APIError{StatusCode: 404, Code: "NOT_FOUND", Message: "moved off board"}}
	if err := m.RecoverAcceptedMentions(context.Background()); err == nil || !strings.Contains(err.Error(), "currently unavailable on this board") {
		t.Fatalf("missing actionable move error: %v", err)
	}
	if len(client.commentsSnapshot()) != 0 || len(client.reactionsSnapshot()) != 0 || m.Executor.(*fakeExecutor).executionCount() != 0 {
		t.Fatal("moved request caused a cross-board side effect")
	}
	if claim, err := m.readMentionClaim("card1", "a"); err != nil || claim.State != "accepted" {
		t.Fatalf("held claim=%+v err=%v", claim, err)
	}
	delete(client.claimErrors, "a")
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("returned request executions=%d", got)
	}
}

func TestReplayManyPagesPreserveCanonicalTieOrder(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	client.requestPages = make(map[string]api.ExecutionRequestPage)
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprintf("comment-%03d", i)
	}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		page := api.ExecutionRequestPage{}
		for _, id := range ids[start:end] {
			var event map[string]any
			if err := json.Unmarshal(replayEvent(t, "card1", id, "@coder "+id), &event); err != nil {
				t.Fatal(err)
			}
			event["created_at"] = "2026-10-05T18:00:00Z"
			page.Requests = append(page.Requests, rawJSON(t, event))
		}
		if end < len(ids) {
			cursor := ids[end-1]
			page.NextAfter = &cursor
		}
		cursor := ""
		if start != 0 {
			cursor = ids[start-1]
		}
		client.requestPages[cursor] = page
	}
	for range 2 {
		if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	sequence, err := m.readReplaySequence("bot-1")
	if err != nil || !sequence.Complete || len(sequence.Items) != len(ids) {
		t.Fatalf("sequence length=%d complete=%v err=%v", len(sequence.Items), sequence.Complete, err)
	}
	for i, want := range ids {
		if sequence.Items[i].CommentID != want {
			t.Fatalf("sequence[%d]=%q, want %q", i, sequence.Items[i].CommentID, want)
		}
	}
	restarted := replayManager(t)
	restarted.ClaimDir = m.ClaimDir
	restarted.Client = client
	underlying := restarted.Executor.(*fakeExecutor)
	restarted.Executor = &orderingExecutor{underlying}
	if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if len(underlying.requests) != len(ids) {
		t.Fatalf("executions=%d, want %d", len(underlying.requests), len(ids))
	}
	for i, want := range ids {
		if !strings.Contains(underlying.requests[i].Prompt, want) {
			t.Fatalf("executor request %d=%q, want %s", i, underlying.requests[i].Prompt, want)
		}
	}
}

func TestReplayMutableCollectionDoesNotRerunOwnedOutcomes(t *testing.T) {
	owner := replayManager(t)
	client := owner.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b}}}
	if err := owner.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := owner.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := owner.Executor.(*fakeExecutor).executionCount(); got != 2 {
		t.Fatalf("initial executions=%d", got)
	}
	// Moving A off this board has the same collection omission as deleting its
	// comment. B's owned terminal result remains local while C is appended.
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{b, c}}}
	restarted := replayManager(t)
	restarted.Client = client
	restarted.ClaimDir = owner.ClaimDir
	for range 2 {
		if err := restarted.ReconcileExecutionRequests(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := restarted.RecoverAcceptedMentions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := restarted.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("restart executions=%d, want C only", got)
	}
	for _, id := range []string{"a", "b", "c"} {
		claim, err := restarted.readMentionClaim("card1", id)
		if err != nil || claim.State != "terminal" {
			t.Fatalf("claim %s=%+v err=%v", id, claim, err)
		}
	}
}

func TestReplaySequenceMigratesOldOrdinalSidecars(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(m.replaySequencePath("bot-1")); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"a", "b"} {
		claim, err := m.readMentionClaim("card1", id)
		if err != nil {
			t.Fatal(err)
		}
		data := rawJSON(t, replayOrderRecord{Version: 1, BoardID: claim.BoardID, CardID: claim.CardID, CommentID: claim.CommentID, TargetBotID: claim.TargetBotID, Order: uint64(i + 1)})
		if err := os.WriteFile(m.replayOrderPath(claim), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{b, c}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	sequence, err := m.readReplaySequence("bot-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sequence.Items), 3; got != want {
		t.Fatalf("sequence items=%d, want %d", got, want)
	}
	for i, want := range []string{"b", "c", "a"} {
		if sequence.Items[i].CommentID != want {
			t.Fatalf("sequence[%d]=%q, want %q", i, sequence.Items[i].CommentID, want)
		}
	}
}

func TestReplayMigrationDoesNotTrustRanksFromDifferentScans(t *testing.T) {
	m := replayManager(t)
	selection := mentionDispatch{Content: "@coder first", Model: "gpt-6-sol", ReasoningEffort: "high"}
	snapshot := structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, CreatedAt: "2026-10-05T18:00:00Z"}
	for _, item := range []struct {
		id, content string
		oldRank     uint64
	}{{"a", "@coder first", 2}, {"b", "@coder second", 1}} {
		selection.Content = item.content
		claim, _, err := m.createOrReadMentionClaim("card1", item.id, "Paul", selection, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		data := rawJSON(t, replayOrderRecord{Version: 1, BoardID: claim.BoardID, CardID: claim.CardID, CommentID: claim.CommentID, TargetBotID: claim.TargetBotID, Order: item.oldRank})
		if err := os.WriteFile(m.replayOrderPath(claim), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Equal timestamps and old absolute positions alone cannot prove FIFO.
	if err := m.RecoverAcceptedMentions(context.Background()); err == nil || !strings.Contains(err.Error(), "unambiguous replay order") {
		t.Fatalf("ambiguous legacy recovery error=%v", err)
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 0 {
		t.Fatalf("executions before canonical replay=%d", got)
	}
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	for _, raw := range []*json.RawMessage{&a, &b} {
		var event map[string]any
		if err := json.Unmarshal(*raw, &event); err != nil {
			t.Fatal(err)
		}
		event["created_at"] = snapshot.CreatedAt
		*raw = rawJSON(t, event)
	}
	m.Client.(*fakeBoardClient).requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	sequence, err := m.readReplaySequence("bot-1")
	if err != nil || len(sequence.Items) != 2 || sequence.Items[0].CommentID != "a" || sequence.Items[1].CommentID != "b" {
		t.Fatalf("canonical sequence=%+v err=%v", sequence, err)
	}
	underlying := m.Executor.(*fakeExecutor)
	m.Executor = &orderingExecutor{underlying}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if len(underlying.requests) != 2 || !strings.Contains(underlying.requests[0].Prompt, "first") || !strings.Contains(underlying.requests[1].Prompt, "second") {
		t.Fatalf("executor order: %+v", underlying.requests)
	}
}

func TestMutableReplayKeepsOwnedOutcomeAcrossOmissionAndDisconnect(t *testing.T) {
	m := replayManager(t)
	client := m.Client.(*fakeBoardClient)
	a := replayEvent(t, "card1", "a", "@coder first")
	b := replayEvent(t, "card1", "b", "@coder second")
	c := replayEvent(t, "card1", "c", "@coder third")
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{a, b}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	owned, err := m.readMentionClaim("card1", "b")
	if err != nil {
		t.Fatal(err)
	}
	owned.State, owned.OwnerID = "outcome", "socket-1"
	owned.OutcomeBody, owned.OutcomeStatus, owned.OutcomeMessage = "B result", "completed", "completed"
	if err := m.saveMentionClaim(owned); err != nil {
		t.Fatal(err)
	}
	client.requestPages = map[string]api.ExecutionRequestPage{"": {Requests: []json.RawMessage{b, c}}}
	if err := m.ReconcileExecutionRequests(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.SetConnectedBot("", "")
	client.loseCommentResponse = true
	if err := m.RecoverAcceptedMentions(context.Background()); err == nil {
		t.Fatal("lost owned outcome response was hidden")
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 0 {
		t.Fatalf("disconnected executions=%d", got)
	}
	m.SetConnectedBot("bot-1", "socket-2")
	m.SetCapabilityRevision("rev-1")
	for range 2 {
		if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("executions=%d, want C once", got)
	}
	if got := len(client.commentsSnapshot()); got != 2 || client.receiptCalls != 2 {
		t.Fatalf("comments=%d receipts=%d, want B and C once", got, client.receiptCalls)
	}
	if client.receiptRequests[0].InstanceID != "socket-1" {
		t.Fatalf("owned B receipt instance=%q", client.receiptRequests[0].InstanceID)
	}
}

func TestRecoveryHoldsUnknownOwnerAndAmbiguousLegacyOrder(t *testing.T) {
	owner := replayManager(t)
	owner.ClaimDir = t.TempDir()
	selection := mentionDispatch{Content: "@coder fix", Model: "gpt-6-sol", ReasoningEffort: "high"}
	snapshot := structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}}
	for _, id := range []string{"comment1", "comment2"} {
		if _, _, err := owner.createOrReadMentionClaim("card1", id, "Paul", selection, snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := owner.createOrReadMentionClaim("card2", "comment3", "Paul", selection, snapshot); err != nil {
		t.Fatal(err)
	}
	unknown := replayManager(t)
	unknown.ClaimDir = owner.ClaimDir
	unknown.SetConnectedBot("", "")
	unknown.verifiedBotID = ""
	if err := unknown.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := unknown.Executor.(*fakeExecutor).executionCount(); got != 0 {
		t.Fatalf("unverified owner executed %d requests", got)
	}
	if got := len(unknown.Client.(*fakeBoardClient).commentsSnapshot()); got != 0 {
		t.Fatalf("unverified owner published %d comments", got)
	}
	if err := owner.RecoverAcceptedMentions(context.Background()); err == nil || !strings.Contains(err.Error(), "replay order") {
		t.Fatalf("missing order error = %v", err)
	}
	if got := owner.Executor.(*fakeExecutor).executionCount(); got != 1 {
		t.Fatalf("different-card executions=%d, want 1", got)
	}
	for _, id := range []string{"comment1", "comment2"} {
		claim, err := owner.readMentionClaim("card1", id)
		if err != nil || claim.State != "accepted" {
			t.Fatalf("claim %s=%+v err=%v", id, claim, err)
		}
	}
}

func TestRecoveryOrdersLegacyClaimsByDistinctCreationTime(t *testing.T) {
	m := replayManager(t)
	underlying := m.Executor.(*fakeExecutor)
	m.Executor = &orderingExecutor{underlying}
	selection := mentionDispatch{Content: "@coder first", Model: "gpt-6-sol", ReasoningEffort: "high"}
	snapshot := structuredSnapshot{Revision: "rev-1", Models: []api.ExecutionModel{{ID: "gpt-6-sol", Efforts: []string{"high"}}}, CreatedAt: "2026-10-05T18:00:00Z"}
	if _, _, err := m.createOrReadMentionClaim("card1", "comment1", "Paul", selection, snapshot); err != nil {
		t.Fatal(err)
	}
	selection.Content = "@coder second"
	snapshot.CreatedAt = "2026-10-05T18:01:00Z"
	if _, _, err := m.createOrReadMentionClaim("card1", "comment2", "Paul", selection, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := m.RecoverAcceptedMentions(context.Background()); err != nil {
		t.Fatal(err)
	}
	underlying.mu.Lock()
	defer underlying.mu.Unlock()
	if len(underlying.requests) != 2 || !strings.Contains(underlying.requests[0].Prompt, "first") || !strings.Contains(underlying.requests[1].Prompt, "second") {
		t.Fatalf("executor order: %+v", underlying.requests)
	}
}
