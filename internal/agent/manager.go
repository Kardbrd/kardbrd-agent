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
	"unicode/utf8"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

const streamChunkWriteTimeout = time.Second

type BoardClient interface {
	GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error)
	GetCard(ctx context.Context, cardID string) (json.RawMessage, error)
	GetCardMarkdown(ctx context.Context, cardID string) (string, error)
	AddComment(ctx context.Context, cardID, content string) (json.RawMessage, error)
	AddCommentOnce(ctx context.Context, cardID, content string) (json.RawMessage, error)
	GetComment(ctx context.Context, cardID, commentID string) (json.RawMessage, error)
	ToggleReaction(ctx context.Context, cardID, commentID, emoji string) (json.RawMessage, error)
	UpdateCard(ctx context.Context, cardID string, patch api.CardPatch) (json.RawMessage, error)
	CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error)
}

type Worktree interface {
	Remove(cardID string, force bool) error
}

type legacyWorktree interface {
	Create(cardID string) (string, error)
}

// lifecycleWorktree is optional so the legacy manager remains binary- and
// behavior-compatible when the reviewed opt-in block is absent.
type lifecycleWorktree interface {
	Prepare(ctx context.Context, cardID, boardID string) (string, error)
	ExistingOrBase(ctx context.Context, cardID, boardID string) (string, error)
}

type WebSocketRunner interface {
	Run(ctx context.Context) error
}

type Config struct {
	BoardID              string
	APIURL               string
	Token                string
	AgentName            string
	CWD                  string
	Timeout              time.Duration
	MaxConcurrent        int
	ExecutorType         string
	Rules                *rules.Engine
	Schedules            []rules.Schedule
	LifecycleFingerprint string

	Client    BoardClient
	Executor  executor.Interface
	Worktree  Worktree
	WebSocket WebSocketRunner
	Reload    func(context.Context) (rules.Config, error)
}

type Manager struct {
	BoardID           string
	APIURL            string
	Token             string
	AgentName         string
	Mention           string
	CWD               string
	Timeout           time.Duration
	MaxConcurrent     int
	ExecutorType      string
	Rules             *rules.Engine
	Schedules         []rules.Schedule
	policyFingerprint string
	Active            map[string]*ActiveSession
	pending           map[string]pendingMention
	commandClaims     map[commandDedupKey]*commandClaim
	commandQueues     map[string][]*commandClaim
	commandSeq        uint64
	Paused            bool
	BotCardID         string
	StartTime         time.Time

	Client    BoardClient
	Executor  executor.Interface
	Worktree  Worktree
	WebSocket WebSocketRunner
	Reload    func(context.Context) (rules.Config, error)

	sem chan struct{}
	mu  sync.Mutex
	// reloadMu covers the entire source-load, schedule-update, and rule-swap
	// transaction. Reload requests can originate from the bot card and the
	// filesystem watcher concurrently; neither may leave rules from one
	// candidate paired with schedules from another.
	reloadMu sync.Mutex

	// cleanupCommandStarted is a test seam for the interval after a cleanup
	// process starts and before its session process handle is published.
	cleanupCommandStarted func()
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
	policyFingerprint := cfg.LifecycleFingerprint
	if policyFingerprint == "" {
		policyFingerprint = rules.LifecycleFingerprint(rules.Config{Rules: ruleEngine.Rules})
	}

	return &Manager{
		BoardID:           cfg.BoardID,
		APIURL:            cfg.APIURL,
		Token:             cfg.Token,
		AgentName:         cfg.AgentName,
		Mention:           "@" + cfg.AgentName,
		CWD:               cwd,
		Timeout:           timeout,
		MaxConcurrent:     maxConcurrent,
		ExecutorType:      executorType,
		Rules:             ruleEngine,
		Schedules:         cfg.Schedules,
		policyFingerprint: policyFingerprint,
		Active:            map[string]*ActiveSession{},
		pending:           map[string]pendingMention{},
		commandClaims:     map[commandDedupKey]*commandClaim{},
		commandQueues:     map[string][]*commandClaim{},
		StartTime:         time.Now().UTC(),
		Client:            cfg.Client,
		Executor:          cfg.Executor,
		Worktree:          cfg.Worktree,
		WebSocket:         cfg.WebSocket,
		Reload:            cfg.Reload,
		sem:               make(chan struct{}, maxConcurrent),
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
		stopSessionProcess(session)
		if session.Cancel != nil {
			session.Cancel()
		}
		if session.Stream != nil {
			_ = session.Stream.Close()
		}
		delete(m.Active, cardID)
	}
	m.pending = map[string]pendingMention{}
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
	if m.queueMentionIfActive(ctx, cardID, commentID, content, authorName) {
		return nil
	}
	if err := m.acquire(ctx); err != nil {
		return err
	}

	execCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	session := &ActiveSession{CardID: cardID, CommentID: commentID, Cancel: cancel, Done: make(chan struct{})}
	m.mu.Lock()
	_, exists := m.Active[cardID]
	if exists {
		if m.pending == nil {
			m.pending = map[string]pendingMention{}
		}
		m.pending[cardID] = pendingMention{
			ctx:        ctx,
			cardID:     cardID,
			commentID:  commentID,
			content:    content,
			authorName: authorName,
		}
	}
	if !exists {
		m.Active[cardID] = session
	}
	m.mu.Unlock()
	if exists {
		cancel()
		m.release()
		m.addReaction(ctx, cardID, commentID, "👀")
		return nil
	}
	m.addReaction(ctx, cardID, commentID, "👀")
	return m.processClaimedMention(ctx, execCtx, session, content, authorName)
}

func (m *Manager) processClaimedMention(ctx, execCtx context.Context, session *ActiveSession, content, authorName string) error {
	defer func() {
		session.Cancel()
		m.finishActiveSession(session)
		m.release()
	}()

	auth := m.Executor.CheckAuth(ctx)
	if !auth.Authenticated {
		m.addReaction(ctx, session.CardID, session.CommentID, "🛑")
		hint := auth.AuthHint
		if hint == "" {
			hint = "Check your LLM provider configuration."
		}
		message := fmt.Sprintf("**Agent not authenticated**\n\n```\n%s\n```\n\n%s\n\n%s", auth.Error, hint, requesterMention(authorName))
		_, _ = m.Client.AddComment(ctx, session.CardID, message)
		return nil
	}

	worktreePath := m.CWD
	path, err := m.selectWorktree(execCtx, session.CardID, rules.ExecutionPrepare)
	if err != nil {
		return err
	}
	if path != "" {
		worktreePath = path
	}

	session.WorktreePath = worktreePath

	cardMarkdown, err := m.Client.GetCardMarkdown(ctx, session.CardID)
	if err != nil {
		return err
	}
	command := m.Executor.ExtractCommand(content, m.Mention)
	promptText := m.Executor.BuildPrompt(executor.PromptRequest{
		CardID:         session.CardID,
		CardMarkdown:   cardMarkdown,
		Command:        command,
		CommentContent: content,
		AuthorName:     authorName,
		BoardID:        m.BoardID,
		CWD:            worktreePath,
	})

	result := m.Executor.Execute(execCtx, executor.Request{
		CardID:  session.CardID,
		BoardID: m.BoardID,
		Prompt:  promptText,
		CWD:     worktreePath,
		OnChunk: m.makeOnChunk(session.CardID),
	})
	if execCtx.Err() != nil {
		return nil
	}

	m.mu.Lock()
	if m.Active[session.CardID] == session {
		session.SessionID = result.SessionID
	}
	m.mu.Unlock()

	if result.Success {
		return m.completeSuccessfulResult(execCtx, session.CardID, session.CommentID, result, authorName, worktreePath)
	}

	m.addReaction(ctx, session.CardID, session.CommentID, "🛑")
	message := buildErrorComment(result, "Error") + "\n\n" + requesterMention(authorName)
	_, _ = m.Client.AddComment(ctx, session.CardID, message)
	return nil
}

type pendingMention struct {
	ctx        context.Context
	cardID     string
	commentID  string
	content    string
	authorName string
}

func (m *Manager) queueMentionIfActive(ctx context.Context, cardID, commentID, content, authorName string) bool {
	m.mu.Lock()
	_, active := m.Active[cardID]
	if active {
		if m.pending == nil {
			m.pending = map[string]pendingMention{}
		}
		m.pending[cardID] = pendingMention{
			ctx:        ctx,
			cardID:     cardID,
			commentID:  commentID,
			content:    content,
			authorName: authorName,
		}
	}
	m.mu.Unlock()
	if active {
		m.addReaction(ctx, cardID, commentID, "👀")
	}
	return active
}

func (m *Manager) finishActiveSession(session *ActiveSession) {
	session.markDone()
	m.mu.Lock()
	if m.Active[session.CardID] != session {
		m.mu.Unlock()
		return
	}
	stream := session.Stream
	session.Stream = nil
	session.Streaming = false
	delete(m.Active, session.CardID)
	if !session.Cleanup {
		next := m.nextCommandLocked(session.CardID)
		if next != nil {
			nextSession := m.newCommandSession(next)
			m.Active[session.CardID] = nextSession
			m.mu.Unlock()
			if stream != nil {
				_ = stream.Close()
			}
			go func() {
				if err := m.runCommand(nextSession, next); err != nil {
					m.postCommandFailure(context.Background(), next.key.CardID, err)
				}
			}()
			return
		}
	}
	pending, queued := m.pending[session.CardID]
	delete(m.pending, session.CardID)
	if session.Cleanup {
		m.mu.Unlock()
		if stream != nil {
			_ = stream.Close()
		}
		return
	}
	var pendingSession *ActiveSession
	var pendingExecCtx context.Context
	var pendingCancel context.CancelFunc
	if queued {
		pendingExecCtx, pendingCancel = context.WithTimeout(pending.ctx, m.Timeout)
		pendingSession = &ActiveSession{CardID: pending.cardID, CommentID: pending.commentID, Cancel: pendingCancel, Done: make(chan struct{})}
		m.Active[session.CardID] = pendingSession
	}
	m.mu.Unlock()
	if stream != nil {
		_ = stream.Close()
	}
	if !queued {
		return
	}
	go func() {
		if err := m.acquire(pendingExecCtx); err != nil {
			pendingSession.Cancel()
			m.finishActiveSession(pendingSession)
			return
		}
		_ = m.processClaimedMention(pending.ctx, pendingExecCtx, pendingSession, pending.content, pending.authorName)
	}()
}

// reserveCleanup gives a Done cleanup exclusive ownership of a card before it
// waits for an execution slot. It cancels prior work without allowing that
// work's deferred completion to replace the cleanup owner.
func (m *Manager) reserveCleanup(ctx context.Context, cardID string) (*ActiveSession, <-chan struct{}) {
	execCtx, cancel := context.WithCancel(ctx)
	cleanup := &ActiveSession{CardID: cardID, Context: execCtx, Cancel: cancel, Cleanup: true, Done: make(chan struct{})}

	m.mu.Lock()
	current := m.Active[cardID]
	if current != nil && current.Cleanup {
		m.mu.Unlock()
		cancel()
		return nil, nil
	}
	stopSessionProcess(current)
	if current != nil && current.Cancel != nil {
		current.Cancel()
	}
	var stream api.StreamConn
	if current != nil {
		stream = current.Stream
		current.Stream = nil
		current.Streaming = false
	}
	delete(m.pending, cardID)
	canceledCommands := m.drainCommandsLocked(cardID)
	m.Active[cardID] = cleanup
	previousDone := currentDone(current)
	m.mu.Unlock()

	if stream != nil {
		_ = stream.Close()
	}
	for range canceledCommands {
		_, _ = m.Client.AddComment(ctx, cardID, "**Command canceled**\n\nThe card entered Done before this queued command could run.")
	}
	return cleanup, previousDone
}

func currentDone(session *ActiveSession) <-chan struct{} {
	if session == nil {
		return nil
	}
	return session.Done
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

func requesterMention(authorName string) string {
	name := strings.ReplaceAll(strings.Join(strings.Fields(authorName), " "), "@", "")
	if name == "" {
		return ""
	}
	name = truncateUTF8(name, 255)
	return "@" + name
}

func (m *Manager) completeSuccessfulResult(ctx context.Context, cardID, commentID string, result executor.Result, authorName, worktreePath string) error {
	if strings.TrimSpace(result.ResultText) != "" {
		m.publishTerminalSummary(ctx, cardID, commentID, result.ResultText, authorName)
		return nil
	}
	if result.SessionID != "" {
		return m.resumeToPublish(ctx, cardID, commentID, result.SessionID, authorName, worktreePath)
	}
	m.postEmptyResultWarning(ctx, cardID, commentID, authorName)
	return nil
}

func (m *Manager) publishTerminalSummary(ctx context.Context, cardID, commentID, text, authorName string) bool {
	const maxCommentLength = 12000
	mention := requesterMention(authorName)
	suffix := ""
	if mention != "" {
		suffix = "\n\n" + mention
	}
	const truncationMarker = "\n\n*(output truncated)*"
	maxTextLength := maxCommentLength - len(suffix)
	if len(text) > maxTextLength {
		text = truncateUTF8(text, maxTextLength-len(truncationMarker)) + truncationMarker
	}
	if _, err := m.Client.AddCommentOnce(ctx, cardID, text+suffix); err != nil {
		m.addReaction(ctx, cardID, commentID, "🛑")
		m.postTerminalPublishFailure(ctx, cardID, authorName)
		return false
	}
	m.addReaction(ctx, cardID, commentID, "✅")
	return true
}

func truncateUTF8(text string, maxLength int) string {
	if len(text) <= maxLength {
		return text
	}
	for maxLength > 0 && !utf8.ValidString(text[:maxLength]) {
		maxLength--
	}
	return text[:maxLength]
}

func (m *Manager) postTerminalPublishFailure(ctx context.Context, cardID, authorName string) {
	message := "**Unable to confirm terminal summary publication**\n\nThe final response may or may not have been saved. Check the card before retrying.\n\n" + requesterMention(authorName)
	_, _ = m.Client.AddComment(ctx, cardID, message)
}

func (m *Manager) postEmptyResultWarning(ctx context.Context, cardID, commentID, authorName string) {
	m.addReaction(ctx, cardID, commentID, "⚠️")
	_, _ = m.Client.AddComment(ctx, cardID, "**No terminal response received**\n\nThe executor completed without a final response, so this run was not marked successful.\n\n"+requesterMention(authorName))
}

func (m *Manager) resumeToPublish(ctx context.Context, cardID, commentID, sessionID, authorName, worktreePath string) error {
	resumePrompt := `The previous execution completed without a final response.

Do not do any new work. Return the concise terminal summary normally so the agent manager can publish it. Do not call kardbrd comment add.`

	result := m.Executor.Execute(ctx, executor.Request{
		CardID:          cardID,
		BoardID:         m.BoardID,
		Prompt:          resumePrompt,
		ResumeSessionID: sessionID,
		CWD:             worktreePath,
	})
	if result.Success {
		if strings.TrimSpace(result.ResultText) != "" {
			m.publishTerminalSummary(ctx, cardID, commentID, result.ResultText, authorName)
			return nil
		}
		m.postEmptyResultWarning(ctx, cardID, commentID, authorName)
		return nil
	}
	m.addReaction(ctx, cardID, commentID, "🛑")
	_, _ = m.Client.AddComment(ctx, cardID, fmt.Sprintf("**Error resuming session**\n\n```\n%s\n```\n\n%s", result.Error, requesterMention(authorName)))
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
		var stream api.StreamConn
		if session != nil {
			stream = session.Stream
		}
		m.mu.Unlock()
		if session == nil || stream == nil {
			return
		}
		streamCtx, cancel := context.WithTimeout(context.Background(), streamChunkWriteTimeout)
		err := api.SendStreamChunk(streamCtx, stream, cardID, content, chunkType, sequence)
		cancel()
		if err != nil {
			m.mu.Lock()
			if m.Active[cardID] == session {
				session.Stream = nil
				session.Streaming = false
			}
			m.mu.Unlock()
			_ = stream.Close()
			return
		}
		sequence++
	}
}

func (m *Manager) ValidateRulesConfig(cfg rules.Config) error {
	if rules.LifecycleFingerprint(cfg) != m.policyFingerprint {
		return errors.New("lifecycle and command-rule changes require an agent restart")
	}
	return nil
}

func (m *Manager) ApplyRulesConfig(cfg rules.Config) error {
	if err := m.ValidateRulesConfig(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Rules = &rules.Engine{Rules: append([]rules.Rule(nil), cfg.Rules...)}
	m.Schedules = append([]rules.Schedule(nil), cfg.Schedules...)
	return nil
}

// ReloadAndApply serializes a complete ordinary-rules reload. The configured
// loader is responsible for loading and validating its candidate and updating
// the scheduler only after validation; the engine swap happens under the same
// transaction lock.
func (m *Manager) ReloadAndApply(ctx context.Context) (rules.Config, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if m.Reload == nil {
		return rules.Config{}, errors.New("rule engine is not reloadable")
	}
	loaded, err := m.Reload(ctx)
	if err != nil {
		return rules.Config{}, err
	}
	if err := m.ApplyRulesConfig(loaded); err != nil {
		return rules.Config{}, err
	}
	return loaded, nil
}

func (m *Manager) selectWorktree(ctx context.Context, cardID string, policy rules.ExecutionPolicy) (string, error) {
	if m.Worktree == nil {
		return "", nil
	}
	if lifecycle, ok := m.Worktree.(lifecycleWorktree); ok {
		if policy == rules.ExecutionExistingOrBase {
			return lifecycle.ExistingOrBase(ctx, cardID, m.BoardID)
		}
		return lifecycle.Prepare(ctx, cardID, m.BoardID)
	}
	if policy == rules.ExecutionExistingOrBase {
		return "", errors.New("existing_or_base requires opt-in lifecycle worktree configuration")
	}
	legacy, ok := m.Worktree.(legacyWorktree)
	if !ok {
		return "", errors.New("worktree implementation cannot prepare")
	}
	return legacy.Create(cardID)
}
