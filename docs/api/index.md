# API Reference

The Go code is organized into internal packages. Use `go doc` for symbol-level details:

```bash
go doc ./internal/api
go doc ./internal/agent
go doc ./internal/executor
go doc ./internal/rules
```

## Packages

| Package | Purpose |
| --- | --- |
| `internal/api` | Kardbrd HTTP and WebSocket clients |
| `internal/agent` | Agent manager, bot card, wizard, events, sessions |
| `internal/cli` | Cobra command tree and output formatting |
| `internal/config` | Environment and flag config loading |
| `internal/executor` | Claude, Codex, Goose, and Pi subprocess adapters |
| `internal/prompt` | Prompt construction and local knowledge loading |
| `internal/rules` | `kardbrd.yml` loading, validation, and matching |
| `internal/scheduler` | Cron schedule dispatch |
| `internal/worktree` | Git worktree management |
