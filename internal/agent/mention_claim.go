package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type mentionClaim struct {
	Version          int                  `json:"version"`
	BoardID          string               `json:"board_id"`
	CardID           string               `json:"card_id"`
	CommentID        string               `json:"comment_id"`
	TargetBotID      string               `json:"target_bot_id"`
	OwnerID          string               `json:"owner_instance_id,omitempty"`
	Content          string               `json:"content"`
	AuthorName       string               `json:"author_name"`
	AuthorID         string               `json:"author_id,omitempty"`
	AuthorIsBot      bool                 `json:"author_is_bot"`
	CreatedAt        string               `json:"created_at,omitempty"`
	AcceptedRevision string               `json:"accepted_revision,omitempty"`
	Request          json.RawMessage      `json:"execution_request,omitempty"`
	AcceptedDefaults json.RawMessage      `json:"accepted_defaults,omitempty"`
	Model            string               `json:"model"`
	Effort           string               `json:"effort"`
	ModelSource      string               `json:"model_source"`
	EffortSource     string               `json:"effort_source"`
	State            string               `json:"state"`
	OutcomeStatus    string               `json:"outcome_status,omitempty"`
	OutcomeBody      string               `json:"outcome_body,omitempty"`
	OutcomeMessage   string               `json:"outcome_message,omitempty"`
	IntakeErrorBody  string               `json:"intake_error_body,omitempty"`
	AcceptedModels   []api.ExecutionModel `json:"accepted_models,omitempty"`
}

// RecoverAcceptedMentions processes durable intake and publication states.
// A started claim remains held for operator reconciliation.
func (m *Manager) RecoverAcceptedMentions(ctx context.Context) error {
	dir := m.ClaimDir
	if dir == "" {
		dir = filepath.Join(m.CWD, "state", "agent-claims")
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var itemErrors []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		var claim mentionClaim
		if err := json.Unmarshal(data, &claim); err != nil {
			return err
		}
		if claim.Version != 1 || claim.BoardID != m.BoardID {
			continue
		}
		switch claim.State {
		case "rejected":
			if err := m.publishStructuredRejection(ctx, claim); err != nil {
				itemErrors = append(itemErrors, fmt.Errorf("rejected request %s: %w", claim.CommentID, err))
			}
			continue
		case "outcome":
			if err := m.reconcileMentionOutcome(ctx, claim); err != nil {
				itemErrors = append(itemErrors, fmt.Errorf("outcome request %s: %w", claim.CommentID, err))
			}
			continue
		case "accepted":
		default:
			continue
		}
		cardRaw, err := m.Client.GetCard(ctx, claim.CardID)
		if err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("accepted request %s card load: %w", claim.CommentID, err))
			continue
		}
		var card struct {
			IsArchived bool   `json:"is_archived"`
			ListName   string `json:"list_name"`
			List       struct {
				Name string `json:"name"`
			} `json:"list"`
		}
		if err := json.Unmarshal(cardRaw, &card); err != nil {
			return err
		}
		if card.IsArchived || strings.EqualFold(card.ListName, "Done") || strings.EqualFold(card.List.Name, "Done") {
			if err := m.setMentionClaimState(claim, "needs_review"); err != nil {
				return err
			}
			continue
		}
		selection := mentionDispatch{Content: claim.Content, Model: claim.Model, ReasoningEffort: claim.Effort, ModelSource: claim.ModelSource, EffortSource: claim.EffortSource}
		if err := m.ProcessStructuredMention(ctx, claim.CardID, claim.CommentID, selection, claim.AuthorName, m.CurrentCapabilityRevision()); err != nil {
			itemErrors = append(itemErrors, fmt.Errorf("accepted request %s: %w", claim.CommentID, err))
		}
	}
	return errors.Join(itemErrors...)
}

func (m *Manager) claimPath(cardID, commentID string) string {
	dir := m.ClaimDir
	if dir == "" {
		dir = filepath.Join(m.CWD, "state", "agent-claims")
	}
	digest := sha256.Sum256([]byte(m.BoardID + "\x00" + cardID + "\x00" + commentID))
	return filepath.Join(dir, hex.EncodeToString(digest[:])+".json")
}

func (m *Manager) lockMentionClaim(cardID, commentID string) (func(), bool, error) {
	path := m.claimPath(cardID, commentID) + ".lock"
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }, true, nil
}

func (m *Manager) createOrReadMentionClaim(cardID, commentID, authorName string, selection mentionDispatch, snapshot structuredSnapshot) (mentionClaim, bool, error) {
	return m.createOrReadMentionClaimWithDisposition(cardID, commentID, authorName, selection, snapshot, "accepted", "")
}

func (m *Manager) createOrReadMentionClaimWithDisposition(cardID, commentID, authorName string, selection mentionDispatch, snapshot structuredSnapshot, state, outcomeBody string) (mentionClaim, bool, error) {
	path := m.claimPath(cardID, commentID)
	m.mu.Lock()
	targetBotID := m.BotID
	m.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mentionClaim{}, false, err
	}
	claim := mentionClaim{Version: 1, BoardID: m.BoardID, CardID: cardID, CommentID: commentID, TargetBotID: targetBotID, Content: selection.Content, AuthorName: authorName, AuthorID: snapshot.AuthorID, AuthorIsBot: snapshot.AuthorIsBot, CreatedAt: snapshot.CreatedAt, AcceptedRevision: snapshot.Revision, Request: snapshot.Request, AcceptedDefaults: snapshot.Defaults, Model: selection.Model, Effort: selection.ReasoningEffort, ModelSource: selection.ModelSource, EffortSource: selection.EffortSource, State: state, OutcomeBody: outcomeBody, AcceptedModels: snapshot.Models}
	data, err := json.Marshal(claim)
	if err != nil {
		return mentionClaim{}, false, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "claim-*.tmp")
	if err != nil {
		return mentionClaim{}, false, err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return mentionClaim{}, false, err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return mentionClaim{}, false, err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return mentionClaim{}, false, err
	}
	if err = file.Close(); err != nil {
		return mentionClaim{}, false, err
	}
	err = os.Link(file.Name(), path)
	if errors.Is(err, os.ErrExist) {
		existingData, readErr := os.ReadFile(path)
		if readErr != nil {
			return mentionClaim{}, false, readErr
		}
		var existing mentionClaim
		if readErr = json.Unmarshal(existingData, &existing); readErr != nil {
			return mentionClaim{}, false, readErr
		}
		if existing.Version != 1 || existing.BoardID != m.BoardID || existing.CardID != cardID || existing.CommentID != commentID || existing.TargetBotID != targetBotID {
			return mentionClaim{}, false, fmt.Errorf("comment claim identity mismatch")
		}
		return existing, false, nil
	}
	if err != nil {
		return mentionClaim{}, false, err
	}
	if err := syncClaimDirectory(filepath.Dir(path)); err != nil {
		return mentionClaim{}, false, err
	}
	return claim, true, nil
}

func (m *Manager) setMentionClaimState(claim mentionClaim, state string) error {
	claim.State = state
	return m.saveMentionClaim(claim)
}

func (m *Manager) saveMentionClaim(claim mentionClaim) error {
	path := m.claimPath(claim.CardID, claim.CommentID)
	data, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "claim-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncClaimDirectory(filepath.Dir(path))
}

func syncClaimDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (m *Manager) readMentionClaim(cardID, commentID string) (mentionClaim, error) {
	data, err := os.ReadFile(m.claimPath(cardID, commentID))
	if err != nil {
		return mentionClaim{}, err
	}
	var claim mentionClaim
	if err := json.Unmarshal(data, &claim); err != nil {
		return mentionClaim{}, err
	}
	return claim, nil
}

func (m *Manager) holdUnstartedClaim(claim mentionClaim) error {
	unlock, acquired, err := m.lockMentionClaim(claim.CardID, claim.CommentID)
	if err != nil || !acquired {
		return err
	}
	defer unlock()
	current, err := m.readMentionClaim(claim.CardID, claim.CommentID)
	if err != nil {
		return err
	}
	if current.State == "accepted" {
		return m.setMentionClaimState(current, "needs_review")
	}
	return nil
}
