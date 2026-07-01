package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCommand(ctx context.Context, cfg Config, cwd string, args []string, promptText string, timeoutError string) (stdout string, stderr string, code *int, err error) {
	commandCtx, cancel := context.WithTimeout(ctx, durationOrDefault(cfg.Timeout))
	defer cancel()

	cmd := exec.CommandContext(commandCtx, args[0], args[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = strings.NewReader(promptText)
	cmd.Env = os.Environ()
	if cfg.Token != "" && cfg.APIURL != "" {
		cmd.Env = append(cmd.Env, "KARDBRD_TOKEN="+cfg.Token, "KARDBRD_API_URL="+cfg.APIURL)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	if commandCtx.Err() == context.DeadlineExceeded {
		return stdoutBuf.String(), stderrBuf.String(), nil, errors.New(timeoutError)
	}

	if cmd.ProcessState != nil {
		exitCode := cmd.ProcessState.ExitCode()
		code = &exitCode
	}
	return stdoutBuf.String(), stderrBuf.String(), code, err
}

func durationOrDefault(value time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return time.Hour
}

func authCommand(ctx context.Context, args ...string) AuthStatus {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return AuthStatus{Authenticated: false, Error: strings.TrimSpace(stderr.String() + stdout.String())}
	}
	return AuthStatus{Authenticated: true}
}

func resultFromRun(parse func(string, string, int, []string) Result, stdout string, stderr string, code *int, cmd []string, runErr error) Result {
	if runErr != nil && code == nil {
		return Result{Success: false, Error: runErr.Error(), Stderr: stderr, Command: cmd}
	}
	exitCode := 0
	if code != nil {
		exitCode = *code
	}
	result := parse(stdout, stderr, exitCode, cmd)
	if runErr != nil && result.Error == "" {
		result.Error = runErr.Error()
		result.Success = false
	}
	return result
}

func missingBinary(name string) Result {
	return Result{Success: false, Error: fmt.Sprintf("%s CLI not found in PATH", name)}
}
