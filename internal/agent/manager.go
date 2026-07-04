package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

type BoardClient interface {
	GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error)
	GetCard(ctx context.Context, cardID string) (json.RawMessage, error)
	GetCardMarkdown(ctx context.Context, cardID string) (string, error)
	AddComment(ctx context.Context, cardID, content string) (json.RawMessage, error)
	GetComment(ctx context.Context, cardID, commentID string) (json.RawMessage, error)
	ToggleReaction(ctx context.Context, cardID, commentID, emoji string) (json.RawMessage, error)
	UpdateCard(ctx context.Context, cardID string, patch api.CardPatch) (json.RawMessage, error)
	CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error)
}

type Worktree interface {
	Create(cardID string) (string, error)
	Remove(cardID string, force bool) error
}

type WebSocketRunner interface {
	Run(ctx context.Context) error
}

type Config struct {
	BoardID       string
	APIURL        string
	Token         string
	AgentName     string
	CWD           string
	Timeout       time.Duration
	MaxConcurrent int
	ExecutorType  string
	Rules         *rules.Engine
	Schedules     []rules.Schedule

	Client    BoardClient
	Executor  executor.Interface
	Worktree  Worktree
	WebSocket WebSocketRunner
	Reload    func(context.Context) (rules.Config, error)
}

type Manager struct {
	BoardID       string
	APIURL        string
	Token         string
	AgentName     string
	Mention       string
	CWD           string
	Timeout       time.Duration
	MaxConcurrent int
	ExecutorType  string
	Rules         *rules.Engine
	Schedules     []rules.Schedule
	Active        map[string]*ActiveSession
	Paused        bool
	BotCardID     string
	StartTime     time.Time

	Client    BoardClient
	Executor  executor.Interface
	Worktree  Worktree
	WebSocket WebSocketRunner
	Reload    func(context.Context) (rules.Config, error)

	sem chan struct{}
	mu  sync.Mutex
}

func NewManager(cfg Config) *Manager {
	cwd := cfg.CWD
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			cwd = current
		}
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = time.Hour
	}
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent == 0 {
		maxConcurrent = 3
	}
	executorType := cfg.ExecutorType
	if executorType == "" {
		executorType = "claude"
	}
	ruleEngine := cfg.Rules
	if ruleEngine == nil {
		ruleEngine = &rules.Engine{}
	}

	return &Manager{
		BoardID:       cfg.BoardID,
		APIURL:        cfg.APIURL,
		Token:         cfg.Token,
		AgentName:     cfg.AgentName,
		Mention:       "@" + cfg.AgentName,
		CWD:           cwd,
		Timeout:       timeout,
		MaxConcurrent: maxConcurrent,
		ExecutorType:  executorType,
		Rules:         ruleEngine,
		Schedules:     cfg.Schedules,
		Active:        map[string]*ActiveSession{},
		StartTime:     time.Now().UTC(),
		Client:        cfg.Client,
		Executor:      cfg.Executor,
		Worktree:      cfg.Worktree,
		WebSocket:     cfg.WebSocket,
		Reload:        cfg.Reload,
		sem:           make(chan struct{}, maxConcurrent),
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if m.Client == nil {
		return errors.New("agent client is not configured")
	}
	if m.Executor == nil {
		return errors.New("agent executor is not configured")
	}
	if _, err := m.Client.GetBoard(ctx, m.BoardID, false); err != nil {
		return fmt.Errorf("validate board token: %w", err)
	}
	auth := m.Executor.CheckAuth(ctx)
	if !auth.Authenticated {
		if auth.Error == "" {
			auth.Error = "executor is not authenticated"
		}
		return errors.New(auth.Error)
	}
	_ = m.EnsureWizardCard(ctx)
	_ = m.EnsureBotCard(ctx)
	_ = m.RegisterSkills(ctx)
	if m.WebSocket != nil {
		return m.WebSocket.Run(ctx)
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for cardID, session := range m.Active {
		if session.Process != nil && session.Process.Process != nil {
			_ = session.Process.Process.Kill()
		}
		if session.Cancel != nil {
			session.Cancel()
		}
		if session.Stream != nil {
			_ = session.Stream.Close()
		}
		delete(m.Active, cardID)
	}
	return ctx.Err()
}

func (m *Manager) ActiveCardIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cardIDs := make([]string, 0, len(m.Active))
	for cardID := range m.Active {
		cardIDs = append(cardIDs, cardID)
	}
	return cardIDs
}

func (m *Manager) ProcessMention(ctx context.Context, cardID, commentID, content, authorName string) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()

	m.mu.Lock()
	if _, exists := m.Active[cardID]; exists {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	m.addReaction(ctx, cardID, commentID, "👀")

	auth := m.Executor.CheckAuth(ctx)
	if !auth.Authenticated {
		m.addReaction(ctx, cardID, commentID, "🛑")
		hint := auth.AuthHint
		if hint == "" {
			hint = "Check your LLM provider configuration."
		}
		message := fmt.Sprintf("**Agent not authenticated**\n\n```\n%s\n```\n\n%s\n\n@%s", auth.Error, hint, authorName)
		_, _ = m.Client.AddComment(ctx, cardID, message)
		return nil
	}

	worktreePath := m.CWD
	if m.Worktree != nil {
		path, err := m.Worktree.Create(cardID)
		if err != nil {
			return err
		}
		worktreePath = path
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	m.mu.Lock()
	m.Active[cardID] = &ActiveSession{CardID: cardID, WorktreePath: worktreePath, CommentID: commentID, Cancel: cancel}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.Active, cardID)
		m.mu.Unlock()
	}()

	cardMarkdown, err := m.Client.GetCardMarkdown(ctx, cardID)
	if err != nil {
		return err
	}
	command := m.Executor.ExtractCommand(content, m.Mention)
	promptText := m.Executor.BuildPrompt(executor.PromptRequest{
		CardID:         cardID,
		CardMarkdown:   cardMarkdown,
		Command:        command,
		CommentContent: content,
		AuthorName:     authorName,
		BoardID:        m.BoardID,
		CWD:            worktreePath,
	})

	result := m.Executor.Execute(execCtx, executor.Request{
		Prompt:  promptText,
		CWD:     worktreePath,
		OnChunk: m.makeOnChunk(cardID),
	})
	if execCtx.Err() != nil {
		return nil
	}

	m.mu.Lock()
	if session := m.Active[cardID]; session != nil {
		session.SessionID = result.SessionID
	}
	m.mu.Unlock()

	if result.Success {
		if m.hasRecentBotComment(ctx, cardID, 60*time.Second) {
			m.addReaction(ctx, cardID, commentID, "✅")
			return nil
		}
		if result.SessionID != "" {
			return m.resumeToPublish(ctx, cardID, commentID, result.SessionID, authorName, worktreePath)
		}
		m.postFallbackComment(ctx, cardID, result, authorName, commentID)
		return nil
	}

	m.addReaction(ctx, cardID, commentID, "🛑")
	message := buildErrorComment(result, "Error") + "\n\n@" + authorName
	_, _ = m.Client.AddComment(ctx, cardID, message)
	return nil
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() {
	<-m.sem
}

func (m *Manager) addReaction(ctx context.Context, cardID, commentID, emoji string) {
	if commentID == "" || m.Client == nil {
		return
	}
	_, _ = m.Client.ToggleReaction(ctx, cardID, commentID, emoji)
}

func (m *Manager) postFallbackComment(ctx context.Context, cardID string, result executor.Result, authorName, commentID string) bool {
	if result.ResultText == "" {
		m.addReaction(ctx, cardID, commentID, "⚠️")
		return false
	}
	text := result.ResultText
	const maxCommentLength = 12000
	if len(text) > maxCommentLength {
		text = text[:maxCommentLength] + "\n\n*(output truncated)*"
	}
	_, _ = m.Client.AddComment(ctx, cardID, text+"\n\n@"+authorName)
	m.addReaction(ctx, cardID, commentID, "✅")
	return true
}

func (m *Manager) hasRecentBotComment(ctx context.Context, cardID string, window time.Duration) bool {
	raw, err := m.Client.GetCard(ctx, cardID)
	if err != nil {
		return false
	}
	var card struct {
		Comments []struct {
			CreatedAt string `json:"created_at"`
			Author    struct {
				IsBot bool `json:"is_bot"`
			} `json:"author"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		return false
	}
	cutoff := time.Now().Add(-window)
	for _, comment := range card.Comments {
		if !comment.Author.IsBot || comment.CreatedAt == "" {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339, strings.Replace(comment.CreatedAt, "Z", "+00:00", 1))
		if err == nil && createdAt.After(cutoff) {
			return true
		}
	}
	return false
}

func (m *Manager) resumeToPublish(ctx context.Context, cardID, commentID, sessionID, authorName, worktreePath string) error {
	resumePrompt := fmt.Sprintf(`You completed the task but forgot to publish your response.

Please post a summary comment using:
kardbrd comment add %s "Your summary here"

End your comment by mentioning @%s.

DO NOT do any new work - just publish what you already did.`, cardID, authorName)

	result := m.Executor.Execute(ctx, executor.Request{
		Prompt:          resumePrompt,
		ResumeSessionID: sessionID,
		CWD:             worktreePath,
	})
	if result.Success {
		if result.ResultText != "" && !m.hasRecentBotComment(ctx, cardID, 60*time.Second) {
			_, _ = m.Client.AddComment(ctx, cardID, result.ResultText+"\n\n@"+authorName)
		}
		m.addReaction(ctx, cardID, commentID, "✅")
		return nil
	}
	m.addReaction(ctx, cardID, commentID, "🛑")
	_, _ = m.Client.AddComment(ctx, cardID, fmt.Sprintf("**Error resuming session**\n\n```\n%s\n```\n\n@%s", result.Error, authorName))
	return nil
}

func buildErrorComment(result executor.Result, label string) string {
	var parts []string
	if result.Error == "" {
		result.Error = "unknown error"
	}
	parts = append(parts, fmt.Sprintf("**%s**\n\n```\n%s\n```", label, result.Error))
	var details []string
	if result.ReturnCode != nil {
		details = append(details, fmt.Sprintf("**Exit code:** `%d`", *result.ReturnCode))
	}
	if len(result.Command) > 0 {
		details = append(details, fmt.Sprintf("**Command:** `%s`", result.Command[0]))
	}
	if result.DurationMS != nil {
		details = append(details, fmt.Sprintf("**Duration:** %.1fs", float64(*result.DurationMS)/1000))
	}
	if result.Stderr != "" {
		stderr := result.Stderr
		if len(stderr) > 2000 {
			stderr = stderr[:2000] + fmt.Sprintf("\n... (%d chars truncated)", len(result.Stderr)-2000)
		}
		details = append(details, "**stderr:**\n```\n"+stderr+"\n```")
	}
	if result.Logs != "" {
		details = append(details, "**Logs:**\n```\n"+result.Logs+"\n```")
	}
	if len(details) > 0 {
		parts = append(parts, strings.Join(details, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func (m *Manager) makeOnChunk(cardID string) func(content string, chunkType string) {
	sequence := 0
	return func(content string, chunkType string) {
		m.mu.Lock()
		session := m.Active[cardID]
		m.mu.Unlock()
		if session == nil || session.Stream == nil {
			return
		}
		err := api.SendStreamChunk(context.Background(), session.Stream, cardID, content, chunkType, sequence)
		if err != nil {
			m.mu.Lock()
			session.Stream = nil
			session.Streaming = false
			m.mu.Unlock()
			return
		}
		sequence++
	}
}

func (m *Manager) ApplyRulesConfig(cfg rules.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Rules = &rules.Engine{Rules: append([]rules.Rule(nil), cfg.Rules...)}
	m.Schedules = append([]rules.Schedule(nil), cfg.Schedules...)
}
