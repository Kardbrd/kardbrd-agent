package rules

import "strings"

func (e Engine) Match(eventType string, message map[string]any) []Rule {
	var matched []Rule
	for _, rule := range e.Rules {
		if matches(rule, eventType, message) {
			matched = append(matched, rule)
		}
	}
	return matched
}

func matches(rule Rule, eventType string, message map[string]any) bool {
	if !containsString(rule.Events, eventType) {
		return false
	}
	if rule.List != "" && !equalFold(stringField(message, "list_name"), rule.List) {
		return false
	}
	if rule.Title != "" && !equalFold(stringField(message, "card_title"), rule.Title) {
		return false
	}
	if rule.Label != "" && !equalFold(stringField(message, "label_name"), rule.Label) {
		return false
	}
	if rule.ContentContains != "" && !strings.Contains(stringsLower(stringField(message, "content")), stringsLower(rule.ContentContains)) {
		return false
	}
	labels := stringSliceField(message, "card_labels")
	if rule.ExcludeLabel != "" && containsFold(labels, rule.ExcludeLabel) {
		return false
	}
	if rule.RequireLabel != "" && !containsFold(labels, rule.RequireLabel) {
		return false
	}
	if rule.Emoji != "" && stringField(message, "emoji") != rule.Emoji {
		return false
	}
	if rule.RequireUser != "" && stringField(message, "user_id") != rule.RequireUser {
		return false
	}
	if len(rule.Assignee) > 0 {
		if containsString(rule.Assignee, "__self__") {
			if boolField(message, "card_assignee_is_bot") != true {
				return false
			}
		} else if !containsString(rule.Assignee, stringField(message, "card_assignee_id")) {
			return false
		}
	}
	if rule.CommentAuthor != "" {
		if rule.CommentAuthor == "__self__" {
			if boolField(message, "comment_author_is_bot") != true {
				return false
			}
		} else if stringField(message, "comment_author_id") != rule.CommentAuthor {
			return false
		}
	}
	return true
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsFold(values []string, needle string) bool {
	for _, value := range values {
		if equalFold(value, needle) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	return strings.EqualFold(a, b)
}

func stringField(message map[string]any, key string) string {
	if value, ok := message[key].(string); ok {
		return value
	}
	return ""
}

func boolField(message map[string]any, key string) bool {
	if value, ok := message[key].(bool); ok {
		return value
	}
	return false
}

func stringSliceField(message map[string]any, key string) []string {
	switch value := message[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func stringsLower(value string) string {
	return strings.ToLower(value)
}
