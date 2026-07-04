# Rule Engine

Rules are loaded from `kardbrd.yml` by `internal/rules`.

## Matching

Every condition on a rule must match:

- `event`
- `list`
- `title`
- `label`
- `content_contains`
- `require_label`
- `exclude_label`
- `emoji`
- `require_user`
- `assignee`
- `comment_author`

Label, assignee, and comment-author conditions can be enriched from the API when the WebSocket event does not include enough data.

## Stop Rules

Use `__stop__` to stop an active session:

```yaml
rules:
  - name: Stop agent
    event: reaction_added
    emoji: "🛑"
    action: __stop__
```

## Validation

```bash
kardbrd agent validate
kardbrd agent validate path/to/kardbrd.yml
```

Validation reports missing required fields, invalid YAML, invalid schedules, and unknown fields or events.
