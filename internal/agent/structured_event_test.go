package agent

import (
	"context"
	"encoding/json"
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
			if err != nil || claim.State != "needs_review" || m.Executor.(*fakeExecutor).executionCount() != 0 || m.Client.(*fakeBoardClient).claimCalls != 0 {
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
