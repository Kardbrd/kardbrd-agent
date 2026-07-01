# Installation

## Prerequisites

- Go 1.24+
- git
- One executor CLI: Claude, Goose, Codex, or Pi

## Build from source

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o kardbrd ./cmd/kardbrd
```

Put the binary somewhere on `PATH`:

```bash
install -m 0755 kardbrd ~/.local/bin/kardbrd
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

## Verify

```bash
kardbrd --help
kardbrd agent --help
claude --version     # or: goose --version, codex --version
```

## Next steps

- [Set up authentication](authentication.md)
- [Quick start](quickstart.md)
