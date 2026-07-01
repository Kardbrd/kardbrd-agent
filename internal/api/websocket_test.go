package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBuildAgentWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "https",
			baseURL: "https://app.kardbrd.com",
			want:    "wss://app.kardbrd.com/ws/agent/?token=tok",
		},
		{
			name:    "http",
			baseURL: "http://localhost:8000",
			want:    "ws://localhost:8000/ws/agent/?token=tok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildAgentWebSocketURL(tt.baseURL, "tok")
			if err != nil {
				t.Fatal(err)
			}
			assertEqual(t, tt.want, got)
		})
	}
}

func TestAgentWebSocketReadsEventsAndSendsStatus(t *testing.T) {
	upgrader := websocket.Upgrader{}
	statusCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "/ws/agent/", r.URL.Path)
		assertEqual(t, "tok", r.URL.Query().Get("token"))

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		messages := []map[string]any{
			{"type": "connected", "agent_id": "a1", "subscribed_boards": []string{"b1"}},
			{"type": "board_event", "event_type": "comment_created", "card_id": "card1"},
			{"type": "pong"},
			{"type": "error", "error": "bad subscription"},
		}
		for _, msg := range messages {
			if err := conn.WriteJSON(msg); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}

		var status map[string]any
		if err := conn.ReadJSON(&status); err != nil {
			t.Errorf("read status: %v", err)
			return
		}
		statusCh <- status
	}))
	defer server.Close()

	client := NewWebSocketClient(server.URL, "tok")
	connectedCh := make(chan ConnectedMessage, 1)
	boardCh := make(chan json.RawMessage, 1)
	serverErrCh := make(chan string, 1)
	client.OnConnected = func(msg ConnectedMessage) { connectedCh <- msg }
	client.OnBoardEvent = func(raw json.RawMessage) { boardCh <- raw }
	client.OnError = func(msg string) { serverErrCh <- msg }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readDone := make(chan error, 1)
	go func() { readDone <- client.ReadLoop(ctx, conn) }()

	connected := receive(t, connectedCh)
	assertEqual(t, "a1", connected.AgentID)
	assertEqual(t, "b1", connected.SubscribedBoards[0])

	boardRaw := receive(t, boardCh)
	var boardEvent map[string]string
	if err := json.Unmarshal(boardRaw, &boardEvent); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "comment_created", boardEvent["event_type"])
	assertEqual(t, "card1", boardEvent["card_id"])

	serverErr := receive(t, serverErrCh)
	assertContains(t, serverErr, "bad subscription")

	if err := client.SendStatusPing(ctx, map[string]string{"board_id": "b1"}, []string{"card1"}); err != nil {
		t.Fatal(err)
	}

	status := receive(t, statusCh)
	assertEqual(t, "status_ping", status["type"].(string))
	assertEqual(t, float64(1), status["active_card_count"].(float64))
	activeCards := status["active_cards"].([]any)
	assertEqual(t, "card1", activeCards[0].(string))
	subscription := status["subscription"].(map[string]any)
	assertEqual(t, "b1", subscription["board_id"].(string))

	cancel()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("read loop did not stop")
	}
}

func TestStreamWebSocketSendsChunk(t *testing.T) {
	upgrader := websocket.Upgrader{}
	chunkCh := make(chan map[string]any, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()

		var chunk map[string]any
		if err := conn.ReadJSON(&chunk); err != nil {
			t.Errorf("read chunk: %v", err)
			return
		}
		chunkCh <- chunk
	}))
	defer server.Close()

	streamURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := ConnectStream(ctx, streamURL)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := SendStreamChunk(ctx, conn, "card1", "chunk", "assistant", 0); err != nil {
		t.Fatal(err)
	}

	chunk := receive(t, chunkCh)
	assertEqual(t, "stream_chunk", chunk["type"].(string))
	assertEqual(t, "card1", chunk["card_id"].(string))
	assertEqual(t, "chunk", chunk["text"].(string))
	assertEqual(t, "assistant", chunk["chunk_type"].(string))
	assertEqual(t, float64(0), chunk["sequence"].(float64))
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(2 * time.Second):
		var zero T
		t.Fatal("timed out waiting for channel")
		return zero
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}
