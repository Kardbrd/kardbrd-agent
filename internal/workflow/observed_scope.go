package workflow

import (
	"context"
	"encoding/json"
	"errors"
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

var ErrObservationNotImplemented = errors.New("observational scope reader not implemented")

// ReadObservedScope is the proposed local-fixture entry point. The RED tests
// define its behavior; no production reader or writer invokes it yet.
func ReadObservedScope(context.Context, *api.Client, []string, ObservedInventory, ScopeLimits, ObservationProjector, time.Time) (ObservedScopeResult, error) {
	return ObservedScopeResult{}, ErrObservationNotImplemented
}
