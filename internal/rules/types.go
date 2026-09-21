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
	// Worktree is nil unless the configuration explicitly opts into the
	// portable lifecycle. Nil deliberately preserves the legacy manager.
	Worktree  *WorktreeConfig
	Rules     []Rule
	Schedules []Schedule
}

type CheckoutMode string

const (
	CheckoutFull      CheckoutMode = "full"
	CheckoutDelegated CheckoutMode = "delegated"
)

type SharingEnv string

const (
	SharingEnvDisabled SharingEnv = "disabled"
	SharingEnvLink     SharingEnv = "link"
)

type SharingSkills string

const (
	SharingSkillsFallback SharingSkills = "fallback"
	SharingSkillsDisabled SharingSkills = "disabled"
)

type ExecutionPolicy string

const (
	ExecutionPrepare        ExecutionPolicy = "prepare"
	ExecutionExistingOrBase ExecutionPolicy = "existing_or_base"
)

// WorktreeConfig is intentionally repository-generic. Project setup behavior
// lives in administrator-installed direct argv helpers, never in this binary.
type WorktreeConfig struct {
	Base        WorktreeBase
	Helpers     WorktreeHelpers
	Checkout    WorktreeCheckout
	Prepare     *PrepareHook
	Sharing     WorktreeSharing
	Environment WorktreeEnvironment
	Adoptions   []WorktreeAdoption
}

type WorktreeBase struct {
	Remote string
	Ref    string
}

type WorktreeHelpers struct {
	StableRoot string
}

type WorktreeCheckout struct {
	Mode      CheckoutMode
	Bootstrap *Hook
}

type Hook struct {
	Argv           []string
	TimeoutSeconds int
}

type PrepareHook struct {
	Hook
	OnCreate bool
	OnReuse  bool
}

type WorktreeSharing struct {
	Env    SharingEnv
	Skills SharingSkills
}

type WorktreeEnvironment struct {
	Passthrough []string
}

type WorktreeAdoption struct {
	CardID       string
	Path         string
	CommonGitDir string
	Branch       string
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
	// CleanupCommand is a direct argv command run when a card enters Done. It
	// deliberately bypasses the executor and worktree lifecycle.
	CleanupCommand []string
	// CommentCommand is an exact normal-card command such as /up. It is a
	// command policy rather than fuzzy content matching.
	CommentCommand string
	Execution      ExecutionPolicy
}

func (r Rule) IsStop() bool {
	return r.Action == StopAction
}

func (r Rule) IsCleanup() bool {
	return len(r.CleanupCommand) > 0
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
	CardID        string
	Name          string
	Cron          string
	Action        string
	Model         string
	Assignee      string
	List          string
	PublishResult *bool
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

// PublishesResult reports whether the daemon should publish a schedule result.
// A missing value preserves the legacy behavior of publishing the result.
func (s Schedule) PublishesResult() bool {
	return s.PublishResult == nil || *s.PublishResult
}

type Engine struct {
	Rules []Rule
}
