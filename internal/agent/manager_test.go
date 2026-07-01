package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/prompt"
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
	assertEqual(t, 1, exec.executeCount)
	assertEqual(t, "card1", client.comments[0].cardID)
	assertContains(t, client.comments[0].content, "Done")
	assertContains(t, client.comments[0].content, "@Paul")
	assertEqual(t, 0, len(manager.Active))
}

func TestHandleBoardEventSkipsDuplicateActiveCard(t *testing.T) {
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
	board              json.RawMessage
	card               json.RawMessage
	comment            json.RawMessage
	markdown           string
	getBoardCalled     bool
	comments           []commentCall
	reactions          []reactionCall
	updatedCardID      string
	updatedDescription string
	createdBoardID     string
	createdListID      string
	createdTitle       string
	createdDescription string
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
	return c.card, nil
}

func (c *fakeBoardClient) GetCardMarkdown(ctx context.Context, cardID string) (string, error) {
	return c.markdown, nil
}

func (c *fakeBoardClient) AddComment(ctx context.Context, cardID, content string) (json.RawMessage, error) {
	c.comments = append(c.comments, commentCall{cardID: cardID, content: content})
	return mustRawJSON(map[string]any{"id": "comment-new"}), nil
}

func (c *fakeBoardClient) GetComment(ctx context.Context, cardID, commentID string) (json.RawMessage, error) {
	if len(c.comment) == 0 {
		return mustRawJSON(map[string]any{"author": map[string]any{}}), nil
	}
	return c.comment, nil
}

func (c *fakeBoardClient) ToggleReaction(ctx context.Context, cardID, commentID, emoji string) (json.RawMessage, error) {
	c.reactions = append(c.reactions, reactionCall{cardID: cardID, commentID: commentID, emoji: emoji})
	return mustRawJSON(map[string]any{"ok": true}), nil
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
	auth               executor.AuthStatus
	result             executor.Result
	checkAuthCalled    bool
	executeCount       int
	lastPromptRequest  prompt.Request
	lastExecuteRequest executor.Request
}

func (e *fakeExecutor) CheckAuth(ctx context.Context) executor.AuthStatus {
	e.checkAuthCalled = true
	return e.auth
}

func (e *fakeExecutor) Execute(ctx context.Context, req executor.Request) executor.Result {
	e.executeCount++
	e.lastExecuteRequest = req
	return e.result
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
