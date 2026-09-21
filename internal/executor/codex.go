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

func (e Codex) Execute(ctx context.Context, req Request) (result Result) {
	if _, err := exec.LookPath("codex"); err != nil {
		return Result{Success: false, Error: "Codex CLI not found. Install: npm install -g @openai/codex"}
	}
	output, err := createCodexFinalMessageFile()
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	defer func() {
		if cleanupErr := output.remove(); cleanupErr != nil {
			result.Success = false
			if result.Error == "" {
				result.Error = cleanupErr.Error()
			} else {
				result.Error += "; " + cleanupErr.Error()
			}
		}
	}()

	cmd := []string{"codex", "exec"}
	if req.ResumeSessionID != "" {
		cmd = append(cmd, "resume")
	}
	cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox", "--json", "--output-last-message", output.path)
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	if req.ResumeSessionID != "" {
		cmd = append(cmd, "--", req.ResumeSessionID)
	}

	var onStdoutLine func(string)
	if req.OnChunk != nil {
		onStdoutLine = newCodexChunkEmitter(req.OnChunk)
	}
	stream := newCodexOutputState()
	stdout, stderr, code, runErr := runCommandWithStdoutObserver(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, req.CardID, req.BoardID, "Codex execution timed out", true, stream.consume, onStdoutLine)
	result = resultFromRun(func(_ string, stderr string, returnCode int, cmd []string) Result {
		return stream.result(stderr, returnCode, cmd)
	}, stdout, stderr, code, cmd, runErr, e.cfg)
	if !result.Success {
		return result
	}

	finalMessage, err := output.read()
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}
	result.ResultText = finalMessage
	return result
}
