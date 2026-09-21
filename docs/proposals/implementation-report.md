# Portable worktree lifecycle — implementation report

**Card:** M4PkGAV6
**Branch commit:** `2e90133` (based on `d01e8b0`, which includes #67)
**Scope:** generic Kardbrd lifecycle only; no project adapters, runtime changes,
release, installation, restart, migration, credentials, or live agents changed.

## Implemented work

1. Added strictly decoded opt-in `worktree` and exact command-rule schema with
   reviewed defaults, relationship checks, legacy setup conflict handling,
   restart-only lifecycle/command fingerprints, and `agent validate` coverage.
2. Added an isolated lifecycle manager that fetches a configured remote ref or
   remote HEAD to an object ID without mutating the base checkout; it creates
   full-ID paths/branches, persists atomic Git-administrative ownership records,
   verifies existing ownership, supports explicit `agent adopt-worktree`, and
   implements `existing_or_base` fail-closed selection.
3. Added full/delegated materialization order, independent sharing policies,
   verified direct-argv helpers with a minimal environment, total context budget,
   process groups, bounded/redacted diagnostics, and retry-safe records.
4. Added exact normal-card command routing before mentions/rules, command
   authorization/state rechecks, a global-cap-respecting eight-entry FIFO,
   delivery deduplication, terminal retention, visible queue/full/denied states,
   and #67 Done draining/cancellation behavior.
5. Preserved the legacy manager for absent lifecycle configuration. Non-Git
   `existing_or_base` explicitly selects only its base CWD; it cannot prepare.

## Verification

Executed on this branch after the final implementation commit:

```text
/usr/local/go/bin/go test ./...
PASS: all packages

/usr/local/go/bin/go vet ./...
PASS: no diagnostics

PATH=/usr/local/go/bin:$PATH pre-commit run --all-files
PASS: whitespace, YAML, conflict/case checks, codespell, kardbrd.yml validation

/usr/local/go/bin/go test -race ./internal/agent ./internal/worktree ./internal/rules ./internal/cli
PASS: agent, worktree, rules, CLI
```

The focused tests include real Git remote/ref creation with a dirty feature base,
adoption preserving dirty custom branches, delegated helper materialization with
repository skills winning, minimal hook environment, descendant cancellation,
exact command precedence, FIFO ordering, Done draining, current Done rejection,
and restart-only policy rejection.

## Deliberate scope boundaries

The CBA `cba-env` helper and `/up`/`/down` skill semantics remain in the CBA
repository. No service/platform/migration behavior was changed. This branch does
not merge itself, release software, install a binary, restart an agent, or change
runtime configuration. The requested merge order remains: Codex A4QOQnmB first,
then rebase this branch on the resulting `main` and re-run checks.
