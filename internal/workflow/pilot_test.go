package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

var pilotNow = time.Date(2026, 11, 17, 16, 0, 0, 0, time.UTC)

func pilotCard(workflow, id string) Snapshot {
	return Snapshot{
		ID: id, BoardID: "AgentsClients", Revision: 7, Actor: "MBPBot",
		Enrollment: Enrollment{Explicit: true, Verified: true, Workflow: workflow, Version: "pilot-v1"},
		Session:    Session{Owner: "Paul", Checkpointed: true, ReleasedTo: "MBPBot", ReleaseVerified: true},
		NextStep:   "SYM_NEXT", Endpoint: "report",
		Report: Report{CanPrepare: true, CanDeliver: true, MaterialSufficient: true},
	}
}

func assertResult(t *testing.T, got Result, status, candidate, gate, input, acceptance string, stop bool) {
	t.Helper()
	if got.Status != status || got.Candidate != candidate || got.Gate != gate || got.Input != input || got.Acceptance != acceptance || got.Stop != stop {
		t.Fatalf("Evaluate() = %+v; want status=%q candidate=%q gate=%q input=%q acceptance=%q stop=%v", got, status, candidate, gate, input, acceptance, stop)
	}
}

func TestPilotEnrollmentDecisionAndOwnership(t *testing.T) {
	base := pilotCard("Backyard", "BY-1")
	base.Decisions = []Decision{{ID: "no_split", Text: "Do not make monetary splits", Accepted: true}, {ID: "category", Text: "Use one category", Accepted: true}}
	base.Gate = Gate{DecisionID: "no_split", ExcludesNextStep: true, Evidence: "supported"}
	base.NextStep = "SYM_SPLIT"

	tests := []struct {
		name, status, candidate, gate string
		change                        func(*Snapshot)
	}{
		{"accepted negative blocks only its dependent edge", "blocked", "", "", func(*Snapshot) {}},
		{"pending negative requests the exact choice", "waiting_user", "", "no_split", func(s *Snapshot) { s.Decisions[0].Accepted = false }},
		{"unrelated accepted positive remains eligible", "ready", "SYM_CATEGORY", "", func(s *Snapshot) {
			s.Decisions[0].Accepted = false // the pending no affects only its own edge
			s.Gate = Gate{DecisionID: "category", Evidence: "supported"}
			s.NextStep = "SYM_CATEGORY"
		}},
		{"stored acceptance with disputed original evidence pauses dependent step", "waiting_user", "", "no_split", func(s *Snapshot) { s.Gate.Evidence = "disputed" }},
		{"withdrawal changes evidence despite unchanged card metadata", "waiting_user", "", "no_split", func(s *Snapshot) { s.Gate.Evidence = "withdrawn" }},
		{"edit without explicit opt in cannot enroll", "invalid", "", "", func(s *Snapshot) { s.Enrollment.Explicit = false }},
		{"assignment without verified opt in cannot enroll", "invalid", "", "", func(s *Snapshot) { s.Enrollment.Verified = false }},
		{"agent-carried approval reference cannot verify opt in", "invalid", "", "", func(s *Snapshot) {
			s.Enrollment.Verified = false
			s.Decisions = append(s.Decisions, Decision{ID: "copied_approval", Text: "Paul approved enrollment", Accepted: true})
		}},
		{"unsupported definition pin fails closed", "invalid", "", "", func(s *Snapshot) { s.Enrollment.Version = "unknown-v9" }},
		{"active work stays with owner even after heartbeat expiry", "waiting_event", "", "", func(s *Snapshot) { s.Session.Active = true; s.Session.HeartbeatExpired = true }},
		{"unverified handoff cannot release owner", "waiting_event", "", "", func(s *Snapshot) { s.Session.ReleaseVerified = false }},
		{"uncheckpointed handoff cannot release owner", "waiting_event", "", "", func(s *Snapshot) { s.Session.Checkpointed = false }},
		{"handoff to another actor cannot release owner to this reader", "waiting_event", "", "", func(s *Snapshot) { s.Session.ReleasedTo = "OtherAgent" }},
		{"verified checkpoint and handoff permit next evaluation", "ready", "SYM_CATEGORY", "", func(s *Snapshot) {
			s.Gate = Gate{DecisionID: "category", Evidence: "supported"}
			s.NextStep = "SYM_CATEGORY"
			s.Session.Active = false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			s.Decisions = append([]Decision(nil), base.Decisions...)
			tt.change(&s)
			assertResult(t, Evaluate(s, pilotNow), tt.status, tt.candidate, tt.gate, "", "undetermined", false)
		})
	}
}

func TestPilotEvidenceRereadAtSameMetadataRevision(t *testing.T) {
	s := pilotCard("Backyard", "BY-evidence")
	s.Decisions = []Decision{{ID: "allow_step", Text: "Use this scoped step", Accepted: true}}
	s.Gate = Gate{DecisionID: "allow_step", Evidence: "supported"}
	assertResult(t, Evaluate(s, pilotNow), "ready", "SYM_NEXT", "", "", "undetermined", false)
	s.Gate.Evidence = "withdrawn" // original material changed; card revision remains 7
	assertResult(t, Evaluate(s, pilotNow), "waiting_user", "", "allow_step", "", "undetermined", false)
	if s.Revision != 7 {
		t.Fatalf("revision unexpectedly changed: %d", s.Revision)
	}
}

func TestPilotReportAndDeploymentEndpoints(t *testing.T) {
	report := pilotCard("DSO", "DSO-1")
	report.Report.DueAt = pilotNow.Add(-time.Hour)
	tests := []struct {
		name, status, candidate, gate, input, acceptance string
		stop, overdue                                    bool
		change                                           func(*Snapshot)
	}{
		{"due report with sufficient material is assigned output", "ready", "SYM_REPORT_CREATE_AND_DELIVER", "", "", "undetermined", false, true, func(*Snapshot) {}},
		{"missing external prerequisite blocks only that edge", "blocked", "", "", "SYM_EXTERNAL_SOURCE", "undetermined", false, true, func(s *Snapshot) { s.Report.MaterialSufficient = false; s.Report.ExternalInput = "SYM_EXTERNAL_SOURCE" }},
		{"artifact grant does not imply delivery grant", "ready", "SYM_REPORT_CREATE_ARTIFACT", "", "", "undetermined", false, true, func(s *Snapshot) { s.Report.CanDeliver = false }},
		{"ready artifact waits for delivery permission", "waiting_user", "", "SYM_REPORT_DELIVERY_GATE", "", "undetermined", false, true, func(s *Snapshot) { s.Report.ArtifactReady = true; s.Report.CanDeliver = false }},
		{"queued send does not produce another send", "waiting_event", "", "", "", "undetermined", false, true, func(s *Snapshot) { s.Effect = Effect{Kind: "report_send", State: "queued"} }},
		{"uncertain send cannot be replayed", "needs_review", "", "", "", "undetermined", false, true, func(s *Snapshot) { s.Effect = Effect{Kind: "report_send", State: "uncertain"} }},
		{"confirmed report stops at exact endpoint despite residual questions", "stopped", "", "", "", "met", true, false, func(s *Snapshot) {
			s.Report.CriteriaComplete = true
			s.Report.DeliveredAt = s.Report.DueAt.Add(-time.Minute)
			s.Effect = Effect{Kind: "report_send", State: "confirmed", Receipt: "send-1"}
		}},
		{"broader requested investigation continues without duplicate report", "ready", "SYM_INVESTIGATE_NEXT", "", "", "undetermined", false, false, func(s *Snapshot) {
			s.Report.CriteriaComplete = true
			s.Report.DeliveredAt = s.Report.DueAt.Add(-time.Minute)
			s.Report.ContinueRequested = true
			s.Effect = Effect{Kind: "report_send", State: "confirmed", Receipt: "send-1"}
		}},
		{"confirmed late report stops but misses on time acceptance", "stopped", "", "", "", "unmet", true, false, func(s *Snapshot) {
			s.Report.CriteriaComplete = true
			s.Report.DeliveredAt = s.Report.DueAt.Add(time.Minute)
			s.Effect = Effect{Kind: "report_send", State: "confirmed", Receipt: "send-1"}
		}},
		{"deployment candidate precedes deployment receipt", "ready", "SYM_DEPLOY", "", "", "undetermined", false, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, ReleaseVerified: true, AccessVerified: true}
		}},
		{"deployment cannot borrow artifact authority as release authority", "waiting_user", "", "SYM_RELEASE_GATE", "", "undetermined", false, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, AccessVerified: true}
		}},
		{"deployment requires current access authority", "waiting_user", "", "SYM_ACCESS_GATE", "", "undetermined", false, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, ReleaseVerified: true}
		}},
		{"uncertain deployment cannot be retried", "needs_review", "", "", "", "undetermined", false, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, ReleaseVerified: true, AccessVerified: true}
			s.Effect = Effect{Kind: "deploy", State: "uncertain"}
		}},
		{"deployment receipt without fresh live verification is incomplete", "waiting_event", "", "", "", "undetermined", false, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, ReleaseVerified: true, AccessVerified: true, TransitionReceipt: "move-1", LiveVerifiedAt: pilotNow.Add(-time.Hour)}
			s.Effect = Effect{Kind: "deploy", State: "confirmed", Receipt: "deploy-1"}
		}},
		{"correlated deployment and transition receipts with fresh live proof complete", "complete", "", "", "", "met", true, false, func(s *Snapshot) {
			s.Endpoint = "deployed_verified"
			s.Deployment = Deployment{CandidateReady: true, CanDeploy: true, ReleaseVerified: true, AccessVerified: true, TransitionReceipt: "move-1", LiveVerifiedAt: pilotNow, LiveEvidenceCurrent: true}
			s.Effect = Effect{Kind: "deploy", State: "confirmed", Receipt: "deploy-1"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := report
			tt.change(&s)
			got := Evaluate(s, pilotNow)
			assertResult(t, got, tt.status, tt.candidate, tt.gate, tt.input, tt.acceptance, tt.stop)
			if got.Overdue != tt.overdue {
				t.Fatalf("Overdue = %v, want %v", got.Overdue, tt.overdue)
			}
		})
	}
}

type pageKey struct {
	feed   Feed
	cursor string
}
type fakeSource struct {
	pages    map[pageKey]Page
	errs     map[pageKey]error
	cards    map[string]Snapshot
	readErrs map[string]error
	reads    []string
}

func (f *fakeSource) Page(_ context.Context, _ string, feed Feed, cursor string) (Page, error) {
	k := pageKey{feed, cursor}
	if err := f.errs[k]; err != nil {
		return Page{}, err
	}
	return f.pages[k], nil
}
func (f *fakeSource) ReadCard(_ context.Context, id string) (Snapshot, error) {
	f.reads = append(f.reads, id)
	if err := f.readErrs[id]; err != nil {
		return Snapshot{}, err
	}
	return f.cards[id], nil
}
func sourceWithEmptyFeeds() *fakeSource {
	return &fakeSource{pages: map[pageKey]Page{
		{Changed, ""}: {Done: true}, {Due, ""}: {Done: true}, {Scope, ""}: {Done: true},
	}, errs: map[pageKey]error{}, cards: map[string]Snapshot{}, readErrs: map[string]error{}}
}
func ids(cards []Snapshot) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.ID
	}
	return out
}

func TestPilotDiscoveryUnknownDueAndAmbiguousOrder(t *testing.T) {
	f := sourceWithEmptyFeeds()
	by := pilotCard("Backyard", "BY-new")
	dso := pilotCard("DSO", "DSO-due")
	f.cards[by.ID], f.cards[dso.ID] = by, dso
	// The due card is unchanged. Same-timestamp edits by two authors must both survive.
	f.cards["BY-peer"] = pilotCard("Backyard", "BY-peer")
	f.pages[pageKey{Changed, ""}] = Page{Entries: []Entry{{"BY-peer", pilotNow, "OtherAgent"}, {by.ID, pilotNow, "Paul"}}, NextCursor: "c2"}
	f.pages[pageKey{Changed, "c2"}] = Page{Entries: []Entry{{by.ID, pilotNow, "Paul"}}, NextCursor: "c3", Done: true}
	f.pages[pageKey{Due, ""}] = Page{Entries: []Entry{{dso.ID, pilotNow.Add(-time.Hour), ""}}, NextCursor: "d2", Done: true}
	f.pages[pageKey{Scope, ""}] = Page{Done: true, NextCursor: "s2"}
	got, err := Discover(context.Background(), f, "AgentsClients", Checkpoint{}, pilotNow)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := []string{"BY-new", "BY-peer", "DSO-due"}; !reflect.DeepEqual(ids(got.Cards), want) {
		t.Fatalf("IDs = %v, want %v", ids(got.Cards), want)
	}
	if got.Next != (Checkpoint{Changed: "c3", Due: "d2", Scope: "s2"}) {
		t.Fatalf("Next = %+v; cursor must follow all accounted pages", got.Next)
	}
}

func TestPilotDiscoveryPartialFailureKeepsCheckpoint(t *testing.T) {
	f := sourceWithEmptyFeeds()
	f.pages[pageKey{Changed, "old"}] = Page{Entries: []Entry{{"BY-new", pilotNow, "Paul"}}, NextCursor: "c2"}
	f.errs[pageKey{Changed, "c2"}] = errors.New("page unavailable")
	old := Checkpoint{Changed: "old", Due: "d0", Scope: "s0"}
	got, err := Discover(context.Background(), f, "AgentsClients", old, pilotNow)
	if err == nil {
		t.Fatal("Discover() succeeded after a partial page failure")
	}
	if len(got.Cards) != 0 || got.Next != old {
		t.Fatalf("partial batch/cursor leaked: %+v; want no candidates and unchanged checkpoint %+v", got, old)
	}
}

func TestPilotDiscoveryLostCursorAndScopeReconciliation(t *testing.T) {
	f := sourceWithEmptyFeeds()
	f.errs[pageKey{Changed, "vanished"}] = ErrCursorGone
	f.pages[pageKey{Scope, ""}] = Page{Entries: []Entry{{"DSO-omitted", pilotNow, ""}}, NextCursor: "scope2", Done: true}
	f.cards["DSO-omitted"] = pilotCard("DSO", "DSO-omitted")
	got, err := Discover(context.Background(), f, "AgentsClients", Checkpoint{Changed: "vanished"}, pilotNow)
	if err != nil {
		t.Fatalf("Discover() could not reconcile vanished cursor: %v", err)
	}
	if want := []string{"DSO-omitted"}; !reflect.DeepEqual(ids(got.Cards), want) {
		t.Fatalf("IDs = %v; scope reconciliation should recover omitted enrolled card", ids(got.Cards))
	}
	if got.Next.Changed == "vanished" {
		t.Fatalf("vanished cursor retained: %+v", got.Next)
	}
}

func TestPilotDiscoveryCurrentReadFiltersMovedDeletedAndUnenrolled(t *testing.T) {
	f := sourceWithEmptyFeeds()
	f.pages[pageKey{Scope, ""}] = Page{Entries: []Entry{{"BY-moved", pilotNow, ""}, {"DSO-deleted", pilotNow, ""}, {"BY-assigned", pilotNow, ""}, {"DSO-live", pilotNow, ""}}, Done: true}
	moved := pilotCard("Backyard", "BY-moved")
	moved.BoardID = "OtherBoard"
	unopted := pilotCard("Backyard", "BY-assigned")
	unopted.Enrollment.Explicit = false
	f.cards[moved.ID], f.cards[unopted.ID], f.cards["DSO-live"] = moved, unopted, pilotCard("DSO", "DSO-live")
	f.readErrs["DSO-deleted"] = ErrNotVisible
	got, err := Discover(context.Background(), f, "AgentsClients", Checkpoint{}, pilotNow)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := []string{"DSO-live"}; !reflect.DeepEqual(ids(got.Cards), want) {
		t.Fatalf("IDs = %v, want %v", ids(got.Cards), want)
	}
	if len(f.reads) != 4 {
		t.Fatalf("current reads = %v; want a read for every candidate before filtering", f.reads)
	}
}

func TestPilotReplayAndCrashSafeReceiptObservation(t *testing.T) {
	f := sourceWithEmptyFeeds()
	f.pages[pageKey{Due, ""}] = Page{Entries: []Entry{{"DSO-report", pilotNow, ""}}, Done: true}
	card := pilotCard("DSO", "DSO-report")
	card.Report.DueAt = pilotNow.Add(-time.Hour)
	card.Report.CriteriaComplete = true
	card.Report.DeliveredAt = pilotNow.Add(-time.Minute)
	card.Effect = Effect{Kind: "report_send", State: "confirmed", Receipt: "durable-send-1"}
	f.cards[card.ID] = card
	for i := 0; i < 2; i++ { // crash before checkpoint publication: replay the same page
		batch, err := Discover(context.Background(), f, "AgentsClients", Checkpoint{}, pilotNow)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if len(batch.Cards) != 1 {
			t.Fatalf("replay %d cards = %v", i, ids(batch.Cards))
		}
		assertResult(t, Evaluate(batch.Cards[0], pilotNow), "stopped", "", "", "", "met", true)
	}
}
