# Portable worktree lifecycle

`worktree` is an opt-in `kardbrd.yml` contract for Git-backed agents. When it
is absent, the existing worktree manager and optional legacy `--setup-cmd`
behavior are unchanged. Do not combine a lifecycle block with `--setup-cmd` or
`KARDBRD_AGENT_SETUP_CMD`.

```yaml
worktree:
  base:
    remote: origin
    ref: refs/heads/main # omit to use an unambiguous remote HEAD
  helpers:
    stable_root: /usr/local/libexec/project-agent
  checkout:
    mode: full # full | delegated
  prepare:
    argv: [/usr/local/libexec/project-agent/prepare]
    on_create: true
    on_reuse: false
    timeout_seconds: 900
  sharing:
    env: disabled # disabled | link
    skills: fallback # fallback | disabled
  environment:
    passthrough: []
```

All lifecycle fields are strictly decoded: null values, unknown lifecycle keys,
and coerced types are errors. Omitted defaults are remote `origin`, remote HEAD,
full checkout, no prepare hook, create-only preparation when a hook is present,
900-second hook caps, disabled environment sharing, fallback skill sharing, and
no passthrough. A hook cap cannot exceed the agent action timeout.

Hooks are direct argv, never a shell string. `argv[0]` must resolve to an
executable regular file beneath the absolute `helpers.stable_root`. Hooks get a
minimal environment plus the fixed card/board/worktree/reason/phase variables;
Kardbrd tokens, API URLs, and executor credentials are not inherited. A single
action deadline covers Git, bootstrap, prepare, and executor work.

## Source and ownership

For new worktrees the agent fetches one configured branch (or remote HEAD),
captures its immutable object ID, and creates `card-<full-card-id>` on
`card/<full-card-id>`. It does not switch, pull, reset, or otherwise mutate the
shared base checkout. Fetch/ref/worktree administration is serialized across
processes using the base Git common directory.

Lifecycle records live in Git worktree administration, not project source.
Existing paths are used only after registration, common-Git-directory, path,
card, branch, and record verification. A present foreign or malformed path
fails closed.

Use `agent adopt-worktree` only for a listed administrative adoption:

```bash
kardbrd agent adopt-worktree CARD_ID --cwd /srv/project --rules /srv/project/kardbrd.yml
```

The `adoptions` entry must declare the exact card ID, path, common Git directory,
and branch. Adoption writes a verified record but never renames, resets, cleans,
rebases, or prepares source. It preserves existing edits and branch names.

`execution: existing_or_base` is a read-only selection policy. It returns a
verified ready worktree, or the trusted base for absent source/stale
registration and verified incomplete/failed preparation. It never creates,
prepares, links, repairs, prunes, or retires anything. Non-Git agents may use
only this base-CWD selection; ordinary preparation remains unavailable.

## Exact card commands

Commands use a rule with `comment_command`; only a trimmed whole `/up` or
`@ThisAgent /up` form is claimed. Prose, code blocks, extra words, and other
agent mentions are not commands. A claimed command never falls through to
ordinary mention or generic rule processing.

```yaml
rules:
  - name: Publish preview
    event: comment_created
    comment_command: /up
    action: /up
  - name: Stop preview
    event: comment_created
    comment_command: /down
    execution: existing_or_base
    action: /down
```

Command authorization continues to use the rule filters. Configure each bare
command once per board and arrange one owning agent during deployment; skill
registration is not command authorization. Commands retain the global
concurrency cap, queue up to eight per card with visible results, deduplicate
redeliveries, recheck authorization and Done state when starting, and are
canceled/drained when #67 Done cleanup takes ownership.

## Reload and sharing

Lifecycle configuration and the full command policy are restart-only. `/reload`
accepts ordinary rule/schedule changes only when that fingerprint is unchanged;
an incompatible candidate leaves the old configuration active. Materialization
always precedes fallback sharing, so tracked repository skills win. Environment
and skill sharing are independent; the agent never overwrites or unlinks a
project file or link.
