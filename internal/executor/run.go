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
	maxPendingProgressLines  = 128
)

// runCommand owns the process group and both output pipes. Keeping the parent
// ends out of os/exec's copy goroutines lets us reap a direct child promptly
// even when one of its descendants inherited stdout or stderr.
func runCommand(ctx context.Context, cfg Config, cwd string, args []string, promptText, cardID, boardID, timeoutError string, onStdoutLine func(string)) (stdout string, stderr string, code *int, err error) {
	return runCommandWithStdoutObserver(ctx, cfg, cwd, args, promptText, cardID, boardID, timeoutError, nil, onStdoutLine)
}

// runCommandWithStdoutObserver delivers each complete stdout line to observer
// before bounded diagnostic retention and best-effort progress delivery. An
// observer is for compact, authoritative protocol state only: it runs on the
// capture goroutine and must not block on external work.
func runCommandWithStdoutObserver(ctx context.Context, cfg Config, cwd string, args []string, promptText, cardID, boardID, timeoutError string, observer, onStdoutLine func(string)) (stdout string, stderr string, code *int, err error) {
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
	cmd.Env = executorEnvironment(os.Environ(), cfg, cardID, boardID)
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return "", "", nil, err
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()
	cmd.Stdin = stdinReader

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
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		return "", "", nil, err
	}
	// Use a parent-owned input pipe too. os/exec otherwise starts an input copy
	// goroutine and Wait can block when a descendant retains stdin after the
	// direct child exits.
	inputDone := make(chan error, 1)
	go func() {
		_, writeErr := io.WriteString(stdinWriter, promptText)
		_ = stdinWriter.Close()
		inputDone <- writeErr
	}()
	_ = stdinReader.Close()

	stdoutBuf := limitedOutput{limit: maxSubprocessStdoutBytes}
	stderrBuf := limitedOutput{limit: maxSubprocessStderrBytes}
	progress := newStdoutLineDispatcher(onStdoutLine)
	stdoutDone := make(chan error, 1)
	go func() {
		defer stdoutReader.Close()
		stdoutDone <- scanStdout(stdoutReader, &stdoutBuf, func(line string) {
			if observer != nil {
				observer(line)
			}
			progress.emit(line)
		})
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
		_ = stopInputWriter(stdinWriter, inputDone)
		_, _ = waitForOutputDrain(stdoutDone, stderrDone, stdoutReader, stderrReader)
		progress.finish(false)
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
	if inputErr := stopInputWriter(stdinWriter, inputDone); inputErr != nil {
		if err == nil {
			err = inputErr
		} else {
			err = errors.Join(err, inputErr)
		}
	}

	outputDrained, drainErr := waitForOutputDrain(stdoutDone, stderrDone, stdoutReader, stderrReader)
	progress.finish(outputDrained)
	if drainErr != nil {
		if err == nil {
			err = drainErr
		} else {
			err = errors.Join(err, drainErr)
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
	for scanner.Scan() {
		line := scanner.Text()
		if limited, ok := stdout.(*limitedOutput); ok {
			limited.WriteLine(line + "\n")
		} else {
			_, _ = io.WriteString(stdout, line+"\n")
		}
		if onStdoutLine != nil {
			onStdoutLine(line)
		}
	}
	return scanner.Err()
}

type stdoutLineDispatcher struct {
	lines     chan string
	stop      chan struct{}
	done      chan struct{}
	closeLine sync.Once
	closeStop sync.Once
}

func newStdoutLineDispatcher(onStdoutLine func(string)) *stdoutLineDispatcher {
	if onStdoutLine == nil {
		return nil
	}
	dispatcher := &stdoutLineDispatcher{
		lines: make(chan string, maxPendingProgressLines),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go func() {
		defer close(dispatcher.done)
		for {
			select {
			case <-dispatcher.stop:
				return
			case line, ok := <-dispatcher.lines:
				if !ok {
					return
				}
				onStdoutLine(line)
			}
		}
	}()
	return dispatcher
}

func (dispatcher *stdoutLineDispatcher) emit(line string) {
	if dispatcher == nil {
		return
	}
	select {
	case <-dispatcher.stop:
		return
	default:
	}
	select {
	case dispatcher.lines <- line:
	default:
		// Progress is best-effort. Capturing terminal protocol data must never
		// wait on a slow receiver or an unbounded progress backlog.
	}
}

func (dispatcher *stdoutLineDispatcher) finish(outputDrained bool) {
	if dispatcher == nil {
		return
	}
	if !outputDrained {
		dispatcher.stopNow()
		return
	}
	dispatcher.closeLine.Do(func() { close(dispatcher.lines) })
	timer := time.NewTimer(stdoutDrainTimeout)
	defer timer.Stop()
	select {
	case <-dispatcher.done:
	case <-timer.C:
		dispatcher.stopNow()
	}
}

func (dispatcher *stdoutLineDispatcher) stopNow() {
	if dispatcher == nil {
		return
	}
	dispatcher.closeStop.Do(func() { close(dispatcher.stop) })
}

func stopInputWriter(writer io.Closer, inputDone <-chan error) error {
	select {
	case err := <-inputDone:
		return err
	default:
	}
	_ = writer.Close()
	timer := time.NewTimer(stdoutDrainTimeout)
	defer timer.Stop()
	select {
	case err := <-inputDone:
		if errors.Is(err, os.ErrClosed) {
			return nil
		}
		return err
	case <-timer.C:
		return errors.New("timed out stopping subprocess stdin writer")
	}
}

func waitForOutputDrain(stdoutDone, stderrDone <-chan error, stdoutReader, stderrReader io.Closer) (bool, error) {
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
			return false, errors.Join(errors.New("timed out draining subprocess output"), drainErr)
		}
	}
	return true, drainErr
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

// WriteLine keeps a retained stdout diagnostic parseable by discarding a whole
// record once it would exceed the cap. stderr is intentionally byte-oriented
// because it is only surfaced as diagnostics, never decoded as a protocol.
func (o *limitedOutput) WriteLine(value string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(value) <= o.limit-o.buffer.Len() {
		_, _ = o.buffer.WriteString(value)
		return
	}
	o.truncated = true
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
