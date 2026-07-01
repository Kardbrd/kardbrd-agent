package scheduler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestValidateCron(t *testing.T) {
	if err := ValidateCron("0 9 * * 1-5"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCron("bad cron"); err == nil {
		t.Fatal("expected invalid cron error")
	}
}

func TestEnsureScheduleCardFindsExistingByTitle(t *testing.T) {
	client := &fakeScheduleClient{board: rawScheduleJSON(t, map[string]any{
		"lists": []any{
			map[string]any{
				"id": "list1",
				"cards": []any{
					map[string]any{"id": "card1", "title": "Daily Summary"},
				},
			},
		},
	})}
	manager := NewManager([]rules.Schedule{}, "board1", client, nil)

	cardID, err := manager.EnsureCard(context.Background(), rules.Schedule{Name: "daily summary"})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "card1", cardID)
	assertEqual(t, "", client.createdListID)
}

func TestEnsureScheduleCardCreatesInNamedListAndAssigns(t *testing.T) {
	client := &fakeScheduleClient{board: rawScheduleJSON(t, map[string]any{
		"lists": []any{
			map[string]any{"id": "backlog", "name": "Backlog", "cards": []any{}},
			map[string]any{"id": "reports", "name": "Reports", "cards": []any{}},
		},
	})}
	manager := NewManager([]rules.Schedule{}, "board1", client, nil)

	cardID, err := manager.EnsureCard(context.Background(), rules.Schedule{
		Name:     "Daily Summary",
		List:     "Reports",
		Assignee: "user1",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "new-card", cardID)
	assertEqual(t, "reports", client.createdListID)
	assertEqual(t, "Daily Summary", client.createdTitle)
	assertEqual(t, "user1", client.assigneeID)
}

func TestTriggerEnsuresCardAndRunsProcessor(t *testing.T) {
	client := &fakeScheduleClient{board: rawScheduleJSON(t, map[string]any{
		"lists": []any{map[string]any{"id": "todo", "name": "Todo", "cards": []any{}}},
	})}
	var processedCard string
	manager := NewManager([]rules.Schedule{}, "board1", client, func(ctx context.Context, cardID string, schedule rules.Schedule) error {
		processedCard = cardID
		return nil
	})

	if err := manager.Trigger(context.Background(), rules.Schedule{Name: "Weekly", Action: "summarize"}); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "new-card", processedCard)
}

type fakeScheduleClient struct {
	board         json.RawMessage
	createdListID string
	createdTitle  string
	assigneeID    string
}

func (c *fakeScheduleClient) GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error) {
	return c.board, nil
}

func (c *fakeScheduleClient) CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error) {
	c.createdListID = listID
	c.createdTitle = title
	return mustScheduleJSON(map[string]any{"id": "new-card"}), nil
}

func (c *fakeScheduleClient) UpdateCard(ctx context.Context, cardID string, patch api.CardPatch) (json.RawMessage, error) {
	if patch.AssigneeID != nil {
		c.assigneeID = *patch.AssigneeID
	}
	return mustScheduleJSON(map[string]any{"id": cardID}), nil
}

func rawScheduleJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustScheduleJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
