# Code Conventions

## Language and style

- **Python 3.12+** — use modern syntax features
- **Line length**: 100 characters (enforced by ruff)
- **Imports**: sorted by ruff (isort rules) — standard library, third-party, local

## Async-first

All async I/O operations use `async/await`. Use `asyncio.create_subprocess_exec` for subprocesses in async contexts. `subprocess.run` is acceptable in synchronous setup code (e.g., worktree git operations) but should not be used in async hot paths.

```python
# Good — async context (executor, manager)
proc = await asyncio.create_subprocess_exec(
    "claude", "-p", "-",
    stdin=asyncio.subprocess.PIPE,
    stdout=asyncio.subprocess.PIPE,
)

# OK — synchronous setup (worktree creation)
subprocess.run(["git", "worktree", "add", path], check=True)

# Bad — blocking call in async context
result = subprocess.run(["claude", "-p", "-"], capture_output=True)
```

## Protocol over inheritance

New executor types implement the `Executor` Protocol — no shared base class. Register via `executor_type` config, not conditional imports.

```python
# Good — implements Protocol
class MyExecutor:
    async def execute(self, prompt, resume_session_id=None, cwd=None, model=None, on_chunk=None):
        ...

# Bad — inherits from base
class MyExecutor(BaseExecutor):
    ...
```

## Dataclasses for data

Use `@dataclass` for structured data. Avoid plain dicts for domain objects.

```python
# Good
@dataclass
class ExecutorResult:
    success: bool
    result_text: str
    error: str | None = None
    cost_usd: float | None = None
    duration_ms: int | None = None
    session_id: str | None = None
    returncode: int | None = None

# Bad
result = {"result": "...", "cost_usd": 0.05, ...}
```

## Type hints

Python 3.12+ style. Use `X | None` not `Optional[X]`.

```python
# Good
def process(card_id: str, model: str | None = None) -> ExecutorResult: ...

# Bad
def process(card_id: str, model: Optional[str] = None) -> ExecutorResult: ...
```

## Logging

Module-level `logger = logging.getLogger("kardbrd_agent")`. Never `print()`.

```python
import logging

logger = logging.getLogger("kardbrd_agent")

# Good
logger.info("Processing card %s", card_id)

# Bad
print(f"Processing card {card_id}")
```

## Architecture guards

- `kardbrd.yml` is the single source of truth for automation rules — don't hardcode event handling
- Executor factory pattern: new executors register via `executor_type`, not conditional imports
- MCP runs as stdio subprocess per session — no shared server state
- Worktrees are ephemeral — don't store persistent state in them
- Card ID (`public_id`) is the universal key for session tracking

## Security

- Never log or expose tokens, API keys, or credentials
- Sanitize worktree paths — no path traversal
- Use `asyncio.create_subprocess_exec` (not `shell=True`) to prevent injection
- Clean up temporary files on session end

## Git workflow

- Work on card-scoped branches (created automatically via worktree)
- Brief verb-phrase commit messages
- One logical change per commit
- Never force-push or amend commits on shared branches
