# Running on the Host

Run the Go `kardbrd` binary directly on a host when the target repository and its toolchain are already installed locally.

## Build

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o ~/.local/bin/kardbrd ./cmd/kardbrd
```

## Environment

```bash
export KARDBRD_API_URL=https://app.kardbrd.com
export KARDBRD_TOKEN=<bot-token>
export KARDBRD_AGENT_BOARD_ID=<board-id>
export KARDBRD_AGENT_NAME=<agent-name>
export KARDBRD_AGENT_CWD=/path/to/your/repo
export ANTHROPIC_API_KEY=<api-key>
```

## Start

```bash
kardbrd agent start
```

## systemd

```ini
[Unit]
Description=kardbrd agent for my-project
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=%h/projects/my-project/.kardbrd.env
ExecStart=%h/.local/bin/kardbrd agent start --cwd %h/projects/my-project
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
```

## launchd

Use `/Users/you/.local/bin/kardbrd` as the program and pass `agent`, `start`, `--cwd`, and the repo path as arguments.

## Updating

```bash
cd ~/kardbrd-agent
git pull
go build -o ~/.local/bin/kardbrd ./cmd/kardbrd
systemctl --user restart kardbrd-agent-myproject
```
