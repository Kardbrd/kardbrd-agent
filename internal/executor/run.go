package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runCommand(ctx context.Context, cfg Config, cwd string, args []string, promptText, cardID, boardID, timeoutError string, onStdoutLine func(string)) (stdout string, stderr string, code *int, err error) {
	commandCtx, cancel := context.WithTimeout(ctx, durationOrDefault(cfg.Timeout))
	defer cancel()

	cmd := exec.CommandContext(commandCtx, args[0], args[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = strings.NewReader(promptText)
	cmd.Env = executorEnvironment(os.Environ(), cfg, cardID, boardID)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return "", stderrBuf.String(), nil, err
	}
	cmd.Stdout = stdoutWriter
	if err = cmd.Start(); err != nil {
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
		return "", stderrBuf.String(), nil, err
	}

	scanDone := make(chan error, 1)
	go func() {
		defer stdoutReader.Close()
		scanDone <- scanStdout(stdoutReader, &stdoutBuf, onStdoutLine)
	}()
	if closeErr := stdoutWriter.Close(); closeErr != nil {
		_ = stdoutReader.Close()
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		scanErr := <-scanDone
		if scanErr != nil && !errors.Is(scanErr, os.ErrClosed) {
			closeErr = errors.Join(closeErr, scanErr)
		}
		if waitErr != nil {
			closeErr = errors.Join(closeErr, waitErr)
		}
		return "", stderrBuf.String(), nil, closeErr
	}

	err = cmd.Wait()
	if scanErr := waitForStdoutDrain(scanDone, stdoutReader); scanErr != nil && !errors.Is(scanErr, os.ErrClosed) {
		if err == nil {
			err = scanErr
		} else {
			err = errors.Join(err, scanErr)
		}
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

const stdoutDrainTimeout = time.Second

func scanStdout(reader io.Reader, stdout *bytes.Buffer, onStdoutLine func(string)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		stdout.WriteString(line)
		stdout.WriteByte('\n')
		if onStdoutLine != nil {
			onStdoutLine(line)
		}
	}
	return scanner.Err()
}

func waitForStdoutDrain(scanDone <-chan error, reader io.Closer) error {
	timer := time.NewTimer(stdoutDrainTimeout)
	defer timer.Stop()
	select {
	case err := <-scanDone:
		return err
	case <-timer.C:
		_ = reader.Close()
		err := <-scanDone
		if err != nil && !errors.Is(err, os.ErrClosed) {
			return errors.Join(errors.New("timed out draining subprocess stdout"), err)
		}
		return errors.New("timed out draining subprocess stdout")
	}
}

func executorEnvironment(parent []string, cfg Config, cardID, boardID string) []string {
	env := make([]string, 0, len(parent)+4)
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		if name == "KARDBRD_CARD_ID" || name == "KARDBRD_BOARD_ID" {
			continue
		}
		env = append(env, entry)
	}
	if cfg.Token != "" && cfg.APIURL != "" {
		env = append(env, "KARDBRD_TOKEN="+cfg.Token, "KARDBRD_API_URL="+cfg.APIURL)
	}
	return append(env, "KARDBRD_CARD_ID="+cardID, "KARDBRD_BOARD_ID="+boardID)
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
