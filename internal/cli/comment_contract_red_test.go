package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestREDBotCLICommentBoundaryAfterExpansion(t *testing.T) {
	t.Setenv("KARDBRD_AGENT_COMMENT_LIMIT", "250")
	for _, tt := range []struct {
		name string
		body string
		post bool
	}{
		{"249 words", strings.Repeat("word ", 248) + "@Paul", true},
		{"250 words", strings.Repeat("word ", 249) + "@Paul", true},
		{"251 words", strings.Repeat("word ", 250) + "@Paul", false},
		{"Unicode whitespace and Markdown", strings.Repeat("word ", 248) + "[résumé](https://example.test/a)\u00a0`日本語`\\n@Paul", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "comment1"}})
			}))
			defer server.Close()
			_, _, err := executeRoot("--api-url", server.URL, "--token", "tok", "comment", "add", "card1", tt.body)
			if tt.post && (err != nil || posts != 1) {
				t.Fatalf("valid comment: posts=%d err=%v", posts, err)
			}
			if !tt.post && (err == nil || posts != 0) {
				t.Fatalf("oversized comment must fail before POST: posts=%d err=%v", posts, err)
			}
		})
	}
}

func TestCLISharedCountFixturesAndWebOuterTrim(t *testing.T) {
	t.Setenv("KARDBRD_AGENT_COMMENT_LIMIT", "250")
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "bot_comment_word_count.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Fixtures []struct {
			ID       string `json:"id"`
			Body     string `json:"body"`
			CLIInput string `json:"cli_input"`
			Words    int    `json:"words"`
		} `json:"fixtures"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, tc := range fixtures.Fixtures {
		t.Run(tc.ID, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts++
				var payload struct {
					Content string `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload.Content != tc.Body {
					t.Errorf("submitted %q, want fixture body %q", payload.Content, tc.Body)
				}
				writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "comment1"}})
			}))
			defer server.Close()
			input := tc.Body
			if tc.CLIInput != "" {
				input = tc.CLIInput
			}
			_, _, err := executeRoot("--api-url", server.URL, "--token", "tok", "comment", "add", "card1", input)
			wantPost := tc.Words <= 250
			if wantPost && (err != nil || posts != 1) || !wantPost && (err == nil || posts != 0) {
				t.Fatalf("words=%d posts=%d err=%v", tc.Words, posts, err)
			}
		})
	}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		writeCLITestJSON(t, w, map[string]any{"data": map[string]string{"id": "comment1"}})
	}))
	defer server.Close()
	_, _, err = executeRoot("--api-url", server.URL, "--token", "tok", "comment", "add", "card1", strings.Repeat("x ", 249)+"@Paul \x1c")
	if err != nil || posts != 1 {
		t.Fatalf("Web-trimmed 250-word body: posts=%d err=%v", posts, err)
	}
}

func TestREDCLIUncertainCommentWriteHasOneAttempt(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	_, _, err := executeRoot("--api-url", server.URL, "--token", "tok", "comment", "add", "card1", "decision retained")
	if err == nil {
		t.Fatal("expected uncertain write error")
	}
	if posts != 1 {
		t.Fatalf("unkeyed CLI comment POST attempted %d times", posts)
	}
}
