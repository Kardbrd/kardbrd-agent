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

## Progress and terminal summaries

Executors may add comments, attachments, links, reactions, and other card updates while
they work. Those updates are progress only: they do not end a run and are never used as
proof of completion.

Previously the manager inferred completion from a recent bot comment, which could mistake
progress for a final summary. Now it uses the executor's returned final assistant text.

For a normal successful run, the executor returns its final assistant text and the agent
manager publishes that text once as the terminal card summary, then adds the success
reaction. If a same-card mention arrives while a session is active, the manager acknowledges
it with 👀, coalesces it with any pending follow-up, and runs it after the active session
clears. The success reaction is never added until the terminal-comment request succeeds.
If that request fails, the manager reports the failure without retrying the non-idempotent
summary post or marking the run successful. Only an empty final response can use the bounded
session-resume recovery path; empty or failed recovery remains visibly non-successful.
