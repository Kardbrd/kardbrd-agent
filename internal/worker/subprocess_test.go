package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWorkerAdapterHelper(t *testing.T) {
	if os.Getenv("GO_WANT_WORKER_ADAPTER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	input, _ := io.ReadAll(os.Stdin)
	switch mode {
	case "complete":
		var packet Packet
		if err := json.Unmarshal(input, &packet); err != nil || packet.Goal != "goal $HOME ; no shell" || os.Getenv("KARDBRD_TOKEN") != "" || os.Getenv("KARDBRD_API_URL") != "" || os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
			fmt.Fprint(os.Stdout, `{"status":"bad"}`)
			os.Exit(0)
		}
		fmt.Fprint(os.Stdout, `{"status":"completed","summary":"fixture complete","receipt_id":"r-1"}`)
	case "bad":
		fmt.Fprint(os.Stdout, "not-json")
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 8192))
	case "unknown-field":
		fmt.Fprint(os.Stdout, `{"status":"completed","unexpected":true}`)
	case "sleep":
		select {}
	case "child":
		child := exec.Command("sleep", "30")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		ready, err := net.Dial("unix", os.Getenv("WORKER_READY_SOCKET"))
		if err != nil {
			os.Exit(3)
		}
		fmt.Fprint(ready, child.Process.Pid)
		_ = ready.Close()
		fmt.Fprint(os.Stdout, child.Process.Pid)
		select {}
	case "child-success":
		child := exec.Command("sleep", "30")
		devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			os.Exit(2)
		}
		child.Stdout, child.Stderr = devNull, devNull
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		ready, err := net.Dial("unix", os.Getenv("WORKER_READY_SOCKET"))
		if err != nil {
			os.Exit(3)
		}
		fmt.Fprint(ready, child.Process.Pid)
		_ = ready.Close()
		fmt.Fprint(os.Stdout, `{"status":"completed"}`)
	case "observer-many":
		fmt.Fprint(os.Stdout, `{"events":[{"source_id":"one","title":"one","proposal":"one"},{"source_id":"two","title":"two","proposal":"two"}]}`)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func helperArgv(mode string) []string {
	return []string{os.Args[0], "-test.run=TestWorkerAdapterHelper", "--", mode}
}

func TestSubprocessRunnerPacketArtifactsAndSanitizedEnvironment(t *testing.T) {
	t.Setenv("KARDBRD_TOKEN", "must-not-reach-runner")
	t.Setenv("KARDBRD_API_URL", "https://must-not-reach-runner")
	t.Setenv("OPENAI_API_KEY", "must-not-reach-runner")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-reach-runner")
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	root := t.TempDir()
	runner := SubprocessRunner{Argv: helperArgv("complete"), ArtifactDir: root, OutputLimit: 4096, Env: []string{"GO_WANT_WORKER_ADAPTER=1", "OPENAI_API_KEY=also-must-not-reach-runner"}}
	result, err := runner.Run(context.Background(), Packet{Version: 1, CardID: "card", BoardID: "board", RunID: "run-1", Goal: "goal $HOME ; no shell", CompletionCriteria: "done", Authorization: json.RawMessage(`{"scope":"fixture"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != ResultCompleted || result.ReceiptID != "r-1" {
		t.Fatalf("result = %#v", result)
	}
	packet, err := os.ReadFile(filepath.Join(root, "run-1", "packet.json"))
	if err != nil || !strings.Contains(string(packet), "goal $HOME ; no shell") {
		t.Fatalf("artifact = %q, err = %v", packet, err)
	}
	info, err := os.Stat(filepath.Join(root, "run-1"))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("run directory permissions = %v, err = %v", info.Mode(), err)
	}
}

func TestSubprocessRunnerRejectsMalformedAndOversizedOutput(t *testing.T) {
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	for _, tc := range []struct {
		name string
		mode string
		want error
	}{
		{name: "malformed", mode: "bad", want: ErrAdapterInvalid},
		{name: "oversized", mode: "large", want: ErrOutputTooLarge},
		{name: "unknown field", mode: "unknown-field", want: ErrAdapterInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := SubprocessRunner{Argv: helperArgv(tc.mode), ArtifactDir: t.TempDir(), OutputLimit: 256, Env: []string{"GO_WANT_WORKER_ADAPTER=1"}}
			_, err := runner.Run(context.Background(), Packet{RunID: "run-1"})
			if err == nil || !strings.Contains(err.Error(), tc.want.Error()) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSubprocessRunnerRejectsOversizedPacketBeforeExecution(t *testing.T) {
	runner := SubprocessRunner{Argv: helperArgv("complete"), ArtifactDir: t.TempDir(), OutputLimit: 4096, PacketLimit: 64}
	_, err := runner.Run(context.Background(), Packet{RunID: "run-1", CardMarkdown: strings.Repeat("x", 100)})
	if !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestSubprocessObserverRejectsTooManyEvents(t *testing.T) {
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	_, err := (SubprocessObserver{Argv: helperArgv("observer-many"), OutputLimit: 4096, MaxEvents: 1, Env: []string{"GO_WANT_WORKER_ADAPTER=1"}}).Observe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "limit is 1") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunDirectoryRejectsTraversal(t *testing.T) {
	for _, runID := range []string{"../escape", "..\\escape", ".", ".."} {
		if _, err := runDirectory(t.TempDir(), runID); err == nil {
			t.Fatalf("run ID %q was accepted", runID)
		}
	}
}

func TestBoundedCommandCancelsProcessGroupAndChild(t *testing.T) {
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	socketPath := filepath.Join(t.TempDir(), "worker-ready.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("WORKER_READY_SOCKET", socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan struct {
		stdout []byte
		err    error
	}, 1)
	go func() {
		stdout, _, err := runBoundedCommand(ctx, helperArgv("child"), nil, 4096, []string{"GO_WANT_WORKER_ADAPTER=1", "WORKER_READY_SOCKET=" + socketPath})
		resultCh <- struct {
			stdout []byte
			err    error
		}{stdout, err}
	}()
	acceptCh := make(chan net.Conn, 1)
	go func() {
		connection, _ := listener.Accept()
		acceptCh <- connection
	}()
	var ready net.Conn
	select {
	case ready = <-acceptCh:
		if ready == nil {
			t.Fatal("child helper did not announce readiness")
		}
	case result := <-resultCh:
		t.Fatalf("adapter ended before child readiness: %q %v", result.stdout, result.err)
	case <-time.After(2 * time.Second):
		t.Fatal("child did not announce readiness")
	}
	announced, err := io.ReadAll(ready)
	_ = ready.Close()
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	result := <-resultCh
	if result.err == nil {
		t.Fatal("cancelled adapter returned nil error")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(announced)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid %q: %v", result.stdout, err)
	}
	if !waitProcessGoneOrZombie(pid) {
		t.Fatalf("child process %d survived process-group cancellation", pid)
	}
}

func TestBoundedCommandReapsSuccessfulAdapterChild(t *testing.T) {
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	socketPath := filepath.Join(t.TempDir(), "worker-ready.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	acceptCh := make(chan net.Conn, 1)
	go func() {
		connection, _ := listener.Accept()
		acceptCh <- connection
	}()
	type commandResult struct {
		stdout []byte
		err    error
	}
	resultCh := make(chan commandResult, 1)
	go func() {
		stdout, _, err := runBoundedCommand(context.Background(), helperArgv("child-success"), nil, 4096, []string{"GO_WANT_WORKER_ADAPTER=1", "WORKER_READY_SOCKET=" + socketPath})
		resultCh <- commandResult{stdout: stdout, err: err}
	}()
	var ready net.Conn
	select {
	case ready = <-acceptCh:
	case <-time.After(2 * time.Second):
		t.Fatal("successful child did not announce readiness")
	}
	announced, err := io.ReadAll(ready)
	_ = ready.Close()
	if err != nil {
		t.Fatal(err)
	}
	result := <-resultCh
	if result.err != nil {
		t.Fatal(result.err)
	}
	if string(result.stdout) != `{"status":"completed"}` {
		t.Fatalf("successful adapter output was truncated: %q", result.stdout)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(announced)))
	if err != nil || pid <= 0 {
		t.Fatalf("child pid %q: %v", announced, err)
	}
	if !waitProcessGoneOrZombie(pid) {
		t.Fatalf("child process %d survived successful adapter cleanup", pid)
	}
}

func waitProcessGoneOrZombie(pid int) bool {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if processGoneOrZombie(pid) {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}

func processGoneOrZombie(pid int) bool {
	if stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		if end := strings.LastIndex(string(stat), ")"); end >= 0 {
			fields := strings.Fields(string(stat[end+1:]))
			if len(fields) > 0 && fields[0] == "Z" {
				return true
			}
		}
	}
	return syscall.Kill(pid, 0) != nil
}
