package executor

import (
	"context"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/prompt"
)

type Config struct {
	CWD     string
	Timeout time.Duration
	APIURL  string
	Token   string
}

type Request struct {
	CardID          string
	BoardID         string
	Prompt          string
	ResumeSessionID string
	CWD             string
	Model           string
	OnChunk         func(content string, chunkType string)
}

type PromptRequest = prompt.Request

type Result struct {
	Success    bool
	ResultText string
	Error      string
	CostUSD    *float64
	DurationMS *int64
	SessionID  string
	ReturnCode *int
	Stderr     string
	Command    []string
	Logs       string
}

type AuthStatus struct {
	Authenticated    bool
	Error            string
	Email            string
	AuthMethod       string
	SubscriptionType string
	AuthHint         string
}

type Interface interface {
	CheckAuth(ctx context.Context) AuthStatus
	Execute(ctx context.Context, req Request) Result
	BuildPrompt(req PromptRequest) string
	ExtractCommand(commentContent string, mentionKeyword string) string
}

type base struct {
	cfg Config
}

func (b base) cwd(req Request) string {
	if req.CWD != "" {
		return req.CWD
	}
	return b.cfg.CWD
}

func (b base) timeout() time.Duration {
	if b.cfg.Timeout > 0 {
		return b.cfg.Timeout
	}
	return time.Hour
}

func (b base) BuildPrompt(req PromptRequest) string {
	return prompt.Build(req)
}

func (b base) ExtractCommand(commentContent string, mentionKeyword string) string {
	return prompt.ExtractCommand(commentContent, mentionKeyword)
}
