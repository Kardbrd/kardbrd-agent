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
	outputPath, err := createCodexFinalMessageFile()
	if err != nil {
		return Result{Success: false, Error: err.Error()}
	}
	defer removeCodexFinalMessageFile(outputPath)

	cmd := []string{"codex", "exec"}
	if req.ResumeSessionID != "" {
		cmd = append(cmd, "resume")
	}
	cmd = append(cmd, "--dangerously-bypass-approvals-and-sandbox", "--json", "--output-last-message", outputPath)
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	if req.ResumeSessionID != "" {
		cmd = append(cmd, req.ResumeSessionID)
	}

	var onStdoutLine func(string)
	if req.OnChunk != nil {
		onStdoutLine = newCodexChunkEmitter(req.OnChunk)
	}
	stdout, stderr, code, runErr := runCommand(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, req.CardID, req.BoardID, "Codex execution timed out", onStdoutLine)
	result := resultFromRun(parseCodexOutput, stdout, stderr, code, cmd, runErr)
	if !result.Success {
		return result
	}

	finalMessage, err := readCodexFinalMessage(outputPath)
	if err != nil {
		result.Success = false
		result.Error = err.Error()
		return result
	}
	result.ResultText = finalMessage
	return result
}
