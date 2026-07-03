package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type ConnectedMessage struct {
	Type             string   `json:"type"`
	AgentID          string   `json:"agent_id"`
	SubscribedBoards []string `json:"subscribed_boards"`
}

type StatusPingPayload struct {
	Type            string   `json:"type"`
	Subscription    any      `json:"subscription"`
	ActiveCards     []string `json:"active_cards"`
	ActiveCardCount int      `json:"active_card_count"`
}

type WebSocketClient struct {
	BaseURL string
	Token   string
	Dialer  *websocket.Dialer
	Conn    *websocket.Conn

	OnConnected  func(ConnectedMessage)
	OnBoardEvent func(json.RawMessage)
	OnError      func(string)
}

type StreamConn interface {
	WriteJSON(v any) error
	SetWriteDeadline(t time.Time) error
	Close() error
}

func NewWebSocketClient(baseURL string, token string) *WebSocketClient {
	return &WebSocketClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		Dialer:  websocket.DefaultDialer,
	}
}

func BuildAgentWebSocketURL(baseURL string, token string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported websocket base URL scheme %q", parsed.Scheme)
	}
	parsed.Path = "/ws/agent/"
	parsed.RawPath = ""
	parsed.RawQuery = url.Values{"token": {token}}.Encode()
	return parsed.String(), nil
}

func (c *WebSocketClient) Connect(ctx context.Context) (*websocket.Conn, error) {
	wsURL, err := BuildAgentWebSocketURL(c.BaseURL, c.Token)
	if err != nil {
		return nil, err
	}
	dialer := c.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, err
	}
	c.Conn = conn
	return conn, nil
}

func (c *WebSocketClient) ReadLoop(ctx context.Context, conn *websocket.Conn) error {
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := c.handleMessage(data); err != nil && c.OnError != nil {
			c.OnError(err.Error())
		}
	}
}

func (c *WebSocketClient) Run(ctx context.Context) error {
	delay := time.Second
	const maxDelay = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return nil
		}
		conn, err := c.Connect(ctx)
		if err == nil {
			delay = time.Second
			err = c.ReadLoop(ctx, conn)
			_ = conn.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		if err != nil && c.OnError != nil {
			c.OnError(err.Error())
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func (c *WebSocketClient) SendStatusPing(ctx context.Context, subscription any, activeCards []string) error {
	if c.Conn == nil {
		return errors.New("websocket is not connected")
	}
	payload := StatusPingPayload{
		Type:            "status_ping",
		Subscription:    subscription,
		ActiveCards:     activeCards,
		ActiveCardCount: len(activeCards),
	}
	return writeWebSocketJSON(ctx, c.Conn, payload)
}

func ConnectStream(ctx context.Context, streamURL string) (*websocket.Conn, error) {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, streamURL, nil)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func SendStreamChunk(ctx context.Context, conn StreamConn, cardID, text, chunkType string, sequence int) error {
	payload := map[string]any{
		"type":       "stream_chunk",
		"card_id":    cardID,
		"text":       text,
		"chunk_type": chunkType,
		"sequence":   sequence,
	}
	return writeWebSocketJSON(ctx, conn, payload)
}

func (c *WebSocketClient) handleMessage(data []byte) error {
	var envelope struct {
		Type  string          `json:"type"`
		Error string          `json:"error"`
		Raw   json.RawMessage `json:"-"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	envelope.Raw = append([]byte(nil), data...)

	switch envelope.Type {
	case "connected":
		if c.OnConnected != nil {
			var msg ConnectedMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				return err
			}
			c.OnConnected(msg)
		}
	case "board_event":
		if c.OnBoardEvent != nil {
			c.OnBoardEvent(envelope.Raw)
		}
	case "pong":
	case "error":
		if c.OnError != nil {
			message := envelope.Error
			if message == "" {
				message = string(data)
			}
			c.OnError(message)
		}
	default:
	}
	return nil
}

func writeWebSocketJSON(ctx context.Context, conn StreamConn, payload any) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer conn.SetWriteDeadline(time.Time{})
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return conn.WriteJSON(payload)
}
