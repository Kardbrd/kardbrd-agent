package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// HandleBoardEventRaw retains the nested request's original JSON so presence,
// duplicate keys and explicit null survive the generic event decoder.
func (m *Manager) HandleBoardEventRaw(ctx context.Context, raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var message map[string]any
	if err := json.Unmarshal(raw, &message); err != nil {
		return err
	}
	if request, ok := fields["execution_request"]; ok {
		message["execution_request_raw"] = request
	}
	if defaults, ok := fields["accepted_defaults"]; ok {
		message["accepted_defaults_raw"] = defaults
	}
	return m.HandleBoardEvent(ctx, message)
}

func (m *Manager) SetConnectedBot(botID, instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.BotID = botID
	m.InstanceID = instanceID
	m.CapabilityRevision = ""
	m.CapabilityExpiresAt = time.Time{}
}

func (m *Manager) SetCapabilityRevision(revision string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CapabilityRevision = revision
}

func (m *Manager) CurrentCapabilityRevision() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CapabilityRevision
}

func (m *Manager) SetCapabilityRegistration(instanceID, revision string, expiresAt time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.InstanceID == "" || m.InstanceID != instanceID {
		return false
	}
	m.CapabilityRevision = revision
	m.CapabilityExpiresAt = expiresAt
	return true
}

func (m *Manager) CurrentCapabilityExpiry() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.CapabilityExpiresAt
}

func (m *Manager) handleStructuredComment(ctx context.Context, message map[string]any, value any) error {
	cardID, commentID := stringField(message, "card_id"), stringField(message, "comment_id")
	if cardID == "" || commentID == "" {
		return fmt.Errorf("structured comment event requires card_id and comment_id")
	}
	raw, ok := value.(json.RawMessage)
	if !ok {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), fmt.Errorf("execution_request: missing raw JSON"))
	}
	request, err := parseExecutionRequest(raw)
	if err != nil {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), err)
	}
	m.mu.Lock()
	botID := m.BotID
	m.mu.Unlock()
	if request.TargetBotID != botID {
		return nil
	}
	if botID == "" {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), fmt.Errorf("target bot identity is not connected"))
	}
	defaultsRaw, ok := message["accepted_defaults_raw"].(json.RawMessage)
	if !ok {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), fmt.Errorf("accepted_defaults: missing server snapshot"))
	}
	defaults, err := parseAcceptedDefaults(defaultsRaw)
	if err != nil {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), err)
	}
	content := stringField(message, "content")
	if !strings.Contains(strings.ToLower(content), strings.ToLower(m.Mention)) {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), fmt.Errorf("target_bot_id: comment does not address this bot"))
	}
	selection, err := m.resolveStructuredSelection(content, request, defaults)
	if err != nil {
		return m.rejectStructuredComment(ctx, cardID, stringField(message, "author_name"), err)
	}
	return m.ProcessStructuredMention(ctx, cardID, commentID, selection, stringField(message, "author_name"), request.CapabilityRevision)
}

func (m *Manager) rejectStructuredComment(ctx context.Context, cardID, authorName string, err error) error {
	_, _ = m.Client.AddCommentOnce(ctx, cardID, "**Execution request error**: "+err.Error()+"\n\n"+requesterMention(authorName))
	return nil
}

func (m *Manager) resolveStructuredSelection(content string, request executionRequest, defaults acceptedDefaults) (mentionDispatch, error) {
	selection, err := parseMentionDispatch(content, m.Mention)
	if err != nil {
		return selection, err
	}
	if request.Model.Present {
		selection.Model = request.Model.Value
		selection.ModelSource = "explicit"
	} else if selection.Model == "" && defaults.Model != nil {
		selection.Model = *defaults.Model
		selection.ModelSource = "accepted_defaults"
	}
	if request.Effort.Present {
		selection.ReasoningEffort = request.Effort.Value
		selection.EffortSource = "explicit"
	} else if selection.ReasoningEffort == "" && defaults.Effort != nil {
		selection.ReasoningEffort = *defaults.Effort
		selection.EffortSource = "accepted_defaults"
	}
	if selection.ModelSource == "" {
		selection.ModelSource = "cli_unknown"
	}
	if selection.EffortSource == "" {
		selection.EffortSource = "cli_unknown"
	}
	if m.ExecutorType != "codex" && m.ExecutorType != "claude" && selection.Model != "" {
		return selection, fmt.Errorf("execution_request.model: executor %q cannot select a model", m.ExecutorType)
	}
	if err := validateReasoning(m.ExecutorType, selection.ReasoningEffort); err != nil {
		return selection, fmt.Errorf("execution_request.effort: %w", err)
	}
	if (request.Model.Present || request.Effort.Present) && (selection.Model == "" || selection.ReasoningEffort == "") {
		return selection, fmt.Errorf("execution_request: explicit selection requires known effective model and effort")
	}
	return selection, nil
}

func (m *Manager) verifiedPair(model, effort string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.suppressedPairs[model+"\x00"+effort] {
		return false
	}
	if m.CommentExecution == nil || m.CommentExecution.Verification.Source == "" {
		return false
	}
	for _, candidate := range m.CommentExecution.Models {
		if candidate.ID == model {
			for _, allowed := range candidate.Efforts {
				if allowed == effort {
					return true
				}
			}
		}
	}
	return false
}
