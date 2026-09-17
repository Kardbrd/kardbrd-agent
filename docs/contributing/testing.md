# Testing

## Run Tests

```bash
go test ./...
go test ./internal/agent
go test ./internal/cli
go test ./internal/api
go test ./internal/worker
```

Some API and CLI tests bind local `httptest` servers.

## Coverage Areas

| Package | Coverage |
| --- | --- |
| `internal/api` | HTTP requests, markdown, attachments, WebSockets |
| `internal/agent` | event routing, rules, bot card, wizard |
| `internal/cli` | command tree, output, config diagnostics |
| `internal/executor` | subprocess command construction and stream parsing |
| `internal/rules` | loading, validation, matching |
| `internal/scheduler` | cron and schedule cards |
| `internal/worktree` | branch naming, worktree commands, symlinks |
| `internal/worker` | durable metadata claims, leases, receipts, subprocess bounds, and suggestion isolation |

## Writing Tests

- Use fakes for network, git, and executor boundaries.
- Keep tests package-local when they need unexported helpers.
- Use `httptest` for API and WebSocket behavior.
- Keep fixtures under `testdata/`.
- Worker tests must use synthetic adapters and local HTTP servers; never use personal inboxes, calendars, or provider credentials.
