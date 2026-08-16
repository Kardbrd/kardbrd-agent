package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/prompt"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestNewManagerDefaults(t *testing.T) {
	manager := NewManager(Config{
		BoardID:   "board1",
		APIURL:    "https://api.test",
		Token:     "tok",
		AgentName: "coder",
	})

	assertEqual(t, "board1", manager.BoardID)
	assertEqual(t, "https://api.test", manager.APIURL)
	assertEqual(t, "tok", manager.Token)
	assertEqual(t, "coder", manager.AgentName)
	assertEqual(t, "@coder", manager.Mention)
	assertEqual(t, time.Hour, manager.Timeout)
	assertEqual(t, 3, manager.MaxConcurrent)
	assertEqual(t, "claude", manager.ExecutorType)
	assertEqual(t, 0, len(manager.Active))
}

func TestStartValidatesBoardAndExecutorAuth(t *testing.T) {
	client := &fakeBoardClient{board: rawJSON(t, map[string]any{"id": "board1"})}
	exec := &fakeExecutor{auth: executor.AuthStatus{Authenticated: true, Email: "bot@example.test"}}
	manager := NewManager(Config{
		BoardID:   "board1",
		APIURL:    "https://api.test",
		Token:     "tok",
		AgentName: "coder",
		Client:    client,
		Executor:  exec,
	})

	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, true, client.getBoardCalled)
	assertEqual(t, true, exec.checkAuthCalled)
}

func TestHandleBoardEventIgnoresCommentWithoutMention(t *testing.T) {
	manager := newTestManager(t)
	manager.Executor = &fakeExecutor{auth: executor.AuthStatus{Authenticated: true}}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type":  "comment_created",
		"card_id":     "card1",
		"comment_id":  "comment1",
		"content":     "plain comment",
		"author_name": "Paul",
	}); err != nil {
		t.Fatal(err)
	}

	exec := manager.Executor.(*fakeExecutor)
	assertEqual(t, 0, exec.executeCount)
}

func TestHandleBoardEventProcessesMention(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	exec := manager.Executor.(*fakeExecutor)
	worktrees := manager.Worktree.(*fakeWorktree)

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type":  "comment_created",
		"card_id":     "card1",
		"comment_id":  "comment1",
		"content":     "@coder /plan this",
		"author_name": "Paul",
	}); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "card1", worktrees.createdCard)
	assertEqual(t, "/plan this", exec.lastPromptRequest.Command)
	assertEqual(t, "/tmp/card-card1", exec.lastExecuteRequest.CWD)
	assertEqual(t, "card1", exec.lastExecuteRequest.CardID)
	assertEqual(t, "board1", exec.lastExecuteRequest.BoardID)
	assertEqual(t, 1, exec.executeCount)
	assertEqual(t, "card1", client.comments[0].cardID)
	assertContains(t, client.comments[0].content, "Done")
	assertContains(t, client.comments[0].content, "@Paul")
	assertEqual(t, 0, len(manager.Active))
}

func TestProcessMentionPublishesTerminalSummaryAfterProgressUpdates(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	exec := manager.Executor.(*fakeExecutor)
	client.card = rawJSON(t, map[string]any{"comments": []any{map[string]any{
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"author":     map[string]any{"is_bot": true},
	}}})
	exec.result = executor.Result{Success: true, ResultText: "terminal response", SessionID: "session1"}
	exec.onExecute = func() {
		_, _ = client.AddComment(context.Background(), "card1", "progress one")
		_, _ = client.AddComment(context.Background(), "card1", "progress two")
	}

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 1, exec.executeCount)
	assertEqual(t, 3, len(client.comments))
	assertEqual(t, "progress one", client.comments[0].content)
	assertEqual(t, "progress two", client.comments[1].content)
	assertContains(t, client.comments[2].content, "terminal response")
	assertContains(t, client.comments[2].content, "@Paul")
}

func TestProcessMentionQueuesFollowUpDuringTerminalPublication(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	exec := manager.Executor.(*fakeExecutor)
	exec.results = []executor.Result{
		{Success: true, ResultText: "first terminal response"},
		{Success: true, ResultText: "follow-up terminal response"},
	}
	finalCommentStarted := make(chan struct{})
	releaseFinalComment := make(chan struct{})
	client.onAddComment = func(_ string, content string) {
		if !strings.Contains(content, "first terminal response") {
			return
		}
		close(finalCommentStarted)
		<-releaseFinalComment
	}

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- manager.ProcessMention(context.Background(), "card1", "comment1", "@coder first", "Paul")
	}()

	select {
	case <-finalCommentStarted:
	case <-time.After(time.Second):
		t.Fatal("terminal comment was not started")
	}

	if err := manager.ProcessMention(context.Background(), "card1", "comment2", "@coder follow up", "Paul"); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, exec.executionCount())
	assertReaction(t, client.reactions, "comment2", "👀")

	close(releaseFinalComment)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("initial mention did not finish")
	}

	waitFor(t, time.Second, func() bool {
		return exec.executionCount() == 2 && client.commentCount() == 2 && len(manager.ActiveCardIDs()) == 0
	})
	assertEqual(t, 0, len(manager.ActiveCardIDs()))
	comments := client.commentsSnapshot()
	assertContains(t, comments[0].content, "first terminal response")
	assertContains(t, comments[1].content, "follow-up terminal response")
}

func TestProcessMentionPublishesBeforeSuccessReactionAndRelease(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	client.onReaction = func(_ string, _ string, emoji string) {
		if emoji != "✅" {
			return
		}
		if _, active := manager.Active["card1"]; !active {
			t.Error("active session was released before success reaction")
		}
	}

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	events := client.eventsSnapshot()
	assertEventBefore(t, events, "comment:Done", "reaction:✅")
	assertEqual(t, 0, len(manager.Active))
}

func TestProcessMentionDoesNotMarkSuccessWhenTerminalPublishFails(t *testing.T) {
	manager := newTestManager(t)
	client := manager.Client.(*fakeBoardClient)
	client.addCommentErrors = []error{errors.New("card comment unavailable")}

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 1, manager.Executor.(*fakeExecutor).executionCount())
	assertEqual(t, 2, client.commentAttemptCount())
	assertEqual(t, 1, client.commentCount())
	assertContains(t, client.commentsSnapshot()[0].content, "Unable to publish terminal summary")
	assertReaction(t, client.reactionsSnapshot(), "comment1", "🛑")
	assertNoReaction(t, client.reactionsSnapshot(), "comment1", "✅")
}

func TestProcessMentionEmptyResultIsVisibleWithoutSuccess(t *testing.T) {
	manager := newTestManager(t)
	manager.Executor.(*fakeExecutor).result = executor.Result{Success: true}
	client := manager.Client.(*fakeBoardClient)

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 1, manager.Executor.(*fakeExecutor).executionCount())
	assertEqual(t, 1, client.commentCount())
	assertContains(t, client.commentsSnapshot()[0].content, "No terminal response received")
	assertReaction(t, client.reactionsSnapshot(), "comment1", "⚠️")
	assertNoReaction(t, client.reactionsSnapshot(), "comment1", "✅")
}

func TestProcessMentionRecoversEmptyResultWithBoundedResume(t *testing.T) {
	manager := newTestManager(t)
	exec := manager.Executor.(*fakeExecutor)
	exec.results = []executor.Result{
		{Success: true, SessionID: "session1"},
		{Success: true, ResultText: "recovered terminal response"},
	}

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 2, exec.executionCount())
	assertEqual(t, "session1", exec.lastExecuteRequest.ResumeSessionID)
	comments := manager.Client.(*fakeBoardClient).commentsSnapshot()
	assertEqual(t, 1, len(comments))
	assertContains(t, comments[0].content, "recovered terminal response")
	assertReaction(t, manager.Client.(*fakeBoardClient).reactionsSnapshot(), "comment1", "✅")
}

func TestProcessMentionFailedExecutorIsVisibleWithoutSuccess(t *testing.T) {
	manager := newTestManager(t)
	manager.Executor.(*fakeExecutor).result = executor.Result{Success: false, Error: "executor failed"}
	client := manager.Client.(*fakeBoardClient)

	if err := manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, 1, client.commentCount())
	assertContains(t, client.commentsSnapshot()[0].content, "executor failed")
	assertReaction(t, client.reactionsSnapshot(), "comment1", "🛑")
	assertNoReaction(t, client.reactionsSnapshot(), "comment1", "✅")
}

func TestHandleBoardEventAcknowledgesDuplicateActiveCard(t *testing.T) {
	manager := newTestManager(t)
	manager.Active["card1"] = &ActiveSession{CardID: "card1", WorktreePath: "/tmp/card-card1"}

	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type":  "comment_created",
		"card_id":     "card1",
		"comment_id":  "comment1",
		"content":     "@coder /plan",
		"author_name": "Paul",
	}); err != nil {
		t.Fatal(err)
	}

	exec := manager.Executor.(*fakeExecutor)
	assertEqual(t, 0, exec.executeCount)
	assertEqual(t, 0, len(manager.Client.(*fakeBoardClient).comments))
	assertReaction(t, manager.Client.(*fakeBoardClient).reactionsSnapshot(), "comment1", "👀")
}

func TestStopReactionCancelsRunningExecutor(t *testing.T) {
	manager := newTestManager(t)
	exec := manager.Executor.(*fakeExecutor)
	exec.blockUntilCancel = true
	exec.started = make(chan struct{})
	exec.cancelled = make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- manager.ProcessMention(context.Background(), "card1", "comment1", "@coder do work", "Paul")
	}()

	select {
	case <-exec.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	if err := manager.HandleStopReaction(context.Background(), "card1", "comment1"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-exec.cancelled:
	case <-time.After(time.Second):
		t.Fatal("executor context was not cancelled")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("mention processing did not finish")
	}

	client := manager.Client.(*fakeBoardClient)
	assertEqual(t, 1, len(client.comments))
	assertContains(t, client.comments[0].content, "Agent stopped")
}

func TestStreamRequestedConnectsActiveSessionAndForwardsChunks(t *testing.T) {
	manager := newTestManager(t)
	stream := &fakeStream{}
	var gotURL string
	oldConnect := connectStream
	connectStream = func(ctx context.Context, streamURL string) (api.StreamConn, error) {
		gotURL = streamURL
		return stream, nil
	}
	defer func() { connectStream = oldConnect }()

	manager.Active["card1"] = &ActiveSession{CardID: "card1", WorktreePath: "/tmp/card-card1"}
	if err := manager.HandleBoardEvent(context.Background(), map[string]any{
		"event_type": "stream_requested",
		"card_id":    "card1",
		"stream_url": "ws://stream.test/session",
	}); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "ws://stream.test/session", gotURL)
	assertEqual(t, true, manager.Active["card1"].Streaming)

	onChunk := manager.makeOnChunk("card1")
	onChunk("hello", "assistant")

	assertEqual(t, 1, len(stream.payloads))
	payload := stream.payloads[0].(map[string]any)
	assertEqual(t, "stream_chunk", payload["type"].(string))
	assertEqual(t, "card1", payload["card_id"].(string))
	assertEqual(t, "hello", payload["text"].(string))
	assertEqual(t, "assistant", payload["chunk_type"].(string))
	assertEqual(t, 0, payload["sequence"].(int))
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	client := &fakeBoardClient{
		board:    rawJSON(t, map[string]any{"id": "board1"}),
		card:     rawJSON(t, map[string]any{"comments": []any{}}),
		markdown: "# Card",
	}
	exec := &fakeExecutor{
		auth:   executor.AuthStatus{Authenticated: true},
		result: executor.Result{Success: true, ResultText: "Done"},
	}
	worktrees := &fakeWorktree{path: "/tmp/card-card1"}
	return NewManager(Config{
		BoardID:   "board1",
		APIURL:    "https://api.test",
		Token:     "tok",
		AgentName: "coder",
		Client:    client,
		Executor:  exec,
		Worktree:  worktrees,
	})
}

type fakeBoardClient struct {
	mu                 sync.Mutex
	board              json.RawMessage
	card               json.RawMessage
	comment            json.RawMessage
	markdown           string
	getBoardCalled     bool
	getCardCalls       int
	comments           []commentCall
	reactions          []reactionCall
	updatedCardID      string
	updatedDescription string
	createdBoardID     string
	createdListID      string
	createdTitle       string
	createdDescription string
	onAddComment       func(cardID, content string)
	onReaction         func(cardID, commentID, emoji string)
	addCommentErrors   []error
	commentAttempts    []commentCall
	events             []string
}

type commentCall struct {
	cardID  string
	content string
}

type reactionCall struct {
	cardID    string
	commentID string
	emoji     string
}

func (c *fakeBoardClient) GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error) {
	c.getBoardCalled = true
	return c.board, nil
}

func (c *fakeBoardClient) GetCard(ctx context.Context, cardID string) (json.RawMessage, error) {
	c.getCardCalls++
	return c.card, nil
}

func (c *fakeBoardClient) GetCardMarkdown(ctx context.Context, cardID string) (string, error) {
	return c.markdown, nil
}

func (c *fakeBoardClient) AddComment(ctx context.Context, cardID, content string) (json.RawMessage, error) {
	if c.onAddComment != nil {
		c.onAddComment(cardID, content)
	}
	c.mu.Lock()
	c.commentAttempts = append(c.commentAttempts, commentCall{cardID: cardID, content: content})
	var err error
	if len(c.addCommentErrors) > 0 {
		err = c.addCommentErrors[0]
		c.addCommentErrors = c.addCommentErrors[1:]
	}
	if err == nil {
		c.comments = append(c.comments, commentCall{cardID: cardID, content: content})
		c.events = append(c.events, "comment:"+content)
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return mustRawJSON(map[string]any{"id": "comment-new"}), nil
}

func (c *fakeBoardClient) GetComment(ctx context.Context, cardID, commentID string) (json.RawMessage, error) {
	if len(c.comment) == 0 {
		return mustRawJSON(map[string]any{"author": map[string]any{}}), nil
	}
	return c.comment, nil
}

func (c *fakeBoardClient) ToggleReaction(ctx context.Context, cardID, commentID, emoji string) (json.RawMessage, error) {
	if c.onReaction != nil {
		c.onReaction(cardID, commentID, emoji)
	}
	c.mu.Lock()
	c.reactions = append(c.reactions, reactionCall{cardID: cardID, commentID: commentID, emoji: emoji})
	c.events = append(c.events, "reaction:"+emoji)
	c.mu.Unlock()
	return mustRawJSON(map[string]any{"ok": true}), nil
}

func (c *fakeBoardClient) commentCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.comments)
}

func (c *fakeBoardClient) commentsSnapshot() []commentCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]commentCall(nil), c.comments...)
}

func (c *fakeBoardClient) commentAttemptCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.commentAttempts)
}

func (c *fakeBoardClient) reactionsSnapshot() []reactionCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]reactionCall(nil), c.reactions...)
}

func (c *fakeBoardClient) eventsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

func (c *fakeBoardClient) UpdateCard(ctx context.Context, cardID string, patch api.CardPatch) (json.RawMessage, error) {
	c.updatedCardID = cardID
	if patch.Description != nil {
		c.updatedDescription = *patch.Description
	}
	return mustRawJSON(map[string]any{"id": cardID}), nil
}

func (c *fakeBoardClient) CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error) {
	c.createdBoardID = boardID
	c.createdListID = listID
	c.createdTitle = title
	c.createdDescription = description
	return mustRawJSON(map[string]any{"id": "new-card"}), nil
}

type fakeExecutor struct {
	mu                 sync.Mutex
	auth               executor.AuthStatus
	result             executor.Result
	results            []executor.Result
	checkAuthCalled    bool
	executeCount       int
	lastPromptRequest  prompt.Request
	lastExecuteRequest executor.Request
	blockUntilCancel   bool
	started            chan struct{}
	cancelled          chan struct{}
	onExecute          func()
}

func (e *fakeExecutor) CheckAuth(ctx context.Context) executor.AuthStatus {
	e.checkAuthCalled = true
	return e.auth
}

func (e *fakeExecutor) Execute(ctx context.Context, req executor.Request) executor.Result {
	e.mu.Lock()
	e.executeCount++
	e.lastExecuteRequest = req
	result := e.result
	if len(e.results) >= e.executeCount {
		result = e.results[e.executeCount-1]
	}
	onExecute := e.onExecute
	blockUntilCancel := e.blockUntilCancel
	started := e.started
	cancelled := e.cancelled
	e.mu.Unlock()
	if onExecute != nil {
		onExecute()
	}
	if blockUntilCancel {
		if started != nil {
			close(started)
		}
		select {
		case <-ctx.Done():
			if cancelled != nil {
				close(cancelled)
			}
			return executor.Result{Success: false, Error: ctx.Err().Error()}
		case <-time.After(200 * time.Millisecond):
			return executor.Result{Success: false, Error: "context was not cancelled"}
		}
	}
	return result
}

func (e *fakeExecutor) executionCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.executeCount
}

func (e *fakeExecutor) BuildPrompt(req executor.PromptRequest) string {
	e.lastPromptRequest = req
	return "prompt"
}

func (e *fakeExecutor) ExtractCommand(commentContent string, mentionKeyword string) string {
	return prompt.ExtractCommand(commentContent, mentionKeyword)
}

type fakeWorktree struct {
	path        string
	createdCard string
	removedCard string
	forced      bool
}

func (w *fakeWorktree) Create(cardID string) (string, error) {
	w.createdCard = cardID
	return w.path, nil
}

func (w *fakeWorktree) Remove(cardID string, force bool) error {
	w.removedCard = cardID
	w.forced = force
	return nil
}

type fakeStream struct {
	payloads []any
	closed   bool
}

func (s *fakeStream) WriteJSON(value any) error {
	s.payloads = append(s.payloads, value)
	return nil
}

func (s *fakeStream) SetWriteDeadline(t time.Time) error {
	return nil
}

func (s *fakeStream) Close() error {
	s.closed = true
	return nil
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustRawJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func assertReaction(t *testing.T, reactions []reactionCall, commentID, emoji string) {
	t.Helper()
	for _, reaction := range reactions {
		if reaction.commentID == commentID && reaction.emoji == emoji {
			return
		}
	}
	t.Fatalf("missing %s reaction on %s", emoji, commentID)
}

func assertNoReaction(t *testing.T, reactions []reactionCall, commentID, emoji string) {
	t.Helper()
	for _, reaction := range reactions {
		if reaction.commentID == commentID && reaction.emoji == emoji {
			t.Fatalf("unexpected %s reaction on %s", emoji, commentID)
		}
	}
}

func assertEventBefore(t *testing.T, events []string, first, second string) {
	t.Helper()
	firstIndex := -1
	secondIndex := -1
	for index, event := range events {
		if strings.Contains(event, first) && firstIndex == -1 {
			firstIndex = index
		}
		if event == second && secondIndex == -1 {
			secondIndex = index
		}
	}
	if firstIndex == -1 || secondIndex == -1 || firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q in %#v", first, second, events)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}
