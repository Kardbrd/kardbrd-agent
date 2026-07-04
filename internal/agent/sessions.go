package agent

import (
	"context"
	"os/exec"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type ActiveSession struct {
	CardID       string
	WorktreePath string
	CommentID    string
	Process      *exec.Cmd
	Cancel       context.CancelFunc
	SessionID    string
	Stream       api.StreamConn
	Streaming    bool
}
