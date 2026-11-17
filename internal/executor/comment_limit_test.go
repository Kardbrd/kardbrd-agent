package executor

import "testing"

func TestExecutorEnvironmentReplacesCommentLimit(t *testing.T) {
	env := executorEnvironment([]string{"KARDBRD_AGENT_COMMENT_LIMIT=999", "OTHER=kept"}, Config{}, "card1", "board1")
	count := 0
	for _, entry := range env {
		if entry == "KARDBRD_AGENT_COMMENT_LIMIT=250" {
			count++
		}
		if entry == "KARDBRD_AGENT_COMMENT_LIMIT=999" {
			t.Fatal("inherited limit leaked")
		}
	}
	if count != 1 {
		t.Fatalf("comment limit entries = %d", count)
	}
}
