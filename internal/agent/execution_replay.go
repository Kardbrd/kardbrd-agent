package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type replayIntakeKey struct{}
type lostReplayCursorError struct{ error }

func (e lostReplayCursorError) Unwrap() error { return e.error }

func replayIntakeOnly(ctx context.Context) bool {
	return ctx.Value(replayIntakeKey{}) != nil
}

// ReconcileExecutionRequests durably ingests every page before local recovery
// dispatches accepted claims. It scans from the beginning on each registration.
// There is no persisted cursor: local claims deduplicate completed items, and
// an item is never skipped after a crash between page receipt and recording.
func (m *Manager) ReconcileExecutionRequests(ctx context.Context) error {
	reader, ok := m.Client.(interface {
		GetExecutionRequests(context.Context, string, string) (api.ExecutionRequestPage, error)
	})
	if !ok {
		return fmt.Errorf("structured request replay API is unavailable")
	}
	// A public cursor can disappear while a scan is in flight. Restart at the
	// beginning; claims already written below make the repeated intake safe.
	for attempt := 0; attempt < 3; attempt++ {
		err := m.scanExecutionRequests(ctx, reader)
		var lost lostReplayCursorError
		if !errors.As(err, &lost) || attempt == 2 {
			return err
		}
	}
	return nil
}

func (m *Manager) scanExecutionRequests(ctx context.Context, reader interface {
	GetExecutionRequests(context.Context, string, string) (api.ExecutionRequestPage, error)
}) error {
	// Mark the previous collection snapshot stale before asking Web for a
	// fresh one. A failed first page must not dispatch its old unstarted tail.
	if err := m.recordReplaySequence(nil, false); err != nil {
		return fmt.Errorf("start execution replay scan: %w", err)
	}
	after := ""
	var seen []replayIdentity
	for {
		page, err := reader.GetExecutionRequests(ctx, m.BoardID, after)
		if err != nil {
			var apiErr *api.APIError
			if after != "" && errors.As(err, &apiErr) && apiErr.StatusCode == 400 && apiErr.Code == "VALIDATION_ERROR" {
				return lostReplayCursorError{err}
			}
			return err
		}
		if len(page.Requests) > 100 {
			return fmt.Errorf("execution request page exceeds 100 items")
		}
		lastCommentID := ""
		for _, raw := range page.Requests {
			var identity struct {
				BoardID   string `json:"board_id"`
				CardID    string `json:"card_id"`
				CommentID string `json:"comment_id"`
			}
			if err := json.Unmarshal(raw, &identity); err != nil {
				return err
			}
			if identity.BoardID != m.BoardID || identity.CardID == "" || identity.CommentID == "" {
				return fmt.Errorf("execution replay item has mismatched identity")
			}
			itemErr := m.HandleBoardEventRaw(context.WithValue(ctx, replayIntakeKey{}, true), raw)
			if err := ctx.Err(); err != nil {
				return err
			}
			claim, err := m.readMentionClaim(identity.CardID, identity.CommentID)
			if err != nil {
				if itemErr != nil {
					return fmt.Errorf("execution replay item %s was not recorded after processing error: %w", identity.CommentID, itemErr)
				}
				return fmt.Errorf("execution replay item %s was not recorded: %w", identity.CommentID, err)
			}
			if claim.BoardID != identity.BoardID || claim.CardID != identity.CardID || claim.CommentID != identity.CommentID || claim.State == "" || !m.ownsRecoveredClaim(claim) {
				return fmt.Errorf("execution replay item %s has an invalid durable disposition", identity.CommentID)
			}
			seen = append(seen, replayIdentity{CardID: identity.CardID, CommentID: identity.CommentID})
			lastCommentID = identity.CommentID
		}
		// Page intake is complete. Persist its observed order before using the
		// public ID cursor; an interrupted scan still leaves a usable prefix.
		if err := m.recordReplaySequence(seen, false); err != nil {
			return fmt.Errorf("persist execution replay page identity: %w", err)
		}
		if page.NextAfter == nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			return m.recordReplaySequence(seen, true)
		}
		if len(page.Requests) == 0 || *page.NextAfter == "" || *page.NextAfter == after || *page.NextAfter != lastCommentID {
			return fmt.Errorf("execution replay returned an invalid cursor")
		}
		after = *page.NextAfter
	}
}
