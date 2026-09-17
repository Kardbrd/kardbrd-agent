package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestCLIWorkerRunnerHelper(t *testing.T) {
	mode := os.Args[len(os.Args)-1]
	if mode != "fixture" && !strings.HasPrefix(mode, "http://") && !strings.HasPrefix(mode, "https://") {
		return
	}
	var packet struct {
		RunID string `json:"run_id"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&packet); err != nil || packet.RunID == "" {
		os.Exit(2)
	}
	if strings.HasPrefix(mode, "http://") || strings.HasPrefix(mode, "https://") {
		_, _ = http.Post(mode, "application/json", nil)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"run_id": packet.RunID, "status": "completed", "summary": "compiled fixture complete", "receipt_id": "fixture-receipt"})
	os.Exit(0)
}

type cliWorkerServer struct {
	mu              sync.Mutex
	metadata        map[string]json.RawMessage
	revision        int64
	comments        int
	runnerCalls     int
	metadataBarrier chan struct{}
	barrierArrived  int
}

type cliMetadataSnapshot struct {
	metadata map[string]json.RawMessage
	revision int64
}

func newCLIWorkerServer() *cliWorkerServer {
	return &cliWorkerServer{metadata: map[string]json.RawMessage{
		"ops":  json.RawMessage(`{"version":1,"goal":"compiled goal","completion_criteria":"fixture completion","delegation":{"delegated":true,"authorization":{"scope":"fixture"}},"state":"ready"}`),
		"keep": json.RawMessage(`9007199254740993`),
	}}
}

func (s *cliWorkerServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	var barrierSnapshot *cliMetadataSnapshot
	if r.URL.Path == "/api/cards/card-1/metadata/" && r.Method == http.MethodGet && s.metadataBarrier != nil && strings.Contains(string(s.metadata["ops"]), `"state":"ready"`) {
		metadata := make(map[string]json.RawMessage, len(s.metadata))
		for key, value := range s.metadata {
			metadata[key] = append(json.RawMessage(nil), value...)
		}
		barrierSnapshot = &cliMetadataSnapshot{metadata: metadata, revision: s.revision}
		s.barrierArrived++
		gate := s.metadataBarrier
		if s.barrierArrived == 2 {
			close(gate)
		}
		s.mu.Unlock()
		<-gate
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	write := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	switch r.URL.Path {
	case "/runner-count/":
		s.runnerCalls++
		write(http.StatusOK, map[string]bool{"ok": true})
	case "/api/boards/board-1/":
		card := map[string]any{"id": "card-1", "title": "Synthetic", "is_archived": false, "board": map[string]string{"id": "board-1"}}
		list := map[string]any{"cards": []any{card}}
		write(http.StatusOK, map[string]any{"data": map[string]any{"id": "board-1", "lists": []any{list}}})
	case "/api/cards/card-1/":
		if r.Header.Get("Accept") == "text/markdown" {
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("# Synthetic\n"))
			return
		}
		write(http.StatusOK, map[string]any{"data": map[string]any{"id": "card-1", "title": "Synthetic", "is_archived": false, "board": map[string]string{"id": "board-1"}}})
	case "/api/cards/card-1/metadata/":
		if r.Method == http.MethodGet {
			if barrierSnapshot != nil {
				write(http.StatusOK, map[string]any{"data": map[string]any{"id": "card-1", "metadata": barrierSnapshot.metadata, "metadata_revision": barrierSnapshot.revision}})
				return
			}
			write(http.StatusOK, map[string]any{"data": map[string]any{"id": "card-1", "metadata": s.metadata, "metadata_revision": s.revision}})
			return
		}
		var patch struct {
			Set      map[string]json.RawMessage `json:"set"`
			Expected int64                      `json:"expected_revision"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil || patch.Expected != s.revision {
			write(http.StatusConflict, map[string]any{"error": "metadata changed", "code": "METADATA_CONFLICT"})
			return
		}
		for key, value := range patch.Set {
			s.metadata[key] = value
		}
		s.revision++
		write(http.StatusOK, map[string]any{"data": map[string]any{"id": "card-1", "metadata": s.metadata, "metadata_revision": s.revision}})
	case "/api/cards/card-1/comments/":
		s.comments++
		write(http.StatusOK, map[string]any{"data": map[string]string{"id": "comment-1"}})
	default:
		write(http.StatusNotFound, map[string]string{"error": "unexpected " + r.URL.Path})
	}
}

func TestWorkerRunOnceCompiledCLIHTTPAndSubprocess(t *testing.T) {
	serverState := newCLIWorkerServer()
	server := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer server.Close()
	bin := filepath.Join(t.TempDir(), "kardbrd")
	root := filepath.Join("..", "..")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", bin, "./cmd/kardbrd")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	artifacts := filepath.Join(t.TempDir(), "artifacts")
	command := exec.Command(bin,
		"worker", "run-once",
		"--board-id", "board-1",
		"--worker-id", "compiled-test",
		"--runner", os.Args[0],
		"--runner-arg=-test.run=TestCLIWorkerRunnerHelper",
		"--runner-arg=--",
		"--runner-arg=fixture",
		"--artifact-dir", artifacts,
	)
	command.Dir = t.TempDir() // the worker must not require a Git checkout
	command.Env = append(os.Environ(), "KARDBRD_TOKEN=cli-test-token", "KARDBRD_API_URL="+server.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("compiled worker: %v\n%s", err, output)
	}
	if !bytes.Contains(output, []byte(`"claimed": 1`)) {
		t.Fatalf("worker output: %s", output)
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if got := string(serverState.metadata["keep"]); got != "9007199254740993" {
		t.Fatalf("unrelated metadata changed: %s", got)
	}
	if !strings.Contains(string(serverState.metadata["ops"]), `"state":"completed"`) {
		t.Fatalf("ops state: %s", serverState.metadata["ops"])
	}
	if serverState.comments != 1 {
		t.Fatalf("comments = %d", serverState.comments)
	}
}

func TestWorkerCrossProcessContentionUsesOneRunner(t *testing.T) {
	serverState := newCLIWorkerServer()
	serverState.metadataBarrier = make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(serverState.serveHTTP))
	defer server.Close()
	bin := filepath.Join(t.TempDir(), "kardbrd")
	build := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", bin, "./cmd/kardbrd")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	newCommand := func(workerID string) *exec.Cmd {
		command := exec.Command(bin,
			"worker", "run-once", "--board-id", "board-1", "--worker-id", workerID,
			"--runner", os.Args[0], "--runner-arg=-test.run=TestCLIWorkerRunnerHelper", "--runner-arg=--", "--runner-arg="+server.URL+"/runner-count/",
			"--artifact-dir", filepath.Join(t.TempDir(), workerID),
		)
		command.Dir = t.TempDir()
		command.Env = append(os.Environ(), "KARDBRD_TOKEN=cli-test-token", "KARDBRD_API_URL="+server.URL)
		return command
	}
	first, second := newCommand("worker-a"), newCommand("worker-b")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first worker: %v", err)
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("second worker: %v", err)
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.runnerCalls != 1 {
		t.Fatalf("runner calls across independent CLI processes = %d, want 1", serverState.runnerCalls)
	}
	if !strings.Contains(string(serverState.metadata["ops"]), `"state":"completed"`) {
		t.Fatalf("state = %s", serverState.metadata["ops"])
	}
}

func TestWorkerReadOnlyCheckAndExecutionValidation(t *testing.T) {
	t.Setenv("KARDBRD_TOKEN", "tok")
	if _, _, err := executeRoot("worker", "check"); err == nil || !strings.Contains(err.Error(), "--board-id") {
		t.Fatalf("missing board error = %v", err)
	}
	if _, _, err := executeRoot("worker", "run-once", "--board-id", "board", "--worker-id", "worker"); err == nil || !strings.Contains(err.Error(), "--runner") {
		t.Fatalf("missing runner error = %v", err)
	}
}

func TestWorkerRunOnceRejectsUnusedObserverConfiguration(t *testing.T) {
	t.Setenv("KARDBRD_TOKEN", "tok")
	if _, _, err := executeRoot("worker", "run-once", "--board-id", "board", "--worker-id", "worker", "--runner", "/trusted/runner", "--observer", "/trusted/observer"); err == nil || !strings.Contains(err.Error(), "only used with worker serve or ingest") {
		t.Fatalf("unused observer error = %v", err)
	}
}
