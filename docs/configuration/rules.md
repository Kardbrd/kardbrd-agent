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
| **Card** | `card_created`, `card_moved`, `card_archived`, `card_unarchived`, `card_deleted` |
| **Comment** | `comment_created`, `comment_deleted` |
| **Reaction** | `reaction_added` |
| **Checklist** | `checklist_created`, `checklist_deleted` |
| **Todo item** | `todo_item_created`, `todo_item_completed`, `todo_item_reopened`, `todo_item_deleted`, `todo_item_assigned`, `todo_item_unassigned` |
| **Attachment** | `attachment_created`, `attachment_deleted` |
| **Link** | `card_link_created`, `card_link_deleted` |
| **Label** | `label_added`, `label_removed` |
| **List** | `list_created`, `list_deleted` |

### Conditions

All conditions use **AND logic** — every condition on a rule must match for the rule to fire.

| Condition | Type | Description |
|-----------|------|-------------|
| `list` | string | Card is in this list (case-insensitive) |
| `title` | string | Card title matches exactly (case-insensitive) |
| `label` | string | Card has this label (case-insensitive) |
| `emoji` | string | Reaction emoji matches (for `reaction_added`) |
| `require_label` | string | Card must have this label (triggers API enrichment) |
| `exclude_label` | string | Card must NOT have this label (triggers API enrichment) |
| `require_user` | string | Event must be from this user ID |
| `content_contains` | string | Comment or card content contains this text |
| `comment_author` | string | Comment must be by this user (supports `__self__` for the bot) |
| `assignee` | list | Card must be assigned to one of these user IDs (supports `__self__`). Must be a YAML list |

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

### Direct Done cleanup commands

`cleanup_command` is an opt-in maintenance rule for retiring resources that
belong to a card after it reaches Done. Unlike `action`, it runs a configured
command directly: it does not create, initialize, or remove a worktree, run a
worktree setup hook, fetch card markdown, or start an executor session.

The command must be a non-empty YAML argv list. It is intentionally restricted
to one `card_moved` event and `list: Done`, and cannot be combined with
`action`. The first argv value cannot be a privilege (`sudo`, `doas`, `su`, or
`pkexec`), environment, or shell wrapper; invoke a dedicated script directly.

```yaml
rules:
  - name: Retire preview when Done
    event: card_moved
    list: Done
    cleanup_command:
      - /srv/cba/bin/retire-preview
      - --quiet
```

The agent appends the exact canonical card ID as the final argv value, so the
example command receives `/srv/cba/bin/retire-preview --quiet CARD_ID`. It also
sets `KARDBRD_CARD_ID` to that same value. The command receives a minimal
runtime environment (`PATH`, home/temp/locale settings, and
`KARDBRD_CARD_ID`), not `KARDBRD_TOKEN`, `KARDBRD_API_URL`, or executor
credentials. Do not use YAML interpolation for card titles, comments, or card
IDs; the direct argv and environment contract keeps those values out of a
shell.

The process working directory is the agent's configured base checkout
(`KARDBRD_AGENT_CWD`), never a card worktree. Cleanup scripts must treat that
directory as read-only; the cleanup contract prevents agent worktree lifecycle
operations but cannot prevent an operator-provided script from editing files.

For example, CBA's `/srv/cba/bin/retire-preview` can read the card ID from its
final argument (or `KARDBRD_CARD_ID`) and make its preview deletion idempotent:
an already-absent preview exits zero. The agent runs it as its existing
unprivileged account and applies the agent execution timeout. Nonzero exits and
timeouts are reported on the card with bounded, redacted diagnostics.

Before invoking a queued cleanup command, the agent reads the authoritative
card state again. If the card has been moved out of Done, it skips the command.
Matching cleanup owns the Done event, so normal Done rules and the default
worktree removal lifecycle are suppressed for that event. Replayed events may
invoke the command again after a prior run completes; make the resource command
idempotent.

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

### Code review on approval

```yaml
rules:
  - name: Review on checkmark
    event: reaction_added
    emoji: "✅"
    require_user: E21K9jmv
    require_label: Agent
    model: sonnet
    action: /kr
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
    assignee:
      - E21K9jmv
    model: opus
    action: |
      Perform a thorough code review of this PR.
```

## Validation

Validate your rules file before deploying:

```bash
kardbrd agent validate              # validates ./kardbrd.yml
kardbrd agent validate path/to/kardbrd.yml
```

## Hot-reload

The rule engine watches `kardbrd.yml` for changes and reloads automatically every 60 seconds. No restart needed after editing rules.
