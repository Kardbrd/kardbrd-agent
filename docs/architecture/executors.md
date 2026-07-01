# Executors

Executors are subprocess adapters in `internal/executor`.

## Interface

```go
type Interface interface {
    CheckAuth(ctx context.Context) AuthStatus
    Execute(ctx context.Context, req Request) Result
    BuildPrompt(req PromptRequest) string
    ExtractCommand(commentContent string, mentionKeyword string) string
}
```

## Supported Executors

| Executor | Command |
| --- | --- |
| Claude | `claude -p - --output-format=stream-json --verbose --dangerously-skip-permissions` |
| Codex | `codex exec --dangerously-bypass-approvals-and-sandbox --json` |
| Goose | `goose run -t - --output-format stream-json --no-session` |
| Pi | `pi --mode json -p - --no-session -a` |

The selected executor comes from `--executor`, `KARDBRD_AGENT_EXECUTOR`, or the `executor` field in `kardbrd.yml`.

Executor subprocesses receive `KARDBRD_TOKEN` and `KARDBRD_API_URL` so prompts can call `kardbrd ...` commands.
