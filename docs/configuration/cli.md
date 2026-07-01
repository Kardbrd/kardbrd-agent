# CLI Reference

`kardbrd` is one binary with two surfaces:

- `kardbrd agent ...` runs and validates the automation agent.
- Every other `kardbrd ...` command is the Kardbrd client CLI.

## Agent

```bash
kardbrd agent start [OPTIONS]
kardbrd agent validate [kardbrd.yml]
```

### `agent start` options

| Flag | Env var | Default | Description |
| --- | --- | --- | --- |
| `--board-id` | `KARDBRD_AGENT_BOARD_ID` | required | Board ID |
| `--token` | `KARDBRD_TOKEN` | required | Bot token |
| `--name` | `KARDBRD_AGENT_NAME` | required | Agent name for @mentions |
| `--api-url` | `KARDBRD_API_URL` | `https://app.kardbrd.com` | API base URL |
| `--executor` | `KARDBRD_AGENT_EXECUTOR` | `claude` | `claude`, `goose`, `codex`, or `pi` |
| `--cwd` | `KARDBRD_AGENT_CWD` | current directory | Target repository |
| `--timeout` | `KARDBRD_AGENT_TIMEOUT` | `3600` | Max seconds per session |
| `--max-concurrent` | `KARDBRD_AGENT_MAX_CONCURRENT` | `3` | Max parallel sessions |
| `--worktrees-dir` | `KARDBRD_AGENT_WORKTREES_DIR` | parent of cwd | Worktree parent directory |
| `--setup-cmd` | `KARDBRD_AGENT_SETUP_CMD` | none | Command run in each worktree |
| `--rules` | `KARDBRD_AGENT_RULES_FILE` | `<cwd>/kardbrd.yml` | Rules file |

Example:

```bash
kardbrd --token tok_xxx agent start \
  --board-id 0gl5MlBZ \
  --name MyBot \
  --cwd /path/to/repo \
  --executor codex
```

## Client Commands

```bash
kardbrd board ...
kardbrd card ...
kardbrd comment ...
kardbrd checklist ...
kardbrd attachment ...
kardbrd link ...
kardbrd list ...
kardbrd md ...
kardbrd search ...
kardbrd activity ...
```

Use `--format json` or `--format md` where supported.

```bash
kardbrd board list
kardbrd md card <card-id>
kardbrd comment add <card-id> "Done. @alice"
```

## Legacy Env Names

Old agent env names are rejected with explicit rename messages:

| Old | New |
| --- | --- |
| `KARDBRD_ID` | `KARDBRD_AGENT_BOARD_ID` |
| `KARDBRD_AGENT` | `KARDBRD_AGENT_NAME` |
| `KARDBRD_URL` | `KARDBRD_API_URL` |
| `AGENT_*` | `KARDBRD_AGENT_*` |
