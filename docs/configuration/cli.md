# CLI Reference

kardbrd-agent provides two commands: `start` and `validate`.

## `kardbrd-agent start`

Start listening for @mentions and rule-based events.

```bash
kardbrd-agent start [OPTIONS]
```

### Options

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--board-id` | `KARDBRD_ID` | — | Board ID (required) |
| `--token` | `KARDBRD_TOKEN` | — | Bot authentication token (required) |
| `--name` | `KARDBRD_AGENT` | — | Agent name for @mentions (required) |
| `--api-url` | `KARDBRD_URL` | `https://app.kardbrd.com` | API base URL |
| `--executor` | `AGENT_EXECUTOR` | `claude` | Executor type: `claude`, `goose`, or `codex` |
| `--cwd` | `AGENT_CWD` | current directory | Working directory (your project repo) |
| `--timeout` | `AGENT_TIMEOUT` | `3600` | Max execution time per session (seconds) |
| `--max-concurrent` | `AGENT_MAX_CONCURRENT` | `3` | Max parallel sessions |
| `--worktrees-dir` | `AGENT_WORKTREES_DIR` | parent of `--cwd` | Where worktrees are created |
| `--setup-cmd` | `AGENT_SETUP_CMD` | — | Command to run in each worktree after creation (e.g., `npm install`) |
| `--rules` | `AGENT_RULES_FILE` | `<cwd>/kardbrd.yml` | Path to rules file |

### Examples

```bash
# Minimal (using env vars)
export KARDBRD_ID=0gl5MlBZ KARDBRD_TOKEN=tok_xxx KARDBRD_AGENT=MyBot
kardbrd-agent start

# Full flags
kardbrd-agent start \
  --board-id 0gl5MlBZ \
  --token tok_xxx \
  --name MyBot \
  --executor goose \
  --cwd /path/to/repo \
  --timeout 7200 \
  --max-concurrent 5 \
  --setup-cmd 'pnpm install'

# With uvx (no clone)
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd /path/to/repo
```

## `kardbrd-agent validate`

Validate a `kardbrd.yml` rules file for syntax errors and configuration issues.

```bash
kardbrd-agent validate [PATH]
```

### Arguments

| Argument | Default | Description |
|----------|---------|-------------|
| `PATH` | `./kardbrd.yml` | Path to the rules file to validate |

### Examples

```bash
# Validate default location
kardbrd-agent validate

# Validate specific file
kardbrd-agent validate path/to/kardbrd.yml
```

### Output

The validator checks for:

- **Errors** — invalid YAML, missing required fields, unknown event types, invalid cron expressions
- **Warnings** — unused conditions, potential misconfigurations

```
$ kardbrd-agent validate
✓ kardbrd.yml is valid (5 rules, 2 schedules, 0 errors, 0 warnings)
```

```
$ kardbrd-agent validate bad-config.yml
✗ kardbrd.yml has errors:
  ERROR [rule 2 "Deploy"]: Unknown event type 'card_deleted'
  WARNING [rule 3 "Review"]: 'emoji' condition has no effect on 'card_created' events
```
