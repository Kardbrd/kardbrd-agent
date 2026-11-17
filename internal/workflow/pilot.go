package workflow

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Decision is a current stored claim. Evidence, permission, and execution state
// deliberately live on the snapshot rather than on every decision record.
type Decision struct {
	ID       string
	Text     string
	Accepted bool
}

type Gate struct {
	DecisionID       string
	Candidate        string // empty binds the gate to Snapshot.NextStep
	ExcludesNextStep bool
	Evidence         string // supported, disputed, or withdrawn
}

type Enrollment struct {
	Explicit bool
	Verified bool
	Workflow string // Backyard or DSO in this pilot
	Version  string
}

type Session struct {
	Owner            string
	Active           bool
	HeartbeatExpired bool
	Checkpointed     bool
	ReleasedTo       string
	ReleaseVerified  bool
}

type Effect struct {
	Kind    string // report_send or deploy
	State   string // none, queued, uncertain, confirmed
	Receipt string
}

type Report struct {
	DueAt              time.Time
	MaterialSufficient bool
	ExternalInput      string
	ArtifactReady      bool
	DeliveredAt        time.Time
	CriteriaComplete   bool
	ContinueRequested  bool
	CanPrepare         bool
	CanDeliver         bool
}

type Deployment struct {
	CandidateReady      bool
	CanDeploy           bool
	ReleaseVerified     bool
	AccessVerified      bool
	TransitionReceipt   string
	LiveVerifiedAt      time.Time
	LiveEvidenceCurrent bool
}

// Snapshot is a frozen, current-card view. It is not a proposed metadata schema.
type Snapshot struct {
	ID         string
	BoardID    string
	Revision   int // a revision cannot substitute for original approval evidence
	Enrollment Enrollment
	Actor      string
	Session    Session
	Decisions  []Decision
	Gate       Gate
	NextStep   string
	Effect     Effect
	Endpoint   string // report or deployed_verified
	Report     Report
	Deployment Deployment
}

type Result struct {
	Status     string // ready, blocked, waiting_user, waiting_event, needs_review, invalid, stopped, complete
	Candidate  string
	Gate       string
	Input      string
	Reasons    []string
	Acceptance string // undetermined, met, unmet
	Stop       bool
	Overdue    bool
}

// Evaluate must only describe the next step. It must never claim or perform it.
func Evaluate(s Snapshot, now time.Time) Result {
	result := Result{Acceptance: "undetermined"}
	set := func(status, reason string) Result {
		result.Status = status
		result.Reasons = []string{reason}
		return result
	}
	// A gate protects only its named candidate. An omitted name retains the
	// original NextStep binding used by the first pilot fixtures.
	ready := func(candidate, reason string) Result {
		if s.Gate.DecisionID != "" &&
			(s.Gate.Candidate == candidate || s.Gate.Candidate == "" && s.NextStep == candidate) {
			var decision *Decision
			for i := range s.Decisions {
				if s.Decisions[i].ID == s.Gate.DecisionID {
					decision = &s.Decisions[i]
					break
				}
			}
			if decision == nil || !decision.Accepted || s.Gate.Evidence != "supported" {
				result.Gate = s.Gate.DecisionID
				return set("waiting_user", "dependent_decision_unresolved")
			}
			if s.Gate.ExcludesNextStep {
				return set("blocked", "accepted_negative_excludes_step")
			}
		}
		result.Candidate = candidate
		return set("ready", reason)
	}
	if !s.Enrollment.Explicit || !s.Enrollment.Verified ||
		(s.Enrollment.Workflow != "Backyard" && s.Enrollment.Workflow != "DSO") ||
		s.Enrollment.Version != "pilot-v1" || s.Endpoint != "report" && s.Endpoint != "deployed_verified" {
		return set("invalid", "unverified_enrollment_or_definition")
	}

	// A send or deployment with an unknown outcome must be reconciled before
	// any candidate can be offered, even when another gate also needs review.
	if s.Effect.State == "uncertain" {
		result.Overdue = reportOverdue(s, now)
		return set("needs_review", "uncertain_effect")
	}
	if s.Effect.Receipt != "" && s.Effect.State != "confirmed" {
		result.Overdue = reportOverdue(s, now)
		return set("needs_review", "receipt_without_confirmed_state")
	}
	if s.Effect.State != "" && s.Effect.State != "none" && s.Effect.State != "queued" && s.Effect.State != "confirmed" ||
		s.Effect.State != "" && s.Effect.State != "none" &&
			(s.Endpoint == "report" && s.Effect.Kind != "report_send" || s.Endpoint == "deployed_verified" && s.Effect.Kind != "deploy") {
		result.Overdue = reportOverdue(s, now)
		return set("needs_review", "unrecognized_effect_observation")
	}
	if s.Endpoint == "report" && !s.Report.DeliveredAt.IsZero() && s.Report.DeliveredAt.After(now) {
		return set("needs_review", "future_report_delivery")
	}
	if s.Endpoint == "report" && s.Effect.State == "confirmed" && s.Effect.Receipt != "" &&
		!s.Report.DeliveredAt.IsZero() && s.Report.CriteriaComplete && !s.Report.ContinueRequested {
		result.Acceptance = "met"
		if !s.Report.DueAt.IsZero() && s.Report.DeliveredAt.After(s.Report.DueAt) {
			result.Acceptance = "unmet"
		}
		result.Stop = true
		return set("stopped", "report_endpoint")
	}
	if s.Endpoint == "deployed_verified" && s.Effect.State == "confirmed" && s.Effect.Receipt != "" &&
		s.Deployment.TransitionReceipt != "" && s.Deployment.LiveEvidenceCurrent &&
		!s.Deployment.LiveVerifiedAt.IsZero() && !s.Deployment.LiveVerifiedAt.After(now) {
		result.Acceptance = "met"
		result.Stop = true
		return set("complete", "verified_deployment_endpoint")
	}
	if s.Session.Owner != "" && s.Session.Owner != s.Actor &&
		(s.Session.Active || !s.Session.Checkpointed || !s.Session.ReleaseVerified || s.Session.ReleasedTo != s.Actor) {
		result.Overdue = reportOverdue(s, now)
		return set("waiting_event", "owned_session")
	}

	if s.Endpoint == "report" {
		if s.Effect.Kind == "report_send" && s.Effect.State == "confirmed" {
			if s.Effect.Receipt == "" || s.Report.DeliveredAt.IsZero() {
				return set("needs_review", "incomplete_send_observation")
			}
			if s.Report.CriteriaComplete {
				if s.Report.ContinueRequested {
					return ready("SYM_INVESTIGATE_NEXT", "continued_investigation")
				}
			}
		}
		result.Overdue = reportOverdue(s, now)
		if s.Effect.Kind == "report_send" && s.Effect.State == "queued" {
			return set("waiting_event", "queued_send")
		}
		if s.Effect.Kind == "report_send" && s.Effect.State == "confirmed" || !s.Report.DeliveredAt.IsZero() {
			return set("needs_review", "unresolved_report_observation")
		}
		if result.Overdue {
			if !s.Report.MaterialSufficient {
				if s.Report.ExternalInput == "" {
					return set("needs_review", "insufficient_report_material")
				}
				result.Input = s.Report.ExternalInput
				return set("blocked", "missing_external_input")
			}
			if s.Report.ArtifactReady {
				if !s.Report.CanDeliver {
					result.Gate = "SYM_REPORT_DELIVERY_GATE"
					return set("waiting_user", "delivery_permission")
				}
				return ready("SYM_REPORT_DELIVER", "deliver_report")
			}
			if !s.Report.CanPrepare {
				result.Gate = "SYM_REPORT_PREPARATION_GATE"
				return set("waiting_user", "preparation_permission")
			}
			if s.Report.CanDeliver {
				return ready("SYM_REPORT_CREATE_AND_DELIVER", "report_due")
			}
			return ready("SYM_REPORT_CREATE_ARTIFACT", "report_due")
		}
	} else {
		if s.Effect.Kind == "deploy" && s.Effect.State == "queued" {
			return set("waiting_event", "queued_deployment")
		}
		if s.Effect.Kind == "deploy" && s.Effect.State == "confirmed" {
			if s.Effect.Receipt == "" {
				return set("needs_review", "incomplete_deployment_observation")
			}
			return set("waiting_event", "awaiting_live_verification")
		}
		if s.Deployment.CandidateReady {
			if !s.Deployment.ReleaseVerified {
				result.Gate = "SYM_RELEASE_GATE"
				return set("waiting_user", "release_permission")
			}
			if !s.Deployment.AccessVerified {
				result.Gate = "SYM_ACCESS_GATE"
				return set("waiting_user", "access_permission")
			}
			if !s.Deployment.CanDeploy {
				result.Gate = "SYM_DEPLOY_GATE"
				return set("waiting_user", "deployment_permission")
			}
			return ready("SYM_DEPLOY", "deployment_candidate")
		}
	}
	if s.NextStep == "" {
		return set("waiting_event", "no_next_step")
	}
	return ready(s.NextStep, "next_step")
}

func reportOverdue(s Snapshot, now time.Time) bool {
	return s.Endpoint == "report" && !s.Report.DueAt.IsZero() && !now.Before(s.Report.DueAt) && s.Report.DeliveredAt.IsZero()
}

type Feed string

const (
	Changed Feed = "changed"
	Due     Feed = "due"
	Scope   Feed = "scope"
)

type Entry struct {
	CardID    string
	UpdatedAt time.Time
	UpdatedBy string // discovery must not filter on this
}

type Page struct {
	Entries    []Entry
	NextCursor string
	Done       bool
}

type Checkpoint struct {
	Changed string
	Due     string
	Scope   string
}

type Batch struct {
	Cards []Snapshot
	Next  Checkpoint // only publish after every selected current-card read is accounted for
}

var (
	ErrCursorGone = errors.New("discovery cursor vanished")
	ErrNotVisible = errors.New("card is not visible")
)

// Source is the capability the future MCP/API reader must prove. Existing
// keyword search and activity endpoints do not yet establish this contract.
type Source interface {
	Page(context.Context, string, Feed, string) (Page, error)
	ReadCard(context.Context, string) (Snapshot, error)
}

// Discover is read-only. The production discovery contract is intentionally
// unconnected until completeness, pagination, and visibility are verified.
func Discover(ctx context.Context, source Source, boardID string, old Checkpoint, _ time.Time) (Batch, error) {
	fail := func(err error) (Batch, error) { return Batch{Next: old}, err }
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	next := old
	seen := make(map[string]struct{})
	for _, feed := range []Feed{Changed, Due, Scope} {
		cursor := ""
		switch feed {
		case Changed:
			cursor = old.Changed
		case Due:
			cursor = old.Due
		case Scope:
			cursor = old.Scope
		}
		visited := make(map[string]struct{})
		reset := false
		for {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			if _, exists := visited[cursor]; exists {
				return fail(fmt.Errorf("%s discovery cursor cycle at %q", feed, cursor))
			}
			visited[cursor] = struct{}{}
			page, err := source.Page(ctx, boardID, feed, cursor)
			if errors.Is(err, ErrCursorGone) && !reset {
				cursor = ""
				visited = make(map[string]struct{})
				reset = true
				continue
			}
			if err != nil {
				return fail(err)
			}
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
			for _, entry := range page.Entries {
				if entry.CardID == "" {
					return fail(fmt.Errorf("%s discovery entry has no card ID", feed))
				}
				seen[entry.CardID] = struct{}{}
			}
			if page.Done {
				switch feed {
				case Changed:
					next.Changed = page.NextCursor
				case Due:
					next.Due = page.NextCursor
				case Scope:
					next.Scope = page.NextCursor
				}
				break
			}
			if page.NextCursor == "" {
				return fail(fmt.Errorf("%s discovery page is incomplete without next cursor", feed))
			}
			cursor = page.NextCursor
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	batch := Batch{Next: next}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		card, err := source.ReadCard(ctx, id)
		if canceled := ctx.Err(); canceled != nil {
			return fail(canceled)
		}
		if errors.Is(err, ErrNotVisible) {
			continue
		}
		if err != nil {
			return fail(err)
		}
		if card.ID != id {
			return fail(fmt.Errorf("read card %q returned %q", id, card.ID))
		}
		if card.BoardID == boardID && card.Enrollment.Explicit && card.Enrollment.Verified {
			batch.Cards = append(batch.Cards, card)
		}
	}
	return batch, nil
}
