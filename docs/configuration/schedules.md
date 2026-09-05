# Schedules

Cron-based schedules run independently of WebSocket events, enabling time-based automation like daily summaries and periodic reviews.

## Format

Schedules are defined in `kardbrd.yml` alongside rules:

```yaml
board_id: 0gl5MlBZ
agent: MyBot

schedules:
  - name: Daily Summary          # required — also the card title
    card_id: XVxBO30E            # optional — bind to this active card exactly
    cron: "0 0 * * *"            # required — standard cron expression
    action: |                    # required — prompt or skill command
      Read the activity on the board for the previous day
      and write a summary of what happened.
    model: haiku                 # optional: opus | sonnet | haiku
    list: Reports                # optional: target list for new cards
    assignee: E21K9jmv           # optional: user ID to assign new cards
```

## Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Schedule name — doubles as the card title |
| `card_id` | No | Fixed active card ID on the configured board; disables title lookup and creation |
| `cron` | Yes | Standard five-field cron expression |
| `action` | Yes | Prompt text or skill command (e.g., `/ke`) |
| `model` | No | Model override: `opus`, `sonnet`, `haiku` |
| `list` | No | Target list for newly created cards |
| `assignee` | No | User ID to assign newly created cards |

## How it works

The `ScheduleManager` runs as a background task alongside the WebSocket listener:

1. At each matching cron time, it runs the schedule.
2. When a schedule with `card_id` fires, it runs only on that active card after verifying it belongs to the configured board. Missing, archived, or foreign IDs fail that firing and never create a replacement card.
3. A schedule without `card_id` **finds or creates a card** with the schedule's `name` as the title (case-insensitive match)
4. If creating a new legacy schedule card, it optionally places it in the specified `list` and assigns the `assignee`
5. The schedule's `action` runs in the card's context, just like a rule-triggered action

!!! note "Card reuse"
    Schedules reuse existing cards by name. A "Daily Summary" schedule always runs in the same "Daily Summary" card, accumulating results over time.

!!! note "Fixed control cards"
    Use `card_id` for an unattended control card whose title might change or
    have duplicates. A failed fixed-card firing is reported once to the agent
    log; it does not create a board comment or durable catch-up job.

## Cron syntax

Standard five-field cron expressions:

```
┌───────────── minute (0–59)
│ ┌───────────── hour (0–23)
│ │ ┌───────────── day of month (1–31)
│ │ │ ┌───────────── month (1–12)
│ │ │ │ ┌───────────── day of week (0–7, 0 and 7 = Sunday)
│ │ │ │ │
* * * * *
```

### Common patterns

| Expression | Description |
|-----------|-------------|
| `0 9 * * 1-5` | 9:00 AM, Monday–Friday |
| `0 0 * * *` | Midnight daily |
| `0 */6 * * *` | Every 6 hours |
| `30 14 * * 5` | 2:30 PM every Friday |
| `0 0 1 * *` | First day of each month |

## Examples

### Daily standup summary

```yaml
schedules:
  - name: Daily Summary
    cron: "0 9 * * 1-5"
    model: haiku
    list: Reports
    action: |
      Read the board activity from the last 24 hours.
      Write a standup-style summary: what was done, what's in progress,
      and any blockers.
```

### Weekly backlog review

```yaml
schedules:
  - name: Weekly Backlog Review
    cron: "0 10 * * 1"
    model: sonnet
    list: Reviews
    action: |
      Review all cards in the Backlog list. For each card:
      1. Check if it's still relevant
      2. Suggest priority and effort estimate
      3. Flag any cards that should be archived
```

### Periodic health check

```yaml
schedules:
  - name: Dependency Audit
    cron: "0 0 * * 0"
    model: haiku
    action: |
      Check for outdated dependencies and known vulnerabilities.
      Report findings as a summary.
```

### Fixed watcher control card

```yaml
schedules:
  - name: CBA Sentry watch       # display name only; title may change
    card_id: XVxBO30E            # exact active card on this board
    cron: "*/15 * * * *"
    model: haiku
    action: |
      Run the watcher checks and publish the bounded result on this card.
```

## Validation

Schedule cron expressions are validated by `kardbrd agent validate`:

```bash
kardbrd agent validate
```

Invalid cron expressions produce an error:

```
ERROR [schedule "Bad Schedule"]: Invalid cron expression '* * *'
```
