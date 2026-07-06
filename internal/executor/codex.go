package executor

import (
	"context"
	"os/exec"
)

type Codex struct {
	base
}

func NewCodex(cfg Config) Codex {
	return Codex{base{cfg: cfg}}
}

func (e Codex) CheckAuth(ctx context.Context) AuthStatus {
	if _, err := exec.LookPath("codex"); err != nil {
		return AuthStatus{Authenticated: false, Error: "Codex CLI not found", AuthHint: "Install Codex CLI: npm install -g @openai/codex"}
	}
	status := authCommand(ctx, "codex", "login", "status")
	if !status.Authenticated {
		status.AuthHint = "Run 'codex login' for subscription access, or authenticate with OPENAI_API_KEY via `codex login --with-api-key`."
		return status
	}
	status.AuthMethod = "codex"
	return status
}

func (e Codex) Execute(ctx context.Context, req Request) Result {
	if _, err := exec.LookPath("codex"); err != nil {
		return Result{Success: false, Error: "Codex CLI not found. Install: npm install -g @openai/codex"}
	}
	cmd := []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "--json"}
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	stdout, stderr, code, err := runCommand(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, "Codex execution timed out", func(line string) {
		emitChunkLine(line, "codex", req.OnChunk)
	})
	return resultFromRun(parseCodexOutput, stdout, stderr, code, cmd, err)
}
