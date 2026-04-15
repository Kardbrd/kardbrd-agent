# Bot Card

kardbrd-agent creates a special **bot card** on startup that serves as a status display. The bot card is titled with a robot emoji prefix followed by the agent's name (e.g., "\U0001f916 MyBot") and displays the agent's configuration.

## How it works

When the agent starts, it creates or updates a bot card on the board. The card description is automatically generated and kept up to date.

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
