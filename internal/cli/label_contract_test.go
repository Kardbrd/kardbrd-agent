package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type contractLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type labelContractServer struct {
	t *testing.T

	mu             sync.Mutex
	server         *httptest.Server
	labels         []contractLabel
	catalog        []contractLabel
	title          string
	calls          []string
	additions      []string
	removals       []string
	genericBodies  []map[string]json.RawMessage
	failNextAdd    bool
	failNextRemove bool
}

func newLabelContractServer(t *testing.T, labelIDs ...string) *labelContractServer {
	t.Helper()
	labels := make([]contractLabel, 0, len(labelIDs))
	for _, labelID := range labelIDs {
		labels = append(labels, contractLabel{ID: labelID, Name: labelID})
	}
	contract := &labelContractServer{
		t:       t,
		labels:  append([]contractLabel(nil), labels...),
		catalog: []contractLabel{{ID: "A", Name: "A"}, {ID: "B", Name: "B"}, {ID: "C", Name: "C"}},
		title:   "Original",
	}
	contract.server = httptest.NewServer(http.HandlerFunc(contract.handle))
	t.Cleanup(contract.server.Close)
	return contract
}

func (s *labelContractServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/cards/card1/":
		s.calls = append(s.calls, "get-card")
		writeCLITestJSON(s.t, w, map[string]any{"data": map[string]any{
			"id": "card1", "board": map[string]string{"id": "board1"}, "labels": s.labels, "title": s.title,
		}})
	case r.Method == http.MethodGet && r.URL.Path == "/api/boards/board1/":
		s.calls = append(s.calls, "get-board")
		if r.Header.Get("Accept") == "text/markdown" {
			_, _ = w.Write([]byte("# Board\n\n## Labels\n\n- A\n- B\n\n## Lists\n\n- Todo\n"))
			return
		}
		writeCLITestJSON(s.t, w, map[string]any{"data": map[string]any{"id": "board1", "labels": s.catalog}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/cards/card1/labels/":
		var body struct {
			LabelID string `json:"label_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.t.Fatal(err)
		}
		s.calls = append(s.calls, "add:"+body.LabelID)
		s.additions = append(s.additions, body.LabelID)
		if s.failNextAdd {
			s.failNextAdd = false
			writeContractError(s.t, w, http.StatusBadRequest, "add failed")
			return
		}
		for _, label := range s.catalog {
			if label.ID == body.LabelID && !containsContractLabel(s.labels, body.LabelID) {
				s.labels = append(s.labels, label)
			}
		}
		writeCLITestJSON(s.t, w, map[string]any{"data": map[string]any{}})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/cards/card1/labels/"):
		labelID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cards/card1/labels/"), "/")
		s.calls = append(s.calls, "remove:"+labelID)
		s.removals = append(s.removals, labelID)
		if s.failNextRemove {
			s.failNextRemove = false
			writeContractError(s.t, w, http.StatusBadRequest, "remove failed")
			return
		}
		for index, label := range s.labels {
			if label.ID == labelID {
				s.labels = append(s.labels[:index], s.labels[index+1:]...)
				break
			}
		}
		writeCLITestJSON(s.t, w, map[string]any{"data": map[string]any{}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/cards/card1/":
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.t.Fatal(err)
		}
		s.calls = append(s.calls, "generic")
		s.genericBodies = append(s.genericBodies, body)
		if rawTitle, ok := body["title"]; ok {
			if err := json.Unmarshal(rawTitle, &s.title); err != nil {
				s.t.Fatal(err)
			}
		}
		writeCLITestJSON(s.t, w, map[string]any{"data": map[string]any{"id": "card1", "title": s.title}})
	default:
		writeContractError(s.t, w, http.StatusNotFound, "unexpected route")
	}
}

func (s *labelContractServer) run(args ...string) (string, string, error) {
	allArgs := append([]string{"--api-url", s.server.URL, "--token", "tok"}, args...)
	return executeRoot(allArgs...)
}

func (s *labelContractServer) snapshot() (labels []string, additions []string, removals []string, calls []string, genericBodies []map[string]json.RawMessage, title string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, label := range s.labels {
		labels = append(labels, label.ID)
	}
	return labels, append([]string(nil), s.additions...), append([]string(nil), s.removals...), append([]string(nil), s.calls...), append([]map[string]json.RawMessage(nil), s.genericBodies...), s.title
}

func (s *labelContractServer) failAddOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextAdd = true
}

func (s *labelContractServer) failRemoveOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNextRemove = true
}

func containsContractLabel(labels []contractLabel, id string) bool {
	for _, label := range labels {
		if label.ID == id {
			return true
		}
	}
	return false
}

func writeContractError(t *testing.T, w http.ResponseWriter, status int, message string) {
	t.Helper()
	w.WriteHeader(status)
	writeCLITestJSON(t, w, map[string]string{"error": message, "code": "contract"})
}

func TestCardLabelReplacementNoOpAndDuplicateIDs(t *testing.T) {
	server := newLabelContractServer(t, "A", "B")
	if _, stderr, err := server.run("card", "update", "card1", "--label-ids", "B", "--label-ids", "C", "--label-ids", "C"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	labels, additions, removals, _, genericBodies, _ := server.snapshot()
	if !reflect.DeepEqual(labels, []string{"B", "C"}) || !reflect.DeepEqual(additions, []string{"C"}) || !reflect.DeepEqual(removals, []string{"A"}) {
		t.Fatalf("first update labels=%#v additions=%#v removals=%#v", labels, additions, removals)
	}
	if len(genericBodies) != 0 {
		t.Fatalf("label-only update made %d generic requests", len(genericBodies))
	}

	if _, stderr, err := server.run("card", "update", "card1", "--label-ids", "C", "--label-ids", "B"); err != nil {
		t.Fatalf("unexpected no-op error: %v\nstderr: %s", err, stderr)
	}
	_, additions, removals, _, _, _ = server.snapshot()
	if !reflect.DeepEqual(additions, []string{"C"}) || !reflect.DeepEqual(removals, []string{"A"}) {
		t.Fatalf("no-op sent mutations additions=%#v removals=%#v", additions, removals)
	}
}

func TestCardLabelReplacementValidatesBeforeMutation(t *testing.T) {
	server := newLabelContractServer(t, "A")
	_, _, err := server.run("card", "update", "card1", "--title", "Changed", "--label-ids", "missing")
	if err == nil || !strings.Contains(err.Error(), "not defined on this card's board") {
		t.Fatalf("error = %v", err)
	}
	labels, additions, removals, calls, genericBodies, title := server.snapshot()
	if !reflect.DeepEqual(labels, []string{"A"}) || len(additions) != 0 || len(removals) != 0 || len(genericBodies) != 0 || title != "Original" {
		t.Fatalf("validation mutated state labels=%#v additions=%#v removals=%#v generic=%#v title=%q", labels, additions, removals, genericBodies, title)
	}
	if !reflect.DeepEqual(calls, []string{"get-card", "get-board"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestCardLabelReplacementAddFailurePreservesExistingLabels(t *testing.T) {
	server := newLabelContractServer(t, "A", "B")
	server.failAddOnce()
	_, _, err := server.run("card", "update", "card1", "--label-ids", "B", "--label-ids", "C")
	if err == nil || !strings.Contains(err.Error(), "label reconciliation incomplete while adding") {
		t.Fatalf("error = %v", err)
	}
	labels, _, removals, _, _, _ := server.snapshot()
	if !reflect.DeepEqual(labels, []string{"A", "B"}) || len(removals) != 0 {
		t.Fatalf("add failure labels=%#v removals=%#v", labels, removals)
	}
}

func TestCardLabelReplacementRemovalFailureRetriesToConvergence(t *testing.T) {
	server := newLabelContractServer(t, "A", "B")
	server.failRemoveOnce()
	_, _, err := server.run("card", "update", "card1", "--label-ids", "B", "--label-ids", "C")
	if err == nil || !strings.Contains(err.Error(), "label reconciliation incomplete while removing") {
		t.Fatalf("error = %v", err)
	}
	labels, additions, removals, _, _, _ := server.snapshot()
	if !reflect.DeepEqual(labels, []string{"A", "B", "C"}) || !reflect.DeepEqual(additions, []string{"C"}) || !reflect.DeepEqual(removals, []string{"A"}) {
		t.Fatalf("failed removal labels=%#v additions=%#v removals=%#v", labels, additions, removals)
	}

	if _, stderr, err := server.run("card", "update", "card1", "--label-ids", "B", "--label-ids", "C"); err != nil {
		t.Fatalf("retry error: %v\nstderr: %s", err, stderr)
	}
	labels, additions, removals, _, _, _ = server.snapshot()
	if !reflect.DeepEqual(labels, []string{"B", "C"}) || !reflect.DeepEqual(additions, []string{"C"}) || !reflect.DeepEqual(removals, []string{"A", "A"}) {
		t.Fatalf("retry did not converge labels=%#v additions=%#v removals=%#v", labels, additions, removals)
	}
}

func TestCardUpdateClearLabelsIsIdempotentAndConflictsWithAliases(t *testing.T) {
	server := newLabelContractServer(t, "A", "B")
	if _, stderr, err := server.run("card", "update", "card1", "--clear-labels"); err != nil {
		t.Fatalf("clear error: %v\nstderr: %s", err, stderr)
	}
	labels, _, removals, _, _, _ := server.snapshot()
	if len(labels) != 0 || !reflect.DeepEqual(removals, []string{"A", "B"}) {
		t.Fatalf("clear labels=%#v removals=%#v", labels, removals)
	}
	if _, stderr, err := server.run("card", "update", "card1", "--clear-labels"); err != nil {
		t.Fatalf("idempotent clear error: %v\nstderr: %s", err, stderr)
	}
	_, _, removals, _, _, _ = server.snapshot()
	if !reflect.DeepEqual(removals, []string{"A", "B"}) {
		t.Fatalf("idempotent clear removed labels: %#v", removals)
	}

	for _, args := range [][]string{
		{"card", "update", "card1", "--clear-labels", "--label", "A"},
		{"card", "update", "card1", "--clear-labels", "--label-ids", "A"},
	} {
		_, _, err := server.run(args...)
		if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatalf("args %#v error = %v", args, err)
		}
	}
}

func TestCardLabelReplacementMixedScalarUpdateReturnsRefreshedCard(t *testing.T) {
	server := newLabelContractServer(t, "A", "B")
	stdout, stderr, err := server.run("card", "update", "card1", "--title", "Changed", "--label", "B", "--label", "C")
	if err != nil {
		t.Fatalf("mixed update error: %v\nstderr: %s", err, stderr)
	}
	labels, _, _, calls, genericBodies, title := server.snapshot()
	if !reflect.DeepEqual(calls, []string{"get-card", "get-board", "generic", "add:C", "remove:A", "get-card"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if len(genericBodies) != 1 || genericBodies[0]["label_ids"] != nil {
		t.Fatalf("generic bodies = %#v", genericBodies)
	}
	if title != "Changed" || !reflect.DeepEqual(labels, []string{"B", "C"}) {
		t.Fatalf("final state title=%q labels=%#v", title, labels)
	}
	var result struct {
		Title  string          `json:"title"`
		Labels []contractLabel `json:"labels"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Title != "Changed" || !reflect.DeepEqual(result.Labels, []contractLabel{{ID: "B", Name: "B"}, {ID: "C", Name: "C"}}) {
		t.Fatalf("final output = %#v", result)
	}
}

func TestBoardLabelsExtractsDetailCatalogInJSONAndMarkdown(t *testing.T) {
	server := newLabelContractServer(t, "A")
	stdout, stderr, err := server.run("board", "labels", "board1")
	if err != nil {
		t.Fatalf("json labels error: %v\nstderr: %s", err, stderr)
	}
	var labels []contractLabel
	if err := json.Unmarshal([]byte(stdout), &labels); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(labels, []contractLabel{{ID: "A", Name: "A"}, {ID: "B", Name: "B"}, {ID: "C", Name: "C"}}) {
		t.Fatalf("json labels = %#v", labels)
	}

	stdout, stderr, err = server.run("--format", "md", "board", "labels", "board1")
	if err != nil {
		t.Fatalf("markdown labels error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "## Labels\n\n- A\n- B\n" {
		t.Fatalf("markdown labels = %q", stdout)
	}
}

func TestBoardLabelsAndCardReplacementErrorsAreControlled(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"bad token","code":"auth"}`, want: "bad token"},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"forbidden","code":"permission"}`, want: "forbidden"},
		{name: "not found", status: http.StatusNotFound, body: `{"error":"missing","code":"not_found"}`, want: "missing"},
		{name: "html body", status: http.StatusNotFound, body: "<html><body>missing</body></html>", want: "API returned an HTML error response"},
		{name: "malformed envelope", status: http.StatusOK, body: `{"data":{"id":[],"labels":[]}}`, want: "decode board"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, _, err := executeRoot("--api-url", server.URL, "--token", "tok", "board", "labels", "board1")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "<html>") {
				t.Fatalf("error leaked HTML: %q", err)
			}
		})
	}

	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations++
		}
		writeCLITestJSON(t, w, map[string]any{"data": map[string]any{"id": "card1", "board": []string{}, "labels": []contractLabel{}}})
	}))
	defer server.Close()
	_, _, err := executeRoot("--api-url", server.URL, "--token", "tok", "card", "update", "card1", "--label-ids", "A")
	if err == nil || !strings.Contains(err.Error(), "decode card") {
		t.Fatalf("malformed card error = %v", err)
	}
	if mutations != 0 {
		t.Fatalf("malformed card response caused %d mutations", mutations)
	}
}
