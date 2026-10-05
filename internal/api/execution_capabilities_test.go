package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterExecutionCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bots/execution-capabilities/" || r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("request = %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		var request ExecutionCapabilities
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.InstanceID != "instance-1" || request.Defaults.Model == nil || *request.Defaults.Model != "verified-model" {
			t.Errorf("body = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"revision":"revision-1","published_at":"2026-10-05T16:00:00Z","expires_at":"2026-10-05T16:03:00Z"}}`))
	}))
	defer server.Close()
	model := "verified-model"
	client := NewClient(server.URL, "tok")
	result, err := client.RegisterExecutionCapabilities(context.Background(), ExecutionCapabilities{Version: 1, InstanceID: "instance-1", BoardID: "board-1", Executor: "codex", Defaults: ExecutionDefaults{Model: &model}})
	if err != nil || result.Revision != "revision-1" {
		t.Fatalf("registration = %+v, %v", result, err)
	}
}

func TestGetExecutionCapabilitiesUsesBoardScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bots/bot-1/execution-capabilities/" || r.URL.Query().Get("board_id") != "board-1" {
			t.Errorf("URL = %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"data":{"revision":"rev-1","expires_at":"2026-10-05T16:03:00Z"}}`))
	}))
	defer server.Close()
	view, err := NewClient(server.URL, "tok").GetExecutionCapabilities(context.Background(), "bot-1", "board-1")
	if err != nil || view.Revision != "rev-1" {
		t.Fatalf("view = %+v, %v", view, err)
	}
}
