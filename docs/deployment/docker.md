# Docker Deployment

The repository Dockerfile builds the Go `kardbrd` binary and runs `kardbrd agent start`. The runtime image includes Go, the official GitHub CLI, Codex, and `pre-commit`; Python support is included only for `pre-commit`, and `uv` remains absent.

## Build

```bash
docker build -t kardbrd .
```

## Run

```bash
docker run --rm \
  -e KARDBRD_API_URL=https://app.kardbrd.com \
  -e KARDBRD_TOKEN=<bot-token> \
  -e KARDBRD_AGENT_BOARD_ID=<board-id> \
  -e KARDBRD_AGENT_NAME=<agent-name> \
  -e KARDBRD_AGENT_CWD=/home/agent/repository \
  -e ANTHROPIC_API_KEY=<api-key> \
  -v ./repository:/home/agent/repository \
  -v ./workspaces:/home/agent/workspaces \
  -v ./claude:/home/agent/.claude \
  -v ./ssh/id_ed25519:/home/agent/.ssh/id_ed25519:ro \
  kardbrd
```

## Compose

Use `examples/docker/docker-compose.yml` as a starting point:

```yaml
services:
  agent:
    build:
      context: ./repository
      target: agent
    command: ["agent", "start", "--cwd", "/home/agent/repository"]
    environment:
      - KARDBRD_API_URL=${KARDBRD_API_URL:-https://app.kardbrd.com}
      - KARDBRD_TOKEN
      - KARDBRD_AGENT_BOARD_ID
      - KARDBRD_AGENT_NAME
      - KARDBRD_AGENT_CWD=/home/agent/repository
      - KARDBRD_AGENT_WORKTREES_DIR=/home/agent/workspaces
      - KARDBRD_AGENT_MAX_CONCURRENT=${KARDBRD_AGENT_MAX_CONCURRENT:-2}
      - KARDBRD_AGENT_EXECUTOR=${KARDBRD_AGENT_EXECUTOR:-claude}
      - ANTHROPIC_API_KEY
    volumes:
      - ./repository:/home/agent/repository
      - ./workspaces:/home/agent/workspaces
      - ./claude:/home/agent/.claude
      - ./ssh/id_ed25519:/home/agent/.ssh/id_ed25519:ro
```

## Executor Images

The provided Dockerfile installs Claude CLI, Codex, Go, the official GitHub CLI, and `pre-commit`. For Goose or Pi, add the relevant CLI install step to your image and set:

```bash
KARDBRD_AGENT_EXECUTOR=goose
```

## Troubleshooting

**Missing config**: set `KARDBRD_AGENT_BOARD_ID`, `KARDBRD_TOKEN`, and `KARDBRD_AGENT_NAME`.

**Worktree setup fails**: either leave `KARDBRD_AGENT_SETUP_CMD` empty or make sure the image contains the tools required by your repository.

**SSH errors**: mount a dedicated deploy key at `/home/agent/.ssh/id_ed25519`.
