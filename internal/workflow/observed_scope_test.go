package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

func observedProjector(origin, enrolled bool) ObservationProjector {
	f := fixtureScopeEvidence{
		origin:   map[string]bool{"DSO-due": origin, "BY-old": origin, "BY-new": origin, "BY-peer": origin},
		enrolled: map[string]bool{"DSO-due": enrolled, "BY-old": enrolled, "BY-new": enrolled, "BY-peer": enrolled},
	}
	return f.Project
}

func TestObservedScopeRepeatedMoveRecoveryKeepsEarlierEvidence(t *testing.T) {
	mode := 0
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	old := observedPrior("BY-old")
	limits := scopeLimits()
	limits.MaxBoards = 4
	for cycle := 0; cycle < 3; cycle++ {
		mode = 0
		missing, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
		if err != nil || !missing.PassComplete || len(missing.Observations) != 0 || !hasUnresolvedKind(missing.Unresolved, "prior_card_omitted") {
			t.Fatalf("cycle %d omission: %+v err=%v", cycle, missing, err)
		}
		assertRetainedPrior(t, missing, old, "BY-old")
		mode = 1
		recovered, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, missing.Next, limits, observedProjector(false, true), pilotNow.Add(time.Duration(cycle+1)*time.Minute))
		if err != nil || !recovered.PassComplete || len(recovered.Observations) != 1 || recovered.Observations[0].Card.BoardID != "B2" {
			t.Fatalf("cycle %d recovery: %+v err=%v", cycle, recovered, err)
		}
		old = recovered.Next
	}
}

func TestObservedScopeRetainedHistoryCapDoesNotEvictUnknownPrior(t *testing.T) {
	mode := 0
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	limits := scopeLimits()
	limits.MaxBoards, limits.MaxRetainedRecords = 4, 2
	old := observedPrior("unknown-1")
	old.Records["unknown-2"] = old.Records["unknown-1"]
	first, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if err != nil || !first.PassComplete || len(first.Next.Records) != 2 {
		t.Fatalf("within retained cap: %+v err=%v", first, err)
	}
	old = first.Next
	old.Records["unknown-3"] = old.Records["unknown-1"]
	got, _ := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if got.PassComplete || len(got.Observations) != 0 || len(got.Next.Records) != 3 || !hasUnresolvedKind(got.Unresolved, "inventory_limit") {
		t.Fatalf("cap silently evicted unresolved history: %+v", got)
	}
	for id := range old.Records {
		assertRetainedPrior(t, got, old, id)
	}
}

func TestObservedScopeNewCardAtRetainedCapIsIncomplete(t *testing.T) {
	f := defaultScopeFixture()
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	old := observedPrior("unknown-1")
	limits := scopeLimits()
	limits.MaxRetainedRecords = 1
	got, _ := ReadObservedScope(context.Background(), client, []string{"B"}, old, limits, observedProjector(true, true), pilotNow)
	if got.PassComplete || len(got.Observations) != 0 || len(got.Next.Records) != 1 || !hasUnresolvedKind(got.Unresolved, "inventory_limit") {
		t.Fatalf("new card exceeded retained cap without incomplete pass: %+v", got)
	}
	assertRetainedPrior(t, got, old, "unknown-1")
}

func TestObservedScopeCancellationAfterApparentCandidatePublishesNothing(t *testing.T) {
	f := defaultScopeFixture()
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	old := observedPrior("BY-old")
	ctx, cancel := context.WithCancel(context.Background())
	project := observedProjector(true, true)
	got, err := ReadObservedScope(ctx, client, []string{"B"}, old, scopeLimits(), func(id string, raw json.RawMessage) (Snapshot, bool) {
		card, ok := project(id, raw)
		cancel()
		return card, ok
	}, pilotNow)
	if !errors.Is(err, context.Canceled) || got.PassComplete || len(got.Observations) != 0 {
		t.Fatalf("cancelled pass published partial observations: %+v err=%v", got, err)
	}
	assertRetainedPrior(t, got, old, "BY-old")
}

func TestObservedScopeFailureAfterStoredObservationRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name, firstID, secondID string
		old                     ObservedInventory
		limit                   int
		failSecondRead          bool
	}{
		{"updated record then detail read fails", "BY-old", "BY-peer", observedPrior("BY-old"), 3, true},
		{"new record then detail read fails", "BY-new", "BY-peer", observedPrior("unknown-1"), 3, true},
		{"updated record then history cap", "BY-old", "BY-peer", observedPrior("BY-old"), 1, false},
		{"new record then history cap", "BY-new", "BY-peer", observedPrior("unknown-1"), 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := tc.old
			for id, record := range old.Records {
				record.Card.Decisions = []Decision{{ID: "prior", Text: "original decision", Accepted: true}}
				old.Records[id] = record
			}
			original := ObservedInventory{Records: make(map[string]ObservedRecord, len(old.Records))}
			for id, record := range old.Records {
				record.Card.Decisions = append([]Decision(nil), record.Card.Decisions...)
				original.Records[id] = record
			}
			f := &httpScopeFixture{
				boards: []string{observedBoard("B", summaryJSON(tc.firstID, "todo", false), summaryJSON(tc.secondID, "todo", false))},
				cards: map[string]string{
					tc.firstID:  detailJSON(tc.firstID, "B", "todo", false, "null", `[]`),
					tc.secondID: detailJSON(tc.secondID, "B", "todo", false, "null", `[]`),
				},
				status: map[string]int{},
			}
			if tc.failSecondRead {
				f.status[tc.secondID] = http.StatusServiceUnavailable
			}
			client, closeServer := fixtureClient(t, f)
			defer closeServer()
			limits := scopeLimits()
			limits.MaxRetainedRecords = tc.limit
			got, _ := ReadObservedScope(context.Background(), client, []string{"B"}, old, limits, observedProjector(true, true), pilotNow)
			wantKind := "inventory_limit"
			if tc.failSecondRead {
				wantKind = "detail_read"
			}
			if got.PassComplete || len(got.Observations) != 0 || len(got.Unresolved) != 1 || got.Unresolved[0].Kind != wantKind {
				t.Fatalf("failed pass exposed partial observations: %+v", got)
			}
			if !reflect.DeepEqual(got.Next, original) || !reflect.DeepEqual(old, original) {
				t.Fatalf("failed pass changed inventory: returned=%+v caller=%+v want=%+v", got.Next, old, original)
			}
			for id, record := range got.Next.Records {
				record.Card.Decisions[0].Text = "changed through result"
				got.Next.Records[id] = record
				delete(got.Next.Records, id)
			}
			if !reflect.DeepEqual(old, original) {
				t.Fatalf("returned records or decision slices alias caller: got=%+v want=%+v", old, original)
			}
		})
	}
}

func TestObservedScopeObservationAndNextDecisionsAreIndependent(t *testing.T) {
	f := &httpScopeFixture{
		boards: []string{observedBoard("B", summaryJSON("BY-new", "todo", false))},
		cards:  map[string]string{"BY-new": detailJSON("BY-new", "B", "todo", false, "null", `[]`)},
		status: map[string]int{},
	}
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	project := observedProjector(true, true)
	got, err := ReadObservedScope(context.Background(), client, []string{"B"}, ObservedInventory{}, scopeLimits(), func(id string, raw json.RawMessage) (Snapshot, bool) {
		card, ok := project(id, raw)
		card.Decisions = []Decision{{ID: "current", Text: "original decision", Accepted: true}}
		return card, ok
	}, pilotNow)
	if err != nil || !got.PassComplete || len(got.Observations) != 1 {
		t.Fatalf("complete pass: %+v err=%v", got, err)
	}
	got.Observations[0].Card.Decisions[0].Text = "changed through observation"
	if got.Next.Records["BY-new"].Card.Decisions[0].Text != "original decision" {
		t.Fatal("observation decision slice aliases next inventory")
	}
}

func TestObservedScopeConflictingCurrentMembershipIsIncomplete(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := observedBoard("B1", summaryJSON("BY-new", "todo", false))
		if strings.Contains(r.URL.Path, "/B2/") {
			body = observedBoard("B2", summaryJSON("BY-new", "todo", false))
		}
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	client := api.NewClient(s.URL, "fixture-token")
	client.SetNoRetry(true)
	old := observedPrior("BY-old")
	limits := scopeLimits()
	limits.MaxBoards = 4
	got, _ := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if got.PassComplete || len(got.Observations) != 0 || !hasUnresolvedKind(got.Unresolved, "scope_identity_conflict") {
		t.Fatalf("conflicting membership accepted: %+v", got)
	}
	assertRetainedPrior(t, got, old, "BY-old")
}

func TestObservedScopePriorDetailOutsideScopeIsObservedExit(t *testing.T) {
	mode := 0
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	old := observedPrior("BY-old")
	limits := scopeLimits()
	limits.MaxBoards = 4
	got, err := ReadObservedScope(context.Background(), client, []string{"B1"}, old, limits, observedProjector(false, true), pilotNow)
	if err != nil || !got.PassComplete || len(got.Observations) != 0 || !hasUnresolvedKind(got.Unresolved, "prior_card_observed_exit") {
		t.Fatalf("prior detail move outside scope was missed: %+v err=%v", got, err)
	}
	assertRetainedPrior(t, got, old, "BY-old")
}

func TestObservedScopePriorDetailReadsCountTowardRequestCap(t *testing.T) {
	mode := 1
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	old := observedPrior("unknown-1")
	old.Records["unknown-2"] = observedPrior("unknown-2").Records["unknown-2"]
	limits := scopeLimits()
	limits.MaxBoards, limits.MaxDetailsPerBoard = 4, 1
	got, _ := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if got.PassComplete || len(got.Observations) != 0 || !hasUnresolvedKind(got.Unresolved, "detail_limit") {
		t.Fatalf("prior detail reads omitted from request budget: %+v", got)
	}
	for id := range old.Records {
		assertRetainedPrior(t, got, old, id)
	}
}

func observedPrior(id string) ObservedInventory {
	s := pilotCard("DSO", id)
	s.BoardID = "B1"
	s.Enrollment.Verified = false
	return ObservedInventory{Records: map[string]ObservedRecord{id: {
		Card: s, ObservedAt: pilotNow.Add(-time.Hour), DecisionEvidence: "unverified_original_decision",
	}}}
}

func assertRetainedPrior(t *testing.T, got ObservedScopeResult, old ObservedInventory, id string) {
	t.Helper()
	if !reflect.DeepEqual(got.Next.Records[id], old.Records[id]) {
		t.Fatalf("prior record changed: got=%+v want=%+v", got.Next.Records[id], old.Records[id])
	}
	if _, ok := got.Next.Records[id]; !ok {
		t.Fatalf("prior record %q was retired", id)
	}
}

func hasUnresolvedKind(items []UnresolvedEvidence, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}

func TestObservedScopeRepeatsDueUnchangedWithoutClaimingPermission(t *testing.T) {
	f := defaultScopeFixture()
	// The owner account and via_agent name in these current Web-shaped comments
	// are attribution, not proof of who made the underlying decision.
	f.cards["DSO-due"] = detailJSON("DSO-due", "B", "todo", false, "2026-11-17T15:00:00Z", `[{"author":{"id":"paul","display_name":"Paul"},"via_agent":"MBPBot"}]`)
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	old := ObservedInventory{Records: map[string]ObservedRecord{}}
	for pass := 0; pass < 2; pass++ {
		now := pilotNow.Add(time.Duration(pass) * time.Minute)
		got, err := ReadObservedScope(context.Background(), client, []string{"B"}, old, scopeLimits(), observedProjector(true, true), now)
		if err != nil || !got.PassComplete {
			t.Fatalf("pass %d: err=%v result=%+v", pass, err, got)
		}
		if len(got.Observations) != 3 {
			t.Fatalf("pass %d: observations=%d, want all three opted-in visible cards", pass, len(got.Observations))
		}
		var due *ObservedRecord
		for i := range got.Observations {
			if got.Observations[i].Card.ID == "DSO-due" {
				due = &got.Observations[i]
			}
		}
		if due == nil || !due.ObservedAt.Equal(now) || due.DecisionEvidence != "unverified_original_decision" || due.Card.Enrollment.Verified || !due.Card.Report.CanDeliver {
			t.Fatalf("pass %d: due observation missing or improperly verified: %+v", pass, due)
		}
		if decision := Evaluate(due.Card, now); decision.Status == "ready" || decision.Candidate != "" {
			t.Fatalf("observation became an actionable recommendation: %+v", decision)
		}
		old = got.Next
	}
}

func observedBoard(id string, cards ...string) string {
	return fmt.Sprintf(`{"data":{"id":%q,"stats":{"archived_cards":0},"lists":[{"id":"todo","cards":[%s]}]}}`, id, strings.Join(cards, ","))
}

func observedMultiBoardClient(t *testing.T, mode *int) (*api.Client, func()) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.RequestURI() {
		case "/api/boards/B1/?include_archived=true":
			if *mode == 2 {
				_, _ = w.Write([]byte(observedBoard("B1", summaryJSON("BY-new", "todo", false))))
			} else {
				_, _ = w.Write([]byte(observedBoard("B1")))
			}
		case "/api/boards/B2/?include_archived=true":
			if *mode == 2 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			cards := []string{}
			if *mode == 1 || *mode == 3 {
				cards = append(cards, summaryJSON("BY-old", "todo", *mode == 3))
			}
			if *mode == 3 {
				_, _ = w.Write([]byte(strings.Replace(observedBoard("B2", cards...), `"archived_cards":0`, `"archived_cards":1`, 1)))
			} else {
				_, _ = w.Write([]byte(observedBoard("B2", cards...)))
			}
		case "/api/cards/BY-old/":
			_, _ = w.Write([]byte(detailJSON("BY-old", "B2", "todo", *mode == 3, "null", `[]`)))
		case "/api/cards/BY-new/":
			_, _ = w.Write([]byte(detailJSON("BY-new", "B1", "todo", false, "null", `[]`)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	c := api.NewClient(s.URL, "fixture-token")
	c.SetNoRetry(true)
	return c, s.Close
}

func TestObservedScopeRecoversSettledCrossBoardMoveAndRetainsOmission(t *testing.T) {
	mode := 0 // The card is between board reads and absent from both lists.
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	old := observedPrior("BY-old")
	limits := scopeLimits()
	limits.MaxBoards = 4
	first, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if err != nil || !first.PassComplete || len(first.Observations) != 0 || !hasUnresolvedKind(first.Unresolved, "prior_card_omitted") {
		t.Fatalf("transient omission should be unresolved, not retired: err=%v result=%+v", err, first)
	}
	assertRetainedPrior(t, first, old, "BY-old")
	mode = 1 // Access and contents settle before the next full pass.
	second, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, first.Next, limits, observedProjector(false, true), pilotNow.Add(time.Minute))
	if err != nil || !second.PassComplete || len(second.Observations) != 1 || second.Observations[0].Card.BoardID != "B2" {
		t.Fatalf("settled move not recovered: err=%v result=%+v", err, second)
	}
	if second.Observations[0].Card.Enrollment.Verified || second.Observations[0].DecisionEvidence != "unverified_original_decision" {
		t.Fatalf("move observation acquired action permission: %+v", second.Observations[0])
	}
}

func TestObservedScopeObservedExitDoesNotRetirePriorRecord(t *testing.T) {
	mode := 3 // The previously seen card is now archived on the destination board.
	client, closeServer := observedMultiBoardClient(t, &mode)
	defer closeServer()
	old := observedPrior("BY-old")
	limits := scopeLimits()
	limits.MaxBoards = 4
	got, err := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
	if err != nil || !got.PassComplete || len(got.Observations) != 0 || !hasUnresolvedKind(got.Unresolved, "prior_card_observed_exit") {
		t.Fatalf("archived exit should be observed and unresolved: err=%v result=%+v", err, got)
	}
	assertRetainedPrior(t, got, old, "BY-old")

	mode = 1 // A current explicit opt-out is also evidence, not retirement permission.
	got, err = ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, false), pilotNow)
	if err != nil || !got.PassComplete || len(got.Observations) != 0 || !hasUnresolvedKind(got.Unresolved, "prior_card_observed_exit") {
		t.Fatalf("opt-out should be observed and unresolved: err=%v result=%+v", err, got)
	}
	assertRetainedPrior(t, got, old, "BY-old")
}

func TestObservedScopeIncompletePassKeepsPriorAndPublishesNoPartialBatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int
		cap  int64
	}{
		{name: "access lost after first board", mode: 2},
		{name: "bounded response too large", mode: 1, cap: 32},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			client, closeServer := observedMultiBoardClient(t, &mode)
			defer closeServer()
			old := observedPrior("BY-old")
			limits := scopeLimits()
			limits.MaxBoards = 4
			if tc.cap != 0 {
				limits.MaxBoardBytes = tc.cap
			}
			got, _ := ReadObservedScope(context.Background(), client, []string{"B1", "B2"}, old, limits, observedProjector(false, true), pilotNow)
			if got.PassComplete || len(got.Observations) != 0 || len(got.Unresolved) == 0 {
				t.Fatalf("incomplete pass published observations: %+v", got)
			}
			assertRetainedPrior(t, got, old, "BY-old")
			got.Next.Records["BY-old"] = ObservedRecord{}
			if reflect.DeepEqual(old.Records["BY-old"], ObservedRecord{}) {
				t.Fatal("returned inventory aliases caller")
			}
		})
	}
}

func TestObservedScopeProvenanceRemainsUnverifiedAcrossCommentRoutes(t *testing.T) {
	for _, comment := range []string{
		`[{"author":{"id":"paul","display_name":"Paul"},"via_agent":null}]`,
		`[{"author":{"id":"paul","display_name":"Paul"},"via_agent":"MBPBot"}]`,
	} {
		f := defaultScopeFixture()
		f.cards["DSO-due"] = detailJSON("DSO-due", "B", "todo", false, "2026-11-17T15:00:00Z", comment)
		client, closeServer := fixtureClient(t, f)
		got, err := ReadObservedScope(context.Background(), client, []string{"B"}, ObservedInventory{}, scopeLimits(), observedProjector(true, true), pilotNow)
		closeServer()
		if err != nil || !got.PassComplete {
			t.Fatalf("comment=%s err=%v result=%+v", comment, err, got)
		}
		for _, observation := range got.Observations {
			if observation.DecisionEvidence != "unverified_original_decision" || observation.Card.Enrollment.Verified {
				t.Fatalf("comment attribution promoted to approval: %+v", observation)
			}
			if !hasUnresolvedKind(got.Unresolved, "decision_provenance_unverified") {
				t.Fatalf("unknown provenance was not exposed as unresolved: %+v", got)
			}
		}
	}
}
