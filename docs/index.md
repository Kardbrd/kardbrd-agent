# kardbrd-agent

**AI agents for your kardbrd boards.**

kardbrd-agent is a proxy that listens for @mentions on [kardbrd](https://kardbrd.com) board cards, spawns AI agents in isolated git worktrees, and coordinates workflows including automated merging.

---

## How it works

1. **@mention your bot** on any card — `@coder fix the login bug`
2. The agent creates an **isolated git worktree** for the card
3. Your chosen **executor** (Claude, Goose, or Codex) works on the task
4. Results are posted back to the card as **comments and PRs**
5. Optionally, the agent runs an **automated merge workflow** (rebase, test, squash merge)

## Quick install

```bash
# Install
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent && uv sync --dev

# Configure
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export ANTHROPIC_API_KEY=<api-key>

# Run
kardbrd-agent start
```

Or run without cloning using `uvx`:

```bash
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd /path/to/your/repo
```

[:material-rocket-launch: Getting Started](getting-started/installation.md){ .md-button .md-button--primary }
[:material-cog: Configuration](configuration/rules.md){ .md-button }
[:material-server: Deployment](deployment/index.md){ .md-button }

---

## Features

<div class="grid cards" markdown>

-   :material-robot:{ .lg .middle } **Multi-Executor Support**

    ---

    Choose from **Claude CLI**, **Goose** (20+ providers), or **OpenAI Codex**. Switch executors per-board or per-rule.

    [:octicons-arrow-right-24: Executors](architecture/executors.md)

-   :material-file-tree:{ .lg .middle } **Declarative Rule Engine**

    ---

    Define automation with `kardbrd.yml` — match events, conditions, and trigger actions. Hot-reloads on file changes.

    [:octicons-arrow-right-24: Rules](configuration/rules.md)

-   :material-source-branch:{ .lg .middle } **Isolated Worktrees**

    ---

    Each card gets its own git worktree. Agents work in isolation — no conflicts between concurrent tasks.

    [:octicons-arrow-right-24: Worktrees](architecture/worktrees.md)

-   :material-merge:{ .lg .middle } **Automated Merging**

    ---

    Full merge workflow: commit, rebase, resolve conflicts (LLM-assisted), run tests, squash merge to main.

    [:octicons-arrow-right-24: Merge Workflow](architecture/merge-workflow.md)

-   :material-clock-outline:{ .lg .middle } **Cron Schedules**

    ---

    Time-based automation with standard cron expressions. Daily summaries, periodic checks, scheduled reviews.

    [:octicons-arrow-right-24: Schedules](configuration/schedules.md)

-   :material-shield-check:{ .lg .middle } **Bot Commands**

    ---

    Control your agent with card commands: `/restart`, `/shutdown`, `/status`, `/reload`, `/pause`, `/resume`.

    [:octicons-arrow-right-24: Bot Commands](configuration/bot-commands.md)

</div>

---

## Architecture at a glance

```
WebSocket Event
    │
    ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  Rule Engine  │────▶│   Worktree   │────▶│   Executor   │
│  (match)      │     │  (isolate)   │     │  (run agent) │
└──────────────┘     └──────────────┘     └──────────────┘
                                                 │
                                                 ▼
                                          ┌──────────────┐
                                          │ Card Updates  │
                                          │ (comments/PR) │
                                          └──────────────┘
```

[:octicons-arrow-right-24: Full architecture overview](architecture/index.md)
