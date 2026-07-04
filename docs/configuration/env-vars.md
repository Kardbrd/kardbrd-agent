# Environment Variables

All configuration can be set via environment variables. CLI flags take precedence when both are set.

## Required

| Variable | CLI flag | Description |
|----------|----------|-------------|
| `KARDBRD_AGENT_BOARD_ID` | `--board-id` | Board ID |
| `KARDBRD_TOKEN` | `--token` | Bot authentication token |
| `KARDBRD_AGENT_NAME` | `--name` | Agent name for @mentions |

## Agent configuration

| Variable | CLI flag | Default | Description |
|----------|----------|---------|-------------|
| `KARDBRD_API_URL` | `--api-url` | `https://app.kardbrd.com` | API base URL |
| `KARDBRD_AGENT_EXECUTOR` | `--executor` | `claude` | Executor: `claude`, `goose`, `codex`, or `pi` |
| `KARDBRD_AGENT_CWD` | `--cwd` | current directory | Working directory (project repo) |
| `KARDBRD_AGENT_TIMEOUT` | `--timeout` | `3600` | Max seconds per session |
| `KARDBRD_AGENT_MAX_CONCURRENT` | `--max-concurrent` | `3` | Parallel sessions |
| `KARDBRD_AGENT_WORKTREES_DIR` | `--worktrees-dir` | parent of cwd | Where worktrees are created |
| `KARDBRD_AGENT_SETUP_CMD` | `--setup-cmd` | — | Run in each worktree (e.g., `npm install`) |
| `KARDBRD_AGENT_RULES_FILE` | `--rules` | `<cwd>/kardbrd.yml` | Path to rules file |

## LLM provider keys

| Variable | Required for | Description |
|----------|-------------|-------------|
| `ANTHROPIC_API_KEY` | Claude, Goose (anthropic) | Anthropic API key |
| `OPENAI_API_KEY` | Codex, Goose (openai) | OpenAI API key |
| `GOOSE_PROVIDER` | Goose | Provider name (e.g., `anthropic`, `openai`, `ollama`) |
| `GOOGLE_API_KEY` | Goose (google) | Google Gemini API key |
| `OPENROUTER_API_KEY` | Goose (openrouter) | OpenRouter API key |
| `GROQ_API_KEY` | Goose (groq) | Groq API key |
| `DATABRICKS_TOKEN` | Goose (databricks) | Databricks token |

## Git identity

| Variable | Description |
|----------|-------------|
| `GIT_AUTHOR_NAME` | Git commit author name |
| `GIT_AUTHOR_EMAIL` | Git commit author email |

## Example `.env` file

```bash
# Board configuration
KARDBRD_AGENT_BOARD_ID=0gl5MlBZ
KARDBRD_TOKEN=tok_xxx
KARDBRD_AGENT_NAME=MyBot

# LLM provider
ANTHROPIC_API_KEY=sk-ant-...

# Optional
# KARDBRD_AGENT_EXECUTOR=goose
# GOOSE_PROVIDER=anthropic
# KARDBRD_AGENT_SETUP_CMD=pnpm install
# KARDBRD_AGENT_MAX_CONCURRENT=3
# KARDBRD_AGENT_TIMEOUT=3600
# LOG_LEVEL=INFO
```

!!! warning "Security"
    Add your `.env` file to `.gitignore` — it contains API keys and tokens.
