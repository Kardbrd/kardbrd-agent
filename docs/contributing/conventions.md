# Code Conventions

## Go Style

- Run `gofmt` on edited Go files.
- Keep package APIs small and explicit.
- Pass `context.Context` through network, subprocess, and long-running operations.
- Prefer table tests for repeated cases.
- Use fakes at external boundaries.

## Architecture Guards

- `kardbrd.yml` drives automation rules.
- Root client commands and agent code share the same API client.
- Executor subprocesses receive `KARDBRD_TOKEN` and `KARDBRD_API_URL`.
- Worktrees are disposable and should not hold persistent state.
- Do not reintroduce MCP server behavior.

## Security

- Never print tokens or provider keys.
- Keep auth errors actionable without leaking credentials.
- Validate file paths used for worktree setup.
- Avoid shell execution except for the explicit user-provided setup command.

## Git Workflow

- Keep commits focused.
- Do not revert unrelated user changes.
- Prefer card-scoped branches for agent-created work.
