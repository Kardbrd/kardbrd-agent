package agent

import (
	"os/exec"

	"github.com/gorilla/websocket"
)

type ActiveSession struct {
	CardID       string
	WorktreePath string
	CommentID    string
	Process      *exec.Cmd
	SessionID    string
	Stream       *websocket.Conn
	Streaming    bool
}
