package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

// ScopeLimits bound one observational pass, including confirming board reads.
type ScopeLimits struct {
	MaxBoards, MaxCardsPerBoard, MaxDetailsPerBoard int
	MaxRetainedRecords                              int // observational inventory cap; unused by verified reader
	MaxBoardBytes                                   int64
	MaxBoardDuration                                time.Duration
}

// ScopeInventory is the last fully reconciled set of enrolled card IDs.
type ScopeInventory struct {
	IDs        []string
	Generation string
}

// UnresolvedEvidence describes a technical or source-evidence gap; it does not
// request a user decision or assert authenticated approval.
type UnresolvedEvidence struct{ BoardID, CardID, Kind, Detail string }

type FullScopeResult struct {
	Cards      []Snapshot
	Next       ScopeInventory
	Unresolved []UnresolvedEvidence
}

// ScopeEvidence has no production implementation. The current API response has
// no established completeness or approval-origin contract; only tests supply it.
type ScopeEvidence interface {
	Complete(boardID string, board json.RawMessage) bool
	Project(cardID string, card json.RawMessage) (Snapshot, bool)
	VerifyOrigin(cardID string, card json.RawMessage) bool
	// VerifyConsistency must tie detail and origin observations to the
	// confirmed scope. No current API response establishes this property.
	VerifyConsistency(detailDigests map[string][sha256.Size]byte) bool
}

type scopeMember struct {
	ListID   string
	Archived bool
	Revision int
}

func decodeScopeBoard(raw json.RawMessage, boardID string, cap int) (map[string]scopeMember, string) {
	var board struct {
		ID    string `json:"id"`
		Lists []struct {
			ID    string `json:"id"`
			Cards []struct {
				ID   string `json:"id"`
				List struct {
					ID string `json:"id"`
				} `json:"list"`
				Archived bool `json:"is_archived"`
				Revision int  `json:"metadata_revision"`
			} `json:"cards"`
		} `json:"lists"`
		Stats struct {
			Archived *int `json:"archived_cards"`
		} `json:"stats"`
	}
	if json.Unmarshal(raw, &board) != nil || board.ID != boardID || board.Lists == nil || board.Stats.Archived == nil {
		return nil, "board_decode"
	}
	members := make(map[string]scopeMember)
	archived, count := 0, 0
	for _, list := range board.Lists {
		if list.ID == "" || list.Cards == nil {
			return nil, "board_decode"
		}
		for _, card := range list.Cards {
			count++
			if count > cap {
				return nil, "card_limit"
			}
			if card.ID == "" || card.List.ID != list.ID {
				return nil, "board_decode"
			}
			member := scopeMember{list.ID, card.Archived, card.Revision}
			if prior, ok := members[card.ID]; ok {
				if prior != member {
					return nil, "scope_identity_conflict"
				}
				continue
			}
			members[card.ID] = member
			if card.Archived {
				archived++
			}
		}
	}
	if archived != *board.Stats.Archived {
		return nil, "board_count_conflict"
	}
	return members, ""
}

// ReadFullScope is a read-only local-fixture adapter. It is deliberately unwired.
func ReadFullScope(ctx context.Context, client *api.Client, boardIDs []string, old ScopeInventory, limits ScopeLimits, proof ScopeEvidence, _ time.Time) (FullScopeResult, error) {
	result := FullScopeResult{Next: ScopeInventory{IDs: append([]string(nil), old.IDs...), Generation: old.Generation}}
	fail := func(boardID, cardID, kind, detail string, err error) (FullScopeResult, error) {
		if canceled := ctx.Err(); canceled != nil {
			kind, detail, err = "cancelled", canceled.Error(), canceled
		}
		result.Unresolved = append(result.Unresolved, UnresolvedEvidence{boardID, cardID, kind, detail})
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return fail("", "", "cancelled", err.Error(), err)
	}
	if limits.MaxBoards <= 0 || len(boardIDs) == 0 || len(boardIDs) > limits.MaxBoards/2 {
		return fail("", "", "board_limit", "board reads exceed pass budget", nil)
	}
	if limits.MaxCardsPerBoard <= 0 || limits.MaxDetailsPerBoard <= 0 || limits.MaxBoardBytes <= 0 || limits.MaxBoardDuration <= 0 {
		return fail("", "", "board_limit", "invalid pass limits", nil)
	}
	passCtx, cancel := context.WithTimeout(ctx, limits.MaxBoardDuration)
	defer cancel()
	readFailure := func(boardID, cardID, kind string, err error) (FullScopeResult, error) {
		if ctx.Err() != nil {
			return fail(boardID, cardID, "cancelled", ctx.Err().Error(), ctx.Err())
		}
		if passCtx.Err() != nil {
			return fail(boardID, cardID, "time_limit", passCtx.Err().Error(), nil)
		}
		return fail(boardID, cardID, kind, err.Error(), nil)
	}
	type located struct {
		boardID string
		member  scopeMember
	}
	locations := make(map[string]located)
	boardMembership := make(map[string]map[string]scopeMember)
	priorIDs := make(map[string]struct{}, len(old.IDs))
	for _, id := range old.IDs {
		priorIDs[id] = struct{}{}
	}
	for _, boardID := range boardIDs {
		if boardID == "" {
			return fail("", "", "board_decode", "empty board ID", nil)
		}
		if _, duplicate := boardMembership[boardID]; duplicate {
			return fail(boardID, "", "scope_identity_conflict", "board ID repeated in pass", nil)
		}
		raw, err := client.GetBoardBounded(passCtx, boardID, true, limits.MaxBoardBytes)
		if err != nil {
			if errors.Is(err, api.ErrResponseTooLarge) {
				return fail(boardID, "", "board_limit", err.Error(), nil)
			}
			return readFailure(boardID, "", "board_read", err)
		}
		members, kind := decodeScopeBoard(raw, boardID, limits.MaxCardsPerBoard)
		if kind != "" {
			return fail(boardID, "", kind, "board response cannot establish scope", nil)
		}
		if len(members) > limits.MaxDetailsPerBoard {
			return fail(boardID, "", "detail_limit", "board members exceed detail read budget", nil)
		}
		if proof == nil || !proof.Complete(boardID, raw) {
			return fail(boardID, "", "scope_completeness_unproved", "board response has no verified completeness contract", nil)
		}
		boardMembership[boardID] = members
		for id, member := range members {
			if _, duplicate := locations[id]; duplicate {
				return fail(boardID, id, "scope_identity_conflict", "card appears on more than one board", nil)
			}
			locations[id] = located{boardID, member}
		}
	}
	ids := make([]string, 0, len(locations)+len(priorIDs))
	for id := range locations {
		ids = append(ids, id)
	}
	for id := range priorIDs {
		if _, listed := locations[id]; !listed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	// One detail read per distinct observed or prior ID. The cap covers omitted
	// prior IDs as well as current board members across the entire pass.
	if int64(len(ids)) > int64(limits.MaxDetailsPerBoard)*int64(len(boardIDs)) {
		return fail("", "", "detail_limit", "current and prior detail reads exceed pass budget", nil)
	}
	all := make(map[string]Snapshot)
	details := make(map[string][sha256.Size]byte, len(ids))
	for _, id := range ids {
		if err := passCtx.Err(); err != nil {
			return readFailure("", id, "detail_read", err)
		}
		cardRaw, err := client.GetCardBounded(passCtx, id, limits.MaxBoardBytes)
		if err != nil {
			if errors.Is(err, api.ErrResponseTooLarge) {
				return fail("", id, "detail_limit", err.Error(), nil)
			}
			if _, prior := priorIDs[id]; prior {
				return readFailure("", id, "prior_card_unresolved", err)
			}
			return readFailure("", id, "detail_read", err)
		}
		details[id] = sha256.Sum256(cardRaw)
		var detail struct {
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
		if json.Unmarshal(cardRaw, &detail) != nil || detail.ID != id || detail.Board.ID == "" || detail.List.ID == "" {
			return fail("", id, "detail_decode", "card identity or location is missing", nil)
		}
		snapshot, ok := proof.Project(id, cardRaw)
		if !ok || snapshot.ID != id || snapshot.BoardID != detail.Board.ID || snapshot.Revision != detail.Revision {
			return fail("", id, "detail_decode", "card detail cannot be projected consistently", nil)
		}
		location, listed := locations[id]
		_, prior := priorIDs[id]
		inScope := false
		for _, boardID := range boardIDs {
			if detail.Board.ID == boardID {
				inScope = true
				break
			}
		}
		if prior && (!inScope || detail.Archived || !snapshot.Enrollment.Explicit) {
			result.Unresolved = append(result.Unresolved, UnresolvedEvidence{detail.Board.ID, id, "prior_card_observed_exit", "current detail shows move, archive, or opt-out"})
			return fail(detail.Board.ID, id, "prior_card_unresolved", "prior evidence retained pending reconciliation", nil)
		}
		if prior && !listed {
			return fail(detail.Board.ID, id, "prior_card_unresolved", "prior card omitted despite current detail", nil)
		}
		if listed && (detail.Board.ID != location.boardID || detail.List.ID != location.member.ListID || detail.Archived != location.member.Archived || detail.Revision != location.member.Revision) {
			return fail(location.boardID, id, "membership_changed", "board summary and current detail disagree", nil)
		}
		if detail.Archived || !snapshot.Enrollment.Explicit || !inScope {
			continue
		}
		if !proof.VerifyOrigin(id, cardRaw) || !snapshot.Enrollment.Verified {
			return fail(detail.Board.ID, id, "approval_origin_unproved", "original approval origin is not verified", nil)
		}
		all[id] = snapshot
	}
	for _, boardID := range boardIDs {
		confirm, err := client.GetBoardBounded(passCtx, boardID, true, limits.MaxBoardBytes)
		if err != nil {
			if errors.Is(err, api.ErrResponseTooLarge) {
				return fail(boardID, "", "board_limit", err.Error(), nil)
			}
			return readFailure(boardID, "", "board_read", err)
		}
		confirmed, kind := decodeScopeBoard(confirm, boardID, limits.MaxCardsPerBoard)
		if kind != "" {
			return fail(boardID, "", kind, "confirming board response cannot establish scope", nil)
		}
		if !reflect.DeepEqual(boardMembership[boardID], confirmed) {
			return fail(boardID, "", "membership_changed", "board membership changed during pass", nil)
		}
		if !proof.Complete(boardID, confirm) {
			return fail(boardID, "", "scope_completeness_unproved", "confirming board response lacks verified completeness", nil)
		}
	}
	if err := passCtx.Err(); err != nil {
		return readFailure("", "", "time_limit", err)
	}
	if !proof.VerifyConsistency(details) {
		return fail("", "", "snapshot_consistency_unproved", "detail and origin evidence cannot be tied to confirmed scope", nil)
	}
	if err := passCtx.Err(); err != nil {
		return readFailure("", "", "time_limit", err)
	}
	result.Next.IDs = make([]string, 0, len(all))
	for id := range all {
		result.Next.IDs = append(result.Next.IDs, id)
	}
	sort.Strings(result.Next.IDs)
	for _, id := range result.Next.IDs {
		result.Cards = append(result.Cards, all[id])
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%v", old.Generation, result.Next.IDs)))
	result.Next.Generation = hex.EncodeToString(sum[:])
	return result, nil
}
