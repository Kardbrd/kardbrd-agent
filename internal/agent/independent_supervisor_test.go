package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestIndependentSupervisorFailedExecutorDoesNotBecomeSucceededClaim(t *testing.T) {
	m := newTestManager(t)
	m.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
	m.Executor.(*fakeExecutor).result = executor.Result{Success: false, Error: "synthetic failure"}
	rule := rules.Rule{Events: []string{"comment_created"}, CommentCommand: "/up", Action: "/up", Execution: rules.ExecutionPrepare}
	message := map[string]any{"card_id": "card1", "comment_id": "comment1", "content": "/up"}
	if err := m.ProcessCommentCommand(context.Background(), "card1", rule, message); err != nil {
		t.Fatal(err)
	}
	key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "comment1", Command: "/up"}
	if got := m.commandClaims[key].state; got != "failed" {
		t.Fatalf("failed executor recorded as %q", got)
	}
	comments := m.Client.(*fakeBoardClient).commentsSnapshot()
	if len(comments) != 1 || !strings.Contains(comments[0].content, "synthetic failure") {
		t.Fatalf("failed executor did not publish one diagnostic: %#v", comments)
	}
}

func TestIndependentSupervisorFailedAuthenticationAndTimeoutDoNotBecomeSucceededClaims(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Manager)
		exec  int
		text  string
	}{
		{
			name: "authentication",
			setup: func(m *Manager) {
				m.Executor = &fakeExecutor{auth: executor.AuthStatus{Authenticated: false, Error: "login required"}}
			},
			exec: 0,
			text: "not authenticated",
		},
		{
			name: "timeout",
			setup: func(m *Manager) {
				m.Timeout = 10 * time.Millisecond
				exec := m.Executor.(*fakeExecutor)
				exec.blockUntilCancel = true
			},
			exec: 1,
			text: "context deadline exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			m.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
			tc.setup(m)
			rule := rules.Rule{Name: "Publish", Events: []string{"comment_created"}, CommentCommand: "/up", Action: "/up", Execution: rules.ExecutionPrepare}
			message := map[string]any{"card_id": "card1", "comment_id": tc.name, "content": "/up"}
			if err := m.ProcessCommentCommand(context.Background(), "card1", rule, message); err != nil {
				t.Fatal(err)
			}
			key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: tc.name, Command: "/up"}
			if got := m.commandClaims[key].state; got != "failed" {
				t.Fatalf("%s failure recorded as %q", tc.name, got)
			}
			client := m.Client.(*fakeBoardClient)
			if got := client.commentAttemptCount(); got != 1 {
				t.Fatalf("%s failure published %d diagnostic comments, want one", tc.name, got)
			}
			comments := client.commentsSnapshot()
			if len(comments) != 1 || !strings.Contains(comments[0].content, tc.text) {
				t.Fatalf("%s diagnostic missing %q: %#v", tc.name, tc.text, comments)
			}
			if got := m.Executor.(*fakeExecutor).executionCount(); got != tc.exec {
				t.Fatalf("%s executed %d times, want %d", tc.name, got, tc.exec)
			}
		})
	}
}

func TestIndependentSupervisorFailurePublicationDoesNotHoldCommandOwnership(t *testing.T) {
	m := newTestManager(t)
	m.Timeout = 10 * time.Millisecond
	m.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
	m.Executor.(*fakeExecutor).blockUntilCancel = true
	publishStarted := make(chan struct{})
	releasePublication := make(chan struct{})
	m.Client.(*fakeBoardClient).onAddComment = func(string, string) {
		close(publishStarted)
		<-releasePublication
	}
	t.Cleanup(func() {
		select {
		case <-releasePublication:
		default:
			close(releasePublication)
		}
	})
	rule := rules.Rule{Name: "Publish", Events: []string{"comment_created"}, CommentCommand: "/up", Action: "/up", Execution: rules.ExecutionPrepare}
	done := make(chan error, 1)
	go func() {
		done <- m.ProcessCommentCommand(context.Background(), "card1", rule, map[string]any{"card_id": "card1", "comment_id": "blocked-publication", "content": "/up"})
	}()
	select {
	case <-publishStarted:
	case <-time.After(time.Second):
		t.Fatal("failure diagnostic was not published")
	}
	deadline := time.After(150 * time.Millisecond)
	released := false
	for !released {
		m.mu.Lock()
		claim := m.commandClaims[commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "blocked-publication", Command: "/up"}]
		_, active := m.Active["card1"]
		m.mu.Unlock()
		if claim != nil && claim.state == "failed" && !active {
			released = true
			break
		}
		select {
		case <-deadline:
			t.Error("blocked diagnostic retained command ownership after the action deadline")
			released = true
		case <-time.After(time.Millisecond):
		}
	}
	close(releasePublication)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestIndependentSupervisorQueuedFailuresPreserveOutcomeAndSingleDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Manager)
		exec  int
		text  string
	}{
		{
			name: "executor",
			setup: func(m *Manager) {
				m.Executor.(*fakeExecutor).result = executor.Result{Success: false, Error: "queued executor failure"}
			},
			exec: 1,
			text: "queued executor failure",
		},
		{
			name: "authentication",
			setup: func(m *Manager) {
				m.Executor = &fakeExecutor{auth: executor.AuthStatus{Authenticated: false, Error: "login required"}}
			},
			exec: 0,
			text: "not authenticated",
		},
		{
			name: "timeout",
			setup: func(m *Manager) {
				m.Timeout = 10 * time.Millisecond
				m.Executor.(*fakeExecutor).blockUntilCancel = true
			},
			exec: 1,
			text: "context deadline exceeded",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestManager(t)
			m.Client.(*fakeBoardClient).card = rawJSON(t, map[string]any{"list": map[string]any{"name": "In Progress"}})
			tc.setup(m)
			active := &ActiveSession{CardID: "card1", Done: make(chan struct{})}
			m.Active["card1"] = active
			rule := rules.Rule{Name: "Publish", Events: []string{"comment_created"}, CommentCommand: "/up", Action: "/up", Execution: rules.ExecutionPrepare}
			if err := m.ProcessCommentCommand(context.Background(), "card1", rule, map[string]any{"card_id": "card1", "comment_id": tc.name, "content": "/up"}); err != nil {
				t.Fatal(err)
			}
			m.finishActiveSession(active)
			key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: tc.name, Command: "/up"}
			waitFor(t, time.Second, func() bool {
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.commandClaims[key].state == "failed"
			})
			client := m.Client.(*fakeBoardClient)
			if got := client.commentAttemptCount(); got != 2 {
				t.Fatalf("queued %s failure posted %d comments, want queue acknowledgement plus one diagnostic", tc.name, got)
			}
			comments := client.commentsSnapshot()
			if len(comments) != 2 || !strings.Contains(comments[1].content, tc.text) {
				t.Fatalf("queued %s diagnostic missing %q: %#v", tc.name, tc.text, comments)
			}
			if got := m.Executor.(*fakeExecutor).executionCount(); got != tc.exec {
				t.Fatalf("queued %s executed %d times, want %d", tc.name, got, tc.exec)
			}
		})
	}
}

func TestIndependentSupervisorDedupCapacityCountsOnlyTerminalRecords(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	for i := 0; i < commandDedupLimit; i++ {
		key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: fmt.Sprint(i), Command: "/up"}
		m.commandClaims[key] = &commandClaim{key: key, state: "succeeded", terminalAt: now.Add(-time.Duration(i) * time.Millisecond)}
	}
	activeKey := commandDedupKey{BoardID: m.BoardID, CardID: "active", Comment: "active", Command: "/up"}
	queuedKey := commandDedupKey{BoardID: m.BoardID, CardID: "queued", Comment: "queued", Command: "/up"}
	m.commandClaims[activeKey] = &commandClaim{key: activeKey, state: "running"}
	m.commandClaims[queuedKey] = &commandClaim{key: queuedKey, state: "queued"}
	rule := rules.Rule{CommentCommand: "/up", Action: "/up"}
	if !m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "new"}, "denied") {
		t.Fatal("new terminal claim was not reserved")
	}
	if got := terminalClaimCount(m); got != commandDedupLimit {
		t.Fatalf("terminal capacity is %d, want %d", got, commandDedupLimit)
	}
	if m.commandClaims[activeKey] == nil || m.commandClaims[queuedKey] == nil {
		t.Fatal("terminal capacity evicted an active or queued claim")
	}
	oldestKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: fmt.Sprint(commandDedupLimit - 1), Command: "/up"}
	if m.commandClaims[oldestKey] != nil {
		t.Fatal("terminal capacity did not evict the oldest terminal claim")
	}
}

func TestIndependentSupervisorDedupRedeliveryRefreshesLRU(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	rule := rules.Rule{CommentCommand: "/up", Action: "/up"}
	for i := 0; i < commandDedupLimit; i++ {
		key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: fmt.Sprint(i), Command: "/up"}
		m.commandClaims[key] = &commandClaim{key: key, rule: rule, state: "succeeded", terminalAt: now.Add(time.Duration(i-commandDedupLimit) * time.Millisecond)}
	}
	touchedKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "0", Command: "/up"}
	if m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "0"}, "denied") {
		t.Fatal("existing command was not deduplicated")
	}
	if !m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "new"}, "denied") {
		t.Fatal("new terminal claim was not reserved")
	}
	m.pruneCommandClaimsLocked(time.Now())
	if m.commandClaims[touchedKey] == nil {
		t.Fatal("recently accessed terminal record was evicted; retention is FIFO rather than specified LRU")
	}
	oldestUntouchedKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "1", Command: "/up"}
	if m.commandClaims[oldestUntouchedKey] != nil {
		t.Fatal("redelivery did not evict the oldest untouched terminal claim")
	}
	if got := terminalClaimCount(m); got != commandDedupLimit {
		t.Fatalf("redelivery left %d terminal claims, want %d", got, commandDedupLimit)
	}
}

func TestIndependentSupervisorDedupLRUTieUsesAccessGeneration(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	rule := rules.Rule{CommentCommand: "/up", Action: "/up"}
	for i := 0; i < commandDedupLimit; i++ {
		key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: fmt.Sprint(i), Command: "/up"}
		m.commandClaims[key] = &commandClaim{key: key, rule: rule, state: "succeeded", terminalAt: now, lastAccessedAt: now, lastAccessSeq: uint64(i + 1)}
	}
	m.commandAccessSeq = commandDedupLimit
	touchedKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "0", Command: "/up"}
	if m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "0"}, "denied") {
		t.Fatal("existing terminal claim was not deduplicated")
	}
	if !m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "new"}, "denied") {
		t.Fatal("new terminal claim was not reserved")
	}
	if m.commandClaims[touchedKey] == nil {
		t.Fatal("redelivery with an equal wall-clock timestamp evicted the refreshed claim")
	}
	if staleKey := (commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "1", Command: "/up"}); m.commandClaims[staleKey] != nil {
		t.Fatal("redelivery with an equal wall-clock timestamp did not evict the least-recently-accessed claim")
	}
}

func TestIndependentSupervisorDedupTTLLeavesNonterminalClaims(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	expiredKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "expired", Command: "/up"}
	runningKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "running", Command: "/up"}
	queuedKey := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "queued", Command: "/up"}
	m.commandClaims[expiredKey] = &commandClaim{key: expiredKey, state: "failed", terminalAt: now.Add(-24*time.Hour - time.Nanosecond)}
	m.commandClaims[runningKey] = &commandClaim{key: runningKey, state: "running"}
	m.commandClaims[queuedKey] = &commandClaim{key: queuedKey, state: "queued"}
	m.pruneCommandClaimsLocked(now)
	if m.commandClaims[expiredKey] != nil {
		t.Fatal("expired terminal claim was retained beyond the fixed TTL")
	}
	if m.commandClaims[runningKey] == nil || m.commandClaims[queuedKey] == nil {
		t.Fatal("dedup TTL expired a nonterminal claim")
	}
}

func TestIndependentSupervisorDedupRedeliveryDoesNotExtendTerminalTTL(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	rule := rules.Rule{CommentCommand: "/up", Action: "/up"}
	key := commandDedupKey{BoardID: m.BoardID, CardID: "card1", Comment: "old", Command: "/up"}
	terminalAt := now.Add(-24*time.Hour + time.Second)
	m.commandClaims[key] = &commandClaim{key: key, rule: rule, state: "failed", terminalAt: terminalAt, lastAccessedAt: terminalAt}
	if m.reserveTerminalCommand("card1", rule, map[string]any{"comment_id": "old"}, "denied") {
		t.Fatal("redelivery did not reuse the terminal claim")
	}
	if got := m.commandClaims[key].terminalAt; !got.Equal(terminalAt) {
		t.Fatalf("redelivery extended fixed terminal TTL from %v to %v", terminalAt, got)
	}
	m.pruneCommandClaimsLocked(now.Add(2 * time.Second))
	if m.commandClaims[key] != nil {
		t.Fatal("redelivered terminal claim survived beyond the fixed 24-hour TTL")
	}
}

func terminalClaimCount(m *Manager) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, claim := range m.commandClaims {
		if !claim.terminalAt.IsZero() {
			count++
		}
	}
	return count
}
