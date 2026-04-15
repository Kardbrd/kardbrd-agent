# Rule Engine

The rule engine is the decision layer between WebSocket events and executor actions. It evaluates declarative rules from `kardbrd.yml` and determines which actions to trigger.

## How matching works

When an event arrives, `RuleEngine.match(event_type, message)` iterates over all rules and returns those that match:

```
Event (type + message)
    │
    ▼
For each rule:
    ├── Does event type match? ──── No → skip
    ├── Does list match? ────────── No → skip
    ├── Does title match? ───────── No → skip
    ├── Does label match? ───────── No → skip
    ├── Does emoji match? ───────── No → skip
    ├── Does require_label match? ─ No → skip
    ├── Does exclude_label match? ─ No → skip
    ├── Does require_user match? ── No → skip
    ├── Does content match? ─────── No → skip
    ├── Does comment_author match?─ No → skip
    ├── Does assignee match? ────── No → skip
    │
    └── All conditions pass → MATCH ✓
```

**AND logic**: every condition on a rule must match. If a condition is not specified, it's ignored (always passes).

## Event matching

Rules declare which events they respond to. Events can be a single string or a list:

```yaml
event: card_created          # single event
event: [card_created, card_moved]  # multiple events
```

The engine checks if the incoming event type is in the rule's event list.

## Condition evaluation

### String conditions

`list`, `title`, `label`, `emoji` — case-insensitive substring match against the event message data.

### Label conditions

`require_label` and `exclude_label` are special: they trigger an **API call** to fetch the card's current labels, since WebSocket events may not include full label data.

- `require_label: Agent` — card must have the "Agent" label
- `exclude_label: Agent` — card must NOT have the "Agent" label

This enables **multi-agent boards** where different bots handle different cards based on labels.

### User conditions

- `require_user: E21K9jmv` — event must originate from this user ID
- `comment_author: __self__` — comment must be from the bot itself
- `assignee: E21K9jmv` — card must be assigned to this user (also supports `__self__`)

## Hot-reload

`ReloadableRuleEngine` wraps the base `RuleEngine` and watches the `kardbrd.yml` file:

- Checks the file's modification time every **60 seconds**
- If the file changed, reloads rules and schedules
- If the new file has errors, keeps the previous valid rules and logs a warning
- No restart required after editing rules

## Validation

`validate_rules_file()` performs comprehensive validation:

**Errors** (prevent loading):

- Missing required fields (`name`, `event`, `action`)
- Unknown event types
- Invalid cron expressions (schedules)
- Invalid YAML syntax

**Warnings** (informational):

- Conditions that have no effect on certain event types (e.g., `emoji` on `card_created`)
- Unused or redundant conditions

Run validation via CLI:

```bash
kardbrd-agent validate
kardbrd-agent validate path/to/kardbrd.yml
```

## Key data structures

### Rule

```python
@dataclass
class Rule:
    name: str
    events: list[str]
    action: str
    model: str | None = None
    list: str | None = None
    title: str | None = None
    label: str | None = None
    content_contains: str | None = None
    exclude_label: str | None = None
    require_label: str | None = None
    emoji: str | None = None
    require_user: str | None = None
    assignee: str | list[str] | None = None
    comment_author: str | None = None
```

### BoardConfig

```python
@dataclass
class BoardConfig:
    board_id: str
    agent_name: str
    api_url: str | None = None
    executor: str | None = None
    schedules: list[Schedule] = field(default_factory=list)
```

### Loading rules

```python
from kardbrd_agent.rules import load_rules

rule_engine, board_config = load_rules("kardbrd.yml")
matched = rule_engine.match("card_created", message_data)
```
