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
	"runtime"
	"runtime/debug"
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
		if err := json.Unmarshal(input, &packet); err != nil || packet.Goal != "goal $HOME ; no shell" || os.Getenv("KARDBRD_TOKEN") != "" || os.Getenv("KARDBRD_API_URL") != "" || os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != "" || os.Getenv("GH_TOKEN") != "" {
			fmt.Fprint(os.Stdout, `{"status":"bad"}`)
			os.Exit(0)
		}
		_ = json.NewEncoder(os.Stdout).Encode(AdapterResult{RunID: packet.RunID, Status: ResultCompleted, Summary: "fixture complete", ReceiptID: "r-1"})
	case "missing-run-id":
		fmt.Fprint(os.Stdout, `{"status":"completed","receipt_id":"r-1"}`)
	case "wrong-run-id":
		fmt.Fprint(os.Stdout, `{"run_id":"other-run","status":"completed","receipt_id":"r-1"}`)
	case "notice-match", "notice-queued", "notice-delivered", "notice-wrong-id", "notice-bad-state", "notice-empty-receipt":
		var packet NoticePacket
		if err := json.Unmarshal(input, &packet); err != nil {
			os.Exit(2)
		}
		if mode == "notice-wrong-id" {
			packet.NoticeID = "another-notice"
		}
		receipt := NoticeReceipt{NoticeID: packet.NoticeID, ReceiptID: "notice-receipt-1"}
		if mode == "notice-queued" {
			receipt.DeliveryState = "queued"
		}
		if mode == "notice-delivered" {
			receipt.DeliveryState = "delivered"
		}
		if mode == "notice-bad-state" {
			receipt.DeliveryState = "later"
		}
		if mode == "notice-empty-receipt" {
			receipt.ReceiptID = ""
		}
		_ = json.NewEncoder(os.Stdout).Encode(receipt)
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
	case "child-failure":
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
		os.Exit(7)
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
	t.Setenv("GH_TOKEN", "must-not-reach-runner")
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
		{name: "missing run ID", mode: "missing-run-id", want: ErrAdapterInvalid},
		{name: "wrong run ID", mode: "wrong-run-id", want: ErrAdapterInvalid},
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

func TestSubprocessRunnerStopsOutputOverflowBeforeRunDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runner := SubprocessRunner{Argv: []string{"python3", "-c", "import sys,time; sys.stdout.write('x'*2048); sys.stdout.flush(); time.sleep(30)"}, ArtifactDir: t.TempDir(), OutputLimit: 1024}
	started := time.Now()
	_, err := runner.Run(ctx, Packet{RunID: "overflow-run"})
	if !errors.Is(err, ErrOutputTooLarge) || time.Since(started) > time.Second {
		t.Fatalf("overflow must stop before the run deadline: elapsed=%s, err=%v", time.Since(started), err)
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

func TestSubprocessNotifierRequiresMatchingNoticeID(t *testing.T) {
	t.Setenv("GO_WANT_WORKER_ADAPTER", "1")
	packet := NoticePacket{Version: SchemaVersion, NoticeID: "current-notice", CardID: "card-1", RunID: "run-1", Message: "fixture"}
	for _, tc := range []struct {
		name    string
		mode    string
		wantErr bool
		state   string
	}{
		{name: "matching defaults delivered", mode: "notice-match", state: "delivered"},
		{name: "explicit delivered", mode: "notice-delivered", state: "delivered"},
		{name: "queued", mode: "notice-queued", state: "queued"},
		{name: "mismatched", mode: "notice-wrong-id", wantErr: true},
		{name: "invalid state", mode: "notice-bad-state", wantErr: true},
		{name: "empty receipt", mode: "notice-empty-receipt", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt, err := (SubprocessNotifier{Argv: helperArgv(tc.mode), OutputLimit: 4096, Env: []string{"GO_WANT_WORKER_ADAPTER=1"}}).Deliver(context.Background(), packet)
			if tc.wantErr {
				if err == nil {
					t.Fatal("mismatched notice receipt was accepted")
				}
				return
			}
			if err != nil || receipt.NoticeID != packet.NoticeID || receipt.ReceiptID != "notice-receipt-1" || receipt.DeliveryState != tc.state {
				t.Fatalf("receipt = %#v, err = %v", receipt, err)
			}
		})
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

func TestBoundedCommandReapsFailedAdapterChild(t *testing.T) {
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
	resultCh := make(chan error, 1)
	go func() {
		_, _, err := runBoundedCommand(context.Background(), helperArgv("child-failure"), nil, 4096, []string{"GO_WANT_WORKER_ADAPTER=1", "WORKER_READY_SOCKET=" + socketPath})
		resultCh <- err
	}()
	var ready net.Conn
	select {
	case ready = <-acceptCh:
	case <-time.After(2 * time.Second):
		t.Fatal("failed parent did not announce its child")
	}
	announced, err := io.ReadAll(ready)
	_ = ready.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-resultCh; err == nil {
		t.Fatal("failed adapter returned no error")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(announced)))
	if err != nil || pid <= 0 {
		t.Fatalf("child PID %q: %v", announced, err)
	}
	if !waitProcessGoneOrZombie(pid) {
		t.Fatalf("child process %d survived failed adapter cleanup", pid)
	}
}

func TestBoundedCommandReleasesDescriptors(t *testing.T) {
	runtime.GC()
	previousGC := debug.SetGCPercent(-1)
	defer func() {
		debug.SetGCPercent(previousGC)
		runtime.GC()
	}()
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skip("Linux descriptor inventory required")
	}
	for range 50 {
		if _, _, err := runBoundedCommand(context.Background(), []string{"/bin/true"}, nil, 4096, nil); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before)+2 {
		t.Fatalf("50 completed adapters leaked descriptors: before=%d after=%d", len(before), len(after))
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
