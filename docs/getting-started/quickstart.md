# Quick Start

This guide walks you through a minimal working setup — from board creation to your first AI-assisted card.

## 1. Create a kardbrd board

Go to [kardbrd.com](https://kardbrd.com) and create a new board. Note the **board ID** from the URL (e.g., `0gl5MlBZ` from `app.kardbrd.com/b/0gl5MlBZ/`).

## 2. Create a bot

In your board, go to **Settings** → **Bots** → **Create Bot**. Choose a name (e.g., `MyBot`) and copy the bot token.

## 3. Set up environment

```bash
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>    # must match the bot name
export ANTHROPIC_API_KEY=<api-key>   # or GOOSE_PROVIDER + provider key
```

## 4. Start the agent

=== "Claude (default)"

    ```bash
    kardbrd-agent start --cwd /path/to/your/repo
    ```

=== "Goose"

    ```bash
    kardbrd-agent start --cwd /path/to/your/repo --executor goose
    ```

=== "Codex"

    ```bash
    kardbrd-agent start --cwd /path/to/your/repo --executor codex
    ```

You should see the agent connect and begin listening:

```
INFO  kardbrd_agent: Connected to board 0gl5MlBZ as MyBot
INFO  kardbrd_agent: Listening for @mentions and rule events...
```

## 5. Trigger the agent

Create a card on your board and add a comment:

```
@MyBot explore this codebase and tell me what it does
```

The agent will:

1. Create an isolated git worktree for the card
2. Spawn the executor with the card context
3. Post results back as comments on the card

## 6. Add automation rules (optional)

Create a `kardbrd.yml` in your repo root to automate responses:

```yaml
board_id: 0gl5MlBZ
agent: MyBot

rules:
  - name: Explore new ideas
    event: card_created
    list: Ideas
    model: sonnet
    action: |
      Explore this card's topic and summarize your findings.

  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```

The agent hot-reloads rules when the file changes — no restart needed.

Validate your rules:

```bash
kardbrd-agent validate
```

## What's next?

- [CLI Reference](../configuration/cli.md) — all available flags and options
- [Rules configuration](../configuration/rules.md) — full `kardbrd.yml` format
- [Deployment guides](../deployment/index.md) — run in production with Docker, systemd, or launchd
