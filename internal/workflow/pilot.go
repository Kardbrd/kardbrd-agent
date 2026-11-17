package workflow

import (
	"context"
	"errors"
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
func Evaluate(Snapshot, time.Time) Result { return Result{Status: "unimplemented"} }

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
func Discover(context.Context, Source, string, Checkpoint, time.Time) (Batch, error) {
	return Batch{}, errors.New("discovery adapter not implemented")
}
