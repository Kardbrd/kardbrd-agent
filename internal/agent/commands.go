package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

const (
	commandQueueLimit = 8
	commandDedupTTL   = 24 * time.Hour
	commandDedupLimit = 4096
)

type commandDedupKey struct {
	BoardID string
	CardID  string
	Comment string
	Command string
}

type commandClaim struct {
	key        commandDedupKey
	sequence   uint64
	rule       rules.Rule
	message    map[string]any
	context    context.Context
	state      string
	terminalAt time.Time
}

// ProcessCommentCommand reserves an exact command before it can execute or
// queue. A redelivery finds the same claim and never performs a second action
// or acknowledgement.
func (m *Manager) ProcessCommentCommand(ctx context.Context, cardID string, rule rules.Rule, message map[string]any) error {
	if err := m.enrichRuleMessage(ctx, message); err != nil {
		return err
	}
	if !rules.RuleMatches(rule, "comment_created", message) {
		if m.reserveTerminalCommand(cardID, rule, message, "denied") {
			_, _ = m.Client.AddComment(ctx, cardID, "**Command denied**\n\nThis command is not authorized by its configured rule.")
		}
		return nil
	}
	claim, session, queuePosition, rejected := m.reserveCommand(ctx, cardID, rule, message)
	if claim == nil {
		return nil
	}
	if queuePosition > 0 {
		_, _ = m.Client.AddComment(ctx, cardID, fmt.Sprintf("**Command queued** (position %d)", queuePosition))
		return nil
	}
	if rejected != "" {
		_, _ = m.Client.AddComment(ctx, cardID, "**Command rejected**\n\n"+rejected)
		return nil
	}
	if err := m.runCommand(session, claim); err != nil {
		m.postCommandFailure(ctx, cardID, err)
	}
	return nil
}

func (m *Manager) reserveTerminalCommand(cardID string, rule rules.Rule, message map[string]any, state string) bool {
	key := commandDedupKey{BoardID: m.BoardID, CardID: cardID, Comment: stringField(message, "comment_id"), Command: strings.ToLower(rule.CommentCommand)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneCommandClaimsLocked(time.Now())
	if _, exists := m.commandClaims[key]; exists {
		return false
	}
	m.commandSeq++
	m.commandClaims[key] = &commandClaim{key: key, sequence: m.commandSeq, rule: cloneCommandRule(rule), message: cloneMessage(message), state: state, terminalAt: time.Now()}
	return true
}

func (m *Manager) reserveCommand(ctx context.Context, cardID string, rule rules.Rule, message map[string]any) (*commandClaim, *ActiveSession, int, string) {
	key := commandDedupKey{BoardID: m.BoardID, CardID: cardID, Comment: stringField(message, "comment_id"), Command: strings.ToLower(rule.CommentCommand)}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneCommandClaimsLocked(time.Now())
	if _, exists := m.commandClaims[key]; exists {
		return nil, nil, 0, ""
	}
	m.commandSeq++
	claim := &commandClaim{key: key, sequence: m.commandSeq, rule: cloneCommandRule(rule), message: cloneMessage(message), context: ctx}
	m.commandClaims[key] = claim
	if current := m.Active[cardID]; current != nil {
		if current.Cleanup {
			claim.state = "canceled_done"
			claim.terminalAt = time.Now()
			return claim, nil, 0, "The card is being cleaned up after entering Done."
		}
		queue := m.commandQueues[cardID]
		if len(queue) >= commandQueueLimit {
			claim.state = "rejected_full"
			claim.terminalAt = time.Now()
			return claim, nil, 0, "The command queue is full; submit a later explicit command to retry."
		}
		claim.state = "queued"
		m.commandQueues[cardID] = append(queue, claim)
		// Capture the position under the reservation lock. A later arrival must
		// not change this command's visible FIFO acknowledgement.
		return claim, nil, len(queue) + 1, ""
	}
	claim.state = "running"
	session := m.newCommandSession(claim)
	m.Active[cardID] = session
	return claim, session, 0, ""
}

func (m *Manager) newCommandSession(claim *commandClaim) *ActiveSession {
	parent := claim.context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &ActiveSession{CardID: claim.key.CardID, CommentID: claim.key.Comment, Context: ctx, Cancel: cancel, Done: make(chan struct{}), Command: claim}
}

func (m *Manager) runCommand(session *ActiveSession, claim *commandClaim) (runErr error) {
	if err := m.acquire(session.Context); err != nil {
		m.finishCommand(claim, "failed")
		m.finishActiveSession(session)
		return err
	}
	defer m.release()
	actionCtx, deadlineCancel := context.WithTimeout(session.Context, m.Timeout)
	defer deadlineCancel()
	defer func() {
		if actionCtx.Err() != nil && session.Context.Err() != nil {
			m.finishCommand(claim, "canceled_done")
		} else if runErr != nil {
			m.finishCommand(claim, "failed")
		} else {
			m.finishCommand(claim, "succeeded")
		}
		session.Cancel()
		m.finishActiveSession(session)
	}()

	if err := m.recheckCommand(actionCtx, claim); err != nil {
		m.finishCommand(claim, "failed")
		_, _ = m.Client.AddComment(context.Background(), claim.key.CardID, "**Command rejected**\n\n"+err.Error())
		return nil
	}
	return m.executeRule(actionCtx, session, claim.rule, claim.message, true, claim.rule.Execution)
}

func (m *Manager) recheckCommand(ctx context.Context, claim *commandClaim) error {
	raw, err := m.Client.GetCard(ctx, claim.key.CardID)
	if err != nil {
		return fmt.Errorf("could not recheck current card state")
	}
	var card struct {
		Title string `json:"title"`
		List  *struct {
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &card); err != nil || card.List == nil || card.List.Name == "" {
		return errors.New("could not recheck current card state")
	}
	if strings.EqualFold(card.List.Name, "done") {
		return errors.New("the card is in Done and cannot run this command")
	}
	message := cloneMessage(claim.message)
	// The claim intentionally keeps the original rule/action snapshot, but
	// authorization itself is evaluated against current card state. Drop all
	// event-derived card fields so enrichment cannot reuse stale labels or
	// assignee data from the queued delivery.
	message["list_name"] = card.List.Name
	message["card_title"] = card.Title
	delete(message, "card_labels")
	delete(message, "card_assignee_id")
	delete(message, "card_assignee_is_bot")
	if err := m.enrichRuleMessage(ctx, message); err != nil {
		return errors.New("could not recheck current command authorization")
	}
	if !rules.RuleMatches(claim.rule, "comment_created", message) {
		return errors.New("the command is no longer authorized by its configured rule")
	}
	return nil
}

func (m *Manager) executeRule(ctx context.Context, session *ActiveSession, rule rules.Rule, message map[string]any, publishResult bool, policy rules.ExecutionPolicy) error {
	auth := m.Executor.CheckAuth(ctx)
	if !auth.Authenticated {
		if !publishResult {
			return errors.New("executor authentication failed")
		}
		_, _ = m.Client.AddComment(ctx, session.CardID, "**Automation Error** ("+rule.Name+")\n\nAgent not authenticated: `"+auth.Error+"`")
		return nil
	}

	worktreePath := m.CWD
	path, err := m.selectWorktree(ctx, session.CardID, policy)
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
	promptText := m.Executor.BuildPrompt(executor.PromptRequest{
		CardID:         session.CardID,
		CardMarkdown:   cardMarkdown,
		Command:        rule.Action,
		CommentContent: "[Automation: " + rule.Name + "]",
		AuthorName:     "automation",
		BoardID:        m.BoardID,
		CWD:            worktreePath,
	})
	result := m.Executor.Execute(ctx, executor.Request{CardID: session.CardID, BoardID: m.BoardID, Prompt: promptText, CWD: worktreePath, Model: rule.ModelID(), OnChunk: m.makeOnChunk(session.CardID)})
	if ctx.Err() != nil {
		return nil
	}
	if result.Success {
		if !publishResult {
			return nil
		}
		return m.completeSuccessfulResult(ctx, session.CardID, session.CommentID, result, "automation", worktreePath)
	}
	if !publishResult {
		return errors.New("executor failed")
	}
	_, _ = m.Client.AddComment(ctx, session.CardID, buildErrorComment(result, "Automation Error ("+rule.Name+")"))
	return nil
}

func (m *Manager) finishCommand(claim *commandClaim, state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !claim.terminalAt.IsZero() {
		return
	}
	claim.state = state
	claim.terminalAt = time.Now()
}

func (m *Manager) postCommandFailure(ctx context.Context, cardID string, err error) {
	diagnostic := redactCleanupDiagnostic(err.Error(), m.Token)
	_, _ = m.Client.AddComment(ctx, cardID, "**Command Error**\n\n```\n"+diagnostic+"\n```")
}

func (m *Manager) commandQueueSnapshot(cardID string) []*commandClaim {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*commandClaim(nil), m.commandQueues[cardID]...)
}

func (m *Manager) nextCommandLocked(cardID string) *commandClaim {
	queue := m.commandQueues[cardID]
	if len(queue) == 0 {
		return nil
	}
	claim := queue[0]
	if len(queue) == 1 {
		delete(m.commandQueues, cardID)
	} else {
		m.commandQueues[cardID] = queue[1:]
	}
	claim.state = "running"
	return claim
}

func (m *Manager) drainCommandsLocked(cardID string) []*commandClaim {
	queued := m.commandQueues[cardID]
	delete(m.commandQueues, cardID)
	for _, claim := range queued {
		claim.state = "canceled_done"
		claim.terminalAt = time.Now()
	}
	return queued
}

func (m *Manager) pruneCommandClaimsLocked(now time.Time) {
	terminal := make([]*commandClaim, 0)
	for key, claim := range m.commandClaims {
		if !claim.terminalAt.IsZero() {
			if now.Sub(claim.terminalAt) > commandDedupTTL {
				delete(m.commandClaims, key)
				continue
			}
			terminal = append(terminal, claim)
		}
	}
	for len(m.commandClaims) > commandDedupLimit && len(terminal) > 0 {
		oldest := 0
		for index := range terminal {
			if terminal[index].terminalAt.Before(terminal[oldest].terminalAt) {
				oldest = index
			}
		}
		delete(m.commandClaims, terminal[oldest].key)
		terminal = append(terminal[:oldest], terminal[oldest+1:]...)
	}
}

func cloneCommandRule(rule rules.Rule) rules.Rule {
	rule.Events = append([]string(nil), rule.Events...)
	rule.Assignee = append([]string(nil), rule.Assignee...)
	rule.CleanupCommand = append([]string(nil), rule.CleanupCommand...)
	return rule
}

func cloneMessage(message map[string]any) map[string]any {
	copy := make(map[string]any, len(message))
	for key, value := range message {
		copy[key] = value
	}
	return copy
}
