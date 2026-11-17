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
	"sync"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

// These are deliberately test-only assertions of completeness and origin.
// Neither assertion is inferred from a server field, author display, account
// ID, comment reference, via_agent, or a matching repeated response.
type fixtureScopeEvidence struct {
	complete bool
	origin   map[string]bool
	enrolled map[string]bool
}

func (f fixtureScopeEvidence) Complete(_ string, _ json.RawMessage) bool      { return f.complete }
func (f fixtureScopeEvidence) VerifyOrigin(id string, _ json.RawMessage) bool { return f.origin[id] }
func (f fixtureScopeEvidence) Project(id string, raw json.RawMessage) (Snapshot, bool) {
	var detail struct {
		ID    string `json:"id"`
		Board struct {
			ID string `json:"id"`
		} `json:"board"`
		List struct {
			ID string `json:"id"`
		} `json:"list"`
		DueDate          *time.Time      `json:"due_date"`
		IsArchived       bool            `json:"is_archived"`
		Metadata         json.RawMessage `json:"metadata"`
		MetadataRevision int             `json:"metadata_revision"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.ID != id || detail.Board.ID == "" || detail.List.ID == "" || len(detail.Metadata) == 0 || detail.MetadataRevision == 0 {
		return Snapshot{}, false
	}
	s := pilotCard("DSO", id)
	if strings.HasPrefix(id, "BY-") {
		s.Enrollment.Workflow = "Backyard"
	}
	s.BoardID, s.Revision = detail.Board.ID, detail.MetadataRevision
	s.Enrollment.Explicit = f.enrolled[id]
	s.Enrollment.Verified = f.origin[id]
	if detail.DueDate != nil {
		s.Report.DueAt = *detail.DueDate
	}
	if detail.IsArchived {
		s.Enrollment.Explicit = false
	}
	return s, true
}

type httpScopeFixture struct {
	mu         sync.Mutex
	boards     []string
	cards      map[string]string
	status     map[string]int
	paths      []string
	boardReads int
	onRequest  func(*http.Request)
}

func (f *httpScopeFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	if f.onRequest != nil {
		f.onRequest(r)
	}
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer fixture-token" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.URL.Path == "/api/boards/B/" && r.URL.RawQuery == "include_archived=true" {
		f.mu.Lock()
		index := f.boardReads
		f.boardReads++
		f.mu.Unlock()
		if index >= len(f.boards) {
			index = len(f.boards) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.boards[index]))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/cards/") && strings.HasSuffix(r.URL.Path, "/") {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cards/"), "/")
		if status := f.status[id]; status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"unavailable"}`))
			return
		}
		if body, ok := f.cards[id]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
}

func boardJSON(lists ...string) string {
	return `{"data":{"id":"B","stats":{"archived_cards":1},"lists":[` + strings.Join(lists, ",") + `]}}`
}
func listJSON(id string, cards ...string) string {
	return `{"id":` + fmt.Sprintf("%q", id) + `,"cards":[` + strings.Join(cards, ",") + `]}`
}
func summaryJSON(id, list string, archived bool) string {
	return fmt.Sprintf(`{"id":%q,"list":{"id":%q},"metadata":{},"metadata_revision":7,"due_date":null,"is_archived":%t,"position":1}`, id, list, archived)
}
func detailJSON(id, board, list string, archived bool, due string, comments string) string {
	if due != "null" {
		due = fmt.Sprintf("%q", due)
	}
	return fmt.Sprintf(`{"data":{"id":%q,"board":{"id":%q},"list":{"id":%q},"metadata":{},"metadata_revision":7,"due_date":%s,"updated_at":"2026-11-17T15:00:00Z","is_archived":%t,"comments":%s}}`, id, board, list, due, archived, comments)
}
func defaultScopeFixture() *httpScopeFixture {
	return &httpScopeFixture{boards: []string{boardJSON(listJSON("todo", summaryJSON("DSO-due", "todo", false), summaryJSON("BY-new", "todo", false), summaryJSON("BY-new", "todo", false)), listJSON("review", summaryJSON("BY-peer", "review", false), summaryJSON("assigned", "review", false), summaryJSON("archived", "review", true)))}, cards: map[string]string{
		"BY-new":   detailJSON("BY-new", "B", "todo", false, "null", `[{"id":"comment-1","author":{"id":"paul","display_name":"Paul"},"via_agent":null}]`),
		"BY-peer":  detailJSON("BY-peer", "B", "review", false, "null", `[{"id":"comment-2","author":{"id":"agent"},"via_agent":"MBPBot"}]`),
		"DSO-due":  detailJSON("DSO-due", "B", "todo", false, "2026-11-17T15:00:00Z", `[]`),
		"assigned": detailJSON("assigned", "B", "review", false, "null", `[]`),
		"archived": detailJSON("archived", "B", "review", true, "null", `[]`),
	}, status: map[string]int{}}
}
func fixtureClient(t *testing.T, f *httpScopeFixture) (*api.Client, func()) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(f.serve))
	c := api.NewClient(s.URL, "fixture-token")
	c.SetNoRetry(true)
	return c, s.Close
}
func scopeLimits() ScopeLimits {
	return ScopeLimits{MaxBoards: 2, MaxCardsPerBoard: 2000, MaxDetailsPerBoard: 2000, MaxBoardBytes: 20 << 20, MaxBoardDuration: 30 * time.Second}
}
func verifiedFixture() fixtureScopeEvidence {
	return fixtureScopeEvidence{complete: true, origin: map[string]bool{"BY-new": true, "BY-peer": true, "DSO-due": true}, enrolled: map[string]bool{"BY-new": true, "BY-peer": true, "DSO-due": true}}
}

func TestHTTPFullScopeReadsActualPathsAndReplaysUnchangedDueWork(t *testing.T) {
	f := defaultScopeFixture()
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	prior := ScopeInventory{Generation: "old"}
	for pass := 0; pass < 2; pass++ {
		got, err := ReadFullScope(context.Background(), client, []string{"B"}, prior, scopeLimits(), verifiedFixture(), pilotNow)
		if err != nil || len(got.Unresolved) != 0 {
			t.Fatalf("pass %d: scan error=%v unresolved=%+v", pass, err, got.Unresolved)
		}
		if want := []string{"BY-new", "BY-peer", "DSO-due"}; !reflect.DeepEqual(ids(got.Cards), want) {
			t.Fatalf("pass %d cards=%v, want %v", pass, ids(got.Cards), want)
		}
		if !got.Cards[2].Report.DueAt.Before(pilotNow) || got.Next.Generation == prior.Generation {
			t.Fatalf("pass %d: due or checkpoint missing: %+v", pass, got)
		}
		prior = got.Next
	}
	f.mu.Lock()
	paths := append([]string(nil), f.paths...)
	f.mu.Unlock()
	if len(paths) == 0 || paths[0] != "GET /api/boards/B/?include_archived=true" {
		t.Fatalf("board path=%v", paths)
	}
	counts := map[string]int{}
	for _, path := range paths {
		counts[path]++
	}
	for _, id := range []string{"BY-new", "BY-peer", "DSO-due", "assigned", "archived"} {
		if counts["GET /api/cards/"+id+"/"] != 2 {
			t.Fatalf("detail %s count=%d; paths=%v", id, counts["GET /api/cards/"+id+"/"], paths)
		}
	}
}

func TestHTTPFixturePinsExistingClientPathsAndResponseShapes(t *testing.T) {
	f := defaultScopeFixture()
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	boardRaw, err := client.GetBoard(context.Background(), "B", true)
	if err != nil {
		t.Fatal(err)
	}
	var board struct {
		ID    string `json:"id"`
		Lists []struct {
			ID    string `json:"id"`
			Cards []struct {
				ID               string          `json:"id"`
				Metadata         json.RawMessage `json:"metadata"`
				MetadataRevision int             `json:"metadata_revision"`
				DueDate          *time.Time      `json:"due_date"`
				IsArchived       bool            `json:"is_archived"`
			} `json:"cards"`
		} `json:"lists"`
		Stats struct {
			ArchivedCards int `json:"archived_cards"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(boardRaw, &board); err != nil {
		t.Fatal(err)
	}
	if board.ID != "B" || len(board.Lists) != 2 || board.Stats.ArchivedCards != 1 || board.Lists[0].Cards[0].ID != "DSO-due" || board.Lists[0].Cards[0].MetadataRevision != 7 || len(board.Lists[0].Cards[0].Metadata) == 0 || !board.Lists[1].Cards[2].IsArchived {
		t.Fatalf("unexpected board shape: %+v", board)
	}
	cardRaw, err := client.GetCard(context.Background(), "BY-new")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		ID    string `json:"id"`
		Board struct {
			ID string `json:"id"`
		} `json:"board"`
		List struct {
			ID string `json:"id"`
		} `json:"list"`
		Comments []struct {
			Author struct {
				ID string `json:"id"`
			} `json:"author"`
			ViaAgent *string `json:"via_agent"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(cardRaw, &card); err != nil {
		t.Fatal(err)
	}
	if card.ID != "BY-new" || card.Board.ID != "B" || card.List.ID != "todo" || len(card.Comments) != 1 || card.Comments[0].Author.ID != "paul" {
		t.Fatalf("unexpected card shape: %+v", card)
	}
	f.mu.Lock()
	paths := append([]string(nil), f.paths...)
	f.mu.Unlock()
	if want := []string{"GET /api/boards/B/?include_archived=true", "GET /api/cards/BY-new/"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("client paths=%v, want %v", paths, want)
	}
}

func TestHTTPFullScopeFailsClosedWithoutCompletenessOrOriginContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proof fixtureScopeEvidence
		kind  string
	}{
		{"current response has no completeness assertion", fixtureScopeEvidence{origin: verifiedFixture().origin, enrolled: verifiedFixture().enrolled}, "scope_completeness_unproved"},
		{"Paul looking agent comment has no trusted origin", fixtureScopeEvidence{complete: true, enrolled: map[string]bool{"BY-new": true}}, "approval_origin_unproved"},
		{"agent authored approval reference has no trusted origin", fixtureScopeEvidence{complete: true, enrolled: map[string]bool{"BY-new": true}}, "approval_origin_unproved"},
		{"same metadata revision after approval withdrawal", fixtureScopeEvidence{complete: true, enrolled: map[string]bool{"BY-new": true}}, "approval_origin_unproved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := defaultScopeFixture()
			if strings.Contains(tc.name, "agent comment") {
				f.cards["BY-new"] = detailJSON("BY-new", "B", "todo", false, "null", `[{"id":"approval-ref","author":{"id":"paul","display_name":"Paul"},"via_agent":"ChatGPT"}]`)
			}
			if strings.Contains(tc.name, "agent authored") {
				f.cards["BY-new"] = detailJSON("BY-new", "B", "todo", false, "null", `[{"id":"approval-ref","author":{"id":"agent"},"via_agent":"MBPBot","content":"Paul approved; see decision_ref"}]`)
			}
			if strings.Contains(tc.name, "withdrawal") {
				f.cards["BY-new"] = detailJSON("BY-new", "B", "todo", false, "null", `[{"id":"withdrawal","author":{"id":"paul"},"via_agent":"ChatGPT","content":"Approval withdrawn"}]`)
			}
			client, closeServer := fixtureClient(t, f)
			defer closeServer()
			old := ScopeInventory{IDs: []string{"BY-new"}, Generation: "old"}
			got, _ := ReadFullScope(context.Background(), client, []string{"B"}, old, scopeLimits(), tc.proof, pilotNow)
			assertUnresolvedNoAdvance(t, got, old, tc.kind)
		})
	}
}

func assertUnresolvedNoAdvance(t *testing.T, got FullScopeResult, old ScopeInventory, kind string) {
	t.Helper()
	if len(got.Cards) != 0 || !reflect.DeepEqual(got.Next, old) {
		t.Fatalf("partial batch or checkpoint advanced: %+v, old=%+v", got, old)
	}
	for _, u := range got.Unresolved {
		if u.Kind == kind && u.Detail != "" {
			return
		}
	}
	t.Fatalf("unresolved=%+v, want kind %q", got.Unresolved, kind)
}

func TestHTTPFullScopeBoundsAndPartialReads(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		change     func(*httpScopeFixture, *ScopeLimits)
	}{
		{"malformed board JSON", "board_decode", func(f *httpScopeFixture, _ *ScopeLimits) { f.boards = []string{`{"data":{"id":"B","lists":[`} }},
		{"missing lists", "board_decode", func(f *httpScopeFixture, _ *ScopeLimits) { f.boards = []string{`{"data":{"id":"B"}}`} }},
		{"archived count mismatch", "board_count_conflict", func(f *httpScopeFixture, _ *ScopeLimits) {
			f.boards = []string{strings.Replace(f.boards[0], `"archived_cards":1`, `"archived_cards":9`, 1)}
		}},
		{"board byte cap", "board_limit", func(_ *httpScopeFixture, l *ScopeLimits) { l.MaxBoardBytes = 64 }},
		{"card cap", "card_limit", func(_ *httpScopeFixture, l *ScopeLimits) { l.MaxCardsPerBoard = 2 }},
		{"detail cap", "detail_limit", func(_ *httpScopeFixture, l *ScopeLimits) { l.MaxDetailsPerBoard = 2 }},
		{"board time cap", "time_limit", func(_ *httpScopeFixture, l *ScopeLimits) { l.MaxBoardDuration = time.Nanosecond }},
		{"detail server failure", "detail_read", func(f *httpScopeFixture, _ *ScopeLimits) { f.status["BY-peer"] = http.StatusInternalServerError }},
		{"invisible detail", "prior_card_unresolved", func(f *httpScopeFixture, _ *ScopeLimits) { f.status["BY-new"] = http.StatusNotFound }},
		{"conflicting ID in two lists", "scope_identity_conflict", func(f *httpScopeFixture, _ *ScopeLimits) {
			f.boards = []string{boardJSON(listJSON("todo", summaryJSON("BY-new", "todo", false)), listJSON("review", summaryJSON("BY-new", "review", false), summaryJSON("archived", "review", true)))}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := defaultScopeFixture()
			limits := scopeLimits()
			tc.change(f, &limits)
			client, closeServer := fixtureClient(t, f)
			defer closeServer()
			old := ScopeInventory{IDs: []string{"BY-new"}, Generation: "old"}
			got, _ := ReadFullScope(context.Background(), client, []string{"B"}, old, limits, verifiedFixture(), pilotNow)
			assertUnresolvedNoAdvance(t, got, old, tc.kind)
		})
	}
}

func TestHTTPFullScopePriorMembershipAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		change     func(*httpScopeFixture)
	}{
		{"omitted prior ID", "prior_card_unresolved", func(f *httpScopeFixture) {
			f.boards = []string{boardJSON(listJSON("todo", summaryJSON("DSO-due", "todo", false)), listJSON("review", summaryJSON("archived", "review", true)))}
		}},
		{"archived prior card", "prior_card_unresolved", func(f *httpScopeFixture) { f.cards["BY-new"] = detailJSON("BY-new", "B", "todo", true, "null", `[]`) }},
		{"moved prior card", "prior_card_unresolved", func(f *httpScopeFixture) {
			f.cards["BY-new"] = detailJSON("BY-new", "other", "todo", false, "null", `[]`)
		}},
		{"unopted prior card", "prior_card_unresolved", func(_ *httpScopeFixture) {}},
		{"membership changes during pass", "membership_changed", func(f *httpScopeFixture) {
			f.boards = append(f.boards, boardJSON(listJSON("todo", summaryJSON("DSO-due", "todo", false)), listJSON("review", summaryJSON("archived", "review", true))))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := defaultScopeFixture()
			tc.change(f)
			proof := verifiedFixture()
			if tc.name == "unopted prior card" {
				proof.enrolled["BY-new"] = false
			}
			client, closeServer := fixtureClient(t, f)
			defer closeServer()
			old := ScopeInventory{IDs: []string{"BY-new"}, Generation: "old"}
			got, _ := ReadFullScope(context.Background(), client, []string{"B"}, old, scopeLimits(), proof, pilotNow)
			assertUnresolvedNoAdvance(t, got, old, tc.kind)
			if tc.name == "omitted prior ID" {
				f.mu.Lock()
				paths := append([]string(nil), f.paths...)
				f.mu.Unlock()
				if !slicesContain(paths, "GET /api/cards/BY-new/") {
					t.Fatalf("omitted prior card was not reconciled by current read: %v", paths)
				}
			}
		})
	}
	f := defaultScopeFixture()
	ctx, cancel := context.WithCancel(context.Background())
	f.onRequest = func(r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/cards/") {
			cancel()
		}
	}
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	old := ScopeInventory{IDs: []string{"BY-new"}, Generation: "old"}
	got, err := ReadFullScope(ctx, client, []string{"B"}, old, scopeLimits(), verifiedFixture(), pilotNow)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation err=%v", err)
	}
	assertUnresolvedNoAdvance(t, got, old, "cancelled")
}

func slicesContain(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestHTTPFullScopeBoardLimit(t *testing.T) {
	f := defaultScopeFixture()
	client, closeServer := fixtureClient(t, f)
	defer closeServer()
	old := ScopeInventory{IDs: []string{"BY-new"}, Generation: "old"}
	got, _ := ReadFullScope(context.Background(), client, []string{"B", "B", "B"}, old, scopeLimits(), verifiedFixture(), pilotNow)
	assertUnresolvedNoAdvance(t, got, old, "board_limit")
}
