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
	"sync"
	"time"
)

const (
	maxSubprocessStdoutBytes = 4 << 20
	maxSubprocessStderrBytes = 64 << 10
	stdoutDrainTimeout       = time.Second
)

// runCommand owns the process group and both output pipes. Keeping the parent
// ends out of os/exec's copy goroutines lets us reap a direct child promptly
// even when one of its descendants inherited stdout or stderr.
func runCommand(ctx context.Context, cfg Config, cwd string, args []string, promptText, cardID, boardID, timeoutError string, onStdoutLine func(string)) (stdout string, stderr string, code *int, err error) {
	commandCtx, cancel := context.WithTimeout(ctx, durationOrDefault(cfg.Timeout))
	defer cancel()
	if err := commandCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", "", nil, errors.New(timeoutError)
		}
		return "", "", nil, err
	}

	cmd := exec.Command(args[0], args[1:]...)
	configureExecutorProcessGroup(cmd)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = strings.NewReader(promptText)
	cmd.Env = executorEnvironment(os.Environ(), cfg, cardID, boardID)

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return "", "", nil, err
	}
	defer stdoutReader.Close()
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdoutWriter.Close()
		return "", "", nil, err
	}
	defer stderrReader.Close()
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err = cmd.Start(); err != nil {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		return "", "", nil, err
	}

	stdoutBuf := limitedOutput{limit: maxSubprocessStdoutBytes}
	stderrBuf := limitedOutput{limit: maxSubprocessStderrBytes}
	stdoutDone := make(chan error, 1)
	go func() {
		defer stdoutReader.Close()
		stdoutDone <- scanStdout(stdoutReader, &stdoutBuf, onStdoutLine)
	}()
	stderrDone := make(chan error, 1)
	go func() {
		defer stderrReader.Close()
		_, copyErr := io.Copy(&stderrBuf, stderrReader)
		stderrDone <- copyErr
	}()

	// Closing the parent copies is essential: only children, and not this
	// runner, should retain the write ends while the command runs.
	if closeErr := errors.Join(stdoutWriter.Close(), stderrWriter.Close()); closeErr != nil {
		killExecutorProcessGroup(cmd)
		_ = cmd.Wait()
		_ = waitForOutputDrain(stdoutDone, stderrDone, stdoutReader, stderrReader)
		return stdoutBuf.String(), stderrBuf.String(), nil, closeErr
	}

	processDone := make(chan struct{})
	go func() {
		select {
		case <-commandCtx.Done():
			killExecutorProcessGroup(cmd)
		case <-processDone:
		}
	}()

	err = cmd.Wait()
	close(processDone)
	// A parent exit is not permission for a forked subprocess with executor
	// credentials to continue running or hold one of our output descriptors.
	killExecutorProcessGroup(cmd)

	if drainErr := waitForOutputDrain(stdoutDone, stderrDone, stdoutReader, stderrReader); drainErr != nil {
		if err == nil {
			err = drainErr
		} else {
			err = errors.Join(err, drainErr)
		}
	}
	if stdoutBuf.Truncated() || stderrBuf.Truncated() {
		outputErr := errors.New("subprocess output exceeds configured diagnostic limit")
		if err == nil {
			err = outputErr
		} else {
			err = errors.Join(err, outputErr)
		}
	}
	if commandCtx.Err() == context.DeadlineExceeded {
		return stdoutBuf.String(), stderrBuf.String(), nil, errors.New(timeoutError)
	}
	if commandCtx.Err() != nil {
		return stdoutBuf.String(), stderrBuf.String(), nil, commandCtx.Err()
	}

	if cmd.ProcessState != nil {
		exitCode := cmd.ProcessState.ExitCode()
		code = &exitCode
	}
	return stdoutBuf.String(), stderrBuf.String(), code, err
}

func scanStdout(reader io.Reader, stdout io.Writer, onStdoutLine func(string)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	callbacksEnabled := onStdoutLine != nil
	for scanner.Scan() {
		line := scanner.Text()
		_, _ = io.WriteString(stdout, line+"\n")
		if callbacksEnabled && !callStdoutLine(onStdoutLine, line) {
			// A progress receiver must not prevent terminal capture or process
			// cleanup. Do not start concurrent callbacks after one is stuck.
			callbacksEnabled = false
		}
	}
	return scanner.Err()
}

func callStdoutLine(onStdoutLine func(string), line string) bool {
	done := make(chan struct{})
	go func() {
		onStdoutLine(line)
		close(done)
	}()
	timer := time.NewTimer(stdoutDrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func waitForOutputDrain(stdoutDone, stderrDone <-chan error, stdoutReader, stderrReader io.Closer) error {
	timer := time.NewTimer(stdoutDrainTimeout)
	defer timer.Stop()
	remaining := 2
	var drainErr error
	for remaining > 0 {
		select {
		case err := <-stdoutDone:
			remaining--
			if err != nil && !errors.Is(err, os.ErrClosed) {
				drainErr = errors.Join(drainErr, err)
			}
		case err := <-stderrDone:
			remaining--
			if err != nil && !errors.Is(err, os.ErrClosed) {
				drainErr = errors.Join(drainErr, err)
			}
		case <-timer.C:
			_ = stdoutReader.Close()
			_ = stderrReader.Close()
			return errors.Join(errors.New("timed out draining subprocess output"), drainErr)
		}
	}
	return drainErr
}

type limitedOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (o *limitedOutput) Write(value []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	remaining := o.limit - o.buffer.Len()
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		_, _ = o.buffer.Write(value[:remaining])
	}
	if remaining < len(value) {
		o.truncated = true
	}
	return len(value), nil
}

func (o *limitedOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	value := o.buffer.String()
	if o.truncated {
		value += "\n... (output truncated)"
	}
	return value
}

func (o *limitedOutput) Truncated() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.truncated
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

func resultFromRun(parse func(string, string, int, []string) Result, stdout string, stderr string, code *int, cmd []string, runErr error, cfg Config) Result {
	if runErr != nil && code == nil {
		return redactResultDiagnostics(Result{Success: false, Error: runErr.Error(), Stderr: stderr, Command: cmd}, cfg)
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
	return redactResultDiagnostics(result, cfg)
}

func redactResultDiagnostics(result Result, cfg Config) Result {
	for _, secret := range executorSensitiveValues(cfg) {
		result.Error = strings.ReplaceAll(result.Error, secret, "[REDACTED]")
		result.Stderr = strings.ReplaceAll(result.Stderr, secret, "[REDACTED]")
		result.Logs = strings.ReplaceAll(result.Logs, secret, "[REDACTED]")
	}
	return result
}

func executorSensitiveValues(cfg Config) []string {
	values := make([]string, 0, 4)
	if cfg.Token != "" {
		values = append(values, cfg.Token)
	}
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if found && value != "" && isSensitiveExecutorEnvironmentKey(key) {
			values = append(values, value)
		}
	}
	return values
}

func isSensitiveExecutorEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	return strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") || strings.HasSuffix(upper, "_KEY")
}

func missingBinary(name string) Result {
	return Result{Success: false, Error: fmt.Sprintf("%s CLI not found in PATH", name)}
}
