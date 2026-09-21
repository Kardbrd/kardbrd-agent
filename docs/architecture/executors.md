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

### Codex JSONL compatibility

The Codex adapter supports the current `codex exec --json` event stream: it records
`thread.started.thread_id`, treats nested completed `item.type=agent_message` records as
the JSONL compatibility fallback, and fails on `turn.failed`, top-level `error`, malformed
JSONL, or a nonzero process exit. The documented phase-less completed-agent-message shape is
accepted; explicit commentary-phase messages are progress, not a fallback terminal summary.
Older top-level `item.message` and `response.message` payloads remain supported for legacy
CLI fixtures.

For real Codex subprocesses, JSONL is not the terminal-summary source. Each invocation passes
the CLI a unique private `--output-last-message` path and uses that bounded final-only file for
`ResultText` after successful process completion. The private directory and file are removed on
success, failure, timeout, or cancellation. A missing or oversized output file is an adapter
failure; a present empty file remains an empty terminal result, so the manager can show its
existing bounded recovery outcome rather than publishing a false success.

Nested assistant messages continue to stream as progress, with repeated item snapshots
suppressed. Reasoning, command execution, and raw tool payloads are never forwarded as Codex
assistant chunks. If the manager needs empty-result recovery and the adapter supplied a thread
ID, Codex uses `codex exec resume <SESSION_ID>` with the original CWD and prompt on stdin; it
never silently starts a new `codex exec` task. The manager still owns the only terminal card
publication and never resumes after receiving a non-empty final result.
