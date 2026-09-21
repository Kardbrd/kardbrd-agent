# Portable worktree lifecycle — code review

**Card:** M4PkGAV6
**Reviewed range:** `d01e8b0..card/M4PkGAV6`
**Scope:** generic Go lifecycle implementation only. The reviewed contract
documents under `docs/proposals/portable-worktree-*.md` were preserved
unchanged. No project adapter, release, installation, restart, migration,
credential, or live-agent change was reviewed or performed.

## Stage 1 — specification compliance

The reviewed lifecycle and migration documents define 19 bounded work packages.
All are implemented in the proposed branch.

| Planned area | Evidence | Result |
| --- | --- | --- |
| Typed strict config and defaults | `internal/rules/{types,load,lifecycle,validate}.go` and loader fixtures | PASS |
| Lifecycle/reload compatibility | `internal/cli/agent_commands.go`, `internal/agent/{manager,events}.go` | PASS |
| Immutable Git source selection and ownership | `internal/worktree/lifecycle_manager.go` plus real Git tests | PASS |
| Full/delegated materialization, sharing and hooks | lifecycle manager and hook/process tests | PASS |
| Exact commands, FIFO, dedup and #67 precedence | `internal/agent/{commands,events,manager}.go` and manager tests | PASS |
| Public config/CLI documentation | `docs/configuration/worktree-lifecycle.md` and linked docs | PASS |

The implementation retains the legacy worktree manager whenever `worktree` is
absent, keeps #67 direct Done cleanup as the retirement path, and excludes CBA
helper/skill behavior from the binary. No unplanned runtime-platform or
application behavior is included. **Stage 1 verdict: PASS.**

## Stage 2 — independent specialized review

| Perspective | Reviewer | Verdict | Review result |
| --- | --- | --- | --- |
| Security | krs | PASS | Validated direct argv/hook environment, ref and remote grammar, Git/path ownership, command routing and visible redacted failures. |
| Code quality | krc | PASS | Validated recovery phases, fresh authorization, env-link acknowledgement, Unix Git lock, deadline validation and adoption aliases. |
| Test/documentation | krt/krd | PASS | Validated real Git/routing/race coverage and the public docs/nav/CLI references. |
| UX/configuration | kru | PASS | Reviewed exact command feedback, adoption help, fixture validity and operator docs. |

Initial independent findings were all treated as blocking and fixed before this
review record was finalized:

1. Exact command rules could reach fuzzy generic matching for non-exact text.
   `rules.Engine.Match` now excludes `comment_command` rules, and a wrong-agent
   regression proves no fallback execution.
2. A dequeued command discarded lifecycle errors. The handoff now posts the
   same redacted bounded `Command Error` feedback as an immediate command.
3. Ref validation admitted control bytes. Branch parsing now rejects every
   control/space byte and fixtures cover YAML-encoded tabs.
4. Queued authorization used stale title data. Recheck now obtains the current
   title, list, labels and assignee before the rule is evaluated.
5. A created but failed lifecycle could be treated as ordinary reuse. Recovery
   retains the initial `create` reason until it reaches `ready`.
6. Schedule and rule reload could interleave between the watcher and `/reload`.
   `ReloadAndApply` serializes load/validation/schedule update/rule swap; its
   deterministic concurrency test proves the final rules and schedules come
   from the same candidate.
7. Adoption uniqueness previously considered only lexical paths. Lifecycle
   startup now resolves declared sources and rejects absent, non-canonical, and
   symlink-alias entries before it can own a card.
8. The canonical no-passthrough example used `passthrough: []`, while strict
   parsing treated every string list as nonempty. Environment passthrough now
   accepts an explicitly empty list; hook argv remains nonempty, and a built
   CLI validation fixture proves the documented configuration loads.
9. Queue acknowledgements calculated position after releasing the claim lock.
   Reservation now captures the FIFO position atomically, with a concurrent
   race regression proving the two acknowledgements are positions one and two.

## Stage 3 — synthesis

All Critical and Important findings from security, quality, test/docs, and
UX/configuration review were corrected and re-reviewed as PASS. The UX
advisory was also documented: explicit authorized commands continue while
generic rule automation is paused. There are no open blocking findings in the
lifecycle implementation.

File-level review notes:

- `internal/rules`: opt-in strict decoding and restart-only policy fingerprint
  preserve legacy behavior while rejecting malformed new contract fields.
- `internal/worktree`: fetches one remote object ID without mutating the base,
  persists ownership in Git administration, verifies adoption/foreign paths,
  and terminates/reaps lifecycle process groups.
- `internal/agent` and `internal/cli`: claim exact commands before normal
  dispatch, retain global concurrency with an eight-entry FIFO, recheck current
  authority/Done state, drain on #67 cleanup, and serialize reload handoff.
- `docs/configuration`: documents lifecycle opt-in, direct-argv helpers,
  `existing_or_base`, adoption, exact command matching, and restart-only
  reloads.

## Verification reviewed

```text
/usr/local/go/bin/go test ./... -count=1
PASS: all packages

/usr/local/go/bin/go vet ./...
PASS: no diagnostics

PATH=/usr/local/go/bin:$PATH pre-commit run --all-files
PASS: all configured hooks

/usr/local/go/bin/go test -race ./internal/agent ./internal/worktree ./internal/rules ./internal/cli ./internal/scheduler
PASS: all selected race packages

/tmp/.../kardbrd agent validate testdata/rules/worktree-lifecycle.yml
PASS: built CLI lifecycle fixture
```

`mkdocs` is not installed in this environment; the documentation source is
covered by the configured YAML, whitespace, spelling, and link/navigation
review instead. This is not a release or rollout approval: merge must follow
the A4QOQnmB adapter fix, then this branch must be rebased on resulting `main`
and checked again. CBA helpers, release publication, and staged migration gates
remain owned by their authorized follow-up work.
