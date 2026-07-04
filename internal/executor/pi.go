package executor

import (
	"context"
	"os"
	"os/exec"
)

type Pi struct {
	base
}

func NewPi(cfg Config) Pi {
	return Pi{base{cfg: cfg}}
}

func (e Pi) CheckAuth(ctx context.Context) AuthStatus {
	if _, err := exec.LookPath("pi"); err != nil {
		return AuthStatus{Authenticated: false, Error: "Pi CLI not found in PATH"}
	}
	provider := os.Getenv("PI_PROVIDER")
	if provider == "" {
		return AuthStatus{Authenticated: false, Error: "PI_PROVIDER env var not set"}
	}
	return AuthStatus{Authenticated: true, AuthMethod: "pi/" + provider}
}

func (e Pi) Execute(ctx context.Context, req Request) Result {
	if _, err := exec.LookPath("pi"); err != nil {
		return missingBinary("Pi")
	}
	cmd := []string{"pi", "--mode", "json", "-p", "-", "--no-session", "-a"}
	if req.Model != "" {
		cmd = append(cmd, "--model", req.Model)
	}
	if req.ResumeSessionID != "" {
		cmd = []string{"pi", "--mode", "json", "-p", "-", "-a", "--session", req.ResumeSessionID}
	}
	stdout, stderr, code, err := runCommand(ctx, e.cfg, e.cwd(req), cmd, req.Prompt, "Pi execution timed out", func(line string) {
		emitChunkLine(line, "pi", req.OnChunk)
	})
	return resultFromRun(parsePiOutput, stdout, stderr, code, cmd, err)
}
