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
	CommentExecution     *rules.CommentExecutionConfig
	ClaimDir             string
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
	BoardID             string
	APIURL              string
	Token               string
	AgentName           string
	Mention             string
	CWD                 string
	Timeout             time.Duration
	MaxConcurrent       int
	ExecutorType        string
	CommentExecution    *rules.CommentExecutionConfig
	ClaimDir            string
	BotID               string
	verifiedBotID       string
	InstanceID          string
	CapabilityRevision  string
	CapabilityExpiresAt time.Time
	structuredQueue     map[string][]pendingMention
	structuredInFlight  map[string]bool
	suppressedPairs     map[string]bool
	Rules               *rules.Engine
	Schedules           []rules.Schedule
	policyFingerprint   string
	Active              map[string]*ActiveSession
	pending             map[string]pendingMention
	commandClaims       map[commandDedupKey]*commandClaim
	commandQueues       map[string][]*commandClaim
	commandSeq          uint64
	commandAccessSeq    uint64
	Paused              bool
	BotCardID           string
	StartTime           time.Time

	Client            BoardClient
	Executor          executor.Interface
	Worktree          Worktree
	WebSocket         WebSocketRunner
	Reload            func(context.Context) (rules.Config, error)
	CapabilityRefresh func(context.Context)
	OrderPending      func()

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
	// cleanupProcessStopped exposes the stop/cancel ordering to the Unix
	// cleanup regression test while the manager lock is held.
	cleanupProcessStopped func(*ActiveSession)
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
		BoardID:            cfg.BoardID,
		APIURL:             cfg.APIURL,
		Token:              cfg.Token,
		AgentName:          cfg.AgentName,
		Mention:            "@" + cfg.AgentName,
		CWD:                cwd,
		Timeout:            timeout,
		MaxConcurrent:      maxConcurrent,
		ExecutorType:       executorType,
		CommentExecution:   cfg.CommentExecution,
		ClaimDir:           cfg.ClaimDir,
		structuredQueue:    map[string][]pendingMention{},
		structuredInFlight: map[string]bool{},
		suppressedPairs:    map[string]bool{},
		Rules:              ruleEngine,
		Schedules:          cfg.Schedules,
		policyFingerprint:  policyFingerprint,
		Active:             map[string]*ActiveSession{},
		pending:            map[string]pendingMention{},
		commandClaims:      map[commandDedupKey]*commandClaim{},
		commandQueues:      map[string][]*commandClaim{},
		StartTime:          time.Now().UTC(),
		Client:             cfg.Client,
		Executor:           cfg.Executor,
		Worktree:           cfg.Worktree,
		WebSocket:          cfg.WebSocket,
		Reload:             cfg.Reload,
		sem:                make(chan struct{}, maxConcurrent),
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
		if session.Cancel != nil {
			session.Stopping = true
			session.Cancel()
		}
		stopSessionProcess(session)
		if session.Stream != nil {
			_ = session.Stream.Close()
		}
		delete(m.Active, cardID)
	}
	m.pending = map[string]pendingMention{}
	m.structuredQueue = map[string][]pendingMention{}
	m.structuredInFlight = map[string]bool{}
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
	selection, err := parseMentionDispatch(content, m.Mention)
	if err != nil {
		return m.processMention(ctx, cardID, commentID, content, authorName, nil, nil)
	}
	m.mu.Lock()
	if m.CommentExecution != nil {
		if selection.Model == "" {
			selection.Model = m.CommentExecution.Defaults.Model
			if selection.Model != "" {
				selection.ModelSource = "yaml"
			}
		}
		if selection.ReasoningEffort == "" {
			selection.ReasoningEffort = m.CommentExecution.Defaults.Effort
			if selection.ReasoningEffort != "" {
				selection.EffortSource = "yaml"
			}
		}
	}
	m.mu.Unlock()
	if selection.ModelSource == "" {
		selection.ModelSource = "cli_unknown"
	}
	if selection.EffortSource == "" {
		selection.EffortSource = "cli_unknown"
	}
	return m.processMention(ctx, cardID, commentID, content, authorName, &selection, nil)
}

func (m *Manager) ProcessStructuredMention(ctx context.Context, cardID, commentID string, selection mentionDispatch, authorName, acceptedRevision string, acceptedModels ...[]api.ExecutionModel) error {
	snapshot := structuredSnapshot{Revision: acceptedRevision}
	if len(acceptedModels) > 0 {
		snapshot.Models = acceptedModels[0]
	}
	return m.processStructuredMentionSnapshot(ctx, cardID, commentID, selection, authorName, snapshot)
}

func (m *Manager) processStructuredMentionSnapshot(ctx context.Context, cardID, commentID string, selection mentionDispatch, authorName string, snapshot structuredSnapshot) error {
	claim, created, err := m.createOrReadMentionClaim(cardID, commentID, authorName, selection, snapshot)
	if err != nil {
		return err
	}
	if replayIntakeOnly(ctx) {
		return nil
	}
	if !created && claim.State == "outcome" {
		return m.reconcileMentionOutcome(ctx, claim)
	}
	if !created && claim.State != "accepted" {
		if claim.State == "started" {
			m.mu.Lock()
			active := m.structuredInFlight[commentID]
			m.mu.Unlock()
			if active {
				return nil
			}
			body := "**Execution claim uncertain**: prior execution may have started under instance " + claim.OwnerID + "; inspect the card and claim before any retry.\n\n" + requesterMention(claim.AuthorName)
			if publisher, ok := m.Client.(interface {
				AddCommentIdempotent(context.Context, string, string, string) (json.RawMessage, error)
			}); ok {
				_, err := publisher.AddCommentIdempotent(ctx, cardID, body, "execution-uncertain:"+claim.BoardID+":"+claim.CardID+":"+claim.CommentID)
				return err
			}
			_, err := m.Client.AddCommentOnce(ctx, cardID, body)
			return err
		}
		return nil
	}
	// The persisted snapshot wins on redelivery, including after a config reload.
	selection = mentionDispatch{Content: claim.Content, Model: claim.Model, ReasoningEffort: claim.Effort, ModelSource: claim.ModelSource, EffortSource: claim.EffortSource}
	if !modelsContainPair(claim.AcceptedModels, claim.Model, claim.Effort) {
		if err := m.setMentionClaimState(claim, "needs_review"); err != nil {
			return err
		}
		return m.rejectStructuredComment(ctx, cardID, authorName, fmt.Errorf("execution_request: resolved selection is outside accepted_models snapshot"))
	}
	m.mu.Lock()
	currentRevision, botID, instanceID := m.CapabilityRevision, m.BotID, m.InstanceID
	m.mu.Unlock()
	if currentRevision == "" || (created && currentRevision != snapshot.Revision) || !m.verifiedPair(selection.Model, selection.ReasoningEffort) {
		return m.rejectStructuredComment(ctx, cardID, authorName, fmt.Errorf("capability_revision: accepted request is held until a compatible sole v1 reader registers; redeliver the same comment after refresh"))
	}
	if reader, ok := m.Client.(interface {
		GetExecutionCapabilities(context.Context, string, string) (api.ExecutionCapabilityView, error)
	}); ok {
		view, err := reader.GetExecutionCapabilities(ctx, botID, m.BoardID)
		if err != nil || view.Revision != currentRevision || view.InstanceID != instanceID || view.BoardID != m.BoardID || view.Executor != m.ExecutorType || !view.ExpiresAt.After(time.Now()) {
			return m.rejectStructuredComment(ctx, cardID, authorName, fmt.Errorf("capability_revision: no fresh sole v1 reader registration; accepted request is held"))
		}
	}
	return m.processMention(ctx, cardID, commentID, selection.Content, claim.AuthorName, &selection, &claim)
}

func (m *Manager) processMention(ctx context.Context, cardID, commentID, content, authorName string, frozen *mentionDispatch, claim *mentionClaim) error {
	if claim != nil {
		m.mu.Lock()
		if m.structuredInFlight[commentID] {
			m.mu.Unlock()
			return nil
		}
		m.structuredInFlight[commentID] = true
		if _, active := m.Active[cardID]; active {
			m.structuredQueue[cardID] = append(m.structuredQueue[cardID], pendingMention{ctx: ctx, cardID: cardID, commentID: commentID, content: content, authorName: authorName, frozen: frozen, claim: claim})
			m.mu.Unlock()
			return nil
		}
		m.mu.Unlock()
	}
	if claim == nil && m.queueMentionIfActive(ctx, cardID, commentID, content, authorName, frozen) {
		return nil
	}
	if err := m.acquire(ctx); err != nil {
		if claim != nil {
			m.mu.Lock()
			delete(m.structuredInFlight, claim.CommentID)
			m.mu.Unlock()
		}
		return err
	}

	execCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	session := &ActiveSession{CardID: cardID, CommentID: commentID, Cancel: cancel, Done: make(chan struct{}), StructuredClaim: claim}
	m.mu.Lock()
	_, exists := m.Active[cardID]
	if exists {
		if claim != nil {
			m.structuredQueue[cardID] = append(m.structuredQueue[cardID], pendingMention{ctx: ctx, cardID: cardID, commentID: commentID, content: content, authorName: authorName, frozen: frozen, claim: claim})
		} else if m.pending == nil {
			m.pending = map[string]pendingMention{}
		}
		if claim == nil {
			m.pending[cardID] = pendingMention{
				ctx:        ctx,
				cardID:     cardID,
				commentID:  commentID,
				content:    content,
				authorName: authorName,
				frozen:     frozen,
				claim:      claim,
			}
		}
	}
	if !exists {
		m.Active[cardID] = session
	}
	m.mu.Unlock()
	if exists {
		cancel()
		m.release()
		if claim == nil {
			m.addReaction(ctx, cardID, commentID, "👀")
		}
		return nil
	}
	if claim == nil {
		m.addReaction(ctx, cardID, commentID, "👀")
	}
	return m.processClaimedMention(ctx, execCtx, session, content, authorName, frozen, claim)
}

func (m *Manager) processClaimedMention(ctx, execCtx context.Context, session *ActiveSession, content, authorName string, frozen *mentionDispatch, claim *mentionClaim) (runErr error) {
	phase := "validating dispatch selection"
	terminalHandled := false
	defer func() {
		if claim != nil && m.sessionStopping(session) {
			_ = m.holdUnstartedClaim(*claim)
		}
		if claim != nil {
			m.mu.Lock()
			delete(m.structuredInFlight, claim.CommentID)
			m.mu.Unlock()
		}
		// Preparation and card-loading failures happen before the executor can
		// produce a reply. Publish here so queued mentions get the same outcome.
		if !terminalHandled && ctx.Err() == nil && (runErr != nil || errors.Is(execCtx.Err(), context.DeadlineExceeded)) {
			failure := runErr
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				failure = fmt.Errorf("request timed out while %s", phase)
			}
			if !errors.Is(failure, context.Canceled) {
				if claim != nil {
					publicationCtx, cancelPublication := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					if err := m.publishStructuredHold(publicationCtx, *claim, phase, failure); err != nil {
						runErr = errors.Join(runErr, err)
					}
					cancelPublication()
				} else {
					m.postMentionFailure(session, authorName, phase, failure)
				}
			}
		}
		session.Cancel()
		m.finishActiveSession(session)
		m.release()
	}()
	if claim != nil {
		unlock, acquired, err := m.lockMentionClaim(claim.CardID, claim.CommentID)
		if err != nil {
			return err
		}
		if !acquired {
			return nil
		}
		defer unlock()
		current, err := m.readMentionClaim(claim.CardID, claim.CommentID)
		if err != nil {
			return err
		}
		if current.State != "accepted" || current.TargetBotID != claim.TargetBotID {
			return nil
		}
		claim = &current
	}
	selection := mentionDispatch{}
	var selectionErr error
	if frozen != nil {
		selection = *frozen
	} else {
		selection, selectionErr = parseMentionDispatch(content, m.Mention)
	}
	if selectionErr == nil && selection.Model != "" && m.ExecutorType != "codex" && (selection.ModelSource == "directive" || m.ExecutorType != "claude") {
		selectionErr = fmt.Errorf("explicit model selection requires the codex executor; current executor is %q", m.ExecutorType)
	}
	if selectionErr != nil {
		terminalHandled = true
		m.addReaction(ctx, session.CardID, session.CommentID, "🛑")
		_, _ = m.Client.AddCommentOnce(ctx, session.CardID, "**Dispatch selection error**: "+selectionErr.Error()+"\n\n"+requesterMention(authorName))
		return nil
	}
	if claim != nil {
		if err := m.verifyCurrentClaim(execCtx, *claim); err != nil {
			return err
		}
	}

	phase = "checking authentication"
	auth := m.Executor.CheckAuth(execCtx)
	if execCtx.Err() != nil {
		return execCtx.Err()
	}
	if !auth.Authenticated {
		terminalHandled = true
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
	phase = "preparing the workspace"
	path, err := m.selectWorktree(execCtx, session.CardID, rules.ExecutionPrepare)
	if err != nil {
		return err
	}
	if path != "" {
		worktreePath = path
	}

	session.WorktreePath = worktreePath

	phase = "loading the card"
	cardMarkdown, err := m.Client.GetCardMarkdown(execCtx, session.CardID)
	if err != nil {
		return err
	}
	command := m.Executor.ExtractCommand(selection.Content, m.Mention)
	promptText := m.Executor.BuildPrompt(executor.PromptRequest{
		CardID:         session.CardID,
		CardMarkdown:   cardMarkdown,
		Command:        command,
		CommentContent: selection.Content,
		AuthorName:     authorName,
		BoardID:        m.BoardID,
		CWD:            worktreePath,
	})
	promptText = m.withBranchContext(execCtx, session.CardID, worktreePath, promptText)

	phase = "running the request"
	if claim != nil {
		if err := execCtx.Err(); err != nil {
			return err
		}
		if err := m.verifyCurrentClaim(execCtx, *claim); err != nil {
			return err
		}
		inDone, err := m.structuredCardInDone(execCtx, claim.CardID)
		if err != nil {
			return err
		}
		if inDone {
			terminalHandled = true
			if err := m.setMentionClaimState(*claim, "needs_review"); err != nil {
				return err
			}
			return m.rejectStructuredComment(ctx, claim.CardID, claim.AuthorName, fmt.Errorf("accepted request held: card entered Done before execution"))
		}
		owner, err := m.claimOnWeb(execCtx, *claim)
		if err != nil {
			terminalHandled = true
			var apiErr *api.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 409 && apiErr.Code == "CLAIM_ORDER_PENDING" {
				// Web has not granted this first claim. Keep the accepted frozen
				// selection and replay from the beginning before another attempt.
				if m.OrderPending != nil {
					m.OrderPending()
				}
				return nil
			}
			if errors.As(err, &apiErr) && apiErr.StatusCode == 409 && apiErr.Code == "CLAIM_REJECTED" {
				// The trusted rejection marker is already terminal on Web. Do not
				// create a claim, receipt, or another error comment for this target.
				claim.State = "terminal"
				claim.OutcomeStatus = "rejected"
				return m.saveMentionClaim(*claim)
			}
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 && apiErr.Code == "NOT_FOUND" {
				// A moved card can later return to this board. Keep the accepted
				// claim and frozen selection for a later canonical replay, without
				// posting an error on a card that may now be on another board.
				return fmt.Errorf("structured request %s is currently unavailable on this board; retry after replay: %w", claim.CommentID, err)
			}
			if errors.As(err, &apiErr) && apiErr.Code == "CLAIM_UNCERTAIN" {
				if saveErr := m.setMentionClaimState(*claim, "needs_review"); saveErr != nil {
					return saveErr
				}
			}
			return m.rejectStructuredComment(ctx, claim.CardID, claim.AuthorName, err)
		}
		if owner.Status != "claimed" {
			terminalHandled = true
			if owner.Status == "completed" || owner.Status == "failed" {
				return m.setMentionClaimState(*claim, "terminal")
			}
			return m.rejectStructuredComment(ctx, claim.CardID, claim.AuthorName, fmt.Errorf("claim status %q requires operator reconciliation", owner.Status))
		}
		claim.OwnerID = owner.InstanceID
		if err := execCtx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		ownsCard := m.Active[session.CardID] == session && !session.Stopping
		m.mu.Unlock()
		if !ownsCard {
			return fmt.Errorf("structured request claimed but card ownership changed before spawn")
		}
		claim.State = "started"
		if err := m.saveMentionClaim(*claim); err != nil {
			return fmt.Errorf("persist execution claim: %w", err)
		}
		m.addReaction(ctx, session.CardID, session.CommentID, "👀")
	}
	result := m.Executor.Execute(execCtx, executor.Request{
		CardID:          session.CardID,
		BoardID:         m.BoardID,
		Prompt:          promptText,
		CWD:             worktreePath,
		Model:           selection.Model,
		ReasoningEffort: selection.ReasoningEffort,
		OnChunk:         m.makeOnChunk(session.CardID),
	})
	// Execute has returned: its result is known even if its deadline or the
	// socket context expired. Finish durable bookkeeping under a bounded context.
	publicationCtx := execCtx
	if claim != nil && execCtx.Err() != nil {
		var cancelPublication context.CancelFunc
		publicationCtx, cancelPublication = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancelPublication()
	}
	if !result.Success && m.withdrawRejectedPair(selection.Model, selection.ReasoningEffort, result.Error) && m.CapabilityRefresh != nil {
		m.CapabilityRefresh(publicationCtx)
	}
	if claim == nil && execCtx.Err() != nil {
		return nil
	}
	terminalHandled = true

	m.mu.Lock()
	if m.Active[session.CardID] == session {
		session.SessionID = result.SessionID
	}
	m.mu.Unlock()

	if result.Success {
		if claim != nil {
			return m.finishStructuredResult(publicationCtx, *claim, result, worktreePath, execCtx.Err() == nil && !m.sessionStopping(session))
		}
		if err := m.completeSuccessfulResult(execCtx, session.CardID, session.CommentID, result, authorName, worktreePath, selection.Model, selection.ReasoningEffort); err != nil {
			return err
		}
		return nil
	}

	m.addReaction(publicationCtx, session.CardID, session.CommentID, "🛑")
	message := buildErrorComment(result, "Error") + "\n\n" + requesterMention(authorName)
	if claim != nil {
		claim.OutcomeStatus = "failed"
		claim.OutcomeBody = message
		claim.OutcomeMessage = truncateUTF8(result.Error, 1000)
		claim.State = "outcome"
		if err := m.saveMentionClaim(*claim); err != nil {
			return err
		}
		return m.publishMentionOutcome(publicationCtx, *claim)
	}
	_, _ = m.Client.AddComment(ctx, session.CardID, message)
	return nil
}

func (m *Manager) postMentionFailure(session *ActiveSession, authorName, phase string, err error) {
	// A job deadline must not cancel its failure notification.
	ctx, cancel := context.WithTimeout(context.Background(), commandPublishWait)
	defer cancel()
	m.addReaction(ctx, session.CardID, session.CommentID, "🛑")
	diagnostic := redactCleanupDiagnostic(err.Error(), m.Token)
	message := "**Unable to complete request**\n\nThe request failed while " + phase +
		". It was not marked successful.\n\n```\n" + diagnostic + "\n```\n\n" + requesterMention(authorName)
	_, _ = m.Client.AddCommentOnce(ctx, session.CardID, message)
}

type pendingMention struct {
	ctx        context.Context
	cardID     string
	commentID  string
	content    string
	authorName string
	frozen     *mentionDispatch
	claim      *mentionClaim
}

func (m *Manager) queueMentionIfActive(ctx context.Context, cardID, commentID, content, authorName string, frozen ...*mentionDispatch) bool {
	m.mu.Lock()
	_, active := m.Active[cardID]
	if active {
		var selection *mentionDispatch
		if len(frozen) > 0 {
			selection = frozen[0]
		}
		if m.pending == nil {
			m.pending = map[string]pendingMention{}
		}
		m.pending[cardID] = pendingMention{
			ctx:        ctx,
			cardID:     cardID,
			commentID:  commentID,
			content:    content,
			authorName: authorName,
			frozen:     selection,
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
	if session.Cleanup {
		m.mu.Unlock()
		if stream != nil {
			_ = stream.Close()
		}
		return
	}
	pending, queued := pendingMention{}, false
	if queue := m.structuredQueue[session.CardID]; len(queue) > 0 {
		pending, queued = queue[0], true
		if len(queue) == 1 {
			delete(m.structuredQueue, session.CardID)
		} else {
			m.structuredQueue[session.CardID] = queue[1:]
		}
	} else {
		pending, queued = m.pending[session.CardID]
		delete(m.pending, session.CardID)
	}
	var pendingSession *ActiveSession
	var pendingExecCtx context.Context
	var pendingCancel context.CancelFunc
	if queued {
		pendingExecCtx, pendingCancel = context.WithTimeout(pending.ctx, m.Timeout)
		pendingSession = &ActiveSession{CardID: pending.cardID, CommentID: pending.commentID, Cancel: pendingCancel, Done: make(chan struct{}), StructuredClaim: pending.claim}
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
			if pending.claim != nil && m.sessionStopping(pendingSession) {
				_ = m.holdUnstartedClaim(*pending.claim)
			}
			if pending.claim != nil {
				m.mu.Lock()
				delete(m.structuredInFlight, pending.commentID)
				m.mu.Unlock()
			}
			if pending.ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				m.postMentionFailure(pendingSession, pending.authorName, "waiting for an execution slot", err)
			}
			pendingSession.Cancel()
			m.finishActiveSession(pendingSession)
			return
		}
		_ = m.processClaimedMention(pending.ctx, pendingExecCtx, pendingSession, pending.content, pending.authorName, pending.frozen, pending.claim)
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
	if current != nil && current.Cancel != nil {
		current.Stopping = true
		current.Cancel()
	}
	stopSessionProcess(current)
	var stream api.StreamConn
	if current != nil {
		stream = current.Stream
		current.Stream = nil
		current.Streaming = false
	}
	delete(m.pending, cardID)
	heldStructured := m.drainStructuredLocked(cardID)
	canceledCommands := m.drainCommandsLocked(cardID)
	m.Active[cardID] = cleanup
	previousDone := currentDone(current)
	m.mu.Unlock()

	if stream != nil {
		_ = stream.Close()
	}
	if current != nil && current.StructuredClaim != nil {
		_ = m.holdUnstartedClaim(*current.StructuredClaim)
	}
	m.reportHeldStructured(ctx, cardID, heldStructured)
	for range canceledCommands {
		_, _ = m.Client.AddComment(ctx, cardID, "**Command canceled**\n\nThe card entered Done before this queued command could run.")
	}
	return cleanup, previousDone
}

func (m *Manager) holdQueuedStructuredForDone(ctx context.Context, cardID string) {
	m.mu.Lock()
	held := m.drainStructuredLocked(cardID)
	m.mu.Unlock()
	m.reportHeldStructured(ctx, cardID, held)
}

// drainStructuredLocked removes queued requests while card ownership is held.
func (m *Manager) drainStructuredLocked(cardID string) []pendingMention {
	held := m.structuredQueue[cardID]
	delete(m.structuredQueue, cardID)
	for _, pending := range held {
		delete(m.structuredInFlight, pending.commentID)
	}
	return held
}

func (m *Manager) reportHeldStructured(ctx context.Context, cardID string, held []pendingMention) {
	for _, pending := range held {
		if pending.claim != nil {
			_ = m.setMentionClaimState(*pending.claim, "needs_review")
		}
		_, _ = m.Client.AddCommentOnce(ctx, cardID, "**Structured request held**: card entered Done before queued comment "+pending.commentID+" could run. Inspect before retrying.")
	}
}

func currentDone(session *ActiveSession) <-chan struct{} {
	if session == nil {
		return nil
	}
	return session.Done
}

func (m *Manager) sessionStopping(session *ActiveSession) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return session.Stopping
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

func (m *Manager) completeSuccessfulResult(ctx context.Context, cardID, commentID string, result executor.Result, authorName, worktreePath, model, effort string) error {
	if strings.TrimSpace(result.ResultText) != "" {
		m.publishTerminalSummary(ctx, cardID, commentID, result.ResultText, authorName)
		return nil
	}
	if result.SessionID != "" {
		return m.resumeToPublish(ctx, cardID, commentID, result.SessionID, authorName, worktreePath, model, effort)
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

func (m *Manager) resumeToPublish(ctx context.Context, cardID, commentID, sessionID, authorName, worktreePath, model, effort string) error {
	resumePrompt := `The previous execution completed without a final response.

Do not do any new work. Return the concise terminal summary normally so the agent manager can publish it. Do not call kardbrd comment add.`

	result := m.Executor.Execute(ctx, executor.Request{
		CardID:          cardID,
		BoardID:         m.BoardID,
		Prompt:          resumePrompt,
		ResumeSessionID: sessionID,
		CWD:             worktreePath,
		Model:           model,
		ReasoningEffort: effort,
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
	if m.CommentExecution != nil && cfg.CommentExecution != nil && m.CommentExecution.Verification.VerifiedAt != cfg.CommentExecution.Verification.VerifiedAt {
		m.suppressedPairs = map[string]bool{}
	}
	m.Rules = &rules.Engine{Rules: append([]rules.Rule(nil), cfg.Rules...)}
	m.Schedules = append([]rules.Schedule(nil), cfg.Schedules...)
	m.CommentExecution = cfg.CommentExecution
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

func (m *Manager) withBranchContext(ctx context.Context, cardID, path, promptText string) string {
	if provider, ok := m.Worktree.(interface {
		BranchContext(context.Context, string, string) string
	}); ok {
		if advisory := provider.BranchContext(ctx, cardID, path); advisory != "" {
			return promptText + "\n\n## Worktree branch context\n\n" + advisory
		}
	}
	return promptText
}
