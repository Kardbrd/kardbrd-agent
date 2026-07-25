package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
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

func TestCommentAddExpandsEscapedNewlines(t *testing.T) {
	var body map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/cards/card1/comments/" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "comment1"}})
	}))
	defer server.Close()

	_, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "comment", "add", "card1", "Ready to roll.\\n\\n@Paul")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertEqual(t, "Ready to roll.\n\n@Paul", body["content"])
}

func TestPublishingCommandsExpandEscapedNewlines(t *testing.T) {
	value := "First\\n\\nSecond"
	wantValue := "First\n\nSecond"

	tests := []struct {
		name     string
		method   string
		path     string
		args     []string
		wantBody map[string]any
	}{
		{
			name:     "board update name",
			method:   "PATCH",
			path:     "/api/boards/board1/",
			args:     []string{"board", "update", "board1", "--name", value},
			wantBody: map[string]any{"name": wantValue},
		},
		{
			name:   "card create title and description",
			method: "POST",
			path:   "/api/boards/board1/lists/list1/cards/",
			args:   []string{"card", "create", "--board", "board1", "--list", "list1", "--title", value, "--description", value},
			wantBody: map[string]any{
				"title":       wantValue,
				"description": wantValue,
			},
		},
		{
			name:   "card update title and description",
			method: "POST",
			path:   "/api/cards/card1/",
			args:   []string{"card", "update", "card1", "--title", value, "--description", value},
			wantBody: map[string]any{
				"title":       wantValue,
				"description": wantValue,
			},
		},
		{
			name:     "checklist create title",
			method:   "POST",
			path:     "/api/cards/card1/checklists/",
			args:     []string{"checklist", "create", "card1", "--title", value},
			wantBody: map[string]any{"title": wantValue},
		},
		{
			name:     "checklist add todo title",
			method:   "POST",
			path:     "/api/cards/card1/checklists/check1/items/",
			args:     []string{"checklist", "add-todo", "card1", "--checklist", "check1", "--title", value},
			wantBody: map[string]any{"title": wantValue},
		},
		{
			name:   "checklist add todos title and items",
			method: "POST",
			path:   "/api/cards/card1/checklists/bulk/",
			args:   []string{"checklist", "add-todos", "card1", "--title", value, value, "Plain"},
			wantBody: map[string]any{
				"title": wantValue,
				"items": []any{wantValue, "Plain"},
			},
		},
		{
			name:     "checklist update title",
			method:   "PATCH",
			path:     "/api/cards/card1/checklists/check1/items/item1/",
			args:     []string{"checklist", "update", "card1", "--checklist", "check1", "--item", "item1", "--title", value},
			wantBody: map[string]any{"title": wantValue},
		},
		{
			name:     "checklist extract todos prefix",
			method:   "POST",
			path:     "/api/cards/card1/extract-todos-to-cards/",
			args:     []string{"checklist", "extract", "card1", "--target-list", "list1", "--prefix", value},
			wantBody: map[string]any{"prefix": wantValue},
		},
		{
			name:     "checklist extract checklist prefix",
			method:   "POST",
			path:     "/api/cards/card1/checklists/check1/extract-to-cards/",
			args:     []string{"checklist", "extract", "card1", "--checklist", "check1", "--target-list", "list1", "--prefix", value},
			wantBody: map[string]any{"prefix": wantValue},
		},
		{
			name:     "link add display text",
			method:   "POST",
			path:     "/api/cards/card1/links/",
			args:     []string{"link", "add", "card1", "https://example.com", "--text", value},
			wantBody: map[string]any{"display_text": wantValue},
		},
		{
			name:     "link update display text",
			method:   "PATCH",
			path:     "/api/cards/card1/links/link1/",
			args:     []string{"link", "update", "card1", "link1", "--text", value},
			wantBody: map[string]any{"display_text": wantValue},
		},
		{
			name:     "list create name",
			method:   "POST",
			path:     "/api/boards/board1/lists/",
			args:     []string{"list", "create", "board1", "--name", value},
			wantBody: map[string]any{"name": wantValue},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tt.method || r.URL.Path != tt.path {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "ok"}})
			}))
			defer server.Close()

			args := append([]string{"--api-url", server.URL, "--token", "tok"}, tt.args...)
			_, stderr, err := executeRoot(args...)
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
			}
			for key, want := range tt.wantBody {
				if !reflect.DeepEqual(want, body[key]) {
					t.Fatalf("%s: want %#v, got %#v", key, want, body[key])
				}
			}
		})
	}
}

func TestAttachmentMarkdownContentExpandsEscapedNewlines(t *testing.T) {
	var uploadedBody string
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Fatalf("unexpected upload request %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		uploadedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cards/card1/attachments/presign/":
			if r.Method != "POST" {
				t.Fatalf("unexpected presign method %s", r.Method)
			}
			writeCLITestJSON(t, w, map[string]any{
				"data": map[string]string{
					"upload_url": uploadServer.URL,
					"s3_key":     "uploads/notes.md",
				},
			})
		case "/api/cards/card1/attachments/confirm/":
			if r.Method != "POST" {
				t.Fatalf("unexpected confirm method %s", r.Method)
			}
			writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "att1"}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	_, stderr, err := executeRoot("--api-url", apiServer.URL, "--token", "tok", "attachment", "markdown", "card1", "--filename", "notes.md", "--content", "First\\n\\nSecond")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	assertEqual(t, "First\n\nSecond", uploadedBody)
}

func TestCardLabelReplacementUsesDedicatedEndpoints(t *testing.T) {
	type label struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	labels := []label{{ID: "A", Name: "A"}, {ID: "B", Name: "B"}}
	validLabels := []label{{ID: "A", Name: "A"}, {ID: "B", Name: "B"}, {ID: "C", Name: "C"}}
	var additions, removals []string
	genericUpdates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/cards/card1/":
			writeCLITestJSON(t, w, map[string]any{"data": map[string]any{
				"id": "card1", "board": map[string]string{"id": "board1"}, "labels": labels,
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/boards/board1/":
			writeCLITestJSON(t, w, map[string]any{"data": map[string]any{"id": "board1", "labels": validLabels}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/cards/card1/labels/":
			var body struct {
				LabelID string `json:"label_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			additions = append(additions, body.LabelID)
			for _, candidate := range validLabels {
				if candidate.ID == body.LabelID {
					labels = append(labels, candidate)
				}
			}
			writeCLITestJSON(t, w, map[string]any{"data": map[string]any{}})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/cards/card1/labels/"):
			labelID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cards/card1/labels/"), "/")
			removals = append(removals, labelID)
			for index, candidate := range labels {
				if candidate.ID == labelID {
					labels = append(labels[:index], labels[index+1:]...)
					break
				}
			}
			writeCLITestJSON(t, w, map[string]any{"data": map[string]any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/cards/card1/":
			genericUpdates++
			writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "card1"}})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	stdout, stderr, err := executeRoot("--api-url", server.URL, "--token", "tok", "card", "update", "card1", "--label-ids", "B", "--label-ids", "C")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr: %s", err, stderr)
	}
	if !reflect.DeepEqual(additions, []string{"C"}) {
		t.Fatalf("additions = %#v, want []string{\"C\"}", additions)
	}
	if !reflect.DeepEqual(removals, []string{"A"}) {
		t.Fatalf("removals = %#v, want []string{\"A\"}", removals)
	}
	if genericUpdates != 0 {
		t.Fatalf("generic card updates = %d, want 0", genericUpdates)
	}
	var result struct {
		Labels []label `json:"labels"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Labels, []label{{ID: "B", Name: "B"}, {ID: "C", Name: "C"}}) {
		t.Fatalf("final labels = %#v", result.Labels)
	}
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
