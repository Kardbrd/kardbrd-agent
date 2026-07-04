package agent

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

var connectStream = func(ctx context.Context, streamURL string) (api.StreamConn, error) {
	return api.ConnectStream(ctx, streamURL)
}

func (m *Manager) HandleBoardEvent(ctx context.Context, message map[string]any) error {
	eventType := stringField(message, "event_type")
	cardID := stringField(message, "card_id")

	switch eventType {
	case "comment_created":
		if boolField(message, "author_is_bot") {
			break
		}
		content := stringField(message, "content")
		if m.isBotCard(message) && strings.HasPrefix(strings.TrimSpace(content), "/") {
			return m.HandleBotCardCommand(ctx, cardID, content, stringField(message, "author_name"))
		}
		if !strings.Contains(strings.ToLower(content), strings.ToLower(m.Mention)) {
			break
		}
		if cardID == "" {
			break
		}
		if err := m.ProcessMention(ctx, cardID, stringField(message, "comment_id"), content, stringField(message, "author_name")); err != nil {
			return err
		}
	case "reaction_added":
		// Reactions are dispatched through rules so custom stop policies can be configured.
	case "card_moved":
		if err := m.HandleCardMoved(ctx, message); err != nil {
			return err
		}
	case "stream_requested":
		if err := m.HandleStreamRequested(ctx, cardID, stringField(message, "stream_url")); err != nil {
			return err
		}
	}

	return m.CheckRules(ctx, eventType, message)
}

func (m *Manager) HandleCardMoved(ctx context.Context, message map[string]any) error {
	cardID := stringField(message, "card_id")
	listName := strings.ToLower(stringField(message, "list_name"))
	if cardID == "" || !strings.Contains(listName, "done") {
		return nil
	}
	m.mu.Lock()
	if session := m.Active[cardID]; session != nil && session.Process != nil && session.Process.Process != nil {
		_ = session.Process.Process.Kill()
	}
	if session := m.Active[cardID]; session != nil && session.Cancel != nil {
		session.Cancel()
	}
	delete(m.Active, cardID)
	m.mu.Unlock()
	if m.Worktree != nil {
		return m.Worktree.Remove(cardID, false)
	}
	return nil
}

func (m *Manager) HandleStopReaction(ctx context.Context, cardID string, commentID string) error {
	m.mu.Lock()
	session := m.Active[cardID]
	if session == nil {
		m.mu.Unlock()
		return nil
	}
	if session.CommentID != "" && session.CommentID != commentID {
		m.mu.Unlock()
		return nil
	}
	if session.Process != nil && session.Process.Process != nil {
		_ = session.Process.Process.Kill()
	}
	if session.Cancel != nil {
		session.Cancel()
	}
	delete(m.Active, cardID)
	m.mu.Unlock()

	_, _ = m.Client.AddComment(ctx, cardID, "**Agent stopped** 🛑\n\nThe active session was terminated.")
	return nil
}

func (m *Manager) CheckRules(ctx context.Context, eventType string, message map[string]any) error {
	if m.Rules == nil || len(m.Rules.Rules) == 0 || m.Paused {
		return nil
	}
	if err := m.enrichRuleMessage(ctx, message); err != nil {
		return err
	}
	cardID := stringField(message, "card_id")
	if cardID == "" {
		return nil
	}
	for _, rule := range m.Rules.Match(eventType, message) {
		if rule.IsStop() {
			if err := m.HandleStopReaction(ctx, cardID, stringField(message, "comment_id")); err != nil {
				return err
			}
			continue
		}
		if err := m.ProcessRule(ctx, cardID, rule, message); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ProcessRule(ctx context.Context, cardID string, rule rules.Rule, message map[string]any) error {
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

	auth := m.Executor.CheckAuth(ctx)
	if !auth.Authenticated {
		_, _ = m.Client.AddComment(ctx, cardID, "**Automation Error** ("+rule.Name+")\n\nAgent not authenticated: `"+auth.Error+"`")
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
	m.Active[cardID] = &ActiveSession{CardID: cardID, WorktreePath: worktreePath, Cancel: cancel}
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
	promptText := m.Executor.BuildPrompt(executor.PromptRequest{
		CardID:         cardID,
		CardMarkdown:   cardMarkdown,
		Command:        rule.Action,
		CommentContent: "[Automation: " + rule.Name + "]",
		AuthorName:     "automation",
		BoardID:        m.BoardID,
		CWD:            worktreePath,
	})
	result := m.Executor.Execute(execCtx, executor.Request{
		Prompt:  promptText,
		CWD:     worktreePath,
		Model:   rule.ModelID(),
		OnChunk: m.makeOnChunk(cardID),
	})
	if execCtx.Err() != nil {
		return nil
	}
	if result.Success {
		if !m.hasRecentBotComment(ctx, cardID, 60*time.Second) {
			if result.SessionID != "" {
				return m.resumeToPublish(ctx, cardID, "", result.SessionID, "automation", worktreePath)
			}
			m.postFallbackComment(ctx, cardID, result, "automation", "")
		}
		return nil
	}
	_, _ = m.Client.AddComment(ctx, cardID, buildErrorComment(result, "Automation Error ("+rule.Name+")"))
	return nil
}

func (m *Manager) ProcessSchedule(ctx context.Context, cardID string, schedule rules.Schedule) error {
	return m.ProcessRule(ctx, cardID, rules.Rule{
		Name:   "schedule:" + schedule.Name,
		Action: schedule.Action,
		Model:  schedule.Model,
	}, map[string]any{"card_id": cardID})
}

func (m *Manager) HandleStreamRequested(ctx context.Context, cardID string, streamURL string) error {
	if cardID == "" || streamURL == "" {
		return nil
	}

	m.mu.Lock()
	session := m.Active[cardID]
	if session == nil || session.Stream != nil {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	stream, err := connectStream(ctx, streamURL)
	if err != nil {
		return err
	}

	m.mu.Lock()
	session = m.Active[cardID]
	if session == nil {
		m.mu.Unlock()
		_ = stream.Close()
		return nil
	}
	session.Stream = stream
	session.Streaming = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) enrichRuleMessage(ctx context.Context, message map[string]any) error {
	needsLabels := false
	needsAssignee := false
	needsCommentAuthor := false
	for _, rule := range m.Rules.Rules {
		if rule.RequireLabel != "" || rule.ExcludeLabel != "" {
			needsLabels = true
		}
		if len(rule.Assignee) > 0 {
			needsAssignee = true
		}
		if rule.CommentAuthor != "" {
			needsCommentAuthor = true
		}
	}

	cardID := stringField(message, "card_id")
	if cardID != "" && ((needsLabels && message["card_labels"] == nil) || (needsAssignee && message["card_assignee_id"] == nil)) {
		raw, err := m.Client.GetCard(ctx, cardID)
		if err != nil {
			if needsLabels && message["card_labels"] == nil {
				message["card_labels"] = []string{}
			}
			if needsAssignee && message["card_assignee_id"] == nil {
				message["card_assignee_id"] = ""
				message["card_assignee_is_bot"] = false
			}
		} else {
			var card struct {
				Labels []struct {
					Name string `json:"name"`
				} `json:"labels"`
				Assignee *struct {
					ID    string `json:"id"`
					IsBot bool   `json:"is_bot"`
				} `json:"assignee"`
			}
			if err := json.Unmarshal(raw, &card); err == nil {
				if needsLabels && message["card_labels"] == nil {
					labels := make([]string, 0, len(card.Labels))
					for _, label := range card.Labels {
						labels = append(labels, label.Name)
					}
					message["card_labels"] = labels
				}
				if needsAssignee && message["card_assignee_id"] == nil {
					if card.Assignee != nil {
						message["card_assignee_id"] = card.Assignee.ID
						message["card_assignee_is_bot"] = card.Assignee.IsBot
					} else {
						message["card_assignee_id"] = ""
						message["card_assignee_is_bot"] = false
					}
				}
			}
		}
	}

	if needsCommentAuthor && message["comment_author_is_bot"] == nil {
		commentID := stringField(message, "comment_id")
		if cardID != "" && commentID != "" {
			raw, err := m.Client.GetComment(ctx, cardID, commentID)
			if err == nil {
				var comment struct {
					Author struct {
						ID    string `json:"id"`
						IsBot bool   `json:"is_bot"`
					} `json:"author"`
				}
				if err := json.Unmarshal(raw, &comment); err == nil {
					message["comment_author_id"] = comment.Author.ID
					message["comment_author_is_bot"] = comment.Author.IsBot
				}
			}
		}
		if message["comment_author_is_bot"] == nil {
			message["comment_author_id"] = ""
			message["comment_author_is_bot"] = false
		}
	}
	return nil
}

func (m *Manager) isBotCard(message map[string]any) bool {
	cardTitle := stringField(message, "card_title")
	if cardTitle == "🤖 "+m.AgentName {
		return true
	}
	cardID := stringField(message, "card_id")
	return cardID != "" && cardID == m.BotCardID
}

func (m *Manager) HandleBotCardCommand(ctx context.Context, cardID string, content string, authorName string) error {
	fields := strings.Fields(content)
	if len(fields) == 0 {
		return nil
	}
	switch strings.ToLower(fields[0]) {
	case "/pause":
		m.Paused = true
		_, _ = m.Client.AddComment(ctx, cardID, "⏸️ Paused - automation rules are now skipped. @mentions still work.\n\n@"+authorName)
	case "/resume":
		m.Paused = false
		_, _ = m.Client.AddComment(ctx, cardID, "▶️ Resumed - automation rules are active again.\n\n@"+authorName)
	case "/status":
		_, _ = m.Client.AddComment(ctx, cardID, "🟢 **Online**\n\n@"+authorName)
	case "/reload":
		if m.Reload == nil {
			_, _ = m.Client.AddComment(ctx, cardID, "⚠️ Rule engine is not reloadable (static rules)\n\n@"+authorName)
			return nil
		}
		loaded, err := m.Reload(ctx)
		if err != nil {
			_, _ = m.Client.AddComment(ctx, cardID, "**Reload failed**\n\n```\n"+err.Error()+"\n```\n\n@"+authorName)
			return nil
		}
		m.ApplyRulesConfig(loaded)
		_ = m.EnsureBotCard(ctx)
		_, _ = m.Client.AddComment(ctx, cardID, reloadMessage(loaded, authorName))
	}
	return nil
}

func reloadMessage(loaded rules.Config, authorName string) string {
	return "🔄 Reloaded " + strconv.Itoa(len(loaded.Rules)) + " rule(s) and " + strconv.Itoa(len(loaded.Schedules)) + " schedule(s) from kardbrd.yml\n\n@" + authorName
}

func stringField(message map[string]any, key string) string {
	if value, ok := message[key].(string); ok {
		return value
	}
	return ""
}

func boolField(message map[string]any, key string) bool {
	if value, ok := message[key].(bool); ok {
		return value
	}
	return false
}
