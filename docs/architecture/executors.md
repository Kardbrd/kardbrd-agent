# Executors

Executors are pluggable AI agent backends that implement the `Executor` Protocol. kardbrd-agent supports three executors: Claude CLI, Goose, and Codex.

## Executor Protocol

All executors implement this runtime-checkable Protocol:

```python
@runtime_checkable
class Executor(Protocol):
    async def execute(
        self,
        prompt: str,
        resume_session_id: str | None = None,
        cwd: Path | None = None,
        model: str | None = None,
        on_chunk: Callable[[str, str], Awaitable[None]] | None = None,
    ) -> ExecutorResult: ...

    def build_prompt(
        self,
        card_id: str,
        card_markdown: str,
        command: str,
        comment_content: str,
        author_name: str,
        board_id: str | None = None,
        cwd: str | Path | None = None,
    ) -> str: ...

    def extract_command(self, comment_content: str, mention_keyword: str) -> str: ...

    @staticmethod
    async def check_auth() -> AuthStatus: ...
```

### Key types

**`ExecutorResult`** — returned by `execute()`:

- `success` — whether the execution succeeded (required)
- `result_text` — the executor's text output
- `error` — error message (if failed)
- `cost_usd` — estimated cost (if available)
- `duration_ms` — execution time
- `session_id` — for session resumption
- `returncode` — process exit code
- `stderr` — standard error output
- `command` — the command that was run
- `claude_logs` — extracted log output (on failure)

**`AuthStatus`** — returned by `check_auth()`:

- `authenticated` — whether credentials are valid
- `error` — error message (if auth failed)
- `email` — authenticated user email
- `auth_method` — authentication method used
- `subscription_type` — subscription tier
- `auth_hint` — executor-specific re-authentication instructions

## ClaudeExecutor

Default executor. Spawns `claude -p` as a subprocess with `--output-format=stream-json`.

**Features:**

- Streaming JSON output parsing
- Session ID extraction for resumption
- Timeout enforcement (SIGTERM then SIGKILL)
- Per-rule model selection via `--model` flag
- Claude log extraction for diagnostics on failure

**Model aliases:** `opus`, `sonnet`, `haiku` (mapped to full model IDs)

**Auth:** Validates via `claude auth status`, checks for `loggedIn` flag. Requires `ANTHROPIC_API_KEY`.

## GooseExecutor

Alternative executor wrapping [Goose](https://block.github.io/goose/) CLI. Spawns `goose run -t` with `--output-format stream-json`.

**Features:**

- 20+ LLM provider support via `GOOSE_PROVIDER`
- Provider-aware auth checking (`PROVIDER_KEY_MAP`)
- Named sessions for resumption (`-n "card-{card_id}"`)
- MCP extension support via `--with-extension`

**Provider key mapping:**

| Provider | Required env var |
|----------|-----------------|
| `anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `OPENAI_API_KEY` |
| `google` | `GOOGLE_API_KEY` |
| `groq` | `GROQ_API_KEY` |
| `openrouter` | `OPENROUTER_API_KEY` |
| `databricks` | `DATABRICKS_TOKEN` |

**Auth:** Validates goose binary, `GOOSE_PROVIDER` env var, and provider-specific API key.

## CodexExecutor

Executor for [OpenAI Codex CLI](https://github.com/openai/codex). Spawns `codex exec --dangerously-bypass-approvals-and-sandbox --json`.

**Features:**

- No-sandbox mode for unrestricted CLI access (kardbrd CLI, shell commands)
- JSON output parsing
- Codex-specific model mapping

**Model aliases:** Maps short names to Codex model IDs (e.g., `gpt-5.4`, `gpt-5.4-mini`)

**Auth:** Validates codex binary and login status. Requires `OPENAI_API_KEY`.

## Factory pattern

`ProxyManager` instantiates the appropriate executor based on the `executor_type` parameter:

```python
# In ProxyManager.start()
if self.executor_type == "goose":
    self.executor = GooseExecutor(cwd=self.cwd, timeout=self.timeout, ...)
elif self.executor_type == "codex":
    self.executor = CodexExecutor(cwd=self.cwd, timeout=self.timeout, ...)
else:
    self.executor = ClaudeExecutor(cwd=self.cwd, timeout=self.timeout, ...)
```

The executor type can be set via:

- `--executor` CLI flag
- `AGENT_EXECUTOR` environment variable
- `executor` field in `kardbrd.yml`

## Adding a new executor

To add a new executor:

1. Create a new module (e.g., `my_executor.py`)
2. Implement the `Executor` Protocol
3. Register in the executor factory in `manager.py`
4. Add auth checking logic specific to your CLI
5. Add the executor type to CLI and config options

The key design principle is **Protocol over inheritance** — there's no shared base class. Each executor is a standalone implementation that satisfies the Protocol interface.
