# Portable worktree lifecycle — bounded implementation and migration plan

**Status:** implementation-ready design, independently reviewed on 2026-09-21 UTC.
This is a future generic-agent implementation plan. Runtime implementation, PRs,
release, restart, migration and CBA helper work remain pending.

## Scope and immutable source

The generated worktree `ffb1395b59729231c4fb2348d117a80b98fd2b8e` is only the
documentation destination. This plan uses authoritative main
`d01e8b018569cff216040c2047884cb46c776291` (`d01e8b0`), including PR #67,
inspected with `git show`. It does not depend on the older generated worktree
for source conclusions.

The agent repository would own the typed lifecycle configuration, non-mutating
Git selection, bounded adoption, process lifecycle, and exact command routing.
Projects own adapters and skill semantics. Administrators install reviewed
stable helpers from the corresponding project repository. CBA database,
fixture, proxy, preview-domain, and VPS behavior remain outside this plan.

## Exact proposed schema and ABI

This schema is intentionally identical to
`docs/proposals/portable-worktree-lifecycle.md`. Current releases do not accept
it.

```yaml
worktree:
  base:
    remote: origin
    ref: refs/heads/main
  helpers:
    stable_root: /usr/local/libexec/project-agent
  checkout:
    mode: full                       # full | delegated
    # Required, and allowed, only when mode is delegated:
    # bootstrap:
    #   argv: ["/usr/local/libexec/project-agent/bootstrap"]
    #   timeout_seconds: 900
  prepare:
    argv: ["/usr/local/libexec/project-agent/prepare"]
    on_create: true
    on_reuse: false
    timeout_seconds: 900
  sharing:
    env: disabled                     # disabled | link
    skills: fallback                  # fallback | disabled
  environment:
    passthrough: []
  adoptions:
    - card_id: kVykJg0e
      path: /srv/cba/workspaces/card-kVykJg0e
      common_git_dir: /srv/cba/repository/.git
      branch: fix/kVykJg0e-demo-fixture-boundary

rules:
  - name: Publish preview
    event: comment_created
    comment_command: /up
    action: /up
    # Existing rule filters are authorization, for example assignee,
    # comment_author, require_label, or a project-specific list restriction.

  - name: Stop preview
    event: comment_created
    comment_command: /down
    execution: existing_or_base       # default: prepare
    action: /down
```

The `worktree` block is opt-in. Without it, legacy worktree behavior and the
creation-only `--setup-cmd` / `KARDBRD_AGENT_SETUP_CMD` shell behavior remain;
with it, a nonempty legacy setup command is a startup error. All present new
fields are non-null and strictly typed, and all unknown new keys are errors in
file load, agent startup, reload candidate parsing, and `agent validate`.

Omitted `base` means remote `origin` and unambiguous remote HEAD. Explicit refs
are only `refs/heads/<name>` (with `main` normalized to
`refs/heads/main`). Omitted checkout is `full`. Bootstrap is required and
allowed only with `delegated` and defaults to a 900-second cap. Prepare is
optional; if present its defaults are `on_create: true`, `on_reuse: false`, and
`timeout_seconds: 900`. The booleans are intentionally independent. Omitted
sharing is `env: disabled` and `skills: fallback`; omitted environment
passthrough and adoptions are empty. Null never means a default.

Configured hooks require an absolute `helpers.stable_root`. Hook `argv[0]` is
absolute, resolves through `EvalSymlinks` to an executable regular file beneath
that root, and cannot use `~`, expansion, a relative path, or worktree-relative
code. The administrator installs it from reviewed project source before
configuration is enabled. Bootstrap is the delegated checkout materializer;
full checkout forbids it. Prepare is optional for either mode.

Every hook receives only present `PATH`, `HOME`, `TMPDIR`, `TMP`, `TEMP`, `TZ`,
`LANG`, `LC_*`, valid explicitly selected `environment.passthrough` names, and
the fixed `KARDBRD_CARD_ID`, `KARDBRD_BOARD_ID`,
`KARDBRD_WORKTREE_PATH`, `KARDBRD_WORKTREE_REASON` (`create` or `reuse`), and
`KARDBRD_WORKTREE_PHASE` (`bootstrap` or `prepare`). No other daemon
environment passes. Reserved `KARDBRD_*`, Kardbrd API/token, and executor
credential passthrough names are rejected. A stable wrapper should establish
project proxy/XDG/runtime settings; validated non-reserved passthrough is the
narrow alternative.

A root command deadline begins when active-card ownership is acquired, before
Git, bootstrap, sharing, prepare, or executor work. Every stage receives its
remaining time; a hook gets the lesser of that remainder and its 900-second (or
configured) cap. A hook owns a process group, captures at most 64 KiB, exposes
at most 2 KiB redacted fence-safe diagnostics, and kills/reaps descendants on
normal exit, error, timeout, cancellation, and Done handoff before ownership is
released. It cannot consume a full timeout and give the executor another full
one.

## Future interfaces and runtime invariants

Replace the creation-only adapter with context-aware operations equivalent to:

```go
type LifecycleRequest struct {
    CardID  string // exact canonical API ID
    BoardID string
    Reason  LifecycleReason // Create or Reuse
}

type Worktree interface {
    Prepare(ctx context.Context, req LifecycleRequest) (verifiedPath string, err error)
    ExistingOrBase(ctx context.Context, req LifecycleRequest) (verifiedPath string, err error)
    Remove(ctx context.Context, cardID string, force bool) error
}
```

`Prepare` creates/reuses and may materialize/share/prepare.
`ExistingOrBase` is read-only: no creation, bootstrap, prepare, sharing, legacy
setup, Git repair/prune, or removal. It selects a verified ready worktree, or the
trusted base CWD when all expected/adopted source paths are absent (even with
stale registration) or a verified owned record is incomplete/failed. A present
source candidate with bad ownership, or two distinct present candidates, is an
error, never a fallback. Git-backed
use requires the opt-in lifecycle; non-Git agents may use base-CWD-only behavior.

New worktrees use `card-<canonical-card-id>` and
`card/<canonical-card-id>`. An atomic Git-worktree-administrative record stores
canonical card ID, resolved path, base common Git directory, branch, origin
(`created`/`adopted`), and stage (`created`, `bootstrapping`, `preparing`,
`ready`, `failed`). An administrator separately invokes adoption preflight for
each `adoptions` manifest entry. It verifies present non-symlink path,
containment, porcelain registration, exact common Git directory, exact card ID,
and exact current declared branch before writing an adopted record. It never
renames, checks out, resets, cleans, rebases, initializes, or changes edits/data.
For example, it may preserve
`fix/kVykJg0e-demo-fixture-boundary` or `republish/z4dEK101`. Unlisted legacy
directories are never automatically adopted.
Card IDs and resolved paths must each be unique independently; aliases fail.
Preflight verifies existing materialization against the project policy and
records `origin: adopted`, `stage: created`, materialization completed, without
claiming application preparation. First ordinary reuse skips bootstrap/sharing,
honors `on_reuse`, then marks ready after the selected prepare phase succeeds.
Before readiness, `/down` uses the base fallback without running setup.

For create/recovery, the exact order is: resolve/fetch the remote ref without
changing the base; add the registered worktree and write `created`; use Git's
full materialization or delegated stable bootstrap; re-verify identity and
inspect the selected Git index/tree; then apply safe sharing; then prepare; then
write `ready`. Fallback skills are never selected from a directory-absence test
before delegated checkout. Repository-owned tracked `.agents/skills` or
`.codex/skills` wins after helper materialization. `env: link` and
`skills: fallback` only link after materialization, only for untracked absent
destinations, and never overwrite/unlink existing content. Ready reuse changes
no links; recovery retries only its incomplete phase.
Serialize fetch/ref capture/worktree administration under the shared Git lock;
project hooks run after releasing it and retain their per-card ownership.

A command claim is immutable at reservation:

```go
type CommandClaim struct {
    Key      CommandDedupKey // board, card, comment, normalized command
    Sequence uint64          // assigned under the per-card lock
    Rule     rules.Rule      // immutable action/execution/filter snapshot
    State    CommandState    // running, queued, succeeded, failed, rejected_full, canceled_done
}
```

There is one active command and an eight-entry FIFO per card. Claims are
reserved before run/queue, so repeated delivery of the same comment does no
work. A queued entry gets one visible position acknowledgement; a full queue
gets one visible full response and no entry is replaced. Terminal entries remain
for 24 hours in a 4,096-entry terminal-only LRU; running/queued entries do not
expire. Failed commands never auto-retry, while a later explicit comment has a
different comment ID and is retryable. This is delivery deduplication, not
durable exactly-once external side effects; repo commands remain idempotent.
Both immediate and dequeued commands retain the global concurrency cap and
recheck current card authorization and Done state before execution. A state-read
failure starts no work. The action deadline starts when a worker slot and
active-card ownership are acquired, excluding queue wait.

Exact command recognition runs after bot-comment suppression and bot-card admin
slash handling, before mention and generic rule dispatch. Only complete trimmed
`/up` or `@ThisAgent /up` forms match (same for `/down`). The leading mention is
optional but, if present, must name this agent and precede the command with
whitespace. Prose, quote, code block, `/up now`, `/upwards`, extra/trailing
mentions, and wrong-agent mentions do not match. An unregistered command
continues through existing behavior. A registered exact command is claimed—even
if policy denies it—then reports and returns, preventing mention preparation or
generic-rule double execution.

One agent owns each normalized bare command per board (the sole v1 command
scope). Administrator rollout preflight inventories workers across hosts and
records the single owner. V1 adds no distributed owner registry and cannot
discover arbitrary remote configurations from one daemon. Per-file
validation rejects duplicate commands regardless of overlapping/disjoint rule
filters. Skill registration is not dispatch authorization. Existing rule filters
remain authorization; generic fuzzy rules never run once a command is claimed.
`execution: prepare` is default. `existing_or_base` is the no-prepare policy
for repository `/down` and does not publish or retire resources.

When #67 claims Done cleanup, it atomically drains every queued claim as
`canceled_done`, cancels active work, waits for its hook/executor group cleanup,
then rechecks state and runs only direct cleanup argv. A late command sees
cleanup ownership and is visibly rejected. Immediate and dequeued starts recheck
Done state even after cleanup completes. Therefore stale queued `/up` cannot republish after cleanup. Done
remains distinct from `/down`.

Lifecycle configuration and complete normalized command rules, including action,
execution, model and authorization filters, are restart-only.
Reload must parse and strictly validate a full candidate, build a fingerprint,
and reject any difference with no manager/rule/schedule mutation. With an equal
fingerprint, ordinary non-command rules and schedules can reload only as a
validated atomic set; a schedule application error preserves the old set.

## File-by-file future implementation plan

These are bounded work packages. Break implementation into reviewable changes
and commit only after the relevant focused tests pass.

1. **Typed schema — `internal/rules/types.go:11-38`.** Add typed lifecycle,
   base, helpers, checkout, hook, sharing, environment, adoption,
   comment-command, and execution-policy fields with presence tracking and
   exact default normalization; preserve legacy fields unchanged.

2. **Strict loader — `internal/rules/load.go:11-115`.** Add `yaml.Node`
   validation of all new blocks before decode and invoke it from `LoadFile`.
   Reject null, unknown, and type-coerced new fields rather than falling back
   to legacy behavior.

3. **Relationship validation — `internal/rules/validate.go:47-208`.**
   Validate refs, helper root/executable containment, direct argv, timeout,
   full/delegated pairing, sharing, passthrough denial, adoption uniqueness,
   command uniqueness, and legacy setup conflict. Document administrator
   board-level owner preflight separately from local startup validation.

4. **Atomic reload — `internal/cli/agent_commands.go:221-323` and
   `internal/agent/events.go:603-636`.** Build/validate/fingerprint the
   candidate before schedule update or `ApplyRulesConfig`. Reject lifecycle or
   command-policy deltas intact; atomically apply equal-fingerprint ordinary
   rules/schedules only.

5. **Context process runner — `internal/worktree/worktree.go:12-50`.**
   Replace context-free `Runner.Run` for lifecycle operations with a direct
   context-aware runner and process-group lifecycle modeled after #67. Keep
   legacy `sh -c` setup untouched behind the no-block path.

6. **Base resolver — `internal/worktree/worktree.go:52-95,154-184`.** Fetch
   explicit remote branch or discover one remote HEAD, resolve object ID, and
   remove lifecycle base checkout/pull/reset behavior. Pass context to every
   Git call and serialize only base Git administration.

7. **Record/adoption — new `internal/worktree/record.go` and
   `internal/worktree/adoption.go`.** Atomically persist/recheck lifecycle
   record fields and implement explicit manifest preflight without any
   mutation beyond the new record.

8. **Lifecycle resolver — `internal/worktree/worktree.go`.** Implement
   `Prepare` and `ExistingOrBase` with full card identity for new paths/branches,
   registered/adopted verification, and safe base fallback for absent source or
   verified incomplete/failed preparation; reject foreign present paths.

9. **Materialize then share — new `internal/worktree/sharing.go`.** Implement
   full/delegated ordering, stable bootstrap, post-materialization Git index
   inspection, safe fallback skills/env linking, existing env-link adoption
   protection, and no ready-reuse link mutation.

10. **Prepare phases and deadline — `internal/worktree/worktree.go`.** Apply
    independent `on_create`/`on_reuse`, recover by stage, build the minimal ABI,
    cap children by remaining root deadline, block executor on failure, and
    retain source without reset/clean.

11. **Active session lifecycle — `internal/agent/manager.go:31-89,192-375`.**
    Claim active ownership before lifecycle work, keep it through descendant
    reap, pass remaining root deadline to executor, and replace the single
    pending-mention slot only for explicitly preserved non-command behavior.

12. **Command parser — `internal/agent/events.go:24-69`.** Add exact
    normal-card parser between bot-card handling and mention/rules. Claim,
    authorize/report, dispatch static action, and return; leave unregistered
    and non-exact text to current dispatch.

13. **FIFO/dedup — `internal/agent/manager.go`.** Add locked claims, sequence,
    eight-entry FIFO, acknowledgements, full response, terminal state/TTL/LRU,
    immutable rule snapshot, and later-comment retry behavior. No silent drop or
    last-value overwrite remains for explicit commands.

14. **Done precedence — `internal/agent/events.go:183-289` and
    `internal/agent/manager.go:377-409`.** Drain
    command FIFO under cleanup reservation, mark claims `canceled_done`, wait
    for prior lifecycle completion, retain authoritative recheck/direct argv,
    and reject immediate/late/dequeued stale publication. Preserve the global
    concurrency limit and recheck current card authorization before execution.

15. **Schema/reload tests — `internal/rules/load_test.go`,
    `internal/rules/validate_test.go`, and
    `internal/cli/agent_commands_test.go`.** Cover defaults, null/unknown/type
    errors, setup conflict, duplicate owner, adoptions, denied passthrough,
    equal-fingerprint reload, and rejected partial reload. Adoption cases include
    duplicate IDs/paths/aliases and the initial adopted phase.

16. **Real Git tests — `internal/worktree/worktree_test.go`.** Use temporary
    main/master remotes, dirty feature base, fetch/HEAD failure, mixed-case
    IDs, the two existing-branch shapes, stale absent registrations, and
    present foreign/symlink paths. Assert no base/source mutation.

17. **Materialization/process tests — `internal/worktree/worktree_test.go`.**
    Use a real delegated bootstrap fixture which creates tracked executor skill
    directories; assert repo skills win. Cover link permutations, minimal ABI,
    absent ambient credentials, passthrough, total budget, descendant cleanup,
    and phase retry.

18. **Routing/race tests — `internal/agent/manager_test.go` and
    `internal/agent/rules_test.go`.** Cover bare/mentioned/wrong-agent/prose/
    quote/code matching, authorization, no fallthrough, overlapping generic
    rules, active `/up` then `/down` FIFO order, redelivery, queue full,
    failure/new-comment retry, down after failed preparation, non-Git/legacy
    policy validation, existing-or-base, Done draining queued `/up`, immediate
    commands while Done, changed card authorization and global concurrency.

19. **Documentation/review — configuration docs and disposable fixtures.**
    Publish the exact contract, request independent review, and prove one
    CBA-shaped adapter plus one existing Docker hook without implementing CBA
    helper behavior in this repository.

## Required test evidence and rollout gates

| Area | Required evidence |
| --- | --- |
| Base/adoption | Real main/master source, untouched dirty base, exact fetched object; dirty custom-branch adoption; foreign rejection; stale-absent base fallback. |
| Checkout/sharing | Full/delegated materialize before sharing; helper-created tracked skills win; env-link protection and all defaults. |
| Hook/process | Minimal ABI, no ambient Kardbrd/executor credentials, total timeout includes prep, descendants gone on all terminal paths. |
| Commands | Exact parser, administrative owner preflight, policy/no fallthrough, FIFO order, dedup/full/failure retry, stop after failed preparation, global concurrency, current authorization and no immediate/queued publish while Done. |
| Reload/#67 | Strict load/start/reload with no partial hot swap; valid ordinary reload; #67 waits then directly cleans only. |

Future implementation verification must include:

```sh
go test ./...
go test ./internal/agent
go test ./internal/cli
go test -race ./internal/agent ./internal/worktree ./internal/rules ./internal/cli
go vet ./...
pre-commit run --all-files
```

No unchanged full Go test suite was run for this documentation-only revision.

Project-owned rollout gates remain: CBA implements/tests `cba-env` and its
idempotent `/up`/`/down`; its administrator installs the stable helper and
preserves external Sentry scheduling; each project inventories actual
container-visible runtime/mount/worktree/link state and proves reuse safety; a
disposable Website pilot precedes one-board handovers; a separately approved
tagged release contains the lifecycle support and #67; images use verified
published assets; prior image/config/binary/source/data survive rollback;
Mobile/ClientBot stay stopped, Phoenix/LiveBot stay retired, HR stays non-Git,
and CBA VPS cutover stays separate.

The design gate is complete. The card remains in **Review**; implementation,
independent test evidence, release and per-project rollout gates remain open.
