package workflow

import (
	"context"
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
		origin:   map[string]bool{"DSO-due": origin, "BY-old": origin, "BY-new": origin},
		enrolled: map[string]bool{"DSO-due": enrolled, "BY-old": enrolled, "BY-new": enrolled},
	}
	return f.Project
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
		if due == nil || !due.ObservedAt.Equal(now) || due.DecisionEvidence != "unverified_original_decision" || due.Card.Enrollment.Verified {
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
		}
	}
}
