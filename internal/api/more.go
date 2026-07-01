package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

type SkillPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *Client) GetBoardActivity(ctx context.Context, boardID string, opts ActivityOptions) (json.RawMessage, error) {
	params := activityParams(opts, 50)
	return c.RequestRaw(ctx, "GET", "/api/boards/"+url.PathEscape(boardID)+"/activity/?"+params.Encode(), nil)
}

func (c *Client) GetBoardActivityMarkdown(ctx context.Context, boardID string, opts ActivityOptions) (string, error) {
	params := activityParams(opts, 50)
	return c.RequestMarkdown(ctx, "/api/boards/"+url.PathEscape(boardID)+"/activity/?"+params.Encode())
}

func (c *Client) Search(ctx context.Context, query string, opts SearchOptions) (json.RawMessage, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 30
	}
	params := url.Values{"q": {query}, "limit": {fmt.Sprint(limit)}, "offset": {fmt.Sprint(opts.Offset)}}
	if !opts.IncludeArchived {
		params.Set("include_archived", "false")
	}
	if opts.Workspace != "" {
		params.Set("workspace", opts.Workspace)
	}
	return c.RequestRaw(ctx, "GET", "/api/search/?"+params.Encode(), nil)
}

func (c *Client) GetActivity(ctx context.Context, opts ActivityOptions) (json.RawMessage, error) {
	params := activityParams(opts, 30)
	if opts.Actor != "" {
		params.Set("actor", opts.Actor)
	}
	if opts.Source != "" {
		params.Set("source", opts.Source)
	}
	if opts.Period != "" {
		params.Set("period", opts.Period)
	}
	if opts.Board != "" {
		params.Set("board", opts.Board)
	}
	params.Set("offset", fmt.Sprint(opts.Offset))
	return c.RequestRaw(ctx, "GET", "/api/activity/?"+params.Encode(), nil)
}

func (c *Client) GetCardActivity(ctx context.Context, cardID string, opts ActivityOptions) (json.RawMessage, error) {
	params := activityParams(opts, 20)
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/activity/?"+params.Encode(), nil)
}

func (c *Client) CreateList(ctx context.Context, boardID, name string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/boards/"+url.PathEscape(boardID)+"/lists/", map[string]any{"name": name})
}

func (c *Client) MoveList(ctx context.Context, listID string, position int) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/lists/"+url.PathEscape(listID)+"/move/", map[string]any{"position": position})
}

func (c *Client) RegisterSkills(ctx context.Context, skills []SkillPayload) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "PUT", "/api/bots/skills/", map[string]any{"skills": skills})
}

func (c *Client) ListCardLinks(ctx context.Context, cardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/links/", nil)
}

func (c *Client) AddCardLink(ctx context.Context, cardID, linkURL, displayText string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/links/", map[string]any{"url": linkURL, "display_text": displayText})
}

func (c *Client) UpdateCardLink(ctx context.Context, cardID, linkID string, patch LinkPatch) (json.RawMessage, error) {
	body := map[string]any{}
	if patch.URL != nil {
		body["url"] = *patch.URL
	}
	if patch.DisplayText != nil {
		body["display_text"] = *patch.DisplayText
	}
	return c.RequestRaw(ctx, "PATCH", "/api/cards/"+url.PathEscape(cardID)+"/links/"+url.PathEscape(linkID)+"/", body)
}

func (c *Client) DeleteCardLink(ctx context.Context, cardID, linkID string) error {
	_, err := c.RequestRaw(ctx, "DELETE", "/api/cards/"+url.PathEscape(cardID)+"/links/"+url.PathEscape(linkID)+"/", nil)
	return err
}

func activityParams(opts ActivityOptions, defaultLimit int) url.Values {
	limit := opts.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	params := url.Values{"limit": {fmt.Sprint(limit)}}
	if opts.Since != "" {
		params.Set("since", opts.Since)
	}
	return params
}
