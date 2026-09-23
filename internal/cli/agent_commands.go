package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/agent"
	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/config"
	"github.com/Kardbrd/kardbrd-agent/internal/executor"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
	"github.com/Kardbrd/kardbrd-agent/internal/scheduler"
	"github.com/Kardbrd/kardbrd-agent/internal/worktree"
	"github.com/spf13/cobra"
)

type agentRuntime struct {
	Config           config.AgentConfig
	Rules            rules.Engine
	Schedules        []rules.Schedule
	NoRetry          bool
	WorktreesEnabled bool
	GitRoot          string
	WorktreeConfig   *rules.WorktreeConfig
}

var runAgentRuntime = realRunAgentRuntime

func NewAgentCommand(root *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Agent daemon commands",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := rejectFormat(cmd, root); err != nil {
				return err
			}
			return cmd.Help()
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return rejectFormat(cmd, root)
		},
	}
	cmd.AddCommand(newAgentStartCommand(root))
	cmd.AddCommand(newAgentAdoptWorktreeCommand(root))
	cmd.AddCommand(&cobra.Command{
		Use:   "validate [kardbrd.yml]",
		Short: "Validate a kardbrd.yml rules file",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "kardbrd.yml"
			if len(args) > 0 {
				path = args[0]
			}
			result := rules.ValidateFile(path)
			for _, issue := range result.Warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", issue.Message)
			}
			for _, issue := range result.Errors {
				fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", issue.Message)
			}
			if !result.IsValid() {
				return fmt.Errorf("kardbrd.yml has errors")
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Valid")
			return err
		},
	})
	return cmd
}

func newAgentAdoptWorktreeCommand(root *rootOptions) *cobra.Command {
	flags := config.AgentFlagValues{}
	cmd := &cobra.Command{
		Use:   "adopt-worktree CARD_ID",
		Short: "Verify and record one configured existing worktree without changing source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.LoadAgentConfig(environMap(), flags)
			if err != nil {
				return err
			}
			rulesCfg, loaded, err := loadAgentRules(cmd, cfg)
			if err != nil {
				return err
			}
			if !loaded || rulesCfg.Worktree == nil {
				return fmt.Errorf("adopt-worktree requires an opt-in worktree block in kardbrd.yml")
			}
			applyRulesConfig(&cfg, rulesCfg)
			if strings.TrimSpace(cfg.SetupCommand) != "" {
				return fmt.Errorf("worktree lifecycle conflicts with legacy setup command")
			}
			gitRoot, gitEnabled := findGitRoot(cfg.CWD)
			if !gitEnabled {
				return fmt.Errorf("adopt-worktree requires a Git working directory")
			}
			manager, err := worktree.NewLifecycleManager(gitRoot, cfg.WorktreesDir, cfg.Executor, *rulesCfg.Worktree)
			if err != nil {
				return err
			}
			if err := verifyAdoptionCardID(cmd.Context(), api.NewClient(cfg.APIURL, cfg.Token), args[0]); err != nil {
				return err
			}
			if err := manager.Adopt(cmd.Context(), args[0]); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Adopted verified worktree for card %s\n", args[0])
			return err
		},
	}
	cmd.Flags().StringVarP(&flags.CWD, "cwd", "C", "", "Base Git working directory")
	cmd.Flags().StringVarP(&flags.WorktreesDir, "worktrees-dir", "w", "", "Directory for worktrees")
	cmd.Flags().StringVarP(&flags.RulesFile, "rules", "r", "", "Path to kardbrd.yml rules file")
	cmd.Flags().StringVarP(&flags.Executor, "executor", "e", "", "Executor type used for lifecycle sharing")
	return cmd
}

// verifyAdoptionCardID makes administrative adoption fail closed when the API
// does not confirm the exact, case-preserving card ID named in the manifest.
// It deliberately runs before LifecycleManager.Adopt, whose successful path
// writes the origin: adopted ownership record.
func verifyAdoptionCardID(ctx context.Context, client *api.Client, expected string) error {
	raw, err := client.GetCard(ctx, expected)
	if err != nil {
		return fmt.Errorf("fetch adoption card %q: %w", expected, err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		return fmt.Errorf("decode adoption card %q: %w", expected, err)
	}
	if card.ID != expected {
		return fmt.Errorf("canonical API card ID %q does not exactly match configured adoption card_id %q", card.ID, expected)
	}
	return nil
}

func newAgentStartCommand(root *rootOptions) *cobra.Command {
	flags := config.AgentFlagValues{}
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the kardbrd agent daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			env := environMap()
			if cmd.Root().PersistentFlags().Changed("api-url") {
				env["KARDBRD_API_URL"] = root.apiURL
			}
			if cmd.Root().PersistentFlags().Changed("token") {
				env["KARDBRD_TOKEN"] = root.token
			}

			cfg, _, err := config.LoadAgentConfig(env, flags)
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return err
			}

			rulesCfg, loadedRules, err := loadAgentRules(cmd, cfg)
			if err != nil {
				return err
			}
			if loadedRules {
				applyRulesConfig(&cfg, rulesCfg)
				if cfg.RulesFile == "" {
					cfg.RulesFile = filepath.Join(cfg.CWD, "kardbrd.yml")
				}
				if rulesCfg.Worktree != nil && strings.TrimSpace(cfg.SetupCommand) != "" {
					return fmt.Errorf("worktree lifecycle conflicts with legacy setup command")
				}
				if err := validateLifecycleDeadline(cfg, rulesCfg); err != nil {
					return err
				}
			}

			missing := missingAgentConfig(cfg)
			if len(missing) > 0 {
				err := fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
				fmt.Fprintf(cmd.ErrOrStderr(), "Error: %s\n", err)
				return err
			}

			gitRoot, worktreesEnabled := findGitRoot(cfg.CWD)
			if !worktreesEnabled && (cfg.WorktreesDir != "" || cfg.SetupCommand != "") {
				fmt.Fprintln(cmd.ErrOrStderr(), "warning: working directory is not a git repository; worktree dir and setup command are ignored")
			}
			printAgentSummary(cmd, cfg, loadedRules, rulesCfg, worktreesEnabled)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runAgentRuntime(ctx, agentRuntime{
				Config:           cfg,
				Rules:            rules.Engine{Rules: rulesCfg.Rules},
				Schedules:        rulesCfg.Schedules,
				NoRetry:          root.noRetry,
				WorktreesEnabled: worktreesEnabled,
				GitRoot:          gitRoot,
				WorktreeConfig:   rulesCfg.Worktree,
			})
		},
	}

	cmd.Flags().StringVar(&flags.BoardID, "board-id", "", "Board ID")
	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Agent name for @mentions")
	cmd.Flags().StringVarP(&flags.CWD, "cwd", "C", "", "Working directory for the executor")
	cmd.Flags().IntVarP(&flags.TimeoutSeconds, "timeout", "t", 0, "Maximum execution time in seconds")
	cmd.Flags().IntVarP(&flags.MaxConcurrent, "max-concurrent", "c", 0, "Maximum number of concurrent sessions")
	cmd.Flags().StringVarP(&flags.WorktreesDir, "worktrees-dir", "w", "", "Directory for worktrees")
	cmd.Flags().StringVar(&flags.SetupCommand, "setup-cmd", "", "Setup command to run in worktrees after creation")
	cmd.Flags().StringVarP(&flags.RulesFile, "rules", "r", "", "Path to kardbrd.yml rules file")
	cmd.Flags().StringVarP(&flags.Executor, "executor", "e", "", "Executor type: claude, goose, codex, or pi")
	return cmd
}

func loadAgentRules(cmd *cobra.Command, cfg config.AgentConfig) (rules.Config, bool, error) {
	path := cfg.RulesFile
	explicit := path != ""
	if path == "" {
		path = filepath.Join(cfg.CWD, "kardbrd.yml")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) && !explicit {
			return rules.Config{}, false, nil
		}
		result := rules.ValidateFile(path)
		for _, issue := range result.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", issue.Message)
		}
		return rules.Config{}, false, fmt.Errorf("kardbrd.yml has errors")
	}

	result := rules.ValidateFile(path)
	for _, issue := range result.Warnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", issue.Message)
	}
	for _, issue := range result.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "error: %s\n", issue.Message)
	}
	if !result.IsValid() {
		return rules.Config{}, false, fmt.Errorf("kardbrd.yml has errors")
	}
	loaded, err := rules.LoadFile(path)
	if err != nil {
		return rules.Config{}, false, err
	}
	return loaded, true, nil
}

func applyRulesConfig(cfg *config.AgentConfig, rulesCfg rules.Config) {
	if rulesCfg.BoardID != "" {
		cfg.BoardID = rulesCfg.BoardID
	}
	if rulesCfg.AgentName != "" {
		cfg.AgentName = rulesCfg.AgentName
	}
	if rulesCfg.APIURL != "" {
		cfg.APIURL = rulesCfg.APIURL
	}
	if rulesCfg.Executor != "" {
		cfg.Executor = rulesCfg.Executor
	}
}

func printAgentSummary(cmd *cobra.Command, cfg config.AgentConfig, loadedRules bool, rulesCfg rules.Config, worktreesEnabled bool) {
	worktreeStatus := "disabled"
	if worktreesEnabled {
		worktreeStatus = "enabled"
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Starting kardbrd agent")
	fmt.Fprintf(cmd.OutOrStdout(), "Board: %s\n", cfg.BoardID)
	fmt.Fprintf(cmd.OutOrStdout(), "Agent: @%s\n", cfg.AgentName)
	fmt.Fprintf(cmd.OutOrStdout(), "API: %s\n", cfg.APIURL)
	fmt.Fprintf(cmd.OutOrStdout(), "Working directory: %s\n", cfg.CWD)
	fmt.Fprintf(cmd.OutOrStdout(), "Worktree isolation: %s\n", worktreeStatus)
	fmt.Fprintf(cmd.OutOrStdout(), "Timeout: %ds\n", cfg.TimeoutSeconds)
	fmt.Fprintf(cmd.OutOrStdout(), "Max concurrent: %d\n", cfg.MaxConcurrent)
	fmt.Fprintf(cmd.OutOrStdout(), "Executor: %s\n", cfg.Executor)
	if cfg.SetupCommand == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Setup command: none")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Setup command: %s\n", cfg.SetupCommand)
	}
	if loadedRules {
		fmt.Fprintf(cmd.OutOrStdout(), "Rules: %d\n", len(rulesCfg.Rules))
		fmt.Fprintf(cmd.OutOrStdout(), "Schedules: %d\n", len(rulesCfg.Schedules))
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Rules: 0")
		fmt.Fprintln(cmd.OutOrStdout(), "Schedules: 0")
	}
}

func realRunAgentRuntime(ctx context.Context, runtime agentRuntime) error {
	cfg := runtime.Config
	client := api.NewClient(cfg.APIURL, cfg.Token)
	client.SetNoRetry(runtime.NoRetry)
	exec, err := newExecutor(cfg)
	if err != nil {
		return err
	}

	var wt agent.Worktree
	if runtime.WorktreeConfig != nil {
		if !runtime.WorktreesEnabled {
			wt = baseOnlyWorktreeAdapter{base: cfg.CWD}
		} else {
			lifecycle, err := worktree.NewLifecycleManager(runtime.GitRoot, cfg.WorktreesDir, cfg.Executor, *runtime.WorktreeConfig)
			if err != nil {
				return err
			}
			wt = lifecycleWorktreeAdapter{manager: lifecycle}
		}
	} else if runtime.WorktreesEnabled {
		wt = worktreeAdapter{worktree.NewManager(runtime.GitRoot, cfg.WorktreesDir, cfg.SetupCommand, cfg.Executor)}
	}
	ws := api.NewWebSocketClient(cfg.APIURL, cfg.Token)
	var scheduleManager *scheduler.Manager
	manager := agent.NewManager(agent.Config{
		BoardID:              cfg.BoardID,
		APIURL:               cfg.APIURL,
		Token:                cfg.Token,
		AgentName:            cfg.AgentName,
		CWD:                  cfg.CWD,
		Timeout:              time.Duration(cfg.TimeoutSeconds) * time.Second,
		MaxConcurrent:        cfg.MaxConcurrent,
		ExecutorType:         cfg.Executor,
		Rules:                &runtime.Rules,
		Schedules:            runtime.Schedules,
		LifecycleFingerprint: rules.LifecycleFingerprint(rules.Config{Worktree: runtime.WorktreeConfig, Rules: runtime.Rules.Rules}),
		Client:               client,
		Executor:             exec,
		Worktree:             wt,
		WebSocket:            ws,
	})
	scheduleManager = scheduler.NewManager(runtime.Schedules, cfg.BoardID, client, manager.ProcessSchedule)
	if cfg.RulesFile != "" {
		manager.Reload = newRuntimeRulesReload(cfg.RulesFile, cfg, manager, scheduleManager)
	}

	ws.OnBoardEvent = func(raw json.RawMessage) {
		var message map[string]any
		if err := json.Unmarshal(raw, &message); err != nil {
			return
		}
		go func() { _ = manager.HandleBoardEvent(ctx, message) }()
	}

	errCh := make(chan error, 3)
	if len(runtime.Schedules) > 0 || cfg.RulesFile != "" {
		go func() {
			if err := scheduleManager.Start(ctx); err != nil {
				errCh <- err
			}
		}()
	}
	if cfg.RulesFile != "" {
		go rulesReloadLoop(ctx, cfg.RulesFile, manager)
	}
	go statusPingLoop(ctx, ws, manager, cfg)
	go func() { errCh <- manager.Start(ctx) }()

	select {
	case <-ctx.Done():
		_ = manager.Stop(context.Background())
		return nil
	case err := <-errCh:
		_ = manager.Stop(context.Background())
		return err
	}
}

// newRuntimeRulesReload validates a complete candidate before it can change
// either running schedule entries or the ordinary rule engine. The candidate
// loader reads the rules file once; policy validation and scheduler staging
// therefore operate on exactly the bytes that passed semantic validation.
func newRuntimeRulesReload(path string, cfg config.AgentConfig, manager *agent.Manager, scheduleManager *scheduler.Manager) func(context.Context) (rules.Config, error) {
	return func(_ context.Context) (rules.Config, error) {
		loaded, err := rules.LoadValidatedFile(path)
		if err != nil {
			return rules.Config{}, err
		}
		if err := manager.ValidateRulesConfig(loaded); err != nil {
			return rules.Config{}, err
		}
		if err := validateLifecycleDeadline(cfg, loaded); err != nil {
			return rules.Config{}, err
		}
		if err := scheduleManager.UpdateSchedules(loaded.Schedules); err != nil {
			return rules.Config{}, err
		}
		return loaded, nil
	}
}

func validateLifecycleDeadline(cfg config.AgentConfig, rulesCfg rules.Config) error {
	if rulesCfg.Worktree == nil {
		return nil
	}
	limit := cfg.TimeoutSeconds
	for _, hook := range []*rules.Hook{rulesCfg.Worktree.Checkout.Bootstrap} {
		if hook != nil && hook.TimeoutSeconds > limit {
			return fmt.Errorf("worktree hook timeout_seconds must not exceed agent timeout")
		}
	}
	if rulesCfg.Worktree.Prepare != nil && rulesCfg.Worktree.Prepare.TimeoutSeconds > limit {
		return fmt.Errorf("worktree hook timeout_seconds must not exceed agent timeout")
	}
	return nil
}

func rulesReloadLoop(ctx context.Context, path string, manager *agent.Manager) {
	lastMod, _ := fileModTime(path)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			modTime, ok := fileModTime(path)
			if !ok || !modTime.After(lastMod) || manager.Reload == nil {
				continue
			}
			if _, err := manager.ReloadAndApply(ctx); err != nil {
				continue
			}
			_ = manager.EnsureBotCard(ctx)
			lastMod = modTime
		}
	}
}

func fileModTime(path string) (time.Time, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

func newExecutor(cfg config.AgentConfig) (executor.Interface, error) {
	execCfg := executor.Config{
		CWD:     cfg.CWD,
		Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second,
		APIURL:  cfg.APIURL,
		Token:   cfg.Token,
	}
	switch strings.ToLower(cfg.Executor) {
	case "claude":
		return executor.NewClaude(execCfg), nil
	case "codex":
		return executor.NewCodex(execCfg), nil
	case "goose":
		return executor.NewGoose(execCfg), nil
	case "pi":
		return executor.NewPi(execCfg), nil
	default:
		return nil, fmt.Errorf("unknown executor %q", cfg.Executor)
	}
}

type worktreeAdapter struct {
	manager *worktree.Manager
}

type lifecycleWorktreeAdapter struct {
	manager *worktree.LifecycleManager
}

// baseOnlyWorktreeAdapter is the explicit non-Git portion of the contract:
// existing_or_base may select the trusted CWD, while ordinary preparation
// cannot accidentally create or set up source outside Git lifecycle support.
type baseOnlyWorktreeAdapter struct {
	base string
}

func (a baseOnlyWorktreeAdapter) Prepare(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("prepare requires a Git worktree lifecycle")
}

func (a baseOnlyWorktreeAdapter) ExistingOrBase(context.Context, string, string) (string, error) {
	return a.base, nil
}

func (baseOnlyWorktreeAdapter) Remove(string, bool) error { return nil }

func (a lifecycleWorktreeAdapter) Prepare(ctx context.Context, cardID, boardID string) (string, error) {
	return a.manager.Prepare(ctx, worktree.LifecycleRequest{CardID: cardID, BoardID: boardID})
}

func (a lifecycleWorktreeAdapter) ExistingOrBase(ctx context.Context, cardID, boardID string) (string, error) {
	return a.manager.ExistingOrBase(ctx, worktree.LifecycleRequest{CardID: cardID, BoardID: boardID})
}

func (a lifecycleWorktreeAdapter) Remove(cardID string, force bool) error {
	return a.manager.Remove(cardID, force)
}

func (a lifecycleWorktreeAdapter) BranchContext(ctx context.Context, cardID, path string) string {
	return a.manager.BranchContext(ctx, cardID, path)
}

func (a worktreeAdapter) Create(cardID string) (string, error) {
	return a.manager.Create(cardID, "")
}

func (a worktreeAdapter) Remove(cardID string, force bool) error {
	return a.manager.Remove(cardID, force)
}

func statusPingLoop(ctx context.Context, ws *api.WebSocketClient, manager *agent.Manager, cfg config.AgentConfig) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = ws.SendStatusPing(ctx, map[string]string{"board_id": cfg.BoardID, "agent_name": cfg.AgentName}, manager.ActiveCardIDs())
		}
	}
}

func findGitRoot(cwd string) (string, bool) {
	dir, err := filepath.Abs(cwd)
	if err != nil {
		dir = cwd
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func environMap() map[string]string {
	env := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func missingAgentConfig(cfg config.AgentConfig) []string {
	var missing []string
	if cfg.BoardID == "" {
		missing = append(missing, "KARDBRD_AGENT_BOARD_ID (--board-id)")
	}
	if cfg.Token == "" {
		missing = append(missing, "KARDBRD_TOKEN (--token)")
	}
	if cfg.AgentName == "" {
		missing = append(missing, "KARDBRD_AGENT_NAME (--name)")
	}
	return missing
}
