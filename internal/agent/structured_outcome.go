package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
)

type executionClaimClient interface {
	ClaimExecution(context.Context, api.ExecutionClaimRequest) (api.ExecutionClaim, error)
	PutExecutionReceipt(context.Context, string, api.ExecutionReceiptRequest) (api.ExecutionClaim, error)
	AddCommentIdempotent(context.Context, string, string, string) (json.RawMessage, error)
}

func exactSelection(model, effort string) api.ExecutionSelection {
	selection := api.ExecutionSelection{}
	if model != "" {
		selection.Model = &model
	}
	if effort != "" {
		selection.Effort = &effort
	}
	return selection
}

func sameSelection(a, b api.ExecutionSelection) bool {
	return sameOptional(a.Model, b.Model) && sameOptional(a.Effort, b.Effort)
}

func sameOptional(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (m *Manager) structuredCardInDone(ctx context.Context, cardID string) (bool, error) {
	raw, err := m.Client.GetCard(ctx, cardID)
	if err != nil {
		return false, err
	}
	var card struct {
		IsArchived bool   `json:"is_archived"`
		ListName   string `json:"list_name"`
		List       struct {
			Name string `json:"name"`
		} `json:"list"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		return false, err
	}
	return card.IsArchived || strings.EqualFold(card.ListName, "Done") || strings.EqualFold(card.List.Name, "Done"), nil
}

func (m *Manager) claimOnWeb(ctx context.Context, claim mentionClaim) (api.ExecutionClaim, error) {
	if err := ctx.Err(); err != nil {
		return api.ExecutionClaim{}, err
	}
	client, ok := m.Client.(executionClaimClient)
	if !ok {
		return api.ExecutionClaim{}, errors.New("structured request is held: Web claim API is unavailable")
	}
	m.mu.Lock()
	instanceID, botID, revision, expiry := m.InstanceID, m.BotID, m.CapabilityRevision, m.CapabilityExpiresAt
	m.mu.Unlock()
	if instanceID == "" || botID != claim.TargetBotID || revision == "" || (!expiry.IsZero() && !expiry.After(time.Now())) {
		return api.ExecutionClaim{}, errors.New("structured request is held: connected owner changed")
	}
	effective := exactSelection(claim.Model, claim.Effort)
	owner, err := client.ClaimExecution(ctx, api.ExecutionClaimRequest{Version: 1, BoardID: claim.BoardID, CardID: claim.CardID, CommentID: claim.CommentID, InstanceID: instanceID, Effective: effective})
	if err != nil {
		return api.ExecutionClaim{}, fmt.Errorf("structured request claim is held for reconciliation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return api.ExecutionClaim{}, err
	}
	m.mu.Lock()
	stillCurrent := m.InstanceID == instanceID && m.BotID == botID && m.CapabilityRevision == revision && (m.CapabilityExpiresAt.IsZero() || m.CapabilityExpiresAt.After(time.Now()))
	m.mu.Unlock()
	if !stillCurrent || !m.verifiedPair(claim.Model, claim.Effort) {
		return api.ExecutionClaim{}, errors.New("structured request claimed but socket or registration changed before spawn; inspect the owning claim")
	}
	if owner.BoardID != claim.BoardID || owner.CardID != claim.CardID || owner.CommentID != claim.CommentID || owner.InstanceID != instanceID || !sameSelection(owner.Effective, effective) {
		return api.ExecutionClaim{}, errors.New("Web returned a mismatched structured execution claim")
	}
	return owner, nil
}

func (m *Manager) finishStructuredResult(ctx context.Context, claim mentionClaim, result executor.Result, worktreePath string, allowResume bool) error {
	if allowResume && strings.TrimSpace(result.ResultText) == "" && result.SessionID != "" {
		result = m.Executor.Execute(ctx, executor.Request{CardID: claim.CardID, BoardID: claim.BoardID, Prompt: `The previous execution completed without a final response.

Do not do any new work. Return the concise terminal summary normally so the agent manager can publish it. Do not call kardbrd comment add.`, ResumeSessionID: result.SessionID, CWD: worktreePath, Model: claim.Model, ReasoningEffort: claim.Effort})
	}
	claim.State = "outcome"
	if result.Success && strings.TrimSpace(result.ResultText) != "" {
		claim.OutcomeStatus = "completed"
		claim.OutcomeBody = terminalSummaryBody(result.ResultText, claim.AuthorName)
		claim.OutcomeMessage = "completed"
	} else {
		claim.OutcomeStatus = "failed"
		if result.Success {
			claim.OutcomeBody = "**No terminal response received**\n\nThe executor completed without a final response.\n\n" + requesterMention(claim.AuthorName)
			claim.OutcomeMessage = "no terminal response"
		} else {
			claim.OutcomeBody = buildErrorComment(result, "Error") + "\n\n" + requesterMention(claim.AuthorName)
			claim.OutcomeMessage = truncateUTF8(result.Error, 1000)
		}
	}
	if err := m.saveMentionClaim(claim); err != nil {
		return err
	}
	return m.publishMentionOutcome(ctx, claim)
}

func terminalSummaryBody(text, authorName string) string {
	const maxCommentLength = 12000
	mention := requesterMention(authorName)
	suffix := ""
	if mention != "" {
		suffix = "\n\n" + mention
	}
	const marker = "\n\n*(output truncated)*"
	if len(text) > maxCommentLength-len(suffix) {
		text = truncateUTF8(text, maxCommentLength-len(suffix)-len(marker)) + marker
	}
	return text + suffix
}

func (m *Manager) publishMentionOutcome(ctx context.Context, claim mentionClaim) error {
	client, ok := m.Client.(executionClaimClient)
	if !ok {
		return errors.New("structured outcome is held: Web receipt API is unavailable")
	}
	key := "execution-outcome:" + claim.BoardID + ":" + claim.CardID + ":" + claim.CommentID
	if _, err := client.AddCommentIdempotent(ctx, claim.CardID, claim.OutcomeBody, key); err != nil {
		return fmt.Errorf("structured outcome publication pending: %w", err)
	}
	response, err := client.PutExecutionReceipt(ctx, claim.CommentID, api.ExecutionReceiptRequest{Version: 1, BoardID: claim.BoardID, CardID: claim.CardID, InstanceID: claim.OwnerID, Status: claim.OutcomeStatus, Message: claim.OutcomeMessage})
	if err != nil {
		return fmt.Errorf("structured receipt pending: %w", err)
	}
	if response.BoardID != claim.BoardID || response.CardID != claim.CardID || response.CommentID != claim.CommentID || response.InstanceID != claim.OwnerID || response.Status != claim.OutcomeStatus || response.Receipt == nil || response.Receipt.Status != claim.OutcomeStatus {
		return errors.New("Web returned a mismatched structured receipt")
	}
	m.addReaction(ctx, claim.CardID, claim.CommentID, map[bool]string{true: "✅", false: "🛑"}[claim.OutcomeStatus == "completed"])
	return m.setMentionClaimState(claim, "terminal")
}

func (m *Manager) publishStructuredHold(ctx context.Context, claim mentionClaim, phase string, reason error) error {
	current, err := m.readMentionClaim(claim.CardID, claim.CommentID)
	if err != nil {
		return err
	}
	if current.State != "accepted" {
		return nil
	}
	if current.IntakeErrorBody == "" {
		current.IntakeErrorBody = "**Execution request held** while " + phase + ": " + reason.Error() + "\n\n" + requesterMention(current.AuthorName)
		if err := m.saveMentionClaim(current); err != nil {
			return err
		}
	}
	publisher, ok := m.Client.(interface {
		AddCommentIdempotent(context.Context, string, string, string) (json.RawMessage, error)
	})
	if !ok {
		return errors.New("structured hold publication pending: idempotent comment API is unavailable")
	}
	_, err = publisher.AddCommentIdempotent(ctx, current.CardID, current.IntakeErrorBody, "execution-held:"+current.BoardID+":"+current.CardID+":"+current.CommentID)
	return err
}

func (m *Manager) reconcileMentionOutcome(ctx context.Context, claim mentionClaim) error {
	unlock, acquired, err := m.lockMentionClaim(claim.CardID, claim.CommentID)
	if err != nil || !acquired {
		return err
	}
	defer unlock()
	current, err := m.readMentionClaim(claim.CardID, claim.CommentID)
	if err != nil || current.State != "outcome" {
		return err
	}
	return m.publishMentionOutcome(ctx, current)
}
