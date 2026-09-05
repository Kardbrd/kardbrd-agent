package rules

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type rawConfig struct {
	BoardID   string        `yaml:"board_id"`
	AgentName string        `yaml:"agent"`
	APIURL    string        `yaml:"api_url"`
	Executor  string        `yaml:"executor"`
	Rules     []rawRule     `yaml:"rules"`
	Schedules []rawSchedule `yaml:"schedules"`
}

type rawRule struct {
	Name            string   `yaml:"name"`
	Event           any      `yaml:"event"`
	Action          string   `yaml:"action"`
	Model           string   `yaml:"model"`
	List            string   `yaml:"list"`
	Title           string   `yaml:"title"`
	Label           string   `yaml:"label"`
	ContentContains string   `yaml:"content_contains"`
	ExcludeLabel    string   `yaml:"exclude_label"`
	RequireLabel    string   `yaml:"require_label"`
	Emoji           string   `yaml:"emoji"`
	RequireUser     string   `yaml:"require_user"`
	Assignee        []string `yaml:"assignee"`
	CommentAuthor   string   `yaml:"comment_author"`
}

type rawSchedule struct {
	CardID   string `yaml:"card_id"`
	Name     string `yaml:"name"`
	Cron     string `yaml:"cron"`
	Action   string `yaml:"action"`
	Model    string `yaml:"model"`
	Assignee string `yaml:"assignee"`
	List     string `yaml:"list"`
}

func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}
	if raw.BoardID == "" {
		return Config{}, fmt.Errorf("kardbrd.yml: 'board_id' is required")
	}
	if raw.AgentName == "" {
		return Config{}, fmt.Errorf("kardbrd.yml: 'agent' is required")
	}

	cfg := Config{
		BoardID:   raw.BoardID,
		AgentName: raw.AgentName,
		APIURL:    raw.APIURL,
		Executor:  stringsLower(raw.Executor),
	}
	for _, rawRule := range raw.Rules {
		events, err := parseEvents(rawRule.Event)
		if err != nil {
			return Config{}, fmt.Errorf("rule %q: %w", rawRule.Name, err)
		}
		cfg.Rules = append(cfg.Rules, Rule{
			Name:            rawRule.Name,
			Events:          events,
			Action:          rawRule.Action,
			Model:           rawRule.Model,
			List:            rawRule.List,
			Title:           rawRule.Title,
			Label:           rawRule.Label,
			ContentContains: rawRule.ContentContains,
			ExcludeLabel:    rawRule.ExcludeLabel,
			RequireLabel:    rawRule.RequireLabel,
			Emoji:           rawRule.Emoji,
			RequireUser:     rawRule.RequireUser,
			Assignee:        rawRule.Assignee,
			CommentAuthor:   rawRule.CommentAuthor,
		})
	}
	for _, rawSchedule := range raw.Schedules {
		cfg.Schedules = append(cfg.Schedules, Schedule{
			CardID:   rawSchedule.CardID,
			Name:     rawSchedule.Name,
			Cron:     rawSchedule.Cron,
			Action:   rawSchedule.Action,
			Model:    rawSchedule.Model,
			Assignee: rawSchedule.Assignee,
			List:     rawSchedule.List,
		})
	}
	return cfg, nil
}

func parseEvents(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case []any:
		events := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("event list entries must be strings")
			}
			events = append(events, text)
		}
		return events, nil
	case nil:
		return nil, fmt.Errorf("event is required")
	default:
		return nil, fmt.Errorf("event must be a string or list")
	}
}
