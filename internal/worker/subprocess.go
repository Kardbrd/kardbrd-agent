package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrOutputTooLarge = errors.New("worker adapter output exceeds configured limit")
	ErrPacketTooLarge = errors.New("worker adapter packet exceeds configured limit")
	ErrAdapterInvalid = errors.New("worker adapter returned an invalid result")
)

const defaultPacketLimit = 64 * 1024

type Packet struct {
	Version            int             `json:"version"`
	CardID             string          `json:"card_id"`
	BoardID            string          `json:"board_id"`
	RunID              string          `json:"run_id"`
	Goal               string          `json:"goal"`
	CompletionCriteria string          `json:"completion_criteria"`
	Authorization      json.RawMessage `json:"authorization"`
	CardMarkdown       string          `json:"card_markdown"`
	Sources            []SourceRef     `json:"sources,omitempty"`
	EventReceipts      []string        `json:"event_receipts,omitempty"`
	Decision           *Decision       `json:"decision,omitempty"`
	Action             *ActionIntent   `json:"action,omitempty"`
	AllowedResults     []string        `json:"allowed_results"`
}

type ResultStatus string

const (
	ResultCompleted    ResultStatus = "completed"
	ResultScheduled    ResultStatus = "scheduled"
	ResultWaitingEvent ResultStatus = "waiting_event"
	ResultWaitingUser  ResultStatus = "waiting_user"
	ResultNeedsReview  ResultStatus = "needs_review"
)

type AdapterResult struct {
	Status         ResultStatus   `json:"status"`
	Summary        string         `json:"summary,omitempty"`
	WakeAt         *time.Time     `json:"wake_at,omitempty"`
	EventID        string         `json:"event_id,omitempty"`
	DecisionPrompt string         `json:"decision_prompt,omitempty"`
	ReceiptID      string         `json:"receipt_id,omitempty"`
	ActionReceipt  *ActionReceipt `json:"action_receipt,omitempty"`
	ActionStatus   string         `json:"action_status,omitempty"`
	ReviewNote     string         `json:"review_note,omitempty"`
}

func (r AdapterResult) Validate(action *ActionIntent, now time.Time) error {
	switch r.Status {
	case ResultCompleted, ResultScheduled, ResultWaitingEvent, ResultWaitingUser, ResultNeedsReview:
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrAdapterInvalid, r.Status)
	}
	if len(r.Summary) > maxSummarySize || len(r.ReviewNote) > maxSummarySize || len(r.EventID) > maxStableIDSize || len(r.DecisionPrompt) > maxSummarySize || len(r.ReceiptID) > maxStableIDSize {
		return fmt.Errorf("%w: summary exceeds %d bytes", ErrAdapterInvalid, maxSummarySize)
	}
	if r.Status == ResultScheduled && (r.WakeAt == nil || !r.WakeAt.After(now)) {
		return fmt.Errorf("%w: scheduled result requires a future wake_at", ErrAdapterInvalid)
	}
	if r.Status == ResultWaitingEvent && strings.TrimSpace(r.EventID) == "" {
		return fmt.Errorf("%w: waiting_event result requires event_id", ErrAdapterInvalid)
	}
	if r.Status == ResultWaitingUser && strings.TrimSpace(r.DecisionPrompt) == "" {
		return fmt.Errorf("%w: waiting_user result requires decision_prompt", ErrAdapterInvalid)
	}
	if r.ActionReceipt != nil {
		if action == nil || r.ActionReceipt.ActionID != action.ID || strings.TrimSpace(r.ActionReceipt.ReceiptID) == "" || len(r.ActionReceipt.ActionID) > maxStableIDSize || len(r.ActionReceipt.ReceiptID) > maxStableIDSize || len(r.ActionReceipt.Status) > maxSummarySize {
			return fmt.Errorf("%w: action receipt does not match durable action intent", ErrAdapterInvalid)
		}
	}
	if action == nil {
		if r.ActionReceipt != nil || r.ActionStatus != "" {
			return fmt.Errorf("%w: result has action fields without a durable action intent", ErrAdapterInvalid)
		}
		return nil
	}
	switch r.ActionStatus {
	case "not_started":
		if r.ActionReceipt != nil {
			return fmt.Errorf("%w: not_started action cannot have a receipt", ErrAdapterInvalid)
		}
	case "performed":
		if r.ActionReceipt == nil {
			return fmt.Errorf("%w: performed action requires a matching receipt", ErrAdapterInvalid)
		}
		if action.Receipt != nil {
			return fmt.Errorf("%w: durable action %q already has a receipt", ErrAdapterInvalid, action.ID)
		}
	case "uncertain":
		if r.Status != ResultNeedsReview {
			return fmt.Errorf("%w: uncertain action must request review", ErrAdapterInvalid)
		}
	default:
		return fmt.Errorf("%w: action result requires action_status", ErrAdapterInvalid)
	}
	return nil
}

type Runner interface {
	Run(context.Context, Packet) (AdapterResult, error)
}

type SubprocessRunner struct {
	Argv        []string
	ArtifactDir string
	OutputLimit int
	PacketLimit int
	// Env is deliberately explicit. Ambient process credentials are never
	// inherited by adapters; trusted bridge configuration belongs in files or
	// a socket owned by that bridge, not this worker's environment.
	Env []string
}

func (r SubprocessRunner) Run(ctx context.Context, packet Packet) (AdapterResult, error) {
	var result AdapterResult
	if len(r.Argv) == 0 || strings.TrimSpace(r.Argv[0]) == "" {
		return result, errors.New("worker runner argv is required")
	}
	input, err := marshalBoundedPacket(packet, r.PacketLimit)
	if err != nil {
		return result, err
	}
	dir, err := runDirectory(r.ArtifactDir, packet.RunID)
	if err != nil {
		return result, err
	}
	if err := writeArtifact(dir, "packet.json", input); err != nil {
		return result, err
	}
	stdout, stderr, err := runBoundedCommand(ctx, r.Argv, input, r.OutputLimit, r.Env)
	_ = writeArtifact(dir, "stdout.txt", stdout)
	_ = writeArtifact(dir, "stderr.txt", stderr)
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("%w: decode JSON result: %v", ErrAdapterInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return result, fmt.Errorf("%w: result has trailing JSON values", ErrAdapterInvalid)
	}
	if err := result.Validate(packet.Action, time.Now().UTC()); err != nil {
		return result, err
	}
	encoded, _ := json.Marshal(result)
	_ = writeArtifact(dir, "result.json", encoded)
	return result, nil
}

func marshalBoundedPacket(packet Packet, configuredLimit int) ([]byte, error) {
	limit := configuredLimit
	if limit == 0 {
		limit = defaultPacketLimit
	}
	if limit <= 0 {
		return nil, ErrPacketTooLarge
	}
	input, err := json.Marshal(packet)
	if err != nil {
		return nil, err
	}
	if len(input) > limit {
		return nil, fmt.Errorf("%w: %d bytes (limit %d)", ErrPacketTooLarge, len(input), limit)
	}
	return input, nil
}

type NoticePacket struct {
	Version  int    `json:"version"`
	NoticeID string `json:"notice_id"`
	CardID   string `json:"card_id"`
	RunID    string `json:"run_id"`
	Message  string `json:"message"`
}

type Notifier interface {
	Deliver(context.Context, NoticePacket) (string, error)
}

type SubprocessNotifier struct {
	Argv        []string
	OutputLimit int
	Env         []string
}

func (n SubprocessNotifier) Deliver(ctx context.Context, packet NoticePacket) (string, error) {
	if len(n.Argv) == 0 {
		return "", errors.New("notification argv is required")
	}
	input, err := json.Marshal(packet)
	if err != nil {
		return "", err
	}
	stdout, _, err := runBoundedCommand(ctx, n.Argv, input, n.OutputLimit, n.Env)
	if err != nil {
		return "", err
	}
	var reply struct {
		ReceiptID string `json:"receipt_id"`
	}
	if err := json.Unmarshal(stdout, &reply); err != nil || strings.TrimSpace(reply.ReceiptID) == "" {
		return "", fmt.Errorf("notification adapter returned no stable receipt")
	}
	return reply.ReceiptID, nil
}

type Observer interface {
	Observe(context.Context) ([]Suggestion, error)
}

type Suggestion struct {
	SourceID  string `json:"source_id"`
	Reference string `json:"reference,omitempty"`
	Title     string `json:"title"`
	Proposal  string `json:"proposal"`
}

type SubprocessObserver struct {
	Argv        []string
	OutputLimit int
	MaxEvents   int
	Env         []string
}

func (o SubprocessObserver) Observe(ctx context.Context) ([]Suggestion, error) {
	if len(o.Argv) == 0 {
		return nil, errors.New("observer argv is required")
	}
	stdout, _, err := runBoundedCommand(ctx, o.Argv, []byte(`{"version":1,"kind":"suggestion_observer"}`), o.OutputLimit, o.Env)
	if err != nil {
		return nil, err
	}
	var response struct {
		Events []Suggestion `json:"events"`
	}
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("%w: decode observer result: %v", ErrAdapterInvalid, err)
	}
	if o.MaxEvents <= 0 {
		return nil, errors.New("observer maximum event count must be positive")
	}
	if len(response.Events) > o.MaxEvents {
		return nil, fmt.Errorf("%w: observer returned %d events, limit is %d", ErrAdapterInvalid, len(response.Events), o.MaxEvents)
	}
	for _, event := range response.Events {
		if strings.TrimSpace(event.SourceID) == "" || len(event.SourceID) > maxStableIDSize || strings.TrimSpace(event.Title) == "" || len(event.Title) > maxSummarySize || strings.TrimSpace(event.Proposal) == "" || len(event.Proposal) > maxSummarySize || len(event.Reference) > maxSummarySize {
			return nil, fmt.Errorf("%w: observer event requires source_id, title, and proposal", ErrAdapterInvalid)
		}
	}
	return response.Events, nil
}

func runDirectory(root, runID string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("worker artifact directory is required")
	}
	if strings.TrimSpace(runID) == "" || filepath.Base(runID) != runID || strings.ContainsAny(runID, `/\\`) || runID == "." || runID == ".." {
		return "", errors.New("invalid worker run ID")
	}
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func writeArtifact(dir, name string, content []byte) error {
	return os.WriteFile(filepath.Join(dir, name), content, 0o600)
}

func runBoundedCommand(ctx context.Context, argv []string, input []byte, limit int, explicitEnv []string) ([]byte, []byte, error) {
	if limit <= 0 {
		return nil, nil, errors.New("worker output limit must be positive")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = adapterEnvironment(explicitEnv)
	configureProcessGroup(cmd)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	type readResult struct {
		data []byte
		over bool
		err  error
	}
	read := func(reader io.ReadCloser) <-chan readResult {
		ch := make(chan readResult, 1)
		go func() {
			defer reader.Close()
			data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
			ch <- readResult{data: data, over: len(data) > limit, err: err}
		}()
		return ch
	}
	stdoutCh := read(stdoutPipe)
	stderrCh := read(stderrPipe)
	type waitResult struct {
		state *os.ProcessState
		err   error
	}
	waitCh := make(chan waitResult, 1)
	go func() {
		// Cmd.Wait closes StdoutPipe/StderrPipe. Reap the direct child through
		// Process.Wait instead so the readers drain their complete output; once
		// the parent exits we still kill its process group before accepting the
		// result, which closes any descendant-held pipe ends.
		state, err := cmd.Process.Wait()
		waitCh <- waitResult{state: state, err: err}
	}()

	var stdout, stderr readResult
	var stdoutDone, stderrDone, waitDone, killed bool
	var runErr error
	contextDone := ctx.Done()
	for !stdoutDone || !stderrDone || !waitDone {
		select {
		case stdout = <-stdoutCh:
			stdoutDone = true
			if stdout.over && !killed {
				killProcessGroup(cmd)
				killed = true
			}
		case stderr = <-stderrCh:
			stderrDone = true
			if stderr.over && !killed {
				killProcessGroup(cmd)
				killed = true
			}
		case waited := <-waitCh:
			waitDone = true
			// A valid parent result is not permission for a forked background
			// child to outlive the run lease. The adapter owns one process group
			// and it is reaped on every terminal path.
			if !killed {
				killProcessGroup(cmd)
				killed = true
			}
			if runErr == nil && waited.err != nil {
				runErr = waited.err
			} else if runErr == nil && waited.state != nil && !waited.state.Success() {
				runErr = fmt.Errorf("exit status %d", waited.state.ExitCode())
			}
		case <-contextDone:
			if !killed {
				killProcessGroup(cmd)
				killed = true
				runErr = ctx.Err()
			}
			contextDone = nil
		}
	}
	if stdout.over || stderr.over {
		return stdout.data[:min(len(stdout.data), limit)], stderr.data[:min(len(stderr.data), limit)], ErrOutputTooLarge
	}
	if stdout.err != nil {
		return stdout.data, stderr.data, stdout.err
	}
	if stderr.err != nil {
		return stdout.data, stderr.data, stderr.err
	}
	if runErr != nil {
		return stdout.data, stderr.data, fmt.Errorf("worker adapter: %w", runErr)
	}
	return stdout.data, stderr.data, nil
}

func adapterEnvironment(explicit []string) []string {
	// Do not derive this list from os.Environ: that would pass the CLI's API
	// token, provider credentials, and other ambient capabilities to the
	// adapter. PATH is a non-secret execution convenience; callers may add
	// only test-safe or operator-reviewed values explicitly.
	env := []string{"PATH=/usr/bin:/bin", "LANG=C"}
	for _, entry := range explicit {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || value == "" || strings.ContainsAny(key, " \t\n") || looksCredentialLike(key) {
			continue
		}
		env = append(env, entry)
	}
	return env
}

func looksCredentialLike(key string) bool {
	key = strings.ToUpper(key)
	for _, marker := range []string{"TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "API_KEY", "AUTH"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
