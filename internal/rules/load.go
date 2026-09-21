package rules

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type rawConfig struct {
	BoardID   string        `yaml:"board_id"`
	AgentName string        `yaml:"agent"`
	APIURL    string        `yaml:"api_url"`
	Executor  string        `yaml:"executor"`
	Worktree  *rawWorktree  `yaml:"worktree"`
	Rules     []rawRule     `yaml:"rules"`
	Schedules []rawSchedule `yaml:"schedules"`
}

type rawWorktree struct {
	Base        *rawWorktreeBase        `yaml:"base"`
	Helpers     *rawWorktreeHelpers     `yaml:"helpers"`
	Checkout    *rawWorktreeCheckout    `yaml:"checkout"`
	Prepare     *rawPrepareHook         `yaml:"prepare"`
	Sharing     *rawWorktreeSharing     `yaml:"sharing"`
	Environment *rawWorktreeEnvironment `yaml:"environment"`
	Adoptions   []rawWorktreeAdoption   `yaml:"adoptions"`
}

type rawWorktreeBase struct {
	Remote string `yaml:"remote"`
	Ref    string `yaml:"ref"`
}

type rawWorktreeHelpers struct {
	StableRoot string `yaml:"stable_root"`
}

type rawWorktreeCheckout struct {
	Mode      string   `yaml:"mode"`
	Bootstrap *rawHook `yaml:"bootstrap"`
}

type rawHook struct {
	Argv           []string `yaml:"argv"`
	TimeoutSeconds *int     `yaml:"timeout_seconds"`
}

type rawPrepareHook struct {
	Argv           []string `yaml:"argv"`
	TimeoutSeconds *int     `yaml:"timeout_seconds"`
	OnCreate       *bool    `yaml:"on_create"`
	OnReuse        *bool    `yaml:"on_reuse"`
}

type rawWorktreeSharing struct {
	Env    string `yaml:"env"`
	Skills string `yaml:"skills"`
}

type rawWorktreeEnvironment struct {
	Passthrough []string `yaml:"passthrough"`
}

type rawWorktreeAdoption struct {
	CardID       string `yaml:"card_id"`
	Path         string `yaml:"path"`
	CommonGitDir string `yaml:"common_git_dir"`
	Branch       string `yaml:"branch"`
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
	CleanupCommand  []string `yaml:"cleanup_command"`
	CommentCommand  string   `yaml:"comment_command"`
	Execution       string   `yaml:"execution"`
}

type rawSchedule struct {
	CardID        string `yaml:"card_id"`
	Name          string `yaml:"name"`
	Cron          string `yaml:"cron"`
	Action        string `yaml:"action"`
	Model         string `yaml:"model"`
	Assignee      string `yaml:"assignee"`
	List          string `yaml:"list"`
	PublishResult *bool  `yaml:"publish_result"`
}

func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := validateLoadedCleanupCommands(data); err != nil {
		return Config{}, err
	}
	if err := validateLoadedLifecycle(data); err != nil {
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
	if raw.Worktree != nil {
		worktree, err := normalizeWorktree(*raw.Worktree)
		if err != nil {
			return Config{}, err
		}
		cfg.Worktree = &worktree
	}
	for _, rawRule := range raw.Rules {
		events, err := parseEvents(rawRule.Event)
		if err != nil {
			return Config{}, fmt.Errorf("rule %q: %w", rawRule.Name, err)
		}
		if rawRule.CleanupCommand != nil {
			if err := validateCleanupCommand(rawRule.Name, events, rawRule.List, rawRule.Action, rawRule.CleanupCommand); err != nil {
				return Config{}, err
			}
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
			CleanupCommand:  append([]string(nil), rawRule.CleanupCommand...),
			CommentCommand:  rawRule.CommentCommand,
			Execution:       ExecutionPolicy(rawRule.Execution),
		})
	}
	if err := normalizeCommandRules(cfg.Rules); err != nil {
		return Config{}, err
	}
	for _, rawSchedule := range raw.Schedules {
		cfg.Schedules = append(cfg.Schedules, Schedule{
			CardID:        rawSchedule.CardID,
			Name:          rawSchedule.Name,
			Cron:          rawSchedule.Cron,
			Action:        rawSchedule.Action,
			Model:         rawSchedule.Model,
			Assignee:      rawSchedule.Assignee,
			List:          rawSchedule.List,
			PublishResult: rawSchedule.PublishResult,
		})
	}
	return cfg, nil
}

func normalizeWorktree(raw rawWorktree) (WorktreeConfig, error) {
	config := WorktreeConfig{
		Base:     WorktreeBase{Remote: "origin"},
		Checkout: WorktreeCheckout{Mode: CheckoutFull},
		Sharing:  WorktreeSharing{Env: SharingEnvDisabled, Skills: SharingSkillsFallback},
	}
	if raw.Base != nil {
		if raw.Base.Remote != "" {
			config.Base.Remote = raw.Base.Remote
		}
		config.Base.Ref = normalizeRef(raw.Base.Ref)
	}
	if raw.Helpers != nil {
		config.Helpers.StableRoot = raw.Helpers.StableRoot
	}
	if raw.Checkout != nil {
		if raw.Checkout.Mode != "" {
			config.Checkout.Mode = CheckoutMode(raw.Checkout.Mode)
		}
		if raw.Checkout.Bootstrap != nil {
			config.Checkout.Bootstrap = normalizeHook(*raw.Checkout.Bootstrap)
		}
	}
	if raw.Prepare != nil {
		onCreate := true
		if raw.Prepare.OnCreate != nil {
			onCreate = *raw.Prepare.OnCreate
		}
		onReuse := false
		if raw.Prepare.OnReuse != nil {
			onReuse = *raw.Prepare.OnReuse
		}
		config.Prepare = &PrepareHook{Hook: *normalizeHook(rawHook{Argv: raw.Prepare.Argv, TimeoutSeconds: raw.Prepare.TimeoutSeconds}), OnCreate: onCreate, OnReuse: onReuse}
	}
	if raw.Sharing != nil {
		if raw.Sharing.Env != "" {
			config.Sharing.Env = SharingEnv(raw.Sharing.Env)
		}
		if raw.Sharing.Skills != "" {
			config.Sharing.Skills = SharingSkills(raw.Sharing.Skills)
		}
	}
	if raw.Environment != nil {
		config.Environment.Passthrough = append([]string(nil), raw.Environment.Passthrough...)
	}
	for _, adoption := range raw.Adoptions {
		config.Adoptions = append(config.Adoptions, WorktreeAdoption{CardID: adoption.CardID, Path: adoption.Path, CommonGitDir: adoption.CommonGitDir, Branch: adoption.Branch})
	}
	if err := validateWorktreeConfig(config); err != nil {
		return WorktreeConfig{}, err
	}
	return config, nil
}

func normalizeHook(raw rawHook) *Hook {
	timeout := 900
	if raw.TimeoutSeconds != nil {
		timeout = *raw.TimeoutSeconds
	}
	return &Hook{Argv: append([]string(nil), raw.Argv...), TimeoutSeconds: timeout}
}

func normalizeRef(ref string) string {
	if ref == "main" {
		return "refs/heads/main"
	}
	return ref
}

func normalizeCommandRules(rules []Rule) error {
	seen := map[string]bool{}
	for index := range rules {
		rule := &rules[index]
		if rule.CommentCommand == "" {
			if rule.Execution != "" {
				return fmt.Errorf("rule %q: execution requires comment_command", rule.Name)
			}
			continue
		}
		if rule.Execution == "" {
			rule.Execution = ExecutionPrepare
		}
		if err := validateCommandRule(*rule); err != nil {
			return err
		}
		key := strings.ToLower(rule.CommentCommand)
		if seen[key] {
			return fmt.Errorf("rule %q: duplicate comment_command %q", rule.Name, rule.CommentCommand)
		}
		seen[key] = true
	}
	return nil
}

func validateCommandRule(rule Rule) error {
	if len(rule.Events) != 1 || rule.Events[0] != "comment_created" {
		return fmt.Errorf("rule %q: comment_command rules must use only the comment_created event", rule.Name)
	}
	if !strings.HasPrefix(rule.CommentCommand, "/") || strings.TrimSpace(rule.CommentCommand) != rule.CommentCommand || strings.ContainsAny(rule.CommentCommand, " \t\n") || len(rule.CommentCommand) == 1 {
		return fmt.Errorf("rule %q: comment_command must be one exact slash command", rule.Name)
	}
	if strings.TrimSpace(rule.Action) == "" {
		return fmt.Errorf("rule %q: comment_command requires action", rule.Name)
	}
	if rule.Execution != ExecutionPrepare && rule.Execution != ExecutionExistingOrBase {
		return fmt.Errorf("rule %q: execution must be prepare or existing_or_base", rule.Name)
	}
	return nil
}

// validateLoadedCleanupCommands retains LoadFile's existing compatibility for
// ordinary rules while preventing yaml.v3 from coercing non-string cleanup
// argv values during direct loading. It validates the same bytes that are
// decoded below, avoiding a reload-time validation/read race.
func validateLoadedCleanupCommands(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	rulesNode := mapping(root.Content[0])["rules"]
	if rulesNode == nil || rulesNode.Kind != yaml.SequenceNode {
		return nil
	}
	for index, entry := range rulesNode.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		fields := mapping(entry)
		command, ok := fields["cleanup_command"]
		if !ok {
			continue
		}
		var result ValidationResult
		name := scalar(fields["name"])
		validateCleanupCommandNode(&result, index, name, fields, parseEventNode(fields["event"]), command)
		if len(result.Errors) > 0 {
			return fmt.Errorf("rule %q: %s", name, result.Errors[0].Message)
		}
	}
	return nil
}

func validateCleanupCommand(name string, events []string, list, action string, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("rule %q: cleanup_command must contain at least one argument", name)
	}
	if len(events) != 1 || events[0] != "card_moved" {
		return fmt.Errorf("rule %q: cleanup_command rules must use only the card_moved event", name)
	}
	if !strings.EqualFold(list, "done") {
		return fmt.Errorf("rule %q: cleanup_command rules must target the Done list", name)
	}
	if action != "" {
		return fmt.Errorf("rule %q: cleanup_command cannot be combined with action", name)
	}
	for _, arg := range command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("rule %q: cleanup_command arguments must not be empty", name)
		}
	}
	if cleanupCommandUsesRestrictedRunner(command[0]) {
		return fmt.Errorf("rule %q: cleanup_command must not invoke sudo, env, or a shell", name)
	}
	return nil
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
