package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type replayIntakeKey struct{}

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
	after := ""
	var order uint64
	for {
		page, err := reader.GetExecutionRequests(ctx, m.BoardID, after)
		if err != nil {
			return err
		}
		if len(page.Requests) > 100 {
			return fmt.Errorf("execution request page exceeds 100 items")
		}
		lastCommentID := ""
		for _, raw := range page.Requests {
			order++
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
			if claim.BoardID != identity.BoardID || claim.CardID != identity.CardID || claim.CommentID != identity.CommentID || claim.State == "" {
				return fmt.Errorf("execution replay item %s has an invalid durable disposition", identity.CommentID)
			}
			if err := m.recordReplayOrder(claim, order); err != nil {
				return fmt.Errorf("persist execution replay order for %s: %w", identity.CommentID, err)
			}
			lastCommentID = identity.CommentID
		}
		if page.NextAfter == nil {
			return nil
		}
		if len(page.Requests) == 0 || *page.NextAfter == "" || *page.NextAfter == after || *page.NextAfter != lastCommentID {
			return fmt.Errorf("execution replay returned an invalid cursor")
		}
		after = *page.NextAfter
	}
}
