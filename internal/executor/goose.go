package executor

import (
	"context"
	"os"
	"os/exec"
)

type Goose struct {
	base
}

func NewGoose(cfg Config) Goose {
	return Goose{base{cfg: cfg}}
}

func (e Goose) CheckAuth(ctx context.Context) AuthStatus {
	if _, err := exec.LookPath("goose"); err != nil {
		return AuthStatus{Authenticated: false, Error: "Goose CLI not found in PATH"}
	}
	provider := os.Getenv("GOOSE_PROVIDER")
	if provider == "" {
		return AuthStatus{Authenticated: false, Error: "GOOSE_PROVIDER env var not set"}
	}
	return AuthStatus{Authenticated: true, AuthMethod: "goose/" + provider}
}

func (e Goose) Execute(ctx context.Context, req Request) Result {
	if _, err := exec.LookPath("goose"); err != nil {
		return missingBinary("Goose")
	}
	cmd := []string{"goose", "run", "-t", "-", "--output-format", "stream-json", "--no-session"}
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	if req.ResumeSessionID != "" {
		cmd = []string{"goose", "run", "-t", "-", "--output-format", "stream-json", "-r", "-n", req.ResumeSessionID}
	}
	stdout, stderr, code, err := runCommand(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, "Goose execution timed out", func(line string) {
		emitChunkLine(line, "goose", req.OnChunk)
	})
	return resultFromRun(parseGooseOutput, stdout, stderr, code, cmd, err)
}
