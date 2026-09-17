# Personal operations worker

`kardbrd worker` is an opt-in, board-scoped durable worker for explicitly delegated personal operations. It is separate from `kardbrd agent start`: it does not load `kardbrd.yml`, does not create coding worktrees, and does not use the coding executors or their approval-bypass flags.

This is an operator-reviewed integration point, not a Gmail, Calendar, browser, inbox, or notification-provider implementation. The installed binary has no provider connectors. Do not replace an existing host watcher with it until the activation checklist at the end of this page is reviewed.

## Durable protocol

The worker owns exactly one literal metadata key, `ops`. It does not rewrite the metadata root, so unrelated keys and their JSON values remain untouched. The server writes that key with `expected_revision` CAS; a conflict is inspected, never blindly refreshed and retried as a claim.

```json
{
  "ops": {
    "version": 1,
    "goal": "Exact operator-delegated goal",
    "completion_criteria": "What success means",
    "delegation": {
      "delegated": true,
      "authorization": {"allowed_actions": ["synthetic-example"]}
    },
    "state": "ready",
    "sources": [{"id": "operator-request-1", "reference": "local fixture"}],
    "action": {
      "id": "action-1",
      "intent": {"kind": "operator-reviewed external action"}
    }
  }
}
```

Supported states are `suggested`, `ready`, `scheduled`, `running`, `waiting_event`, `waiting_user`, `completed`, `cancelled`, `paused`, and `needs_review`.

- Only explicitly delegated `ready` and due `scheduled` records can run.
- `suggested` records are proposals, never executable metadata. A runner cannot grant delegation or broaden authorization.
- A `running` record contains a random run ID, owner token, authorization/action fence, heartbeat, and lease expiry. The worker renews or finishes only a matching, unexpired claim whose current authorization and action intent still match that fence.
- A run timeout, expired lease, lost owner, cancelled/revoked delegation, malformed/oversized adapter result, or ambiguous write becomes `needs_review`; the worker does not automatically perform the external action a second time.
- `outcome`, action receipt, decision, journal, notice ID/state, event receipts, and wake time are retained in `ops`. Metadata activity is the machine journal; comments are reserved for meaningful completion, questions, and uncertainty.

An external system without idempotency or fencing cannot promise exactly-once effects. Use an action ID and external receipt where available, and reconcile `needs_review` manually before a new delegation.

## Commands and configuration

Every command requires an explicit `--board-id`. Commands that execute a delegated task require a trusted operator-selected `--runner`; `check` is read-only and needs no runner. `check` reports each recognized card ID, durable state, and wake time in addition to aggregates. `run-once` reports `processed` rather than incorrectly calling waits or schedules “completed.” `run-once` and `serve` validate worker identity, poll interval, lease, timeout, concurrency, artifact directory, packet limit, output limit, and notice timeout.

```bash
# Read-only: reports recognized/due cards and changes nothing.
kardbrd worker check --board-id PERSONAL_BOARD

# Compile safe local fixtures for an end-to-end dry run.
go build -o /tmp/kardbrd-fixture-runner ./examples/personal-worker/runner
go build -o /tmp/kardbrd-fixture-observer ./examples/personal-worker/observer

# Enroll a synthetic card. Authorization is required and is data, not a flag
# that the runner may change. Action intent is persisted before any runner call.
kardbrd worker enroll SYNTHETIC_CARD --board-id PERSONAL_BOARD \
  --goal 'Complete the synthetic fixture' \
  --completion-criteria 'A fixture receipt is recorded' \
  --authorization '{"allowed_actions":["fixture"]}' \
  --source fixture-request-1=fixture://request-1 \
  --action-id fixture-action-1 \
  --action-intent '{"kind":"fixture"}'

# Inspect due work, then run a single pass. This works from a non-Git directory.
kardbrd worker check --board-id PERSONAL_BOARD
kardbrd worker run-once --board-id PERSONAL_BOARD --worker-id agents-local-fixture \
  --runner /tmp/kardbrd-fixture-runner --artifact-dir /var/tmp/kardbrd-worker-artifacts \
  --lease 2m --timeout 5m --max-concurrent 1 --poll-interval 30s \
  --packet-limit 65536 --output-limit 65536 --notice-timeout 30s

# Inspect the durable state and human journal.
kardbrd card metadata get SYNTHETIC_CARD ops
kardbrd md card SYNTHETIC_CARD
```

`serve` runs exactly one board-wide polling loop. It is appropriate under an operator's service manager after review; it does not create a scheduler job per card:

```bash
kardbrd worker serve --board-id PERSONAL_BOARD --worker-id agents-local-fixture \
  --runner /trusted/host/bridge --artifact-dir /var/tmp/kardbrd-worker-artifacts \
  --poll-interval 30s --lease 2m --timeout 5m --max-concurrent 2
# Stop with SIGTERM or Ctrl-C. The current lease is allowed to expire/reconcile;
# never delete metadata to force a rerun.
```

An observer is optional in `serve`, but when configured it runs on that same board-wide pass after due delegated work. It needs its own trusted argv, a pre-initialized registry, and an explicit destination list. `run-once` rejects observer configuration rather than silently ignoring it.

```bash
kardbrd worker serve --board-id PERSONAL_BOARD --worker-id agents-local-fixture \
  --runner /trusted/host/bridge --artifact-dir /var/tmp/kardbrd-worker-artifacts \
  --observer /trusted/read-only-observer \
  --suggestion-registry-card SUGGESTION_REGISTRY_CARD \
  --observer-list-id SUGGESTIONS_LIST --observer-timeout 2m
```

## Runner bridge contract

The runner command and argv are trusted operator configuration. Card title, card Markdown, email-like text, proposals, and source references are JSON data sent through stdin; no shell interpolation selects a command. Each invocation gets an isolated `0700` artifact directory named by run ID. Packet/result files and bounded stdout/stderr are `0600` and should be retained only under the operator's retention policy.

The runner receives a JSON packet with exact goal, completion criteria, authorization, latest card context, run ID, sources, decision/action receipts, and allowed result values. The packet is rejected before artifact write or process start if it exceeds `--packet-limit`; stdout and stderr are independently bounded by `--output-limit`. It must write exactly one strict JSON object to stdout, including the exact `run_id` received in its packet:

```json
{"run_id":"packet-run-id","status":"completed","summary":"Human-readable result","receipt_id":"stable-receipt"}
```

The only statuses are `completed`, `scheduled` (requires a future `wake_at`), `waiting_event` (requires `event_id`), `waiting_user` (requires `decision_prompt`), and `needs_review`. An action-bearing packet also requires `action_status`: `not_started`, `performed` with a matching pre-enrolled action receipt, or `uncertain` with `needs_review`. Authorization, delegation, runner command, and action intent are not output fields.

Adapters receive a minimal fixed environment (`PATH` and `LANG`) rather than an inherited environment: no Kardbrd token, provider key, cloud credential, or executor credential is copied. On supported POSIX hosts they run as one process group, which is terminated/reaped on timeout, cancellation, output overflow, and normal completion so background children cannot escape a lease; do not activate an adapter on an unsupported host until equivalent descendant cleanup is independently reviewed. A host-side Codex/CLI bridge must use its own separately provisioned connection (for example an operator-owned configuration file or socket) and policy; the worker does not infer connector availability. The fixture runner is the complete, safe reference adapter.

## Scheduled steps, events, and decisions

Pass `--runner-arg=scheduled`, `waiting_event`, `waiting_user`, or `needs_review` when invoking the fixture runner to exercise each transition. Due `scheduled` cards re-enter the one worker loop at `wake_at`; no separate cron job is created. A past returned wake time is rejected into `needs_review` rather than immediately re-running an adapter.

```bash
# Record an external event exactly once for the card that awaits it.
kardbrd worker wake SYNTHETIC_CARD --board-id PERSONAL_BOARD --event-id fixture-event-1
kardbrd worker wake SYNTHETIC_CARD --board-id PERSONAL_BOARD --event-id fixture-event-1

# Read the current `ops.decision.id`, then record a decision for exactly that
# question. Repeating the same (already consumed) ID is a no-op; an ID for a
# different/older question is rejected and cannot unblock a new wait.
kardbrd worker decide SYNTHETIC_CARD --board-id PERSONAL_BOARD \
  --decision-id CURRENT_OPS_DECISION_ID --value '{"approved":true}'
```

Event and decision receipt histories retain at most 64 stable IDs each. At capacity, a new receipt is rejected without changing state or evicting older deduplication evidence; reconcile or enroll a new bounded task rather than treating an old event as new work.

## Read-only suggestion ingestion

Observers are separate from runners. A configured observer may read its own permitted source and emit bounded events (the `--observer-max-events` default is 100) with stable source IDs, title, reference, and proposal. It has no action-runner capability or inherited credentials by default. Before ingesting, initialize one paused, operator-selected registry card; its CAS-protected `ops.suggestion_claims` reserves each source ID before a proposal card is created. A failed/ambiguous create is held as `needs_review`, never re-created automatically. The worker creates only `suggested`, non-delegated cards.

```bash
kardbrd worker registry SUGGESTION_REGISTRY_CARD --board-id PERSONAL_BOARD
kardbrd worker ingest --board-id PERSONAL_BOARD --list-id SUGGESTIONS_LIST \
  --suggestion-registry-card SUGGESTION_REGISTRY_CARD \
  --observer /tmp/kardbrd-fixture-observer --observer-timeout 2m
kardbrd worker ingest --board-id PERSONAL_BOARD --list-id SUGGESTIONS_LIST \
  --suggestion-registry-card SUGGESTION_REGISTRY_CARD \
  --observer /tmp/kardbrd-fixture-observer --observer-timeout 2m
```

This command does not invoke `--runner`, cannot self-approve a proposal, and provides no built-in Gmail connection. To make a suggestion executable, an operator must explicitly run `worker enroll` on that specific `suggested` card with the new goal, criteria, and authorization; the proposal's source references are retained unless replacement `--source` values are supplied. Preserve the existing host Gmail/Calendar watcher until a separately reviewed cutover.

## Notices, reconciliation, and rollback

Meaningful results first receive a stable journal/notice ID in metadata. The worker posts one card comment with a one-attempt request. A comment is not notification delivery. A committed outcome whose metadata response was lost remains `journal.state=prepared`; the next pass may safely claim and send that never-started journal item. Before any comment or notification request it durably changes that item to `sending`; a restart from `sending` or `unknown` is held for reconciliation and is never blindly replayed. A successful comment response without its actual ID is also `unknown`, never a fabricated receipt.

With `--notice-command`, repeated `--notice-arg`, and a bounded `--notice-timeout`, a trusted notification adapter receives a `NoticePacket`. For a `waiting_user` outcome it also receives that exact pending `decision_id`, so a host relay can route the displayed question and later reply precisely. The adapter must echo a bounded nonempty `notice_id` and `receipt_id`:

```json
{"notice_id":"packet-notice-id","receipt_id":"stable-id","delivery_state":"queued"}
```

`delivery_state` is either `queued` or `delivered`; omission remains compatible with synchronous adapters and means `delivered`. `queued` means only that transport accepted the notice. The worker stores `notice.state=queued` and `queue_receipt_id=stable-id`, with no delivery `receipt_id`, and never resends it in `run-once`. `delivered` preserves the synchronous behavior: `notice.state=delivered` with the final `receipt_id`. Invalid adapter receipts, including a different notice ID or delivery state, become `unknown` rather than delivery proof.

When a relay later verifies actual UI display, an operator records it without calling a runner, notifier, or comment endpoint:

```bash
kardbrd worker notice-receipt SYNTHETIC_CARD --board-id PERSONAL_BOARD \
  --notice-id notice-run-123 --queue-receipt-id host-queue-456 \
  --receipt-id host-delivery-789
```

The command uses revision CAS and matches the exact current or retained unresolved notice. A queued notice must match its stored queue receipt exactly. It can explicitly reconcile a `sending` or `unknown` notice that has no known queue receipt; it refuses `prepared`, `not_configured`, missing, or mismatched notices. It keeps both queue and final receipts on delivery. Repeating the exact acknowledgement is a no-op, while a different final receipt cannot rewrite delivered evidence. Notifier completion uses the same exact-ID, monotonic CAS merge: a late queue result, adapter error, or malformed receipt cannot downgrade retained delivery proof; different non-empty transport receipts are held and reported for reconciliation. In that conflict case, `worker run-once` returns a receipt-conflict error without changing the already authoritative evidence; inspect `ops.notice` or the matching item in `ops.unresolved_notices`, reconcile it with the relay, then record only a verified exact receipt. When a later task step replaces a queued, sending, or unknown current notice, the old evidence is retained in `unresolved_notices`; acknowledgements always re-find the requested ID after a conflict and cannot alter a different current notice.

To inspect an uncertainty, use `kardbrd card metadata get CARD_ID ops` and its card activity/comment history. Confirm external action receipts before a human explicitly resolves it. Do not alter a `running` claim from another owner.

Rollback is operational, not destructive: stop `worker serve`, retain per-run artifacts and `ops` metadata for reconciliation, and remove only the operator's service configuration after review. Do not delete card records, remove receipts, or turn an uncertain run into `ready` automatically.

## Live activation checklist

1. Select a personal board and token with only the necessary card/metadata/comment permissions.
2. Review a trusted host-side runner and, separately, any observer/notification adapter; verify no token forwarding or approval bypass is present.
3. Compile/run the fixture flow on synthetic cards and inspect metadata, activity, artifacts, and notice state.
4. Establish artifact retention/access controls and an on-call reconciliation owner.
5. Obtain separate approval for each provider connector or a concierge cutover. This repository change supplies neither.
6. Start the worker under a supervised host service, observe a bounded rollout, and keep the old watcher until the approved cutover is complete.

## Acceptance matrix

| Card scope | Implementation and executable proof |
| --- | --- |
| 1. isolated commands/configuration | `internal/cli/worker.go`, `TestWorkerRunOnceCompiledCLIHTTPAndSubprocess` |
| 2. versioned metadata/due filtering | `internal/worker/types.go`, `TestParseRecordRejectsUnknownVersionAndSuggestionPromotion` |
| 3. CAS claims/reconciliation | `internal/worker/service.go`, cross-process barrier E2E, `TestExpiredCompletionCannotCommit`, `TestAuthorizationFenceChangeCancelsRunner` |
| 4. bounded subprocess contract | `internal/worker/subprocess.go`, packet/strict-output/environment tests including missing/mismatched `run_id`, cancellation and successful-child cleanup tests |
| 5. wakeups/decisions/action receipts | `internal/worker/operations.go`, future-wake/action-receipt tests, stable current-question IDs, replay rejection, and non-evicting receipt-capacity tests |
| 6. suggestions-only boundary | `SubprocessObserver`, fixture observer, CAS registry concurrent/ambiguous-ingestion tests, explicit suggestion enrollment, and scheduled-observer pass test |
| 7. journal/notice receipts/quiet passes | `service.go`, `TestQueuedNoticeReceiptIsNotDeliveryAndIsRetained`, `TestAcknowledgeNoticeDeliveryMatchesExactCurrentOrRetainedNotice`, `TestLateNoticeUpdatesPreserveVerifiedDeliveryEvidence`, compiled CLI/HTTP metadata-only acknowledgement, committed-write/lost-response prepared-journal recovery, conflict-safe receipt and bounded-notifier tests, `TestQuietNoopAndWaitingUserIsolation` |
| 8. operations/activation/docs | this page, fixtures, `go test -race ./...`, vet, pre-commit, strict MkDocs |
