# kardbrd-agent

**AI agents for your kardbrd boards.**

kardbrd-agent is a proxy that listens for @mentions on [kardbrd](https://kardbrd.com) board cards, spawns AI agents in isolated git worktrees, and coordinates workflows — all driven by a single `kardbrd.yml` configuration file.

---

## How it works

1. **@mention your bot** on any card — `@coder fix the login bug`
2. The agent creates an **isolated git worktree** for the card
3. Your chosen **executor** (Claude, Goose, or Codex) works on the task
4. Results are posted back to the card as **comments and PRs**

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

-   :material-file-tree:{ .lg .middle } **Event-Driven Rules**

    ---

    Match 25+ board events with conditions (list, label, emoji, user, content) using AND logic. Route cards to different executors, models, and skills.

    [:octicons-arrow-right-24: Rules](configuration/rules.md)

-   :material-source-branch:{ .lg .middle } **Isolated Worktrees**

    ---

    Each card gets its own git worktree. Agents work in isolation — no conflicts between concurrent tasks.

    [:octicons-arrow-right-24: Worktrees](architecture/worktrees.md)

-   :material-file-cog:{ .lg .middle } **`kardbrd.yml` Configuration**

    ---

    One YAML file controls everything — board identity, executor choice, event rules, cron schedules, and model selection. Hot-reloads on save.

    [:octicons-arrow-right-24: Configuration](configuration/rules.md)

-   :material-clock-outline:{ .lg .middle } **Cron Schedules**

    ---

    Time-based automation with standard cron expressions. Daily summaries, periodic checks, scheduled reviews.

    [:octicons-arrow-right-24: Schedules](configuration/schedules.md)

-   :material-card-account-details:{ .lg .middle } **Bot Card**

    ---

    Live status display on your board showing active sessions, configured triggers, schedules, and available skills.

    [:octicons-arrow-right-24: Bot Card](configuration/bot-commands.md)

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
