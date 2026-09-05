package prompt

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildPromptForSlashCommandIncludesAgentFilesKnowledgeAndCLIInstructions(t *testing.T) {
	repo := filepath.Join("..", "..", "testdata", "executor", "repo")

	got := Build(Request{
		CardID:         "card1",
		CardMarkdown:   "# Card\nDetails",
		Command:        "/ke",
		CommentContent: "@Bot /ke",
		AuthorName:     "alice",
		BoardID:        "board1",
		CWD:            repo,
	})

	assertContains(t, got, "## Agent Identity")
	assertContains(t, got, "I am a focused Kardbrd coding agent.")
	assertContains(t, got, "## Agent Rules")
	assertContains(t, got, "Always post a card comment when finished.")
	assertContains(t, got, "## Knowledge")
	assertContains(t, got, "### High Priority")
	assertContains(t, got, "Remember the repository-specific workflow.")
	if strings.Contains(got, "This should not be loaded.") {
		t.Fatal("loaded low-priority knowledge document")
	}
	assertContains(t, got, "/ke")
	assertContains(t, got, "**Card ID:** card1")
	assertContains(t, got, "# Card\nDetails")
	assertContains(t, got, "kardbrd board labels board1")
	assertContains(t, got, "kardbrd card update card1 --label-ids ID1 --label-ids ID2")
	assertContains(t, got, "kardbrd card update card1 --clear-labels")
	assertContains(t, got, "kardbrd comment add card1")
	assertContains(t, got, "You may post progress updates to the card while you work")
	assertContains(t, got, "Return your final response normally")
	assertContains(t, got, "The agent manager will publish it once as the terminal summary")
	assertContains(t, got, "This instruction overrides any earlier generic instruction to post a comment when finished")
	if strings.Contains(got, "When you complete this task, you MUST post your response as a comment") {
		t.Fatal("prompt still requires executors to publish their own terminal comment")
	}
}

func TestBuildPromptForFreeFormRequest(t *testing.T) {
	got := Build(Request{
		CardID:         "card1",
		CardMarkdown:   "# Card",
		Command:        "fix this",
		CommentContent: "@Bot fix this",
		AuthorName:     "alice",
		BoardID:        "board1",
	})

	assertContains(t, got, "## Task Request")
	assertContains(t, got, "@Bot fix this")
	assertContains(t, got, "**Requested by:** @alice")
	assertContains(t, got, "Please complete this request.")
}

func TestExtractCommandRemovesMentionPreservingOriginalCase(t *testing.T) {
	got := ExtractCommand("@Bot Fix Login", "@bot")
	if got != "Fix Login" {
		t.Fatalf("want %q, got %q", "Fix Login", got)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected prompt to contain %q:\n%s", want, got)
	}
}
