# Installation

## Prerequisites

- Go 1.24+
- git
- One executor CLI: Claude, Goose, Codex, or Pi

## Download Linux binary

Download the release archive for your CPU architecture:

```bash
VERSION=v0.10.0
ARCH=amd64  # or: arm64
curl -LO "https://github.com/Kardbrd/kardbrd-agent/releases/download/${VERSION}/kardbrd_${VERSION}_linux_${ARCH}.tar.gz"
curl -LO "https://github.com/Kardbrd/kardbrd-agent/releases/download/${VERSION}/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
tar -xzf "kardbrd_${VERSION}_linux_${ARCH}.tar.gz"
mkdir -p ~/.local/bin
install -m 0755 "kardbrd_${VERSION}_linux_${ARCH}/kardbrd" ~/.local/bin/kardbrd
```

## Download macOS binary

Download the release archive for either an Intel Mac (`amd64`) or Apple Silicon
Mac (`arm64`):

```bash
VERSION=v0.10.0
ARCH=arm64  # or: amd64
curl -LO "https://github.com/Kardbrd/kardbrd-agent/releases/download/${VERSION}/kardbrd_${VERSION}_darwin_${ARCH}.tar.gz"
curl -LO "https://github.com/Kardbrd/kardbrd-agent/releases/download/${VERSION}/checksums.txt"
shasum -a 256 -c --ignore-missing checksums.txt
tar -xzf "kardbrd_${VERSION}_darwin_${ARCH}.tar.gz"
mkdir -p ~/.local/bin
install -m 0755 "kardbrd_${VERSION}_darwin_${ARCH}/kardbrd" ~/.local/bin/kardbrd
```

### macOS security notice

The v0.10.0 macOS archives are unsigned and not notarized. After verifying the
checksum above, try the installed binary once:

```bash
kardbrd --help
```

If Gatekeeper blocks it, open **System Settings** > **Privacy & Security**,
select **Open Anyway**, then confirm **Open**. See [Apple's guidance on opening
an app from an unidentified developer](https://support.apple.com/en-us/102445)
for the current flow. A managed Mac may prohibit this override.

## Update an existing installation

Run:

```bash
kardbrd self-update
```

The command retrieves the latest GitHub release, selects the published archive
for the current Linux or macOS `amd64`/`arm64` platform, verifies it against
that release's `checksums.txt`, and atomically replaces the installed
executable. It leaves the current executable unchanged if release discovery,
download, checksum validation, archive validation, or replacement fails.

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
