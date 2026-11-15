package agent

import (
	"context"
	"encoding/json"

	"github.com/Kardbrd/kardbrd-agent/internal/commentpolicy"
)

// publishBotNotice keeps nonterminal notices bounded and makes one write attempt.
// Variable diagnostics stay in run logs when they cannot fit in a comment.
func (m *Manager) publishBotNotice(ctx context.Context, cardID, body string) (json.RawMessage, error) {
	return m.Client.AddCommentOnce(ctx, cardID, botNoticeBody(body))
}

func botNoticeBody(body string) string {
	if err := commentpolicy.ValidateBotComment(commentpolicy.WebStoredBody(body)); err != nil || len(body) > 12000 {
		return "**Agent notice needs review**\n\nThe detailed diagnostic exceeded the bot comment limit. Inspect the agent run log before retrying."
	}
	return body
}
