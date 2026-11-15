package prompt

import (
	"fmt"
	"strings"
	"testing"
)

// These are acceptance tests for the proposed contract. They deliberately fail
// against the current source until the supervisor approves implementation.
func TestREDHandoffRetainsAllCommentsAndDoesNotRepeatTrigger(t *testing.T) {
	var history strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&history, "### Comment %02d\nDecision %02d remains useful.\n\n", i, i)
	}
	for _, command := range []string{"fix this", "/plan"} {
		t.Run(command, func(t *testing.T) {
			trigger := "@Bot " + command
			got := Build(Request{
				CardID: "card1", CardMarkdown: history.String() + "### Trigger\n" + trigger, Command: command,
				CommentContent: trigger, AuthorName: "Paul",
			})
			previous := -1
			for i := 1; i <= 40; i++ {
				needle := fmt.Sprintf("Decision %02d remains useful.", i)
				at := strings.Index(got, needle)
				if at <= previous || strings.Count(got, needle) != 1 {
					t.Fatalf("history comment %d missing, repeated, or out of order", i)
				}
				previous = at
			}
			if strings.Count(got, trigger) != 1 {
				t.Fatalf("trigger repeated %d times", strings.Count(got, trigger))
			}
			if !strings.Contains(got, "250 words") {
				t.Fatal("handoff lacks the per-comment limit")
			}
			if !strings.Contains(got, "decision") || !strings.Contains(got, "evidence") {
				t.Fatal("handoff lacks concise, substantive writing guidance")
			}
		})
	}
}
