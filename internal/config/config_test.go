package config

import (
	"strings"
	"testing"
)

func TestAgentEnvUsesKardbrdAgentPrefix(t *testing.T) {
	env := map[string]string{
		"KARDBRD_API_URL":              "https://app.kardbrd.com",
		"KARDBRD_TOKEN":                "tok",
		"KARDBRD_AGENT_BOARD_ID":       "board",
		"KARDBRD_AGENT_NAME":           "bot",
		"KARDBRD_AGENT_CWD":            "/repo",
		"KARDBRD_AGENT_TIMEOUT":        "7200",
		"KARDBRD_AGENT_MAX_CONCURRENT": "2",
		"KARDBRD_AGENT_EXECUTOR":       "codex",
	}

	cfg, warnings, err := LoadAgentConfig(env, AgentFlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "https://app.kardbrd.com", cfg.APIURL)
	assertEqual(t, "tok", cfg.Token)
	assertEqual(t, "board", cfg.BoardID)
	assertEqual(t, "bot", cfg.AgentName)
	assertEqual(t, "/repo", cfg.CWD)
	assertEqual(t, 7200, cfg.TimeoutSeconds)
	assertEqual(t, 2, cfg.MaxConcurrent)
	assertEqual(t, "codex", cfg.Executor)
	assertEqual(t, 0, len(warnings))
}

func TestAgentFlagsOverrideEnvironment(t *testing.T) {
	env := map[string]string{
		"KARDBRD_API_URL":              "https://env.example",
		"KARDBRD_TOKEN":                "env-token",
		"KARDBRD_AGENT_BOARD_ID":       "env-board",
		"KARDBRD_AGENT_NAME":           "env-bot",
		"KARDBRD_AGENT_CWD":            "/env",
		"KARDBRD_AGENT_TIMEOUT":        "111",
		"KARDBRD_AGENT_MAX_CONCURRENT": "4",
		"KARDBRD_AGENT_EXECUTOR":       "claude",
	}

	cfg, _, err := LoadAgentConfig(env, AgentFlagValues{
		BoardID:        "flag-board",
		Name:           "flag-bot",
		CWD:            "/flag",
		TimeoutSeconds: 222,
		MaxConcurrent:  7,
		Executor:       "goose",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "flag-board", cfg.BoardID)
	assertEqual(t, "flag-bot", cfg.AgentName)
	assertEqual(t, "/flag", cfg.CWD)
	assertEqual(t, 222, cfg.TimeoutSeconds)
	assertEqual(t, 7, cfg.MaxConcurrent)
	assertEqual(t, "goose", cfg.Executor)
}

func TestAgentDefaults(t *testing.T) {
	env := map[string]string{
		"KARDBRD_TOKEN":          "tok",
		"KARDBRD_AGENT_BOARD_ID": "board",
		"KARDBRD_AGENT_NAME":     "bot",
	}

	cfg, _, err := LoadAgentConfig(env, AgentFlagValues{})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "https://app.kardbrd.com", cfg.APIURL)
	assertEqual(t, 3600, cfg.TimeoutSeconds)
	assertEqual(t, 3, cfg.MaxConcurrent)
	assertEqual(t, "claude", cfg.Executor)
	if cfg.CWD == "" {
		t.Fatal("expected cwd default")
	}
}

func TestLegacyAgentEnvFailsWithRenameMessage(t *testing.T) {
	env := map[string]string{
		"KARDBRD_TOKEN": "tok",
		"KARDBRD_ID":    "board",
		"KARDBRD_AGENT": "bot",
		"AGENT_CWD":     "/repo",
	}

	_, _, err := LoadAgentConfig(env, AgentFlagValues{})
	if err == nil {
		t.Fatal("expected rename error")
	}
	msg := err.Error()
	assertContains(t, msg, "KARDBRD_ID was renamed to KARDBRD_AGENT_BOARD_ID")
	assertContains(t, msg, "KARDBRD_AGENT was renamed to KARDBRD_AGENT_NAME")
	assertContains(t, msg, "AGENT_CWD was renamed to KARDBRD_AGENT_CWD")
}

func TestInvalidAgentEnvNumbers(t *testing.T) {
	env := map[string]string{
		"KARDBRD_TOKEN":                "tok",
		"KARDBRD_AGENT_BOARD_ID":       "board",
		"KARDBRD_AGENT_NAME":           "bot",
		"KARDBRD_AGENT_TIMEOUT":        "abc",
		"KARDBRD_AGENT_MAX_CONCURRENT": "0",
	}

	_, _, err := LoadAgentConfig(env, AgentFlagValues{})
	if err == nil {
		t.Fatal("expected validation error")
	}
	assertContains(t, err.Error(), "KARDBRD_AGENT_TIMEOUT must be an integer")
	assertContains(t, err.Error(), "KARDBRD_AGENT_MAX_CONCURRENT must be greater than 0")
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}

func assertContains(t *testing.T, text string, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Fatalf("expected %q to contain %q", text, want)
	}
}
