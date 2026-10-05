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
)

type mentionClaim struct {
	Version      int    `json:"version"`
	BoardID      string `json:"board_id"`
	CardID       string `json:"card_id"`
	CommentID    string `json:"comment_id"`
	Content      string `json:"content"`
	AuthorName   string `json:"author_name"`
	Model        string `json:"model"`
	Effort       string `json:"effort"`
	ModelSource  string `json:"model_source"`
	EffortSource string `json:"effort_source"`
	State        string `json:"state"`
}

// RecoverAcceptedMentions only replays claims that have never entered the
// executor. A started claim remains held for operator reconciliation.
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
	for _, entry := range entries {
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
		if claim.Version != 1 || claim.BoardID != m.BoardID || claim.State != "accepted" {
			continue
		}
		cardRaw, err := m.Client.GetCard(ctx, claim.CardID)
		if err != nil {
			return err
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
			return err
		}
	}
	return nil
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { _ = file.Close(); _ = os.Remove(path) }, true, nil
}

func (m *Manager) createOrReadMentionClaim(cardID, commentID, authorName string, selection mentionDispatch) (mentionClaim, bool, error) {
	path := m.claimPath(cardID, commentID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return mentionClaim{}, false, err
	}
	claim := mentionClaim{Version: 1, BoardID: m.BoardID, CardID: cardID, CommentID: commentID, Content: selection.Content, AuthorName: authorName, Model: selection.Model, Effort: selection.ReasoningEffort, ModelSource: selection.ModelSource, EffortSource: selection.EffortSource, State: "accepted"}
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
		if existing.Version != 1 || existing.BoardID != m.BoardID || existing.CardID != cardID || existing.CommentID != commentID {
			return mentionClaim{}, false, fmt.Errorf("comment claim identity mismatch")
		}
		return existing, false, nil
	}
	if err != nil {
		return mentionClaim{}, false, err
	}
	return claim, true, nil
}

func (m *Manager) setMentionClaimState(claim mentionClaim, state string) error {
	claim.State = state
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
	return os.Rename(file.Name(), path)
}
