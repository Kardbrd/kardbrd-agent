package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCommand(ctx context.Context, cfg Config, cwd string, args []string, promptText string, timeoutError string, onStdoutLine func(string)) (stdout string, stderr string, code *int, err error) {
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
	cmd.Stderr = &stderrBuf

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", stderrBuf.String(), nil, err
	}
	if err = cmd.Start(); err != nil {
		return "", stderrBuf.String(), nil, err
	}

	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line)
			stdoutBuf.WriteByte('\n')
			if onStdoutLine != nil {
				onStdoutLine(line)
			}
		}
		scanDone <- scanner.Err()
	}()

	err = cmd.Wait()
	if scanErr := <-scanDone; err == nil && scanErr != nil {
		err = scanErr
	}
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
