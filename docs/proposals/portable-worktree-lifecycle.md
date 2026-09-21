# Portable worktree lifecycle — exploration and proposed contract

**Status:** implementation-ready design, independently reviewed on 2026-09-21 UTC.
This is an unreleased contract. Runtime implementation, release, migration,
restart, preview operation and secret changes remain separate work.

## Source boundary and review result

The generated card worktree is at
`ffb1395b59729231c4fb2348d117a80b98fd2b8e` (`ffb1395b`). It is solely the
destination for these documents. All implementation assertions below were read
with `git show` from immutable authoritative main
`d01e8b018569cff216040c2047884cb46c776291` (`d01e8b0`, PR #67). No canonical
checkout was switched, fetched, reset, pulled, stashed, or otherwise changed.

The independent source review is confirmed:

- `HandleBoardEvent` invokes mention handling at
  `internal/agent/events.go:24-45`, then unconditionally reaches generic rules
  at `:68`. A claimed normal-card command must return before both paths can
  dispatch it.
- `processClaimedMention` and `processRule` call `Worktree.Create` before the
  executor prompt at `manager.go:230-280` and `events.go:395-455`; skipping only the final
  setup hook cannot make `/down` no-prepare.
- A busy rule returns `nil` at `:401-408`; busy mentions replace one
  `pending[cardID]` value at `internal/agent/manager.go:192-227,302-375`.
- `worktree.Runner.Run` has no `context.Context` at
  `internal/worktree/worktree.go:18-20`; the legacy setup runner cannot meet a
  cancellable lifecycle contract.
- Rules reload loads and updates schedules before `ApplyRulesConfig` at
  `internal/cli/agent_commands.go:236-267,302-323`, while the worktree manager
  is constructed only at startup. A lifecycle or command-policy hot swap would
  mix configuration generations.

PR #67's direct Done cleanup remains the sole direct retirement path. It
reserves the card, waits for the prior session, rechecks card state, runs a
minimal-environment direct argv command, and reaps its process group at
`events.go:183-289` and `manager.go:377-409`. This proposal extends that ownership boundary; it
does not add a competing cleanup mechanism.

## Current state and constraints

At `d01e8b0`, `worktree.Manager.Create` accepts an existing derived directory
without ownership validation (`worktree.go:60-69`), ignores a base update error
and mutates the base checkout through `checkout`/`pull` (`:74,154-175`), creates
from the base's current `HEAD` (`:76-84`), couples `.env` and executor skill
links (`:130-152`), and runs a creation-only shell setup (`:177-184`). These
behaviors remain legacy compatibility behavior only when the new block is
absent.

The generic agent owns card routing, Git identity/path checks, lifecycle
serialization, hook execution, and cancellation. A project repository owns
application environments, fixtures, database/cache isolation, preview behavior,
and repo skills. An administrator installs reviewed stable helpers from the
project repository and owns runtime accounts, packages, secrets, firewall and
service changes. A base-CWD fallback is a read-only *contract for the agent*;
it is not a new filesystem sandbox or permission boundary.

## Canonical proposed schema

This is the one proposed v1 schema. The companion implementation plan repeats
it verbatim. It is not accepted by current releases.

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

The documented examples deliberately do not hard-code a particular requester,
label, or project. Existing board/rule authorization decides who may invoke a
route; optional project filters refine that policy.

### Decode, defaults, and precedence

1. No `worktree` key preserves all legacy worktree behavior, including the
   creation-only `--setup-cmd` / `KARDBRD_AGENT_SETUP_CMD` shell string. A
   present `worktree` key and a nonempty legacy setup command are a startup
   error. Strings are never reinterpreted as argv.
2. Every present new field is non-null and has the exact YAML type shown.
   Unknown keys anywhere below `worktree`, `worktree.adoptions[*]`, or a rule
   containing `comment_command` or `execution` are errors. Existing unrelated
   legacy fields retain their current compatibility behavior. This strict node
   validation is used by file load, agent startup, reload candidate loading,
   and `agent validate`; malformed opt-in input must not silently become
   legacy input.
3. `base` may be omitted. `base.remote` then defaults to `origin`; an omitted
   `base.ref` means one unambiguous remote HEAD branch. An explicit ref is only
   `refs/heads/<name>` (with `main` normalized to `refs/heads/main`), never a
   SHA, tag, `HEAD`, empty component, or another namespace.
4. `checkout` may be omitted and defaults to `mode: full`. `bootstrap` is
   required for `delegated`, forbidden for `full`, and has a 900-second default
   hook cap when its timeout is omitted. It must be positive and no larger than
   the daemon command deadline.
5. `prepare` may be omitted, which means no structured prepare hook. If it is
   present, `argv` is a nonempty list of nonempty strings; `on_create` defaults
   to `true`, `on_reuse` independently defaults to `false`, and
   `timeout_seconds` independently defaults to `900`. A null timeout, boolean,
   numeric string, or zero is an error. The two selection booleans intentionally
   remain independent: a project can prepare only a new worktree, only reuse,
   both, or neither.
6. `helpers` is required whenever bootstrap or prepare is configured.
   `stable_root` is an absolute, clean path. Each hook `argv[0]` is an absolute
   path that resolves with `EvalSymlinks` to an executable regular file beneath
   the resolved stable root; no `~`, environment expansion, relative path, or
   worktree-relative helper is accepted. The administrator installs that helper
   from reviewed project-repository source before enabling the configuration.
7. Omitted `sharing` means `env: disabled` and `skills: fallback`. `env` and
   `skills` are independent closed enums. Omitted `environment` means no
   passthrough. `passthrough` is a list of exact portable environment variable
   names; missing parent values are omitted, not synthesized. Reserved
   `KARDBRD_*` names, `KARDBRD_TOKEN`, `KARDBRD_API_URL`, and executor
   credential names are rejected. A stable wrapper is the normal way a project
   establishes proxy, XDG, or application settings; an explicit non-reserved
   passthrough is the narrow alternative.
8. `adoptions` defaults to an empty list. Each entry has all four nonempty
   fields shown. Card IDs and canonical resolved paths must each be unique;
   duplicate IDs, duplicate paths and aliases are errors. It is an administrator
   preflight manifest, not permission to adopt every `card-*` directory.

The daemon command timeout begins when an accepted command or mention acquires
active-card ownership, before ref resolution, checkout, sharing, bootstrap, or
prepare. It is the single overall deadline. A bootstrap, prepare, Git command,
and executor each receive the remaining root deadline; a hook is additionally
capped by its configured timeout. Preparation therefore cannot consume one
full timeout and then grant the executor another full timeout.

### Lifecycle configuration is restart-only

`worktree` in its entirety and the complete normalized command rules (including
action, execution, model and authorization filters) are lifecycle command policy.
They are immutable for one daemon process. On
`/reload`, the daemon first parses and strictly validates a complete candidate,
normalizes its lifecycle fingerprint and command registry, and compares them
with the running manager. A difference rejects the reload as a whole: schedules,
ordinary rules, and the old manager remain unchanged. An operator restart is
required to apply it.

For a candidate with the same fingerprint, ordinary non-command rule changes
and schedules retain reload support. The daemon validates and builds the new
rule engine and schedule set before either is published; a schedule-application
failure leaves both prior sets active. This avoids a new schedule or command
policy running with an old worktree manager.

## Source selection, new creation, and explicit adoption

For a newly created card worktree, the agent resolves the selected remote ref,
fetches only it, resolves `refs/remotes/<remote>/<branch>` to an object ID, and
runs `git worktree add` from that object without checking out, switching,
pulling, resetting, stashing, or otherwise modifying the shared base. A failed
fetch, missing remote HEAD, or ambiguous remote HEAD fails before creation.
Fetch, ref resolution and worktree/record administration share a repository
lock, so another card cannot change the selected ref between fetch and object-ID
capture. Release that lock before project hooks; keep per-card ownership through
the entire operation.

New worktrees use the full exact, case-preserving canonical card ID:

| Identity | New-worktree value |
| --- | --- |
| Path under configured worktree root | `card-<canonical-card-id>` |
| Branch | `card/<canonical-card-id>` |
| Lifecycle record | canonical ID, resolved path, base common Git directory, branch, origin `created`, and stage |

The lifecycle record is atomically written in the corresponding Git worktree
administrative directory, never into project source. Its stages are `created`,
`bootstrapping`, `preparing`, `ready`, and `failed`. It is an ownership record,
not a promise that source can be reset or removed.

Existing CBA worktrees are not required to use the new branch convention. An
administrator invokes an explicit adoption preflight for one manifest entry at
a time. Before it writes an `origin: adopted` lifecycle record, that preflight
must verify all of the following using the container-visible paths exactly as
recorded:

1. `path` is lexically within the configured worktree root, `Lstat` shows no
   symlink/escape, and the canonical resolved path equals the manifest path.
2. `git worktree list --porcelain` registers that exact present path; its Git
   common directory exactly equals both the configured base common Git directory
   and `common_git_dir` after canonical resolution.
3. the API card ID equals `card_id` exactly (case preserved), and Git's current
   branch equals `branch` exactly. The branch may be
   `fix/kVykJg0e-demo-fixture-boundary`, `republish/z4dEK101`, or another
   explicitly declared existing branch.
4. no lifecycle record already claims a different card/path/common directory,
   and the new record is atomically durable before preflight reports success.

Adoption also requires the administrator to verify that existing source is
materialized according to the project's checkout policy. Its initial record is
`origin: adopted`, `stage: created`, with materialization recorded as completed;
it does not claim application preparation occurred. The first ordinary use has
reason `reuse`, skips bootstrap and sharing changes, runs prepare only when
`on_reuse` is selected, and marks `ready` after that selected phase succeeds.
Until then, a stop command uses the base fallback described below.

It neither renames the directory nor branch, checks out, resets, rebases,
cleans, initializes, modifies staged/unstaged/untracked files, or touches
uploads/databases. Startup accepts an adopted worktree only after the same
checks and record validation. It never discovers or adopts an unlisted legacy
directory implicitly.

Ordinary reuse verifies lexical containment, `Lstat`, porcelain registration,
the same Git common directory, canonical card ID, branch, and lifecycle record.
Present but foreign, symlinked, escaped, wrong-common-directory, wrong-card,
wrong-branch, unregistered, or unmarked paths fail closed and are neither used,
repaired, pruned, adopted, nor removed.

`existing_or_base` makes the missing-source distinction explicit. It first
`Lstat`s the expected new path and an applicable adopted path. If neither source
path exists, it selects the base CWD even when `git worktree list` still has a
stale registration; it must not run `git worktree prune`, repair metadata, or
create/setup a worktree. If any candidate path is present, it must fully verify
its ownership record. Two distinct present candidates are an ambiguity error;
never choose one silently. A valid `ready` record selects that worktree. A valid owned
record with incomplete or failed preparation selects the trusted base CWD so
`/down` can stop existing resources without repairing setup. A malformed present
path or invalid record is an error, never a base fallback. The selected base is read-only by lifecycle contract;
repository skills still need their own safe behavior.

For Git-backed projects, `existing_or_base` requires the opt-in lifecycle block;
it cannot silently adopt legacy directories lacking records. A non-Git agent
may use it as a base-CWD-only policy with no Git or preparation operations.

## Checkout, sharing, and hooks

The exact create/recovery ordering is:

1. resolve/fetch the base and create the registered worktree; write `created`;
2. for `full`, Git has materialized tracked source; for `delegated`, write
   `bootstrapping`, run the administrator-installed stable bootstrap helper,
   and require it to materialize the intended tracked source before continuing;
3. re-verify worktree identity and inspect the selected Git index/tree for
   tracked `.env`, `.agents/skills`, and `.codex/skills` paths;
4. apply opted-in fallback sharing only after that materialization and only
   where the selected repository has no tracked path and the destination is
   absent; record created links;
5. if selected, write `preparing`, run prepare, then write `ready`; otherwise
   write `ready`.

The manager never infers that a repository lacks skills merely because a
directory was absent before a delegated checkout. Repository skills win in both
modes: a materializing bootstrap may create tracked `.agents/skills` or
`.codex/skills`, and the subsequent tracked-path inspection prevents a fallback
link from hiding it. `skills: fallback` links only the executor-specific skills
source when the source exists, the destination is absent, and Git reports no
repository-owned path there. `skills: disabled` links nothing.

`env: link` is likewise post-materialization and creation/recovery only: it
creates a base `.env` symlink only when the base source exists, Git reports no
tracked target, and the target is absent. It never replaces a file or link. A
pre-existing base `.env` link requires an explicit recorded adoption/acknowledge
step before a project enables `env: disabled`; the agent does not unlink it.
Ready reuse changes no sharing links. A failed bootstrap retries bootstrap;
a failed prepare retries only prepare; neither recovery cleans or resets source.

Bootstrap is solely the required delegated materializer. Full checkout forbids
it because no pre-checkout helper is needed. Prepare is optional in either mode
and obeys its independent `on_create` and `on_reuse` booleans. Incomplete
creation/recovery runs the phase that has not completed even if `on_reuse` is
false. A hook failure records `failed`, retains source, blocks executor start,
and is retryable without data loss.

Both hook types use direct argv, a verified absolute worktree CWD, a dedicated
process group, and this minimal environment only:

| Variable | Value |
| --- | --- |
| `PATH`, `HOME`, `TMPDIR`, `TMP`, `TEMP`, `TZ`, `LANG`, `LC_*` | inherited only when present |
| configured `environment.passthrough` names | inherited only when present and valid |
| `KARDBRD_CARD_ID` | exact canonical card ID |
| `KARDBRD_BOARD_ID` | configured board ID |
| `KARDBRD_WORKTREE_PATH` | verified absolute worktree path |
| `KARDBRD_WORKTREE_REASON` | `create` or `reuse` |
| `KARDBRD_WORKTREE_PHASE` | `bootstrap` or `prepare` |

No other daemon variables are inherited. In particular hooks never receive
Kardbrd API/token values or executor credentials by ambient inheritance, and
diagnostics never print environment values. Captured output is limited to 64
KiB; at most 2 KiB of redacted, fence-safe output may be reported.

On normal exit or hook failure, the manager reaps the direct child and terminates
any surviving group members. On timeout, cancellation, stop or Done handoff,
it signals the group before waiting, escalates after a bounded grace period,
reaps the child, and confirms the group is gone before releasing
same-card ownership or closing `ActiveSession.Done`. A context-aware runner is
therefore required for Git and hooks. #67 may claim cleanup, cancel the prior
session, and wait on that completion signal only after descendants cannot keep
mutating a card environment.

## Exact comment-command path, queue, and Done ordering

Command recognition runs after bot-authored suppression and bot-card
administrative slash handling, but before normal mention dispatch and generic
rules for an ordinary card. It recognizes only either of these complete texts
after outer whitespace is trimmed:

```text
/down
@ThisAgent /down
```

The optional leading mention must name this agent exactly under the existing
case-insensitive mention comparison and be followed by whitespace. The command
must be the rest of the text. Thus prose, quoted text, blockquotes, fenced code,
`/up now`, `/upwards`, a trailing mention, multiple leading mentions, and
`@OtherAgent /down` do not match. An unregistered command is not claimed and
continues through existing mention/rule behavior. A registered exact command
targeted at this agent or bare is claimed even when authorization fails: the
agent posts a visible policy-denied result and returns, so `@ThisAgent /down`
cannot fall through to an ordinary prepare path or double execute.

An accepted command has exactly one owning agent per board in v1 (the board is
the only command scope). Ownership is an administrator deployment invariant,
not skill discovery: rollout preflight inventories workers across hosts and
records one owner per board/command before enabling routes. V1 has no distributed
owner registry; a daemon cannot discover arbitrary remote configurations. Within one
file, any second `comment_command` with the same normalized token is invalid,
even if its filters appear disjoint. A command rule may have normal filters for
authorization, but generic matching rules never run for a claimed command;
they run only for non-command comments. This makes overlapping fuzzy filters
deterministic and prevents duplicate side effects.

The command rule supplies a static `action`; comment text is never shell input.
`execution: prepare` (the default) uses full lifecycle preparation.
`existing_or_base` calls the read-only resolver described above and never calls
create, bootstrap, prepare, sharing, legacy setup, metadata repair, or prune.
It is the minimal generic policy for a repository-owned `/down`; it neither
publishes nor retires the worktree. Done cleanup remains separate.

Commands use a per-card FIFO of eight accepted entries plus one active entry.
All starts retain the existing global concurrency cap, including ordinary coding
and command execution. Waiting entries do not bypass that cap; the action deadline
starts only when a worker slot and active-card ownership are acquired.
Under the manager lock, an authorized exact event reserves the key
`(board_id, card_id, comment_id, normalized_command)` before it can run or
queue. The lock's monotonically increasing claim sequence defines FIFO order.

| Situation | Deterministic behavior |
| --- | --- |
| No active command/session | mark reservation `running` and start it. |
| Active coding or command; fewer than eight queued commands | append one immutable rule/action/context snapshot; post one `Command queued (position N)` acknowledgement. |
| Queue full | mark the event `rejected_full`; post one visible busy/full response; do not overwrite or evict a queued command. |
| Redelivery of same comment | find the reservation and do no work or duplicate acknowledgement. |
| Command success/failure/auth/preparation error | mark terminal; never automatically retry that comment. A later explicit comment has a new comment ID and is independently retryable. |
| Terminal dedup retention | retain terminal records for 24 hours in a 4,096-entry LRU; prune only terminal records. Running/queued records never expire. |

This is deduplication of deliveries, not a promise of exactly-once external
side effects across a daemon crash; repository `/up` and `/down` remain
idempotent. If the terminal LRU is full, the oldest terminal record is evicted;
it is never used to displace an active or queued reservation.

Before starting either an immediate or a dequeued command, the manager reads
current card state and rechecks authorization filters and the configured
Done-cleanup lists. Denied/Done requests get a terminal visible result; read
failure starts no work. This applies after cleanup finishes too. When #67 claims Done ownership, it
cancels the active session, atomically drains the complete per-card command
FIFO, marks each reservation `canceled_done`, posts one cancellation result per
command, and waits for the active hook/executor process group to be gone. A
late command claim sees cleanup ownership and is visibly rejected. Only after
that wait does #67 recheck authoritative card state and run its direct cleanup
argv. No queued stale `/up` can run or republish after Done cleanup; a later
new comment after the card leaves Done is a new explicitly authorized request.

## Resolved readiness findings and remaining gates

| Finding | Proposed resolution | Evidence required before implementation approval |
| --- | --- | --- |
| Existing feature/republication branches rejected | Bounded administrator `adoptions` preflight persists verified records and preserves declared path/branch/edits. | Real custom-branch adoption; independent ID/path uniqueness; initial adoption stage; dirty/staged/untracked preservation. |
| Delegated sharing could hide skills | Materialize first, inspect tracked paths after materialization, then fallback-link; defaults are explicit. | A real bootstrap that creates tracked skill directories, proving repository skills win. |
| Busy `/up`/`/down` silently lost | Eight-entry FIFO, visible acknowledgment/full response, immutable reservation states, terminal TTL, and Done drain. | Ordered active-coding races, redelivery, full queue, failure/new-comment retry, and Done-with-queued-up tests. |
| Reload mixed generations | Strict atomic candidate validation; lifecycle and command policy restart-only; ordinary rules/schedules still atomically reload. | Load/start/reload malformed-field and changed-fingerprint tests, plus valid ordinary/schedule reload. |
| Command falls through mention/rules | Exact whole-comment parser, administrative single-owner preflight, policy result, dispatch return, and command precedence. | Bare/mentioned/wrong-agent/prose/code-block/authorization/overlap routing tests; immediate/dequeued Done checks and global concurrency. |
| Stop after failed preparation | Verified incomplete/failed ownership selects the base helper without preparation. | Failed-init then down; down before first adopted use; missing source with stale registration; foreign present path remains rejected. |
| Preparation exceeds execution window or leaves children | One root deadline includes prep and executor; context-aware process-group cleanup precedes ownership release. | Timeout-budget and descendant tests on normal/failure/cancel/Done. |
| Hooks receive ambient secrets | Minimal allowlist, fixed ABI, denied reserved passthrough, and stable admin-installed wrappers. | Parent credential-absence and explicit passthrough validation tests. |

The remaining gates are project-owned, not generic design omissions: CBA must
implement and test `cba-env` plus idempotent `/up`/`/down`; its administrator
must install the reviewed stable wrapper and record real container paths;
projects must prove existing setup is reuse-safe; and release, Docker-image,
single-worker handover, pilot, rollback, CBA VPS, and stopped-agent policies
remain separate approved rollout work. The design is ready for implementation. This card stays in **Review**;
implementation tests, a reviewed PR, release and rollout evidence remain pending.
