# Bot Card

kardbrd-agent creates a special **bot card** on startup that serves as both a status display and a control panel. The bot card is titled with a robot emoji prefix followed by the agent's name (e.g., "🤖 MyBot").

## How it works

When the agent starts, it creates or finds its bot card on the board. The card description is automatically generated with the agent's current configuration. To issue a command, post a comment on the bot card starting with `/` — the agent detects it and runs the corresponding handler.

## Bot card contents

The bot card description is auto-generated and includes:

- **Agent settings** — executor type, timeout, max concurrent sessions
- **Active triggers** — rules from `kardbrd.yml` with their events and conditions
- **Schedules** — cron schedules with next run times
- **Skills** — available skill commands (`/ke`, `/kp`, `/ki`, `/kr`)
- **Version** — current kardbrd-agent version

!!! tip
    The bot card updates automatically when rules are reloaded. Check it for the current agent configuration.

## Commands

Post any of these as a comment on the bot card to control the agent:

### `/status`

Reports current agent state including uptime, active card count, executor type, loaded rules count, and whether the agent is paused.

**Example response:**

> 🟢 **Online** — uptime 2h 15m
>
> - Active cards: 1
> - Executor: claude
> - Rules: 5
> - Paused: no

### `/pause`

Pauses automation rule processing. While paused, the agent **skips all rule-triggered actions** (card moves, reactions, new cards, etc.) but still responds to direct **@mentions**.

**Response:** ⏸️ Paused — automation rules are now skipped. @mentions still work.

### `/resume`

Re-enables automation rule processing after a pause.

**Response:** ▶️ Resumed — automation rules are active again.

### `/reload`

Hot-reloads rules and schedules from `kardbrd.yml` without restarting the agent. Requires a `ReloadableRuleEngine` (the default when using a `kardbrd.yml` file). After reloading, the bot card description is updated to reflect the new configuration.

**Response:** 🔄 Reloaded 5 rule(s) from kardbrd.yml

!!! note
    If the agent was started with static rules (no `kardbrd.yml`), reload will respond with: ⚠️ Rule engine is not reloadable (static rules)

### `/restart`

Gracefully finishes active work, then exits with code 0. Designed for use with a process supervisor (systemd, launchd, Docker) that will restart the agent automatically.

**Response:** 🔄 Restarting — finishing active work...

### `/shutdown`

Gracefully stops all active sessions and exits. Unlike `/restart`, the intent is a full stop — though the behavior depends on your process supervisor configuration.

**Response:** ⏹️ Shutting down.

!!! warning
    `/restart` and `/shutdown` both call `sys.exit(0)`. The difference is semantic — use `/restart` when you expect your supervisor to bring the agent back up, and `/shutdown` when you want it to stay down.

## Stopping sessions

To stop an active agent session on any card, add a 🛑 reaction to the triggering comment. This works independently of bot commands — you don't need to use the bot card.

Define a stop rule in `kardbrd.yml` for this:

```yaml
rules:
  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```
