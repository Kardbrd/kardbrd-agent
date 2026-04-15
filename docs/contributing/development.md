# Development Setup

## Prerequisites

- Python 3.12+
- [uv](https://docs.astral.sh/uv/)
- git
- An AI executor CLI for integration testing

## Clone and install

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync --dev
```

## Project structure

```
kardbrd-agent/
├── kardbrd_agent/              # Main package
│   ├── __init__.py
│   ├── cli.py                  # Typer CLI entry point
│   ├── manager.py              # ProxyManager — main orchestrator
│   ├── executor.py             # Executor Protocol + ClaudeExecutor
│   ├── goose_executor.py       # GooseExecutor
│   ├── codex_executor.py       # CodexExecutor
│   ├── rules.py                # RuleEngine + validation
│   ├── scheduler.py            # ScheduleManager (cron)
│   ├── worktree.py             # WorktreeManager
│   ├── merge_workflow.py       # MergeWorkflow state machine
│   ├── merge_tools.py          # Git operations for merging
│   ├── mcp_proxy.py            # Session tracking data classes
│   ├── wizard.py               # Onboarding card creation
│   └── tests/
│       ├── conftest.py         # Shared fixtures
│       ├── test_rules.py
│       ├── test_executor.py
│       ├── test_merge_workflow.py
│       ├── test_integration.py
│       └── ...
├── examples/                   # Deployment guides
│   ├── docker/
│   ├── uv/
│   ├── linux/
│   └── macos/
├── docs/                       # Documentation (MkDocs)
├── kardbrd.yml.example         # Example rules file
├── pyproject.toml              # Project metadata and dependencies
├── mkdocs.yml                  # Documentation site config
├── CLAUDE.md                   # AI agent guidance
├── RULES.md                    # Code conventions
├── SOUL.md                     # Agent identity
└── CONTRIBUTING.md             # Contributor quick start
```

## Running the agent locally

```bash
# Set environment
export KARDBRD_ID=<board-id>
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT=<agent-name>
export ANTHROPIC_API_KEY=<api-key>

# Run
uv run kardbrd-agent start --cwd /path/to/test/repo
```

## Useful commands

```bash
# Run all tests
uv run pytest

# Run a single test file
uv run pytest kardbrd_agent/tests/test_rules.py

# Run a specific test
uv run pytest kardbrd_agent/tests/test_integration.py::TestConcurrentProcessingIntegration

# Lint and format
uv run pre-commit run --all-files

# Run ruff only
uv run pre-commit run ruff --all-files

# Validate rules file
uv run kardbrd-agent validate
```

## Building docs locally

```bash
# Install docs dependencies
uv pip install mkdocs-material mkdocstrings[python]

# Serve locally with hot-reload
mkdocs serve

# Build static site
mkdocs build
```

The docs site will be available at `http://127.0.0.1:8000`.
