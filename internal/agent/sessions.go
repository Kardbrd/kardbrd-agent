package agent

import (
	"context"
	"os/exec"
	"sync"

	"github.com/Kardbrd/kardbrd-agent/internal/api"
)

type ActiveSession struct {
	CardID       string
	Context      context.Context
	WorktreePath string
	CommentID    string
	Process      *exec.Cmd
	Cancel       context.CancelFunc
	SessionID    string
	Stream       api.StreamConn
	Streaming    bool
	Cleanup      bool
	Stopping     bool
	Done         chan struct{}
	doneOnce     sync.Once
}

func (s *ActiveSession) markDone() {
	if s == nil || s.Done == nil {
		return
	}
	s.doneOnce.Do(func() { close(s.Done) })
}

func stopSessionProcess(session *ActiveSession) {
	if session == nil || session.Process == nil || session.Process.Process == nil {
		return
	}
	if session.Cleanup {
		killCleanupProcessGroup(session.Process)
		return
	}
	_ = session.Process.Process.Kill()
}
