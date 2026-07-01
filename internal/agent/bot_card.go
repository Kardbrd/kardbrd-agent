package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/version"
	"gopkg.in/yaml.v3"
)

const wizardCardTitle = "Kardbrd.yml Workflow Generator"

type SkillInfo struct {
	Command     string
	Name        string
	Description string
}

var executorSkillDirs = map[string][]string{
	"claude": {".claude/skills", ".claude/commands"},
	"codex":  {".agents/skills", ".codex/skills"},
	"goose":  {".claude/skills", ".claude/commands"},
	"pi":     {".claude/skills", ".claude/commands"},
}

func (m *Manager) BotCardTitle() string {
	return "🤖 " + m.AgentName
}

func (m *Manager) BuildBotCardDescription() string {
	now := m.StartTime
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ghState := "absent"
	if os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("GH_TOKEN") != "" {
		ghState = "present"
	}
	ruleCount := 0
	if m.Rules != nil {
		ruleCount = len(m.Rules.Rules)
	}

	lines := []string{
		"| **Setting** | **Value** |",
		"| --- | --- |",
		fmt.Sprintf("| Agent | @%s |", m.AgentName),
		fmt.Sprintf("| Version | %s |", version.Version),
		fmt.Sprintf("| Executor | %s |", m.ExecutorType),
		fmt.Sprintf("| Board | `%s` |", m.BoardID),
		fmt.Sprintf("| API | %s |", m.APIURL),
		fmt.Sprintf("| GH Token | %s |", ghState),
		fmt.Sprintf("| Working directory | `%s` |", m.CWD),
		fmt.Sprintf("| Timeout | %s |", m.Timeout),
		fmt.Sprintf("| Max concurrent | %d |", m.MaxConcurrent),
		"| Board access | kardbrd CLI/API |",
		fmt.Sprintf("| Rules | %d |", ruleCount),
		fmt.Sprintf("| Schedules | %d |", len(m.Schedules)),
		fmt.Sprintf("| Last started | %s |", now.UTC().Format("2006-01-02 15:04 UTC")),
	}

	if m.Rules != nil && len(m.Rules.Rules) > 0 {
		lines = append(lines, "", "## Triggers", "")
		for _, rule := range m.Rules.Rules {
			lines = append(lines,
				"### "+rule.Name,
				"",
				"- **Event:** "+strings.Join(rule.Events, ", "),
				"- **Action:** "+truncate(rule.Action, 80),
				"",
			)
		}
	}

	if len(m.Schedules) > 0 {
		lines = append(lines, "", "## Schedules", "")
		for _, schedule := range m.Schedules {
			lines = append(lines,
				"### "+schedule.Name,
				"",
				"- **Cron:** `"+schedule.Cron+"`",
				"- **Action:** "+truncate(schedule.Action, 80),
				"",
			)
		}
	}

	if skills := m.DiscoverSkills(); len(skills) > 0 {
		lines = append(lines, "", "## Skills", "")
		for _, skill := range skills {
			description := skill.Name
			if skill.Description != "" {
				description += " - " + skill.Description
			}
			lines = append(lines, fmt.Sprintf("- `/%s` - %s", skill.Command, description))
		}
	}

	return strings.Join(lines, "\n")
}

func (m *Manager) DiscoverSkills() []SkillInfo {
	dirs := executorSkillDirs[m.ExecutorType]
	if len(dirs) == 0 {
		dirs = executorSkillDirs["claude"]
	}

	seen := map[string]SkillInfo{}
	for _, relDir := range dirs {
		dir := filepath.Join(m.CWD, relDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() {
				command := entry.Name()
				if _, ok := seen[command]; ok {
					continue
				}
				mdFiles, _ := filepath.Glob(filepath.Join(dir, command, "*.md"))
				sort.Strings(mdFiles)
				if len(mdFiles) > 0 {
					seen[command] = extractSkillInfo(mdFiles[0], command)
				}
				continue
			}
			if filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			command := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			if _, ok := seen[command]; ok {
				continue
			}
			seen[command] = extractSkillInfo(filepath.Join(dir, entry.Name()), command)
		}
	}

	commands := make([]string, 0, len(seen))
	for command := range seen {
		commands = append(commands, command)
	}
	sort.Strings(commands)
	skills := make([]SkillInfo, 0, len(commands))
	for _, command := range commands {
		skills = append(skills, seen[command])
	}
	return skills
}

func (m *Manager) EnsureBotCard(ctx context.Context) error {
	board, err := m.loadBoard(ctx)
	if err != nil {
		return err
	}
	if len(board.Lists) == 0 {
		return nil
	}

	title := m.BotCardTitle()
	description := m.BuildBotCardDescription()
	for _, list := range board.Lists {
		for _, card := range list.Cards {
			if card.Title == title {
				m.BotCardID = card.ID
				_, err := m.Client.UpdateCard(ctx, card.ID, api.CardPatch{Description: &description})
				return err
			}
		}
	}

	raw, err := m.Client.CreateCard(ctx, m.BoardID, board.Lists[0].ID, title, description)
	if err != nil {
		return err
	}
	m.BotCardID = idFromRaw(raw)
	return nil
}

func (m *Manager) EnsureWizardCard(ctx context.Context) error {
	if m.Rules != nil && len(m.Rules.Rules) > 0 {
		return nil
	}
	board, err := m.loadBoard(ctx)
	if err != nil {
		return err
	}
	if len(board.Lists) == 0 {
		return nil
	}
	for _, list := range board.Lists {
		for _, card := range list.Cards {
			if card.Title == wizardCardTitle {
				return nil
			}
		}
	}

	listID := chooseWizardList(board.Lists)
	description := "Describe the workflow you want this agent to automate, then mention @" + m.AgentName + "."
	raw, err := m.Client.CreateCard(ctx, m.BoardID, listID, wizardCardTitle, description)
	if err != nil {
		return err
	}
	cardID := idFromRaw(raw)
	if cardID != "" {
		_, _ = m.Client.AddComment(ctx, cardID, "Welcome. Mention @"+m.AgentName+" with the workflow you want to generate.")
	}
	return nil
}

func (m *Manager) RegisterSkills(ctx context.Context) error {
	registrar, ok := m.Client.(interface {
		RegisterSkills(ctx context.Context, skills []api.SkillPayload) (json.RawMessage, error)
	})
	if !ok {
		return nil
	}
	discovered := m.DiscoverSkills()
	payload := make([]api.SkillPayload, 0, len(discovered))
	for _, skill := range discovered {
		description := skill.Description
		if description == "" {
			description = skill.Name
		}
		payload = append(payload, api.SkillPayload{Name: skill.Command, Description: description})
	}
	_, err := registrar.RegisterSkills(ctx, payload)
	return err
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

func chooseWizardList(lists []boardList) string {
	preferred := []string{"to do", "todo", "backlog", "inbox", "ideas"}
	for _, target := range preferred {
		for _, list := range lists {
			if strings.EqualFold(strings.TrimSpace(list.Name), target) {
				return list.ID
			}
		}
	}
	return lists[0].ID
}

func extractSkillInfo(path string, fallbackCommand string) SkillInfo {
	content, err := os.ReadFile(path)
	if err != nil {
		return SkillInfo{Command: fallbackCommand, Name: fallbackCommand}
	}
	text := string(content)
	if strings.HasPrefix(text, "---") {
		parts := strings.SplitN(text, "---", 3)
		if len(parts) == 3 {
			var frontmatter struct {
				Name        string `yaml:"name"`
				Description string `yaml:"description"`
			}
			if err := yaml.Unmarshal([]byte(parts[1]), &frontmatter); err == nil {
				name := frontmatter.Name
				if name == "" {
					name = fallbackCommand
				}
				return SkillInfo{Command: fallbackCommand, Name: name, Description: frontmatter.Description}
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return SkillInfo{Command: fallbackCommand, Name: strings.TrimSpace(strings.TrimPrefix(line, "# "))}
		}
	}
	return SkillInfo{Command: fallbackCommand, Name: fallbackCommand}
}

func idFromRaw(raw json.RawMessage) string {
	var payload struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &payload)
	return payload.ID
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
