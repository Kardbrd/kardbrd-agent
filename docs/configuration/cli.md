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
kardbrd self-update
```

### Output formats

Row-oriented reads default to TSV with a stable header row. The following commands support `--format tsv`, `--format json`, and `--format md`:

- `board list`, `board members`, `board labels`, `board search`, and `board activity`
- `attachment list` and `link list`
- `search`, `card activity`, and `activity`

TSV uses tab-delimited CSV encoding, so tabs, quotes, and newlines inside values remain safe. Use `--no-headers` to suppress the TSV header row. `--no-headers` affects TSV only.

Use `--format json` for the existing lossless, indented JSON response shape. Use `--format md` for a Markdown table using the same columns as TSV. Repeated values such as `match_locations` are compact JSON inside a table cell.

Detail commands continue to default to JSON; `board get` and `card get` also support `--format md`. Mutations and delete confirmations support JSON only, while `md` is always Markdown. Passing a known but unsupported format fails instead of being ignored. Output formats apply only to client commands: `agent` commands do not support `--format`, and reject it when supplied.

```bash
kardbrd board list
kardbrd --no-headers board list
kardbrd --format json board list
kardbrd board search <board-id> "auth"
kardbrd md card <card-id>
kardbrd comment add <card-id> "Done. @alice"
kardbrd self-update
```

`self-update` downloads the latest published archive for the current Linux or
macOS `amd64`/`arm64` platform, verifies its checksum, and atomically replaces
the running executable.

### One-attempt client mode

Add the global `--no-retry` flag when a write must not be retried by the CLI:

```bash
kardbrd --no-retry card create --board BOARD_ID --list LIST_ID --title "Control card"
```

In this mode every client operation, including reads, JSON writes, attachment
presign/confirm requests, and presigned attachment uploads, makes at most one
HTTP attempt. Redirects are not followed, so an authenticated mutation is not
replayed at a redirect target. A timeout, connection loss, or server error
returns a nonzero result without retrying. This is an **at-most-one attempt**
mode, not an exactly-once delivery guarantee: the server may have committed a
write before the client sees a failure, so reconcile state before manually
retrying.

### Labels

Discover the labels available to a board from its board-detail response:

```bash
kardbrd board labels BOARD_ID
kardbrd --format md board labels BOARD_ID
```

JSON output is the label collection itself. Markdown output is a label list
rendered from that same board-detail catalog, including each label ID.

`card update --label` and `--label-ids` are repeatable aliases. They replace
the complete desired label set, rather than adding labels to the existing set:

```bash
# Keep LABEL_A and LABEL_B, removing every other label from the card.
kardbrd card update CARD_ID --label-ids LABEL_A --label-ids LABEL_B

# Remove every label explicitly.
kardbrd card update CARD_ID --clear-labels
```

The CLI de-duplicates requested IDs, validates every requested label against
the card's board before mutation, adds missing labels before removing obsolete
ones, and does nothing for an already-matching set. `--clear-labels` cannot be
combined with `--label` or `--label-ids`.

Scalar card fields and labels use separate server endpoints. A command that
updates both is therefore not database-atomic: scalar changes can succeed
before label reconciliation fails. The command exits non-zero with a clear
reconciliation error; retrying the same complete desired set converges without
duplicate effects.

## Legacy Env Names

Old agent env names are rejected with explicit rename messages:

| Old | New |
| --- | --- |
| `KARDBRD_ID` | `KARDBRD_AGENT_BOARD_ID` |
| `KARDBRD_AGENT` | `KARDBRD_AGENT_NAME` |
| `KARDBRD_URL` | `KARDBRD_API_URL` |
| `AGENT_*` | `KARDBRD_AGENT_*` |
