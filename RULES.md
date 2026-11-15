# Rules

## Code Conventions

- Keep changes scoped to the requested behavior.
- Prefer existing package boundaries over new abstractions.
- Use `context.Context` for network, subprocess, and long-running operations.
- Keep API payloads typed where practical; use `json.RawMessage` at command boundaries.
- Do not log or print tokens, API keys, or executor credentials.

## Testing

```bash
go test ./...
go test ./internal/agent
go test ./internal/cli
```

In constrained local environments:

```bash
GOCACHE=/private/tmp/kardbrd-go-build-cache \
GOMODCACHE=/private/tmp/kardbrd-go-mod-cache \
/private/tmp/go/bin/go test ./...
```

## Git Workflow

- Work on card-scoped branches when possible.
- Do not revert unrelated user changes.
- Keep commits focused on one logical change.

## Architecture Guards

- `kardbrd.yml` is the declarative source of automation rules.
- Board operations go through the Kardbrd API/client, not MCP.
- Worktrees are ephemeral; persistent state belongs in Kardbrd or the base repo.
- Executor subprocesses receive `KARDBRD_TOKEN` and `KARDBRD_API_URL`.

## Bot comment writing

Keep each published bot comment within 250 words of the raw Web-stored body after outer trim, including Markdown and any appended requester mention. Preserve the decision, reasoning, evidence takeaway, blockers and unresolved questions. Link detailed logs or reports with a useful pointer. Write a short, natural summary instead of repeating the same status across comments. Preserve every prior card comment in its original order when building context. The manager owns terminal publication; return the final response normally and do not also post it with `kardbrd comment add`.
