package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/agent"
	"github.com/Kardbrd/kardbrd-agent/internal/api"
	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

func TestPublishExecutionCapabilitiesSetsRevision(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"revision": "rev-1", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}})
	}))
	defer server.Close()
	m := agent.NewManager(agent.Config{BoardID: "board1", ExecutorType: "codex", CommentExecution: &rules.CommentExecutionConfig{Models: []rules.CommentModel{{ID: "verified-model", Efforts: []string{"high"}}}, Verification: rules.CommentVerification{Source: "operator_probe", VerifiedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), ExecutorVersion: "codex 0.156.0"}}})
	m.SetConnectedBot("bot-1", "socket-1")
	if err := publishExecutionCapabilities(context.Background(), m, api.NewClient(server.URL, "tok")); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || m.CurrentCapabilityRevision() != "rev-1" {
		t.Fatalf("calls=%d revision=%q", calls, m.CurrentCapabilityRevision())
	}
}

func TestOrderPendingCoalescesBoundedFromStartReplay(t *testing.T) {
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":{"requests":[],"next_after":null}}`))
	}))
	defer server.Close()
	m := agent.NewManager(agent.Config{BoardID: "board1", ClaimDir: t.TempDir(), Client: api.NewClient(server.URL, "tok")})
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	r := &capabilityRegistrationLoop{manager: m, orderRetryDelay: 10 * time.Millisecond, orderRetryCh: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.orderReplayLoop(ctx, r.orderRetryCh)
	r.orderPending()
	r.orderPending()
	select {
	case query := <-requests:
		if query != "board_id=board1" {
			t.Fatalf("replay query=%q, want from-start board scan", query)
		}
	case <-time.After(time.Second):
		t.Fatal("order hold did not trigger replay")
	}
	select {
	case query := <-requests:
		t.Fatalf("coalesced hold triggered a second immediate scan: %s", query)
	case <-time.After(50 * time.Millisecond):
	}
	r.orderPending()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("later hold did not retry")
	}
}

func TestOrderPendingRetriesBackOffWhileHeld(t *testing.T) {
	times := make(chan time.Time, 3)
	var loop *capabilityRegistrationLoop
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		times <- time.Now()
		if count < 3 {
			loop.orderPending()
		}
		_, _ = w.Write([]byte(`{"data":{"requests":[],"next_after":null}}`))
	}))
	defer server.Close()
	m := agent.NewManager(agent.Config{BoardID: "board1", ClaimDir: t.TempDir(), Client: api.NewClient(server.URL, "tok")})
	m.SetConnectedBot("bot-1", "socket-1")
	m.SetCapabilityRevision("rev-1")
	loop = &capabilityRegistrationLoop{manager: m, orderRetryDelay: 20 * time.Millisecond, orderRetryCh: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go loop.orderReplayLoop(ctx, loop.orderRetryCh)
	loop.orderPending()
	var observed [3]time.Time
	for i := range observed {
		select {
		case observed[i] = <-times:
		case <-time.After(time.Second):
			t.Fatalf("replay %d did not occur", i+1)
		}
	}
	if observed[1].Sub(observed[0]) < 35*time.Millisecond || observed[2].Sub(observed[1]) < 70*time.Millisecond {
		t.Fatalf("retries lacked backoff: intervals %s and %s", observed[1].Sub(observed[0]), observed[2].Sub(observed[1]))
	}
}
