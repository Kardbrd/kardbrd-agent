# Structured model and effort for addressed comments

This protocol is opt-in. A text-only `@Bot` comment keeps the existing dispatch behavior. Rule and schedule `model`/`reasoning` fields never supply defaults for an addressed comment.

## Direct-comment YAML scope

A bot operator can add a verified, exact target-runtime inventory to the **live** `kardbrd.yml`:

```yaml
comment_execution:
  defaults:
    model: <exact-model-id>
    effort: high
  models:
    - id: <exact-model-id>
      efforts: [low, medium, high]
  verification:
    source: operator_probe
    verified_at: "2026-10-05T16:00:00Z"
    executor_version: "<exact CLI version observed in target runtime>"
```

The example is a schema illustration, not a claim that any model is available to this bot. Replace every placeholder only after an exact model and effort probe succeeds in the target container/account. `source` accepts `operator_probe` or `runtime_probe`; the timestamp must be past RFC 3339. The verification record describes how the administrator established the configured choices; the daemon cannot guarantee future provider access. A provider rejection remains visible and withdraws the rejected model/effort pair from subsequent publication. Config without a verified model inventory does not publish selectable capabilities. An absent `comment_execution` block leaves plain mentions on executor CLI defaults. If `defaults.model` or `defaults.effort` is absent, the exact CLI default is unknown and registered as JSON `null`; the UI keeps selectors unavailable. No rule is selected as a substitute default.

`kardbrd agent validate <path>` checks strict nested fields, duplicate models/efforts, default membership and verification metadata. The daemon reads the live file on startup and accepted hot reload. No change to this repository's sample `kardbrd.yml` activates a model in a running bot.

## Shared v1 wire contract

Web accepts an optional comment POST `execution_request`:

```json
{"version":1,"target_bot_id":"<bot public_id>","capability_revision":"<server revision>","model":"<exact executor ID>","effort":"high"}
```

`version`, `target_bot_id`, and `capability_revision` are required when the object exists. `model` and `effort` are independently optional. Omitted means: valid first-line directive field, then the Web-frozen accepted default for that field, then unresolved CLI default with no flag. Explicit null, empty, wrong type, unsupported value, duplicate key or unknown field is an error; it never falls back. A plain comment has no object and keeps the legacy route. Web validates the sole addressed bot and fresh registration before persistence, returns HTTP 400 `VALIDATION_ERROR` with a field path for malformed values, or HTTP 409 `CAPABILITIES_STALE` for unavailable/stale registration. It persists the original object, including omissions, and sends that object on the existing `comment_created` event. A sibling event-only `accepted_defaults:{"model":null,"effort":null}` is frozen by Web at acceptance and cannot be supplied by a commenter. The event envelope remains v1; there is no second dispatch.

The bot-token authenticated PUT `/api/bots/execution-capabilities/` publishes `version`, server-issued live WebSocket `instance_id`, `executor`, `board_id`, exact `models:[{id,efforts}]`, nullable `defaults:{model,effort}`, and `provenance:{source,config_fingerprint,verified_at}`. The server derives bot identity from the bearer token and issues a `revision`, `published_at`, and `expires_at`. A same-instance, same-content heartbeat extends expiry without changing revision. GET `/api/bots/<bot public_id>/execution-capabilities/?board_id=<board public_id>` is board scoped. On connect and accepted YAML reload the client registers; it refreshes before expiry. Older Web lacks `instance_id`, so the client runs text/directive comments without registration or selectors. An older client cannot register v1.

The client validates structured request and server-only defaults before slash commands or exact command rules. Wrong-target events do not run. Before any executor start it checks the current registration and verified pair. It stores the resolved model, effort, source of each field (`explicit`, `directive`, `accepted_defaults`, or `cli_unknown`), text and identity in a per-comment durable claim under the base repository's ignored `state/agent-claims/` directory. Accepted claims are recovered after a fresh registration; a claim that reached the executor is not automatically re-executed after a crash. Repeated events and queued comments use the same claim. Structured comments queue FIFO for a busy card; the original selection is used for continuation and publication recovery. The base repository mount must be persistent across container restarts.

## Rollout gate

1. Review both the Agent and Web PRs and their CI at the exact heads. Do not enable the composer yet.
2. Install the Agent reader and Web registration/instance endpoints. The Web UI remains disabled until exactly one authenticated v1 socket exists for the target bot and board. Drain old bot sockets; targeting cannot force a legacy board-wide reader to ignore a matching text mention.
3. Paul alone verifies the **actual** MBP container, live config path, mount persistence and account. In that container, run `codex --version`, `codex exec --help`, `kardbrd agent validate <live-kardbrd.yml-path>`, and the exact model/effort probe from [Addressed Comment Dispatch](mention-dispatch.md). A rebuild does not establish `gpt-6.1-sol` access; a prior ChatGPT-backed CLI rejected it. Do not edit credentials or live YAML as part of this PR.
4. After approval, Paul alone drains and rebuilds/restarts MBP using his deployment's actual service name and paths. The repository's example Compose service is `agent` (`docker compose -f examples/docker/docker-compose.yml ps agent`), but the live MBP service must be verified before using any build or restart command.
5. Confirm one live v1 registration, its revision and expiry, then submit one Web-board addressed comment with an exact verified model and effort. Check the persisted request, event, claim, actual executor argv, provider outcome, and one terminal response. Only after this gate may any other board or VPS rollout proceed. Resolve the referred-to CPA/CBA VPS identity before touching it.
