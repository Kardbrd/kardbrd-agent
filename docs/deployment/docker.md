# Docker Deployment

Run kardbrd-agent inside your project's own container. The agent and your chosen executor CLI are injected into an image built from your project's toolchain, so setup commands like `pnpm install` or `uv sync` just work.

## Prerequisites

- Docker with Compose plugin (`docker compose`)
- A git repository the agent will work on
- An SSH deploy key (for git push/pull from inside the container)

## Quick start

### 1. Create the project directory

```bash
mkdir kardbrd-project && cd kardbrd-project
mkdir -p workspaces claude ssh
```

### 2. Clone your repo

```bash
git clone git@github.com:yourorg/yourrepo.git repository
```

### 3. Add an `agent` target to your Dockerfile

Add an `agent` target that extends your existing stage:

=== "Node/pnpm"

    ```dockerfile
    FROM node:22-slim AS base
    RUN corepack enable
    RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

    # ... your existing stages ...

    FROM base AS agent
    RUN apt-get update && apt-get install -y python3 python3-venv && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
    RUN useradd -m -s /bin/bash -u 1000 agent
    USER agent
    WORKDIR /home/agent/repository
    ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
    CMD ["start", "--cwd", "/home/agent/repository"]
    ```

=== "Python/uv"

    ```dockerfile
    FROM python:3.12-slim AS base
    RUN apt-get update && apt-get install -y git openssh-client curl && rm -rf /var/lib/apt/lists/*
    COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

    # ... your existing stages ...

    FROM base AS agent
    RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
        && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    RUN useradd -m -s /bin/bash -u 1000 agent
    USER agent
    WORKDIR /home/agent/repository
    ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
    CMD ["start", "--cwd", "/home/agent/repository"]
    ```

=== "Go"

    ```dockerfile
    FROM golang:1.23 AS base
    RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

    # ... your existing stages ...

    FROM base AS agent
    RUN apt-get update && apt-get install -y python3 python3-venv nodejs npm && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
    RUN useradd -m -s /bin/bash -u 1000 agent
    USER agent
    WORKDIR /home/agent/repository
    ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
    CMD ["start", "--cwd", "/home/agent/repository"]
    ```

!!! note "Non-root user required"
    Claude CLI refuses to run as root with `--dangerously-skip-permissions`, so a non-root user is required.

!!! tip "Using Goose instead?"
    Replace `RUN npm install -g @anthropic-ai/claude-code` with `RUN curl -fsSL https://github.com/block/goose/releases/latest/download/install.sh | sh` and set `AGENT_EXECUTOR=goose`.

**No Dockerfile?** Create a standalone `Dockerfile.agent` instead — see [standalone examples](#standalone-dockerfileagent) below.

### 4. Copy `docker-compose.yml`

Copy the [`docker-compose.yml`](https://github.com/Kardbrd/kardbrd-agent/blob/main/examples/docker/docker-compose.yml) from the examples directory. Adjust `AGENT_SETUP_CMD` and `AGENT_TEST_CMD` for your project.

### 5. Set up SSH key

Generate a **dedicated** SSH key — never use your personal keys:

```bash
ssh-keygen -t ed25519 -f ssh/id_ed25519 -N "" -C "kardbrd-agent"
cat ssh/id_ed25519.pub  # Add as deploy key with write access
```

### 6. Configure environment

```bash
cat > .env << 'EOF'
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>
ANTHROPIC_API_KEY=sk-ant-...
# For Goose: AGENT_EXECUTOR=goose, GOOSE_PROVIDER=anthropic
EOF
```

### 7. Start the agent

```bash
docker compose up -d
```

## Directory structure

```
kardbrd-project/
├── docker-compose.yml
├── .env                    # Credentials (gitignored)
├── repository/             # Your cloned git repo
│   └── Dockerfile          # Contains the agent target
├── workspaces/             # Worktrees (created automatically)
├── claude/                 # Claude CLI home (~/.claude)
└── ssh/
    ├── id_ed25519          # Dedicated SSH private key
    └── id_ed25519.pub      # Add to git hosting
```

## Standalone `Dockerfile.agent`

If your repo has no Dockerfile, create a standalone `Dockerfile.agent`:

=== "Node/pnpm"

    ```dockerfile
    FROM node:22-slim
    RUN corepack enable
    RUN apt-get update && apt-get install -y \
          git openssh-client python3 python3-venv \
        && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
    RUN useradd -m -s /bin/bash -u 1000 agent
    USER agent
    WORKDIR /home/agent/repository
    ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
    CMD ["start", "--cwd", "/home/agent/repository"]
    ```

=== "Python/uv"

    ```dockerfile
    FROM python:3.12-slim
    RUN apt-get update && apt-get install -y git openssh-client curl && rm -rf /var/lib/apt/lists/*
    RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
        && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
    RUN npm install -g @anthropic-ai/claude-code
    COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
    RUN useradd -m -s /bin/bash -u 1000 agent
    USER agent
    WORKDIR /home/agent/repository
    ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
    CMD ["start", "--cwd", "/home/agent/repository"]
    ```

Set `dockerfile: Dockerfile.agent` in your `docker-compose.yml`.

## Auto-updates with Watchtower

[Watchtower](https://containrrr.dev/watchtower/) monitors container registries and automatically pulls and restarts containers when a new image is published.

### How it works

Watchtower polls the registry on an interval. When it detects a new image digest:

1. Pulls the new image
2. Gracefully stops the running container
3. Starts a new container with the same options

### Setup

Add Watchtower to your compose file:

```yaml
services:
  agent:
    image: ghcr.io/yourorg/yourproject:agent
    labels:
      - "com.centurylinklabs.watchtower.enable=true"
    # ... rest of config

  watchtower:
    image: containrrr/watchtower
    restart: unless-stopped
    environment:
      - WATCHTOWER_LABEL_ENABLE=true
      - WATCHTOWER_POLL_INTERVAL=300
      - WATCHTOWER_CLEANUP=true
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

!!! warning
    Watchtower only works with **registry-pulled images** (`image:` in compose). It cannot trigger `docker compose build`.

## Management

```bash
docker compose logs -f          # View logs
docker compose restart          # Restart
docker compose down             # Stop
docker compose build && docker compose up -d  # Rebuild
docker compose run --rm --entrypoint sh agent  # Shell access
```

## Volumes

| Mount point | Purpose | Mode |
|---|---|---|
| `/home/agent/repository` | Base git repository | rw |
| `/home/agent/workspaces` | Worktrees (one per card) | rw |
| `/home/agent/.claude` | Claude CLI home | rw |
| `/home/agent/.ssh/id_ed25519` | SSH private key | ro |

## Troubleshooting

**Agent can't push/pull** — Test SSH from inside the container:

```bash
docker compose run --rm --entrypoint sh agent -c "ssh -T git@github.com"
```

**"Missing required config"** — Ensure `KARDBRD_ID`, `KARDBRD_TOKEN`, and `KARDBRD_AGENT` are in `.env`.

**Setup command fails** — Make sure the base image includes your project's toolchain.

**Permission errors** — The container runs as UID 1000. Fix with:

```bash
sudo chown -R 1000:1000 workspaces claude
```
