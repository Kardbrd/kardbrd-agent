// Package worker implements an opt-in durable personal-operations worker.
//
// The worker intentionally owns only the literal top-level "ops" metadata
// value. All other card metadata remains the server's data and is never
// reconstructed or replaced by this package.
package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	MetadataKey     = "ops"
	SchemaVersion   = 1
	maxReceiptIDs   = 64
	maxSummarySize  = 8 * 1024
	maxStableIDSize = 512
)

var ErrReceiptCapacity = errors.New("worker receipt history reached its durable capacity")

// State is a durable lifecycle state. Only ready and due scheduled records
// can be claimed. In particular, a stale running record is reconciled rather
// than treated as ready work.
type State string

const (
	StateSuggested    State = "suggested"
	StateReady        State = "ready"
	StateScheduled    State = "scheduled"
	StateRunning      State = "running"
	StateWaitingEvent State = "waiting_event"
	StateWaitingUser  State = "waiting_user"
	StateCompleted    State = "completed"
	StateCancelled    State = "cancelled"
	StatePaused       State = "paused"
	StateNeedsReview  State = "needs_review"
)

func (s State) valid() bool {
	switch s {
	case StateSuggested, StateReady, StateScheduled, StateRunning, StateWaitingEvent, StateWaitingUser, StateCompleted, StateCancelled, StatePaused, StateNeedsReview:
		return true
	default:
		return false
	}
}

type Delegation struct {
	Delegated     bool            `json:"delegated"`
	Authorization json.RawMessage `json:"authorization,omitempty"`
}

type SourceRef struct {
	ID        string `json:"id"`
	Reference string `json:"reference,omitempty"`
}

// ActionIntent is supplied by the enrolling operator. It is durable before an
// adapter starts, so a runner cannot invent or expand an outward action.
type ActionIntent struct {
	ID      string          `json:"id"`
	Intent  json.RawMessage `json:"intent"`
	Receipt *ActionReceipt  `json:"receipt,omitempty"`
}

type ActionReceipt struct {
	ActionID  string `json:"action_id"`
	ReceiptID string `json:"receipt_id"`
	Status    string `json:"status,omitempty"`
}

type Run struct {
	ID         string `json:"id"`
	OwnerToken string `json:"owner_token"`
	// Fence binds a claim to the authorization and outward-action intent that
	// the operator approved. A metadata edit cannot retain an old claim while
	// changing either permission boundary.
	Fence        string    `json:"fence"`
	StartedAt    time.Time `json:"started_at"`
	HeartbeatAt  time.Time `json:"heartbeat_at"`
	LeaseExpires time.Time `json:"lease_expires_at"`
}

type Outcome struct {
	RunID      string `json:"run_id"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary,omitempty"`
	ReceiptID  string `json:"receipt_id,omitempty"`
	ReviewNote string `json:"review_note,omitempty"`
}

type Decision struct {
	Prompt string          `json:"prompt,omitempty"`
	ID     string          `json:"id,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
}

type Journal struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	CommentID string `json:"comment_id,omitempty"`
}

type Notice struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	ReceiptID string `json:"receipt_id,omitempty"`
}

func (j Journal) needsReconciliation() bool {
	return j.State == "prepared" || j.State == "sending" || j.State == "unknown"
}

func (n Notice) needsReconciliation() bool {
	return n.State == "prepared" || n.State == "sending" || n.State == "unknown"
}

// SuggestionClaim is an idempotency reservation stored on an explicitly
// configured registry card. It is deliberately separate from a suggestion
// card: a create response can be ambiguous, in which case the reservation is
// held for review instead of risking a second source card.
type SuggestionClaim struct {
	State  string `json:"state"`
	CardID string `json:"card_id,omitempty"`
}

// Record is the versioned metadata value at MetadataKey. It intentionally
// contains no provider connection or secret fields.
type Record struct {
	Version            int           `json:"version"`
	Goal               string        `json:"goal"`
	CompletionCriteria string        `json:"completion_criteria"`
	Delegation         Delegation    `json:"delegation"`
	State              State         `json:"state"`
	WakeAt             *time.Time    `json:"wake_at,omitempty"`
	Sources            []SourceRef   `json:"sources,omitempty"`
	Action             *ActionIntent `json:"action,omitempty"`
	Run                *Run          `json:"run,omitempty"`
	Outcome            *Outcome      `json:"outcome,omitempty"`
	Decision           *Decision     `json:"decision,omitempty"`
	EventID            string        `json:"event_id,omitempty"`
	Journal            *Journal      `json:"journal,omitempty"`
	Notice             *Notice       `json:"notice,omitempty"`
	// Unresolved outbox entries are retained when a later task step creates a
	// new current journal/notice. They are evidence for operator reconciliation
	// and are never replayed automatically.
	UnresolvedJournals []Journal                  `json:"unresolved_journals,omitempty"`
	UnresolvedNotices  []Notice                   `json:"unresolved_notices,omitempty"`
	EventReceipts      []string                   `json:"event_receipts,omitempty"`
	DecisionReceipts   []string                   `json:"decision_receipts,omitempty"`
	SuggestionRegistry bool                       `json:"suggestion_registry,omitempty"`
	SuggestionClaims   map[string]SuggestionClaim `json:"suggestion_claims,omitempty"`
}

func ParseRecord(raw json.RawMessage) (Record, error) {
	var record Record
	if len(raw) == 0 {
		return record, errors.New("worker metadata is empty")
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		return record, fmt.Errorf("decode worker metadata: %w", err)
	}
	if err := record.Validate(); err != nil {
		return record, err
	}
	return record, nil
}

func (r Record) Validate() error {
	if r.Version != SchemaVersion {
		return fmt.Errorf("unsupported worker metadata version %d", r.Version)
	}
	if !r.State.valid() {
		return fmt.Errorf("unknown worker state %q", r.State)
	}
	if strings.TrimSpace(r.Goal) == "" {
		return errors.New("worker goal is required")
	}
	if strings.TrimSpace(r.CompletionCriteria) == "" {
		return errors.New("worker completion criteria are required")
	}
	for _, source := range r.Sources {
		if strings.TrimSpace(source.ID) == "" || len(source.ID) > maxStableIDSize {
			return errors.New("worker source ID is required")
		}
	}
	for _, receipt := range append(append([]string(nil), r.EventReceipts...), r.DecisionReceipts...) {
		if strings.TrimSpace(receipt) == "" || len(receipt) > maxStableIDSize {
			return errors.New("worker receipt ID is invalid")
		}
	}
	if len(r.EventReceipts) > maxReceiptIDs || len(r.DecisionReceipts) > maxReceiptIDs {
		return fmt.Errorf("%w (%d IDs per kind)", ErrReceiptCapacity, maxReceiptIDs)
	}
	if r.SuggestionRegistry {
		if r.Delegation.Delegated || r.State != StatePaused || r.Action != nil {
			return errors.New("suggestion registry must be a non-delegated paused record without an action")
		}
		for sourceID, claim := range r.SuggestionClaims {
			if strings.TrimSpace(sourceID) == "" || len(sourceID) > maxStableIDSize || (claim.State != "creating" && claim.State != "created" && claim.State != "needs_review") {
				return errors.New("suggestion registry contains an invalid source claim")
			}
		}
		return nil
	}
	if r.Action != nil {
		if strings.TrimSpace(r.Action.ID) == "" || !jsonObject(r.Action.Intent) {
			return errors.New("worker action requires an ID and JSON object intent")
		}
		if r.Action.Receipt != nil && (r.Action.Receipt.ActionID != r.Action.ID || strings.TrimSpace(r.Action.Receipt.ReceiptID) == "" || len(r.Action.Receipt.ReceiptID) > maxStableIDSize || len(r.Action.Receipt.Status) > maxSummarySize) {
			return errors.New("worker action receipt must match its action ID and contain a bounded receipt ID")
		}
	}
	if r.State == StateSuggested {
		if r.Delegation.Delegated {
			return errors.New("suggested work cannot be delegated")
		}
		if r.Action != nil {
			return errors.New("suggested work cannot contain an action intent")
		}
		return nil
	}
	if r.Delegation.Delegated && !jsonObject(r.Delegation.Authorization) {
		return errors.New("delegated work requires explicit JSON object authorization")
	}
	if r.executableState() && (!r.Delegation.Delegated || !jsonObject(r.Delegation.Authorization)) {
		return errors.New("executable worker state requires explicit delegation and authorization")
	}
	if r.State == StateScheduled && r.WakeAt == nil {
		return errors.New("scheduled work requires wake_at")
	}
	if r.State == StateRunning && (r.Run == nil || r.Run.ID == "" || r.Run.OwnerToken == "" || r.Run.Fence == "" || r.Run.LeaseExpires.IsZero()) {
		return errors.New("running work requires a live run claim")
	}
	if r.State == StateWaitingUser && (r.Decision == nil || strings.TrimSpace(r.Decision.ID) == "" || strings.TrimSpace(r.Decision.Prompt) == "") {
		return errors.New("waiting_user work requires a stable decision ID and prompt")
	}
	if r.State == StateWaitingEvent && r.Decision != nil {
		return errors.New("waiting_event work cannot contain a user decision")
	}
	if r.State == StateWaitingEvent && strings.TrimSpace(r.EventID) == "" {
		return errors.New("waiting_event work requires an event ID")
	}
	return nil
}

func (r Record) executableState() bool {
	switch r.State {
	case StateReady, StateScheduled, StateRunning, StateWaitingEvent, StateWaitingUser:
		return true
	default:
		return false
	}
}

func (r Record) Eligible(now time.Time) bool {
	if !r.Delegation.Delegated {
		return false
	}
	switch r.State {
	case StateReady:
		return true
	case StateScheduled:
		return r.WakeAt != nil && !r.WakeAt.After(now)
	default:
		return false
	}
}

func (r Record) HasEventReceipt(id string) bool {
	for _, receipt := range r.EventReceipts {
		if receipt == id {
			return true
		}
	}
	return false
}

func (r *Record) AddEventReceipt(id string) error {
	if r.HasEventReceipt(id) {
		return nil
	}
	if len(r.EventReceipts) >= maxReceiptIDs {
		return ErrReceiptCapacity
	}
	r.EventReceipts = append(r.EventReceipts, id)
	return nil
}

func (r Record) HasDecisionReceipt(id string) bool {
	for _, receipt := range r.DecisionReceipts {
		if receipt == id {
			return true
		}
	}
	return false
}

func (r *Record) AddDecisionReceipt(id string) error {
	if r.HasDecisionReceipt(id) {
		return nil
	}
	if len(r.DecisionReceipts) >= maxReceiptIDs {
		return ErrReceiptCapacity
	}
	r.DecisionReceipts = append(r.DecisionReceipts, id)
	return nil
}

func (r *Record) RetainUnresolvedOutbox() {
	if r.Journal != nil && r.Journal.needsReconciliation() && !hasJournal(r.UnresolvedJournals, r.Journal.ID) {
		r.UnresolvedJournals = append(r.UnresolvedJournals, *r.Journal)
	}
	if r.Notice != nil && r.Notice.needsReconciliation() && !hasNotice(r.UnresolvedNotices, r.Notice.ID) {
		r.UnresolvedNotices = append(r.UnresolvedNotices, *r.Notice)
	}
}

func hasJournal(journals []Journal, id string) bool {
	for _, journal := range journals {
		if journal.ID == id {
			return true
		}
	}
	return false
}

func hasNotice(notices []Notice, id string) bool {
	for _, notice := range notices {
		if notice.ID == id {
			return true
		}
	}
	return false
}

func jsonObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && value != nil
}

func newDelegatedRecord(goal, criteria string, authorization json.RawMessage, sources []SourceRef, action *ActionIntent) (Record, error) {
	record := Record{
		Version:            SchemaVersion,
		Goal:               goal,
		CompletionCriteria: criteria,
		Delegation:         Delegation{Delegated: true, Authorization: authorization},
		State:              StateReady,
		Sources:            append([]SourceRef(nil), sources...),
		Action:             action,
	}
	return record, record.Validate()
}

func newSuggestionRegistryRecord() Record {
	return Record{
		Version:            SchemaVersion,
		Goal:               "Deduplicate read-only suggestion source events",
		CompletionCriteria: "Retain source claims so duplicate ingestion cannot create another proposal.",
		State:              StatePaused,
		SuggestionRegistry: true,
		SuggestionClaims:   map[string]SuggestionClaim{},
	}
}
