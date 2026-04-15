# Installation

## Prerequisites

- **Python 3.12+**
- **[uv](https://docs.astral.sh/uv/)** — Python package manager
- **git**
- **One of the following AI agent CLIs:**
    - [Claude CLI](https://docs.anthropic.com/en/docs/claude-code) — default executor
    - [Goose](https://block.github.io/goose/) — open-source, multi-provider executor
    - [Codex CLI](https://github.com/openai/codex) — OpenAI Codex executor

## Install from source

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
uv sync --dev
```

## Install with uvx (no clone)

Run directly from GitHub without cloning:

```bash
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git" \
  kardbrd-agent start --cwd /path/to/your/repo
```

This fetches the latest version automatically. To pin a specific version:

```bash
uvx --from "git+https://github.com/Kardbrd/kardbrd-agent.git@v1.0.0" \
  kardbrd-agent start --cwd /path/to/your/repo
```

## Install executor CLIs

=== "Claude CLI"

    ```bash
    npm install -g @anthropic-ai/claude-code
    ```

=== "Goose"

    ```bash
    curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh
    ```

=== "Codex CLI"

    ```bash
    npm install -g @openai/codex
    ```

## Verify installation

```bash
# Check kardbrd-agent
kardbrd-agent --help

# Check your executor
claude --version     # or: goose --version, codex --version
```

## Next steps

- [Set up authentication](authentication.md) for your board and LLM provider
- [Quick start guide](quickstart.md) for an end-to-end walkthrough
