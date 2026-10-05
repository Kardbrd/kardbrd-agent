# Explicit selection on addressed comments

For a daemon started with the Codex executor, place this directive on the **first line** of an addressed comment:

```text
@MBPBot [dispatch model=gpt-6.1-sol effort=high]
Implement the requested change. Keep this task text exactly as written.
```

The entire first line is the header. Put the task on the next line. Both `model` and `effort` are required; each may appear once, in either order. The header is removed before prompt construction, while the task body and its whitespace are preserved. A plain mention has no override and continues to use the Codex CLI's configured defaults. Text such as `model=...` in the task, quoted examples, and directives on later lines are ordinary prompt text. This syntax selects the executor model; it does not switch an agent from Claude, Goose, or Pi to Codex.

Supported direct comment model IDs are `gpt-6.1-sol`, `gpt-6-sol`, `gpt-6-astra`, and `gpt-6-luna`. Supported efforts are `low`, `medium`, `high`, `xhigh`, and `max`. The exact [OpenAI model page](https://developers.openai.com/api/docs/models/gpt-6.1-sol) lists `gpt-6.1-sol` and confirms `high`. Codex's [configuration reference](https://developers.openai.com/codex/config-reference) documents `model_reasoning_effort`. The daemon rejects malformed, duplicate, unknown, or unsupported selections with a comment on the card. It never substitutes a different model.

This feature requires a Codex CLI and account that actually offer the selected model. The repository Dockerfile currently pins Codex CLI 0.144.5. In this development environment, CLI 0.156.0 did **not** list `gpt-6.1-sol` in its model catalog, and a direct probe returned HTTP 400: “The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account.” The container's model support has therefore not been verified. Operators must verify runtime access before relying on the example. The agent's request and continuation keep the selected model and effort; a provider rejection remains a visible failure.

After independent review, release, and merge, the operator can check the container's runtime before pulling or restarting it:

```bash
codex --version
codex exec --help
codex exec --ephemeral --skip-git-repo-check --model gpt-6.1-sol --config 'model_reasoning_effort="high"' --json 'Reply OK only.'
```

A successful probe must finish without `turn.failed` or an HTTP error. Then follow the repository's deployment procedure to pull the approved latest main and rebuild/restart the container. Do not change credentials or model settings to make a failed probe pass without a separate decision.
