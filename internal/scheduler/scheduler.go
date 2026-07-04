package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
	"github.com/robfig/cron/v3"
)

type Client interface {
	GetBoard(ctx context.Context, boardID string, includeArchived bool) (json.RawMessage, error)
	CreateCard(ctx context.Context, boardID, listID, title, description string) (json.RawMessage, error)
	UpdateCard(ctx context.Context, cardID string, patch api.CardPatch) (json.RawMessage, error)
}

type Processor func(ctx context.Context, cardID string, schedule rules.Schedule) error

type Manager struct {
	Schedules []rules.Schedule
	BoardID   string
	Client    Client
	Processor Processor
	parser    cron.Parser
	cron      *cron.Cron
	entries   []cron.EntryID
	ctx       context.Context
	mu        sync.Mutex
}

func NewManager(schedules []rules.Schedule, boardID string, client Client, processor Processor) *Manager {
	return &Manager{
		Schedules: schedules,
		BoardID:   boardID,
		Client:    client,
		Processor: processor,
		parser:    standardParser(),
	}
}

func ValidateCron(expr string) error {
	_, err := standardParser().Parse(expr)
	return err
}

func (m *Manager) Start(ctx context.Context) error {
	c := cron.New(cron.WithParser(m.parser))
	m.mu.Lock()
	m.cron = c
	m.ctx = ctx
	if err := m.installSchedulesLocked(ctx); err != nil {
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()

	c.Start()
	<-ctx.Done()
	stopCtx := c.Stop()
	<-stopCtx.Done()
	m.mu.Lock()
	if m.cron == c {
		m.cron = nil
		m.entries = nil
		m.ctx = nil
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) UpdateSchedules(schedules []rules.Schedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Schedules = append([]rules.Schedule(nil), schedules...)
	if m.cron == nil {
		return nil
	}
	for _, entry := range m.entries {
		m.cron.Remove(entry)
	}
	m.entries = nil
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return m.installSchedulesLocked(ctx)
}

func (m *Manager) installSchedulesLocked(ctx context.Context) error {
	for _, schedule := range m.Schedules {
		schedule := schedule
		entryID, err := m.cron.AddFunc(schedule.Cron, func() {
			if ctx.Err() == nil {
				_ = m.Trigger(ctx, schedule)
			}
		})
		if err != nil {
			return err
		}
		m.entries = append(m.entries, entryID)
	}
	return nil
}

func (m *Manager) Trigger(ctx context.Context, schedule rules.Schedule) error {
	cardID, err := m.EnsureCard(ctx, schedule)
	if err != nil {
		return err
	}
	if m.Processor != nil {
		return m.Processor(ctx, cardID, schedule)
	}
	return nil
}

func (m *Manager) EnsureCard(ctx context.Context, schedule rules.Schedule) (string, error) {
	board, err := m.loadBoard(ctx)
	if err != nil {
		return "", err
	}
	for _, list := range board.Lists {
		for _, card := range list.Cards {
			if strings.EqualFold(card.Title, schedule.Name) {
				return card.ID, nil
			}
		}
	}
	if len(board.Lists) == 0 {
		return "", nil
	}
	listID := board.Lists[0].ID
	if schedule.List != "" {
		for _, list := range board.Lists {
			if strings.EqualFold(list.Name, schedule.List) {
				listID = list.ID
				break
			}
		}
	}
	raw, err := m.Client.CreateCard(ctx, m.BoardID, listID, schedule.Name, scheduleDescription(schedule))
	if err != nil {
		return "", err
	}
	cardID := cardIDFromRaw(raw)
	if schedule.Assignee != "" && cardID != "" {
		assignee := schedule.Assignee
		_, err = m.Client.UpdateCard(ctx, cardID, api.CardPatch{AssigneeID: &assignee, AssigneeSet: true})
		if err != nil {
			return "", err
		}
	}
	return cardID, nil
}

func (m *Manager) loadBoard(ctx context.Context) (boardData, error) {
	raw, err := m.Client.GetBoard(ctx, m.BoardID, true)
	if err != nil {
		return boardData{}, err
	}
	var board boardData
	if err := json.Unmarshal(raw, &board); err != nil {
		return boardData{}, err
	}
	return board, nil
}

type boardData struct {
	Lists []boardList `json:"lists"`
}

type boardList struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Cards []boardCard `json:"cards"`
}

type boardCard struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

func cardIDFromRaw(raw json.RawMessage) string {
	var payload struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.ID
}

func standardParser() cron.Parser {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
}

func scheduleDescription(schedule rules.Schedule) string {
	if schedule.Action == "" {
		return "Scheduled kardbrd automation."
	}
	return "Scheduled kardbrd automation:\n\n" + schedule.Action
}
