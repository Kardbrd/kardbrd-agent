# Host Binary Example

This example shows running the Go `kardbrd` binary directly on a host.

```bash
git clone https://github.com/Kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o ~/.local/bin/kardbrd ./cmd/kardbrd
```

Create an env file:

```bash
KARDBRD_API_URL=https://app.kardbrd.com
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT_BOARD_ID=<board-id>
KARDBRD_AGENT_NAME=<agent-name>
KARDBRD_AGENT_CWD=/path/to/repo
ANTHROPIC_API_KEY=<api-key>
```

Run:

```bash
set -a && source .kardbrd.env && set +a
kardbrd agent start
```
