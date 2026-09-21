# Portable worktree lifecycle — implementation-readiness review notes

**Review status:** independently reviewed and ready for implementation as of
2026-09-21 UTC. This card remains design-only and in **Review**; no runtime
implementation or migration is claimed.

## Immutable source record

| Role | Revision | Use |
| --- | --- | --- |
| Generated card worktree | `ffb1395b59729231c4fb2348d117a80b98fd2b8e` | Documentation destination only. |
| Authoritative main with PR #67 | `d01e8b018569cff216040c2047884cb46c776291` | Sole implementation source inspected for these recommendations. |

Review used `git show d01e8b018569cff216040c2047884cb46c776291:<path>`.
No canonical checkout, runtime, CLI installation, service, release, secret, or
live project configuration was changed.

## Source-review findings incorporated

The revised documents directly address the independent source review:

- `events.go:24-69` currently permits mention dispatch then generic rules, so
  exact commands are claimed between bot-card handling and both of those paths,
  and return once claimed.
- `manager.go:230-280` and `events.go:395-455` call `Worktree.Create` before executor
  dispatch for both mentions and rules. The proposed `existing_or_base` bypasses
  the complete create/setup/share path, not merely the final hook.
- `manager.go:192-375` has a replaceable last pending mention and
  `events.go:401-408` silently ignores busy rules. Proposed explicit commands
  use immutable reservations and a bounded FIFO with visible acknowledgements.
- `worktree.go:18-20` has no context-aware runner. Proposed lifecycle Git/hooks
  use the root action context, process groups, and descendant cleanup before
  releasing ownership to #67.
- `agent_commands.go:236-267,302-323` reloads rules/schedules without replacing
  the startup worktree manager. Lifecycle and command policy are now
  restart-only; reload candidates are strict and atomic.

## Final proposed schema and defaults

The canonical literal schema is repeated identically in:

- `docs/proposals/portable-worktree-lifecycle.md`
- `docs/proposals/portable-worktree-migration.md`

Its public keys are `worktree.base`, `helpers.stable_root`, `checkout.mode` and
delegated `bootstrap`, optional `prepare` with independent `on_create` and
`on_reuse`, `sharing.env`/`sharing.skills`, `environment.passthrough`, bounded
`adoptions`, and rule `comment_command`/`execution`.

The lifecycle block is opt-in. Omitting it retains legacy behavior; null and
unknown opt-in fields are errors. Defaults are remote `origin` plus
unambiguous remote HEAD, full checkout, 900-second declared hook caps,
prepare-on-create true, prepare-on-reuse false, env sharing disabled, skills
fallback enabled, no environment passthrough, and no adoptions. New lifecycle
and command policy are restart-only. Full checkout forbids bootstrap; delegated
checkout requires the administrator-installed stable bootstrap helper.

The hook ABI is minimal: portable process variables, explicit non-reserved
passthrough, and fixed card/board/path/reason/phase context only. It does not
inherit Kardbrd API/token or executor credentials. A stable wrapper installed
by the administrator from reviewed repository source is responsible for project
proxy/XDG/runtime setup. Base fallback is a read-only agent contract, not a
sandbox.

## Resolved readiness findings

| Readiness concern | Proposed resolution |
| --- | --- |
| Existing CBA branches | Explicit bounded adoption preflight verifies exact present path, Git registration/common directory, canonical card ID, and declared branch before an adopted record is written. It preserves `fix/kVykJg0e-demo-fixture-boundary`, `republish/z4dEK101`, edits, data, and names. |
| Stale, incomplete and foreign source | `existing_or_base` uses base when source is absent, even with stale registration, or when verified owned preparation is incomplete/failed. A present foreign/malformed path fails closed without prune/repair. |
| Delegated skills order | Full/delegated materialize first; then Git tracked-path inspection selects fallback sharing. A pre-checkout missing directory is never treated as proof that repo skills do not exist. |
| Creation/reuse and sharing defaults | Prepare selection is independently `on_create`/`on_reuse`; env/skills sharing are independent and explicit. Existing env links require recorded acknowledgement and are never overwritten/unlinked. |
| Busy command behavior | One active command plus eight FIFO entries; reservation/delivery dedup, visible queue/full outcomes, 24-hour terminal retention, failure without auto-retry, and retry through a later comment ID. |
| Done interaction | #67 atomically cancels/drains queued commands as `canceled_done`, waits for child cleanup, rechecks state, then directly cleans. A stale queued `/up` cannot republish. |
| Command dispatch/authorization | Exact bare or leading-this-agent grammar, administrator single-owner preflight across hosts, static action, current card authorization before immediate/dequeued execution, and return-before-mention/rules. No distributed owner registry is assumed. |
| Reload/strictness | Strict new-field parsing at all entry points; candidate fingerprint rejection avoids partial hot swap, while unchanged-policy ordinary rules/schedules still reload. |
| Deadline/process contract | One root action deadline covers prep and executor. Context-aware process groups clean descendants before session ownership releases to #67. |

Final supervisor clarifications: immediate commands are rejected while the card
is Done even after cleanup finishes; queued commands preserve the global
concurrency cap; complete command-rule changes require restart; adoption requires
independent card/path uniqueness and begins at `created` with materialization
verified, without claiming application setup. First ordinary adopted reuse honors
`on_reuse`; stop uses the base until ready. Git-backed `existing_or_base` requires
the opt-in lifecycle, while non-Git agents can select base only. The companion
contract and implementation plan include these clarifications and their tests.

## Remaining project-owned rollout gates

1. CBA separately implements and proves `cba-env` plus idempotent `/up`/`/down`
   behavior; its administrator installs a reviewed stable helper and preserves
   the external Sentry schedule.
2. Each project separately records real container-visible CWD/mount/worktree
   paths, existing links, dirty worktrees, runtime/process versions, rules,
   schedules, subscriptions, and reuse effects without copying secret values.
3. A disposable Website pilot precedes one-project-at-a-time adoption. Web uses
   its actual `runtime-main` mount; DSO/Trading prove their existing setup
   commands safe on reuse. Agent migration waits for another project to validate
   the released support.
4. A separately approved tagged release includes lifecycle support and #67.
   Docker images use verified published assets; board handover drains one
   subscription at a time and retains rollback artifacts/source/data.
5. Mobile/ClientBot remain stopped, Phoenix/LiveBot remain retired, HR remains
   non-Git, and the CBA VPS cutover remains its own established migration.

No runtime implementation, PR, release, migration, test rerun, or live action
has occurred. The detailed evidence matrix and future test commands are in the
implementation plan; no unchanged full Go test suite was rerun for this
documentation-only revision.

## Independent acceptance evidence

The supervisor read all three revised documents, compared the source claims
against an immutable `d01e8b0` snapshot, and consolidated the final edge cases
from card comment `BwlJ3prL`. The two literal YAML examples are identical and
parse successfully; this checks proposed-document syntax, not support in the
currently installed binary. The worktree contains only `docs/proposals/`
additions. Existing unrelated canonical-checkout edits were preserved.

All six findings in `ZwgJ20kq`, the restricted-environment correction in
`arDZ8QwR`, and the final edge cases are resolved in the contract and mapped to
future acceptance tests. Bot execution ended with a missing-final-response
status; acceptance rests on inspection of the actual files and source, not that
executor status. No implementation or live-runtime checks are represented as
passing. The VPS cutover/runbook now requires a published compatible release
and the separately reviewed CBA repository adapter.
