package executor

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
)

type Claude struct {
	base
}

func NewClaude(cfg Config) Claude {
	return Claude{base{cfg: cfg}}
}

func (e Claude) CheckAuth(ctx context.Context) AuthStatus {
	if _, err := exec.LookPath("claude"); err != nil {
		return AuthStatus{Authenticated: false, Error: "Claude CLI not found in PATH", AuthHint: "Ensure `claude` is in PATH"}
	}
	stdout, stderr, code, err := runCommand(ctx, Config{Timeout: e.timeout()}, "", []string{"claude", "auth", "status"}, "", "claude auth status timed out", nil)
	if err != nil || code == nil || *code != 0 {
		return AuthStatus{Authenticated: false, Error: strings.TrimSpace(stderr + stdout)}
	}
	var data struct {
		LoggedIn         bool   `json:"loggedIn"`
		Email            string `json:"email"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if err := json.Unmarshal([]byte(stdout), &data); err != nil || !data.LoggedIn {
		return AuthStatus{Authenticated: false, Error: "Claude CLI is not logged in"}
	}
	return AuthStatus{Authenticated: true, Email: data.Email, AuthMethod: data.AuthMethod, SubscriptionType: data.SubscriptionType}
}

func (e Claude) Execute(ctx context.Context, req Request) Result {
	if _, err := exec.LookPath("claude"); err != nil {
		return missingBinary("Claude")
	}
	cmd := []string{"claude", "-p", "-", "--output-format=stream-json", "--verbose", "--dangerously-skip-permissions"}
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	if req.ResumeSessionID != "" {
		cmd = append(cmd, "--resume", req.ResumeSessionID)
	}
	stdout, stderr, code, err := runCommand(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, "Claude execution timed out", func(line string) {
		emitChunkLine(line, "claude", req.OnChunk)
	})
	return resultFromRun(parseClaudeOutput, stdout, stderr, code, cmd, err)
}
