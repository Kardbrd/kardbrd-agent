package cli

import (
	"context"
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
		_, _ = w.Write([]byte(`{"data":{"revision":"rev-1","expires_at":"2026-10-05T23:00:00Z"}}`))
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
