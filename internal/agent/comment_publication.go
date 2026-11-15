package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Kardbrd/kardbrd-agent/internal/commentpolicy"
)

// publishBotNotice keeps nonterminal notices bounded and makes one write attempt.
// Variable diagnostics stay in run logs when they cannot fit in a comment.
func (m *Manager) publishBotNotice(ctx context.Context, cardID, body string) (json.RawMessage, error) {
	return m.Client.AddCommentOnce(ctx, cardID, botNoticeBody(body))
}

func botNoticeBody(body string) string {
	if err := commentpolicy.ValidateBotComment(commentpolicy.WebStoredBody(body)); err != nil || len(body) > 12000 {
		heading, _, _ := strings.Cut(body, "\n")
		if len(heading) > 100 {
			heading, _, _ = strings.Cut(heading, ":")
		}
		if len(heading) > 100 || commentpolicy.ValidateBotComment(heading) != nil {
			heading = "**Agent notice needs review**"
		}
		mention := ""
		if index := strings.LastIndex(body, "\n@"); index >= 0 {
			candidate := strings.TrimSpace(body[index:])
			if len(candidate) <= 100 && !strings.ContainsAny(candidate, "\n\r") {
				mention = "\n\n" + candidate
			}
		}
		return heading + "\n\nDetails exceeded the bot comment limit. Inspect the agent run log before retrying." + mention
	}
	return body
}
