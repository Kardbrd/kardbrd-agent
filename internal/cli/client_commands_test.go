package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

func TestClientCommandPathParity(t *testing.T) {
	cmd := NewRootCommand()
	got := commandPaths(cmd, "")
	want := []string{
		"activity",
		"agent",
		"agent start",
		"agent validate",
		"attachment",
		"attachment get",
		"attachment list",
		"attachment markdown",
		"attachment upload",
		"board",
		"board activity",
		"board archive",
		"board favorite",
		"board get",
		"board labels",
		"board list",
		"board members",
		"board search",
		"board unarchive",
		"board update",
		"card",
		"card activity",
		"card archive",
		"card assign",
		"card create",
		"card get",
		"card move",
		"card move-to-board",
		"card unarchive",
		"card unassign",
		"card update",
		"checklist",
		"checklist add-todo",
		"checklist add-todos",
		"checklist complete",
		"checklist create",
		"checklist extract",
		"checklist reopen",
		"checklist update",
		"comment",
		"comment add",
		"comment delete",
		"comment react",
		"link",
		"link add",
		"link delete",
		"link list",
		"link update",
		"list",
		"list create",
		"list move",
		"md",
		"search",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command path mismatch:\nwant: %#v\n got: %#v", want, got)
	}
}

func TestBoardListOutputsIndentedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/boards/" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeCLITestJSON(t, w, map[string]any{"data": []map[string]string{{"id": "board1"}}})
	}))
	defer server.Close()

	stdout, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "board", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertCLIContains(t, stdout, "{\n")
	assertCLIContains(t, stdout, "  \"id\": \"board1\"")
}

func TestMDCardOutputsRawMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/markdown" {
			t.Fatalf("expected markdown accept header, got %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte("# Card\n"))
	}))
	defer server.Close()

	stdout, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "md", "card", "card1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "# Card\n" {
		t.Fatalf("unexpected markdown output: %q", stdout)
	}
}

func TestCommentDeleteConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/api/cards/card1/comments/comment1/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		writeCLITestJSON(t, w, map[string]any{"data": map[string]any{"deleted": true}})
	}))
	defer server.Close()

	stdout, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "comment", "delete", "card1", "comment1")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if stdout != "Comment deleted.\n" {
		t.Fatalf("unexpected output: %q", stdout)
	}
}

func TestCardUpdateAcceptsLabelIDsAlias(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/cards/card1/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "card1"}})
	}))
	defer server.Close()

	_, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "card", "update", "card1", "--label-ids", "lbl1", "--label-ids", "lbl2")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	labels := body["label_ids"].([]any)
	assertEqual(t, "lbl1", labels[0].(string))
	assertEqual(t, "lbl2", labels[1].(string))
}

func TestChecklistUpdateAcceptsNoCompleted(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/cards/card1/checklists/check1/items/item1/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "item1"}})
	}))
	defer server.Close()

	_, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "checklist", "update", "card1", "--checklist", "check1", "--item", "item1", "--no-completed")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertEqual(t, false, body["is_completed"].(bool))
}

func commandPaths(cmd *cobra.Command, prefix string) []string {
	var paths []string
	for _, child := range cmd.Commands() {
		name := child.Name()
		if name == "help" || name == "completion" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + " " + name
		}
		paths = append(paths, path)
		paths = append(paths, commandPaths(child, path)...)
	}
	sort.Strings(paths)
	return paths
}

func writeCLITestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
