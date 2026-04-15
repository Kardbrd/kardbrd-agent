# Worktree Management

Each card gets its own isolated git worktree, preventing conflicts between concurrent agent sessions. The `WorktreeManager` handles creation, configuration, and cleanup of worktrees.

## Directory layout

Worktrees are created as **sibling directories** to the base repository:

```
~/projects/
├── my-project/              # Base repo (--cwd target)
│   ├── kardbrd.yml
│   └── ...
├── card-abc12345/           # Worktree for card abc12345
│   ├── .env → ../my-project/.env
│   ├── .claude/ → ../my-project/.claude/
│   └── ... (full repo checkout)
└── card-def67890/           # Worktree for card def67890
    └── ...
```

Override the location with `--worktrees-dir` or `AGENT_WORKTREES_DIR`.

## Lifecycle

### Creation

When a card triggers an executor session:

1. **Branch creation** — creates a branch named `card/<short_id>` (first 8 chars of card ID)
2. **Worktree setup** — `git worktree add` creates the worktree directory
3. **Symlinks** — configuration files are symlinked from the base repo
4. **Setup command** — runs `AGENT_SETUP_CMD` (e.g., `npm install`, `uv sync`) if configured

### Symlinked files

The following files are symlinked from the base repo into each worktree:

| File | Purpose | Condition |
|------|---------|-----------|
| `.env` | Environment variables | Always |
| `.claude/settings.local.json` | Claude CLI settings | Claude executor only |
| `.agents/skills/` | Skill definitions | When present |

### Cleanup

After a session completes (or on error), the worktree is removed:

1. `git worktree remove` cleans up the worktree directory
2. The branch may be kept (for PR workflows) or deleted (after merge)

## Main branch updates

Before creating a new worktree, `WorktreeManager` updates the local main branch:

1. Fetches from origin
2. Fast-forwards the local main branch
3. Creates the new worktree branch from the updated main

This ensures each new worktree starts from the latest code.

## Configuration

| Setting | Description |
|---------|-------------|
| `--cwd` / `AGENT_CWD` | Base repository path |
| `--worktrees-dir` / `AGENT_WORKTREES_DIR` | Parent directory for worktrees (default: parent of cwd) |
| `--setup-cmd` / `AGENT_SETUP_CMD` | Command to run after creating each worktree |

## Non-git repositories

If `--cwd` points to a directory that is not a git repository, worktree management is skipped entirely. The executor runs directly in the specified directory. This is useful for non-code tasks or when git isolation isn't needed.

## Concurrency

Multiple worktrees can exist simultaneously — one per active card session. The `asyncio.Semaphore` in `ProxyManager` limits concurrency (default: 3), and per-card session tracking prevents duplicate worktrees for the same card.
