package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

type RootConfig struct {
	APIURL string
	Token  string
	Format string
}

type AgentConfig struct {
	RootConfig
	BoardID        string
	AgentName      string
	CWD            string
	TimeoutSeconds int
	MaxConcurrent  int
	WorktreesDir   string
	SetupCommand   string
	RulesFile      string
	Executor       string
}

type AgentFlagValues struct {
	BoardID        string
	Name           string
	CWD            string
	TimeoutSeconds int
	MaxConcurrent  int
	WorktreesDir   string
	SetupCommand   string
	RulesFile      string
	Executor       string
}

func LoadAgentConfig(env map[string]string, flags AgentFlagValues) (AgentConfig, []string, error) {
	var warnings []string
	var errs []string

	if legacy := legacyRenameErrors(env); len(legacy) > 0 {
		errs = append(errs, legacy...)
	}

	cwd, err := os.Getwd()
	if err != nil {
		errs = append(errs, fmt.Sprintf("cannot resolve current directory: %v", err))
		cwd = "."
	}

	cfg := AgentConfig{
		RootConfig: RootConfig{
			APIURL: firstNonEmpty(env["KARDBRD_API_URL"], "https://app.kardbrd.com"),
			Token:  env["KARDBRD_TOKEN"],
			Format: "json",
		},
		BoardID:        env["KARDBRD_AGENT_BOARD_ID"],
		AgentName:      env["KARDBRD_AGENT_NAME"],
		CWD:            firstNonEmpty(env["KARDBRD_AGENT_CWD"], cwd),
		TimeoutSeconds: 3600,
		MaxConcurrent:  3,
		WorktreesDir:   env["KARDBRD_AGENT_WORKTREES_DIR"],
		SetupCommand:   env["KARDBRD_AGENT_SETUP_CMD"],
		RulesFile:      env["KARDBRD_AGENT_RULES_FILE"],
		Executor:       firstNonEmpty(env["KARDBRD_AGENT_EXECUTOR"], "claude"),
	}

	if value := env["KARDBRD_AGENT_TIMEOUT"]; value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			errs = append(errs, "KARDBRD_AGENT_TIMEOUT must be an integer")
		} else {
			cfg.TimeoutSeconds = parsed
		}
	}
	if value := env["KARDBRD_AGENT_MAX_CONCURRENT"]; value != "" {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil {
			errs = append(errs, "KARDBRD_AGENT_MAX_CONCURRENT must be an integer")
		} else {
			cfg.MaxConcurrent = parsed
		}
	}

	applyAgentFlags(&cfg, flags)

	if cfg.TimeoutSeconds <= 0 {
		errs = append(errs, "KARDBRD_AGENT_TIMEOUT must be greater than 0")
	}
	if cfg.MaxConcurrent <= 0 {
		errs = append(errs, "KARDBRD_AGENT_MAX_CONCURRENT must be greater than 0")
	}

	if len(errs) > 0 {
		return AgentConfig{}, warnings, errors.New(strings.Join(errs, "\n"))
	}
	return cfg, warnings, nil
}

func applyAgentFlags(cfg *AgentConfig, flags AgentFlagValues) {
	if flags.BoardID != "" {
		cfg.BoardID = flags.BoardID
	}
	if flags.Name != "" {
		cfg.AgentName = flags.Name
	}
	if flags.CWD != "" {
		cfg.CWD = flags.CWD
	}
	if flags.TimeoutSeconds != 0 {
		cfg.TimeoutSeconds = flags.TimeoutSeconds
	}
	if flags.MaxConcurrent != 0 {
		cfg.MaxConcurrent = flags.MaxConcurrent
	}
	if flags.WorktreesDir != "" {
		cfg.WorktreesDir = flags.WorktreesDir
	}
	if flags.SetupCommand != "" {
		cfg.SetupCommand = flags.SetupCommand
	}
	if flags.RulesFile != "" {
		cfg.RulesFile = flags.RulesFile
	}
	if flags.Executor != "" {
		cfg.Executor = flags.Executor
	}
}

func legacyRenameErrors(env map[string]string) []string {
	renames := map[string]string{
		"KARDBRD_ID":           "KARDBRD_AGENT_BOARD_ID",
		"KARDBRD_AGENT":        "KARDBRD_AGENT_NAME",
		"KARDBRD_URL":          "KARDBRD_API_URL",
		"AGENT_CWD":            "KARDBRD_AGENT_CWD",
		"AGENT_TIMEOUT":        "KARDBRD_AGENT_TIMEOUT",
		"AGENT_MAX_CONCURRENT": "KARDBRD_AGENT_MAX_CONCURRENT",
		"AGENT_WORKTREES_DIR":  "KARDBRD_AGENT_WORKTREES_DIR",
		"AGENT_SETUP_CMD":      "KARDBRD_AGENT_SETUP_CMD",
		"AGENT_RULES_FILE":     "KARDBRD_AGENT_RULES_FILE",
		"AGENT_EXECUTOR":       "KARDBRD_AGENT_EXECUTOR",
	}

	var names []string
	for oldName := range renames {
		names = append(names, oldName)
	}
	sort.Strings(names)

	var errs []string
	for _, oldName := range names {
		newName := renames[oldName]
		if env[oldName] != "" && env[newName] == "" {
			errs = append(errs, fmt.Sprintf("%s was renamed to %s", oldName, newName))
		}
	}
	return errs
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
