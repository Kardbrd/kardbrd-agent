package rules

const StopAction = "__stop__"

var modelMap = map[string]string{
	"opus":   "claude-opus-4-6",
	"sonnet": "claude-sonnet-4-5-20250929",
	"haiku":  "claude-haiku-4-5-20251001",
}

type Config struct {
	BoardID   string
	AgentName string
	APIURL    string
	Executor  string
	Rules     []Rule
	Schedules []Schedule
}

type Rule struct {
	Name            string
	Events          []string
	Action          string
	Model           string
	List            string
	Title           string
	Label           string
	ContentContains string
	ExcludeLabel    string
	RequireLabel    string
	Emoji           string
	RequireUser     string
	Assignee        []string
	CommentAuthor   string
}

func (r Rule) IsStop() bool {
	return r.Action == StopAction
}

func (r Rule) ModelID() string {
	if r.Model == "" {
		return ""
	}
	if resolved, ok := modelMap[stringsLower(r.Model)]; ok {
		return resolved
	}
	return r.Model
}

type Schedule struct {
	CardID   string
	Name     string
	Cron     string
	Action   string
	Model    string
	Assignee string
	List     string
}

func (s Schedule) ModelID() string {
	if s.Model == "" {
		return ""
	}
	if resolved, ok := modelMap[stringsLower(s.Model)]; ok {
		return resolved
	}
	return s.Model
}

type Engine struct {
	Rules []Rule
}
