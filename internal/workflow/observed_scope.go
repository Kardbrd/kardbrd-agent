package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

// ObservationProjector decodes a card for read-only display. It does not
// attest enrollment, decision authorship, or permission to perform a step.
type ObservationProjector func(string, json.RawMessage) (Snapshot, bool)

type ObservedRecord struct {
	Card             Snapshot
	ObservedAt       time.Time
	DecisionEvidence string // "unverified_original_decision" until separately established
}

// ObservedInventory retains prior records, including those no longer visible.
// A successful read alone cannot authorize their retirement.
type ObservedInventory struct {
	Records map[string]ObservedRecord
}

// ObservedScopeResult is a display-only pass result. PassComplete means the
// bounded reads finished, not that Web supplied an atomic snapshot. It carries
// no evaluator candidates, claims, dispatch instructions, or action checkpoint.
type ObservedScopeResult struct {
	Observations []ObservedRecord
	Next         ObservedInventory
	Unresolved   []UnresolvedEvidence
	PassComplete bool
}

type observedDetail struct {
	ID    string `json:"id"`
	Board struct {
		ID string `json:"id"`
	} `json:"board"`
	List struct {
		ID string `json:"id"`
	} `json:"list"`
	Archived bool `json:"is_archived"`
	Revision int  `json:"metadata_revision"`
}

func decodeObservedDetail(raw json.RawMessage, id string) (observedDetail, bool) {
	var detail observedDetail
	ok := json.Unmarshal(raw, &detail) == nil && detail.ID == id && detail.Board.ID != "" && detail.List.ID != ""
	return detail, ok
}

// ReadObservedScope uses bounded local-fixture HTTP reads. A complete pass
// describes observed current state, not a server snapshot or action permission.
func ReadObservedScope(ctx context.Context, client *api.Client, boardIDs []string, old ObservedInventory, limits ScopeLimits, project ObservationProjector, now time.Time) (ObservedScopeResult, error) {
	prior := ObservedInventory{Records: make(map[string]ObservedRecord, len(old.Records))}
	for id, record := range old.Records {
		record.Card.Decisions = append([]Decision(nil), record.Card.Decisions...)
		prior.Records[id] = record
	}
	result := ObservedScopeResult{Next: prior}
	fail := func(boardID, cardID, kind, detail string, err error) (ObservedScopeResult, error) {
		if canceled := ctx.Err(); canceled != nil {
			kind, detail, err = "cancelled", canceled.Error(), canceled
		}
		return ObservedScopeResult{Next: prior, Unresolved: []UnresolvedEvidence{{BoardID: boardID, CardID: cardID, Kind: kind, Detail: detail}}}, err
	}
	if err := ctx.Err(); err != nil {
		return fail("", "", "cancelled", err.Error(), err)
	}
	if client == nil || project == nil || len(boardIDs) == 0 || limits.MaxBoards <= 0 || len(boardIDs) > limits.MaxBoards ||
		limits.MaxCardsPerBoard <= 0 || limits.MaxDetailsPerBoard <= 0 || limits.MaxRetainedRecords <= 0 ||
		limits.MaxBoardBytes <= 0 || limits.MaxBoardDuration <= 0 {
		return fail("", "", "board_limit", "invalid observational pass limits", nil)
	}
	if len(prior.Records) > limits.MaxRetainedRecords {
		return fail("", "", "inventory_limit", "prior inventory exceeds retained history cap", nil)
	}
	passCtx, cancel := context.WithTimeout(ctx, limits.MaxBoardDuration)
	defer cancel()
	readFailure := func(boardID, cardID, kind string, err error) (ObservedScopeResult, error) {
		if ctx.Err() != nil {
			return fail(boardID, cardID, "cancelled", ctx.Err().Error(), ctx.Err())
		}
		if passCtx.Err() != nil {
			return fail(boardID, cardID, "time_limit", passCtx.Err().Error(), nil)
		}
		if errors.Is(err, api.ErrResponseTooLarge) {
			return fail(boardID, cardID, "response_limit", err.Error(), nil)
		}
		return fail(boardID, cardID, kind, err.Error(), nil)
	}
	type location struct {
		boardID string
		member  scopeMember
	}
	current := make(map[string]location)
	boards := make(map[string]struct{}, len(boardIDs))
	for _, boardID := range boardIDs {
		if boardID == "" {
			return fail("", "", "board_decode", "empty board ID", nil)
		}
		if _, exists := boards[boardID]; exists {
			return fail(boardID, "", "scope_identity_conflict", "board repeated in pass", nil)
		}
		boards[boardID] = struct{}{}
		raw, err := client.GetBoardBounded(passCtx, boardID, true, limits.MaxBoardBytes)
		if err != nil {
			return readFailure(boardID, "", "board_read", err)
		}
		members, kind := decodeScopeBoard(raw, boardID, limits.MaxCardsPerBoard)
		if kind != "" {
			return fail(boardID, "", kind, "board response cannot be reconciled", nil)
		}
		if len(members) > limits.MaxDetailsPerBoard {
			return fail(boardID, "", "detail_limit", "board members exceed detail read budget", nil)
		}
		for id, member := range members {
			if _, exists := current[id]; exists {
				return fail(boardID, id, "scope_identity_conflict", "card appears on more than one board", nil)
			}
			current[id] = location{boardID, member}
		}
	}
	priorMissing := 0
	for id := range prior.Records {
		if _, listed := current[id]; !listed {
			priorMissing++
		}
	}
	if int64(len(current))+int64(priorMissing) > int64(limits.MaxDetailsPerBoard)*int64(len(boardIDs)) {
		return fail("", "", "detail_limit", "current and prior cards exceed pass request budget", nil)
	}
	ids := make([]string, 0, len(current))
	for id := range current {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := passCtx.Err(); err != nil {
			return readFailure("", id, "detail_read", err)
		}
		loc := current[id]
		raw, err := client.GetCardBounded(passCtx, id, limits.MaxBoardBytes)
		if err != nil {
			return readFailure(loc.boardID, id, "detail_read", err)
		}
		detail, ok := decodeObservedDetail(raw, id)
		if !ok {
			return fail(loc.boardID, id, "detail_decode", "card identity or location missing", nil)
		}
		if detail.Board.ID != loc.boardID || detail.List.ID != loc.member.ListID || detail.Archived != loc.member.Archived || detail.Revision != loc.member.Revision {
			return fail(loc.boardID, id, "membership_changed", "board and card detail disagree", nil)
		}
		card, ok := project(id, raw)
		if err := ctx.Err(); err != nil {
			return fail(loc.boardID, id, "cancelled", err.Error(), err)
		}
		if !ok || card.ID != id || card.BoardID != detail.Board.ID || card.Revision != detail.Revision {
			return fail(loc.boardID, id, "detail_decode", "card cannot be projected consistently", nil)
		}
		_, wasPrior := prior.Records[id]
		if detail.Archived || !card.Enrollment.Explicit {
			if wasPrior {
				result.Unresolved = append(result.Unresolved, UnresolvedEvidence{loc.boardID, id, "prior_card_observed_exit", "current card shows archive or opt-out"})
			}
			continue
		}
		card.Enrollment.Verified = false // Web attribution does not prove an original decision.
		record := ObservedRecord{Card: card, ObservedAt: now, DecisionEvidence: "unverified_original_decision"}
		result.Observations = append(result.Observations, record)
		result.Unresolved = append(result.Unresolved, UnresolvedEvidence{BoardID: loc.boardID, CardID: id, Kind: "decision_provenance_unverified", Detail: "Web attribution does not establish the original decision"})
		if !wasPrior && len(result.Next.Records) >= limits.MaxRetainedRecords {
			return fail(loc.boardID, id, "inventory_limit", "new observation exceeds retained history cap", nil)
		}
		result.Next.Records[id] = record
	}
	priorIDs := make([]string, 0, len(prior.Records))
	for id := range prior.Records {
		priorIDs = append(priorIDs, id)
	}
	sort.Strings(priorIDs)
	for _, id := range priorIDs {
		if _, visible := current[id]; !visible {
			if err := passCtx.Err(); err != nil {
				return readFailure("", id, "prior_card_read", err)
			}
			raw, err := client.GetCardBounded(passCtx, id, limits.MaxBoardBytes)
			if err != nil {
				var apiErr *api.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
					result.Unresolved = append(result.Unresolved, UnresolvedEvidence{CardID: id, Kind: "prior_card_omitted", Detail: "prior card is not visible on a scanned board; detail returned 404"})
					continue
				}
				return readFailure("", id, "prior_card_read", err)
			}
			detail, ok := decodeObservedDetail(raw, id)
			if !ok {
				return fail("", id, "detail_decode", "prior card identity or location missing", nil)
			}
			card, ok := project(id, raw)
			if err := ctx.Err(); err != nil {
				return fail(detail.Board.ID, id, "cancelled", err.Error(), err)
			}
			if !ok || card.ID != id || card.BoardID != detail.Board.ID || card.Revision != detail.Revision {
				return fail(detail.Board.ID, id, "detail_decode", "prior card cannot be projected consistently", nil)
			}
			if _, inScope := boards[detail.Board.ID]; !inScope || detail.Archived || !card.Enrollment.Explicit {
				result.Unresolved = append(result.Unresolved, UnresolvedEvidence{detail.Board.ID, id, "prior_card_observed_exit", "current detail shows move, archive, or opt-out"})
			} else {
				result.Unresolved = append(result.Unresolved, UnresolvedEvidence{detail.Board.ID, id, "prior_card_omitted", "current detail exists but board read omitted card"})
			}
		}
	}
	if err := passCtx.Err(); err != nil {
		return readFailure("", "", "time_limit", err)
	}
	result.PassComplete = true
	return result, nil
}
