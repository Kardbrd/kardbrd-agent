package api

import (
	"context"
	"encoding/json"
	"net/url"
)

func (c *Client) CreateChecklist(ctx context.Context, cardID, title string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/checklists/", map[string]any{"title": title})
}

func (c *Client) AddTodo(ctx context.Context, cardID, checklistID, title string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/checklists/"+url.PathEscape(checklistID)+"/items/", map[string]any{"title": title})
}

func (c *Client) AddTodos(ctx context.Context, cardID, checklistTitle string, items []string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/checklists/bulk/", map[string]any{"title": checklistTitle, "items": items})
}

func (c *Client) UpdateTodo(ctx context.Context, cardID, checklistID, itemID string, patch TodoPatch) (json.RawMessage, error) {
	body := map[string]any{}
	if patch.Title != nil {
		body["title"] = *patch.Title
	}
	if patch.IsCompleted != nil {
		body["is_completed"] = *patch.IsCompleted
	}
	if patch.DueDate != nil {
		body["due_date"] = *patch.DueDate
	}
	if patch.AssigneeSet {
		body["assignee_ids"] = patch.AssigneeIDs
	}
	return c.RequestRaw(ctx, "PATCH", "/api/cards/"+url.PathEscape(cardID)+"/checklists/"+url.PathEscape(checklistID)+"/items/"+url.PathEscape(itemID)+"/", body)
}

func (c *Client) CompleteTodo(ctx context.Context, cardID, todoID string) (json.RawMessage, error) {
	return c.updateTodoCompletion(ctx, cardID, todoID, true)
}

func (c *Client) ReopenTodo(ctx context.Context, cardID, todoID string) (json.RawMessage, error) {
	return c.updateTodoCompletion(ctx, cardID, todoID, false)
}

func (c *Client) updateTodoCompletion(ctx context.Context, cardID, todoID string, completed bool) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "PATCH", "/api/cards/"+url.PathEscape(cardID)+"/todos/"+url.PathEscape(todoID)+"/", map[string]any{"completed": completed})
}

func (c *Client) ExtractTodosToCards(ctx context.Context, sourceCardID, targetListID, prefix string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(sourceCardID)+"/extract-todos-to-cards/", map[string]any{"target_list_id": targetListID, "prefix": prefix})
}

func (c *Client) ExtractChecklistToCards(ctx context.Context, sourceCardID, checklistID, targetListID, prefix string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(sourceCardID)+"/checklists/"+url.PathEscape(checklistID)+"/extract-to-cards/", map[string]any{"target_list_id": targetListID, "prefix": prefix})
}
