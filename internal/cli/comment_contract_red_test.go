package cli

import (
	"net/http"
	"net/http/httptest"
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
