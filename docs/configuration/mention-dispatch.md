# Explicit selection on addressed comments

For a daemon started with the Codex executor, place this directive on the **first line** of an addressed comment:

```text
@MBPBot [dispatch model=gpt-6.1-sol effort=high]
Implement the requested change. Keep this task text exactly as written.
```

The entire first line is the header. Put the task on the next line. Both `model` and `effort` are required; each may appear once, in either order. The header is removed before prompt construction, while the task body and its whitespace are preserved. A plain mention has no override and continues to use the Codex CLI's configured defaults. Text such as `model=...` in the task, quoted examples, and directives on later lines are ordinary prompt text. This syntax selects the executor model; it does not switch an agent from Claude, Goose, or Pi to Codex.

Supported direct comment model IDs are `gpt-6.1-sol`, `gpt-6-sol`, `gpt-6-astra`, and `gpt-6-luna`. Supported efforts are `low`, `medium`, `high`, `xhigh`, and `max`. The exact [OpenAI model page](https://developers.openai.com/api/docs/models/gpt-6.1-sol) lists `gpt-6.1-sol` and confirms `high`. Codex's [configuration reference](https://developers.openai.com/codex/config-reference) documents `model_reasoning_effort`. The daemon rejects malformed, duplicate, unknown, or unsupported selections with a comment on the card. It never substitutes a different model.

This feature requires a Codex CLI and account that actually offer the selected model. The official OpenAI API model page establishes `gpt-6.1-sol` as an API model ID and lists `high` reasoning effort; it does not establish availability through Codex using a ChatGPT account. The repository Dockerfile currently pins Codex CLI 0.144.5. In this development environment, CLI 0.156.0 did **not** list `gpt-6.1-sol` in its model catalog, and a direct probe returned HTTP 400: “The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account.” The container's model support has therefore not been verified. A container rebuild alone is not a demonstrated fix for this account-level rejection. The agent's request and continuation keep the selected model and effort; a provider rejection remains a visible failure.

## Operator migration checklist

Complete these checks with the operator before upgrading or starting a daemon with this feature:

1. Identify the **actual live** rules file from the daemon's `KARDBRD_AGENT_RULES_FILE` setting, or its configured working directory's `kardbrd.yml`. Inspect its rules and schedules. The bundled file in this repository may differ from the live copy.
2. For the applicable MBPBot workflow rules, stage a change from `reasoning: xhigh` to `reasoning: high`, retaining their current model IDs and all rule conditions/actions. Preserve intentional effort settings outside this MBPBot scope. Validate the staged file with `kardbrd agent validate path/to/kardbrd.yml` using the reviewed binary. Coordinate any live file replacement because ordinary rules hot-reload.
3. In the **actual target runtime and account**, check `codex --version`, `codex exec --help`, the available model catalog (`codex debug models` where supported), and the exact probe below. A successful probe must finish without `turn.failed` or an HTTP error. Do not infer target-model access from the official API catalog or a newer CLI version alone.
4. If the probe still reports the ChatGPT-account rejection, stop before enabling a rule or dispatching a comment that selects `gpt-6.1-sol`. The remaining operator decision is whether to obtain verified access to that exact model or defer its use. Do not silently substitute a model, change credentials/account, or retarget existing rules as part of this migration.
5. After independent review, an approved release, successful compatibility verification, and the MBPBot live-config change to `high`, follow the normal deployment procedure. No rule activation or runtime restart is implied by this document.

Exact compatibility probe (run in the target container/account before pulling or restarting it):

```bash
codex --version
codex exec --help
codex exec --ephemeral --skip-git-repo-check --model gpt-6.1-sol --config 'model_reasoning_effort="high"' --json 'Reply OK only.'
```

The command selects the exact requested model and effort. The local CLI 0.156.0 probe failed with HTTP 400 as described above; the operator must resolve that compatibility decision before using this model in a live dispatch.
