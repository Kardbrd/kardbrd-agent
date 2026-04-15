# Bot Commands

kardbrd-agent creates a special **bot card** on startup that serves as a control panel. The bot card is titled with the agent's name (e.g., "MyBot") and displays the agent's configuration, active triggers, and schedules.

## How it works

When the agent starts, it creates or updates a bot card on the board. You can post commands as comments on this card to control the agent.

## Available commands

| Command | Description |
|---------|-------------|
| `/restart` | Restart the agent process |
| `/shutdown` | Gracefully shut down the agent |
| `/status` | Report current status (active sessions, uptime, configuration) |
| `/reload` | Reload the `kardbrd.yml` rules file |
| `/pause` | Pause processing new events (active sessions continue) |
| `/resume` | Resume processing events after a pause |

## Usage

Post a command as a comment on the bot card:

```
@MyBot /status
```

The agent responds with a comment containing the result.

## Bot card contents

The bot card description is auto-generated and includes:

- **Agent settings** — executor type, timeout, max concurrent sessions
- **Active triggers** — rules from `kardbrd.yml` with their events and conditions
- **Schedules** — cron schedules with next run times
- **Skills** — available skill commands (`/ke`, `/kp`, `/ki`, `/kr`)
- **Version** — current kardbrd-agent version

!!! tip
    The bot card updates automatically when rules are reloaded. Check it for the current agent configuration.

## Stopping sessions

To stop an active agent session on any card, add a :octicons-stop-16: reaction to the triggering comment. This works independently of bot commands — you don't need to use the bot card.

Define a stop rule in `kardbrd.yml` for this:

```yaml
rules:
  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```
