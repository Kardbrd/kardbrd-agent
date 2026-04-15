# Rules (`kardbrd.yml`)

The rule engine is the core of kardbrd-agent's automation. Define rules in `kardbrd.yml` to match WebSocket events and trigger AI agent sessions or built-in actions.

## File format

```yaml
board_id: 0gl5MlBZ        # required — your board ID
agent: MyBot               # required — agent name for @mentions
api_url: http://app.kardbrd.com  # optional — API base URL
executor: goose            # optional — "claude" (default), "goose", or "codex"

rules:
  - name: Rule name        # required
    event: card_created    # required — event type(s)
    # ... conditions ...
    model: sonnet          # optional — model selection
    action: /ke            # required — what to do
```

## Rules

Each rule has a **name**, one or more **events**, optional **conditions**, and an **action**.

### Events

Events can be a single string or a YAML list:

```yaml
# Single event
event: card_created

# Multiple events
event:
  - card_created
  - card_moved
```

#### Available events

| Category | Events |
|----------|--------|
| **Card** | `card_created`, `card_moved`, `card_updated` |
| **Comment** | `comment_created`, `comment_updated`, `comment_deleted` |
| **Reaction** | `reaction_added`, `reaction_removed` |
| **Checklist** | `checklist_created`, `checklist_updated`, `checklist_deleted` |
| **Todo item** | `todo_item_created`, `todo_item_updated`, `todo_item_completed`, `todo_item_reopened`, `todo_item_deleted` |
| **Attachment** | `attachment_created`, `attachment_deleted` |
| **Link** | `link_created`, `link_deleted` |
| **Label** | `label_added`, `label_removed` |
| **List** | `list_created`, `list_updated`, `list_deleted` |

### Conditions

All conditions use **AND logic** — every condition on a rule must match for the rule to fire.

| Condition | Type | Description |
|-----------|------|-------------|
| `list` | string | Card is in this list (case-insensitive) |
| `title` | string | Card title contains this text (case-insensitive) |
| `label` | string | Card has this label (case-insensitive) |
| `emoji` | string | Reaction emoji matches (for `reaction_added`/`reaction_removed`) |
| `require_label` | string | Card must have this label (triggers API enrichment) |
| `exclude_label` | string | Card must NOT have this label (triggers API enrichment) |
| `require_user` | string | Event must be from this user ID |
| `content_contains` | string | Comment or card content contains this text |
| `comment_author` | string | Comment must be by this user (supports `__self__` for the bot) |
| `assignee` | string or list | Card must be assigned to this user ID (supports `__self__`) |

!!! info "Label enrichment"
    `require_label` and `exclude_label` trigger an API call to fetch the card's current labels. Other label conditions use data from the WebSocket event.

### Actions

Actions define what happens when a rule matches. Three types:

**Skill commands** — invoke a predefined workflow:

```yaml
action: /ke    # explore codebase
action: /kp    # create implementation plan
action: /ki    # execute implementation plan
action: /kr    # code review
```

**Inline prompts** — send a custom prompt to the executor:

```yaml
action: |
  Review this PR and check for security vulnerabilities.
  Focus on SQL injection and XSS risks.
```

**Built-in actions** — special system actions:

```yaml
action: __stop__   # kill the active session for this card
```

### Model selection

Override the default model per-rule:

```yaml
model: opus      # Claude Opus
model: sonnet    # Claude Sonnet
model: haiku     # Claude Haiku
```

For Goose, use provider-specific model names or the short aliases above.

## Examples

### Auto-explore new cards

```yaml
rules:
  - name: Explore new ideas
    event:
      - card_created
      - card_moved
    list: Ideas
    model: sonnet
    action: /ke
```

### Stop agent on reaction

```yaml
rules:
  - name: Stop on red flag
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```

### Merge on approval

```yaml
rules:
  - name: Ship on approval
    event: reaction_added
    emoji: "✅"
    require_user: E21K9jmv
    require_label: Agent
    model: sonnet
    action: |
      Merge the PR to main. Run tests first and fix any failures.
```

### Multi-agent board

Use `require_label` and `exclude_label` to scope rules per agent:

```yaml
# Agent A handles "Agent"-labeled cards
rules:
  - name: Handle agent cards
    event: comment_created
    require_label: Agent
    action: /ki

# Agent B handles everything else
rules:
  - name: Handle other cards
    event: comment_created
    exclude_label: Agent
    action: /ke
```

### Per-user workflows

```yaml
rules:
  - name: Auto-assign senior review
    event: card_moved
    list: Review
    assignee: E21K9jmv
    model: opus
    action: |
      Perform a thorough code review of this PR.
```

## Validation

Validate your rules file before deploying:

```bash
kardbrd-agent validate              # validates ./kardbrd.yml
kardbrd-agent validate path/to/kardbrd.yml
```

## Hot-reload

The rule engine watches `kardbrd.yml` for changes and reloads automatically every 60 seconds. No restart needed after editing rules.
