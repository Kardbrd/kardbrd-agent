package rules

import "testing"

func TestEngineMatchesAllConditions(t *testing.T) {
	engine := Engine{Rules: []Rule{
		{
			Name:            "Match",
			Events:          []string{"comment_created"},
			List:            "Ideas",
			Title:           "Fix Login",
			Label:           "Agent",
			ContentContains: "please",
			RequireLabel:    "Ready",
			ExcludeLabel:    "Blocked",
			RequireUser:     "user1",
			Assignee:        []string{"__self__"},
			CommentAuthor:   "__self__",
			Action:          "/ke",
		},
	}}

	matches := engine.Match("comment_created", map[string]any{
		"list_name":             "ideas",
		"card_title":            "fix login",
		"label_name":            "agent",
		"content":               "Please help",
		"card_labels":           []string{"Ready"},
		"user_id":               "user1",
		"card_assignee_is_bot":  true,
		"comment_author_is_bot": true,
	})

	assertEqual(t, 1, len(matches))
	assertEqual(t, "Match", matches[0].Name)
}

func TestEngineRejectsNonMatchingConditions(t *testing.T) {
	engine := Engine{Rules: []Rule{
		{Name: "Emoji", Events: []string{"reaction_added"}, Emoji: "✅", Action: "/kr"},
		{Name: "Excluded", Events: []string{"card_created"}, ExcludeLabel: "Blocked", Action: "/ke"},
	}}

	emojiMatches := engine.Match("reaction_added", map[string]any{"emoji": "🛑"})
	assertEqual(t, 0, len(emojiMatches))

	labelMatches := engine.Match("card_created", map[string]any{"card_labels": []string{"Blocked"}})
	assertEqual(t, 0, len(labelMatches))
}
