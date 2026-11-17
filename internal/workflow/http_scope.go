package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

// ScopeLimits are proposed bounds for an observational full-board read.
type ScopeLimits struct {
	MaxBoards, MaxCardsPerBoard, MaxDetailsPerBoard int
	MaxBoardBytes                                   int64
	MaxBoardDuration                                time.Duration
}

// ScopeInventory is the last fully reconciled set of enrolled card IDs.
type ScopeInventory struct {
	IDs        []string
	Generation string
}

// UnresolvedEvidence describes a technical or source-evidence gap. It does not
// itself request a user decision or assert that an approval was authenticated.
type UnresolvedEvidence struct {
	BoardID string
	CardID  string
	Kind    string
	Detail  string
}

type FullScopeResult struct {
	Cards      []Snapshot
	Next       ScopeInventory
	Unresolved []UnresolvedEvidence
}

// ScopeEvidence is an unimplemented verification boundary. The current API
// response has no established completeness or approval-origin contract. RED
// fixtures provide an explicitly test-only implementation; production has none.
type ScopeEvidence interface {
	Complete(boardID string, board json.RawMessage) bool
	Project(cardID string, card json.RawMessage) (Snapshot, bool)
	VerifyOrigin(cardID string, card json.RawMessage) bool
}

var ErrFullScopeUnimplemented = errors.New("full-scope HTTP adapter unimplemented")

// ReadFullScope is a read-only proposed adapter. It is deliberately unwired.
func ReadFullScope(context.Context, *api.Client, []string, ScopeInventory, ScopeLimits, ScopeEvidence, time.Time) (FullScopeResult, error) {
	return FullScopeResult{}, ErrFullScopeUnimplemented
}
