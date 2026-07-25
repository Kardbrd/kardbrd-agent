package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

type APIError struct {
	Message    string
	Code       string
	StatusCode int
}

func (e *APIError) Error() string {
	parts := []string{e.Message}
	if e.Code != "" {
		parts = append(parts, fmt.Sprintf("(code: %s)", e.Code))
	}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("[HTTP %d]", e.StatusCode))
	}
	return strings.Join(parts, " ")
}

type CardPatch struct {
	Title       *string
	Description *string
	DueDate     *string
	AssigneeID  *string
	AssigneeSet bool
}

// Label is the minimal label representation returned by card and board detail
// endpoints.
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// CardLabelState is the subset of a card response required to reconcile its
// label set.
type CardLabelState struct {
	ID    string `json:"id"`
	Board struct {
		ID string `json:"id"`
	} `json:"board"`
	Labels []Label `json:"labels"`
}

// BoardLabelCatalog is the subset of a board response required to validate
// card labels.
type BoardLabelCatalog struct {
	ID     string  `json:"id"`
	Labels []Label `json:"labels"`
}

type TodoPatch struct {
	Title       *string
	IsCompleted *bool
	DueDate     *string
	AssigneeIDs []string
	AssigneeSet bool
}

type LinkPatch struct {
	URL         *string
	DisplayText *string
}

type SearchOptions struct {
	Workspace       string
	IncludeArchived bool
	Limit           int
	Offset          int
}

type ActivityOptions struct {
	Since  string
	Offset int
	Limit  int
	Actor  string
	Source string
	Period string
	Board  string
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) Request(ctx context.Context, method, path string, body any, out any) error {
	raw, err := c.RequestRaw(ctx, method, path, body)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) RequestRaw(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var rawBody []byte
	var err error
	if body != nil {
		rawBody, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		raw, err := c.doJSON(ctx, method, path, rawBody)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) RequestMarkdown(ctx context.Context, path string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		text, err := c.doMarkdown(ctx, path)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return "", err
		}
	}
	return "", lastErr
}

func (c *Client) doJSON(ctx context.Context, method, path string, body []byte) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	if len(data) == 0 {
		return json.RawMessage(`null`), nil
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err == nil {
		if unwrapped, ok := envelope["data"]; ok {
			return unwrapped, nil
		}
	}
	return json.RawMessage(data), nil
}

func (c *Client) doMarkdown(ctx context.Context, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "text/markdown")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", parseAPIError(resp.StatusCode, data)
	}
	return string(data), nil
}

func parseAPIError(status int, data []byte) error {
	message := strings.TrimSpace(string(data))
	if strings.HasPrefix(message, "<") {
		message = "API returned an HTML error response"
	}
	apiErr := &APIError{StatusCode: status, Message: message}
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(data, &payload); err == nil {
		if payload.Error != "" {
			apiErr.Message = payload.Error
		}
		apiErr.Code = payload.Code
	}
	if apiErr.Message == "" {
		apiErr.Message = "Unknown error"
	}
	return apiErr
}

func isRetryable(err error) bool {
	apiErr, ok := err.(*APIError)
	return !ok || apiErr.StatusCode >= 500
}

func (c *Client) ListBoards(ctx context.Context) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/boards/", nil)
}

func (c *Client) GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/boards/"+url.PathEscape(boardID)+"/"+includeArchivedQuery(includeArchived), nil)
}

func (c *Client) ListBoardsMarkdown(ctx context.Context) (string, error) {
	return c.RequestMarkdown(ctx, "/api/boards/")
}

func (c *Client) GetBoardMarkdown(ctx context.Context, boardID string, includeArchived bool) (string, error) {
	return c.RequestMarkdown(ctx, "/api/boards/"+url.PathEscape(boardID)+"/"+includeArchivedQuery(includeArchived))
}

func (c *Client) GetBoardLabels(ctx context.Context, boardID string) (json.RawMessage, error) {
	catalog, err := c.GetBoardLabelCatalog(ctx, boardID)
	if err != nil {
		return nil, err
	}
	labels, err := json.Marshal(catalog.Labels)
	if err != nil {
		return nil, fmt.Errorf("encode board label catalog: %w", err)
	}
	return labels, nil
}

func (c *Client) UpdateBoard(ctx context.Context, boardID, name string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "PATCH", "/api/boards/"+url.PathEscape(boardID)+"/", map[string]any{"name": name})
}

func (c *Client) ArchiveBoard(ctx context.Context, boardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/boards/"+url.PathEscape(boardID)+"/archive/", nil)
}

func (c *Client) UnarchiveBoard(ctx context.Context, boardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/boards/"+url.PathEscape(boardID)+"/unarchive/", nil)
}

func (c *Client) ToggleBoardFavorite(ctx context.Context, boardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/boards/"+url.PathEscape(boardID)+"/favorite/", nil)
}

func (c *Client) BoardCardSearch(ctx context.Context, boardID, query string, limit int) (json.RawMessage, error) {
	params := url.Values{"q": {query}, "limit": {fmt.Sprint(limit)}}
	return c.RequestRaw(ctx, "GET", "/api/boards/"+url.PathEscape(boardID)+"/cards/search/?"+params.Encode(), nil)
}

func (c *Client) GetCard(ctx context.Context, cardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/", nil)
}

func (c *Client) GetCardLabelState(ctx context.Context, cardID string) (CardLabelState, error) {
	raw, err := c.GetCard(ctx, cardID)
	if err != nil {
		return CardLabelState{}, err
	}
	var state CardLabelState
	if err := json.Unmarshal(raw, &state); err != nil {
		return CardLabelState{}, fmt.Errorf("decode card %q label state: %w", cardID, err)
	}
	if state.ID == "" {
		return CardLabelState{}, fmt.Errorf("decode card %q label state: missing card ID", cardID)
	}
	if state.Board.ID == "" {
		return CardLabelState{}, fmt.Errorf("decode card %q label state: missing board ID", cardID)
	}
	return state, nil
}

func (c *Client) GetCardMarkdown(ctx context.Context, cardID string) (string, error) {
	return c.RequestMarkdown(ctx, "/api/cards/"+url.PathEscape(cardID)+"/")
}

func (c *Client) GetBoardLabelCatalog(ctx context.Context, boardID string) (BoardLabelCatalog, error) {
	raw, err := c.GetBoard(ctx, boardID, false)
	if err != nil {
		return BoardLabelCatalog{}, err
	}
	var catalog BoardLabelCatalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return BoardLabelCatalog{}, fmt.Errorf("decode board %q label catalog: %w", boardID, err)
	}
	if catalog.ID == "" {
		return BoardLabelCatalog{}, fmt.Errorf("decode board %q label catalog: missing board ID", boardID)
	}
	return catalog, nil
}

func (c *Client) CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/boards/"+url.PathEscape(boardID)+"/lists/"+url.PathEscape(listID)+"/cards/", map[string]any{
		"title":       title,
		"description": description,
	})
}

func (c *Client) UpdateCard(ctx context.Context, cardID string, patch CardPatch) (json.RawMessage, error) {
	body := map[string]any{}
	if patch.Title != nil {
		body["title"] = *patch.Title
	}
	if patch.Description != nil {
		body["description"] = *patch.Description
	}
	if patch.DueDate != nil {
		body["due_date"] = *patch.DueDate
	}
	if patch.AssigneeSet {
		if patch.AssigneeID == nil {
			body["assignee_id"] = nil
		} else {
			body["assignee_id"] = *patch.AssigneeID
		}
	}
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/", body)
}

func (c *Client) AddCardLabel(ctx context.Context, cardID, labelID string) error {
	_, err := c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/labels/", struct {
		LabelID string `json:"label_id"`
	}{LabelID: labelID})
	return err
}

func (c *Client) RemoveCardLabel(ctx context.Context, cardID, labelID string) error {
	_, err := c.RequestRaw(ctx, "DELETE", "/api/cards/"+url.PathEscape(cardID)+"/labels/"+url.PathEscape(labelID)+"/", nil)
	return err
}

func (c *Client) MoveCard(ctx context.Context, cardID, listID string, position *int) (json.RawMessage, error) {
	body := map[string]any{"list_id": listID}
	if position != nil {
		body["position"] = *position
	}
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/move/", body)
}

func (c *Client) ArchiveCard(ctx context.Context, cardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/archive/", nil)
}

func (c *Client) UnarchiveCard(ctx context.Context, cardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/unarchive/", nil)
}

func (c *Client) MoveCardToBoard(ctx context.Context, cardID, boardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/move-to-board/", map[string]any{"board_id": boardID})
}

func (c *Client) AddComment(ctx context.Context, cardID, content string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/comments/", map[string]any{"content": content})
}

func (c *Client) GetComment(ctx context.Context, cardID, commentID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/comments/"+url.PathEscape(commentID)+"/", nil)
}

func (c *Client) DeleteComment(ctx context.Context, cardID, commentID string) error {
	_, err := c.RequestRaw(ctx, "DELETE", "/api/cards/"+url.PathEscape(cardID)+"/comments/"+url.PathEscape(commentID)+"/", nil)
	return err
}

func (c *Client) ToggleReaction(ctx context.Context, cardID, commentID, emoji string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/comments/"+url.PathEscape(commentID)+"/react/", map[string]any{"emoji": emoji})
}

func includeArchivedQuery(includeArchived bool) string {
	if !includeArchived {
		return ""
	}
	return "?include_archived=true"
}
