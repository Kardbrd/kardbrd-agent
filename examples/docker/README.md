# Docker Compose Setup

Run kardbrd-agent inside your project's own container. The agent and Claude CLI are injected into an image built from your project's toolchain, so setup commands like `pnpm install` or `uv sync` just work.

## Prerequisites

- Docker with Compose plugin (`docker compose`)
- A git repository the agent will work on
- An SSH deploy key (for git push/pull from inside the container)

## Quick Start

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

If your repo already has a Dockerfile, add an `agent` target that extends your existing stage. This shares base layers and avoids a second Dockerfile.

Example for a Node/pnpm project:

```dockerfile
# your existing stages...
FROM node:22-slim AS base
RUN corepack enable
RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

# add this at the end of your Dockerfile
FROM base AS agent
RUN apt-get update && apt-get install -y python3 python3-venv && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

The `agent` target adds only what's needed on top of your existing image: `python3`, `python3-venv` (for uvx), Claude CLI, `uv`, and a non-root user. Claude CLI refuses to run as root with `--dangerously-skip-permissions`, so the non-root user is required.

**No Dockerfile yet?** Create a `Dockerfile.agent` instead — see [Dockerfile.agent examples](#dockerfileagent-examples) below.

### 4. Copy `docker-compose.yml`

Copy the [`docker-compose.yml`](docker-compose.yml) from this directory into your project root. The compose file builds the `agent` target by default:

```yaml
build:
  context: ./repository
  target: agent                          # uses the agent target from your Dockerfile
  # dockerfile: Dockerfile.agent         # uncomment if using a separate Dockerfile.agent
```

Adjust `AGENT_SETUP_CMD` and `AGENT_TEST_CMD` to match your project.

### 5. Set up SSH key

Generate a **dedicated** SSH key — never use your personal keys. The agent runs Claude with `--dangerously-skip-permissions`, so any key it has access to could be used in unpredictable ways.

```bash
ssh-keygen -t ed25519 -f ssh/id_ed25519 -N "" -C "kardbrd-agent"

# Add the public key as a deploy key on your git repo (with write access)
cat ssh/id_ed25519.pub
```

### 6. Configure environment

Create a `.env` file with your board credentials and API key:

```bash
cat > .env << 'EOF'
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>
ANTHROPIC_API_KEY=sk-ant-...
EOF
```

### 7. Start the agent

```bash
docker compose up -d
```

## Directory Structure

After setup:

```
kardbrd-project/
├── docker-compose.yml
├── .env                    # KARDBRD_ID, KARDBRD_TOKEN, KARDBRD_AGENT, ANTHROPIC_API_KEY
├── repository/             # Your cloned git repository
│   └── Dockerfile          # Contains the agent target (or Dockerfile.agent)
├── workspaces/             # Worktrees (created automatically)
│   └── card-<id>/
├── claude/                 # Claude CLI home (~/.claude)
└── ssh/
    ├── id_ed25519          # Dedicated SSH private key
    └── id_ed25519.pub      # Public key (add to git hosting)
```

The repository and workspaces are **separate mounts**. The agent creates worktrees in `workspaces/` while the base repo stays in `repository/`.

## Configuration

### Environment variables

Set these in a `.env` file or export them before running `docker compose up`:

| Variable | Default | Notes |
|---|---|---|
| `KARDBRD_ID` | — | Board ID (required) |
| `KARDBRD_TOKEN` | — | Bot authentication token (required) |
| `KARDBRD_AGENT` | — | Agent name for @mentions (required) |
| `KARDBRD_URL` | `https://app.kardbrd.com` | API base URL |
| `ANTHROPIC_API_KEY` | — | Required unless stored in `claude/` volume |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARNING`, `ERROR` |
| `AGENT_CWD` | cwd | Path to git repo inside the container |
| `AGENT_WORKTREES_DIR` | parent of `AGENT_CWD` | Where worktrees are created |
| `AGENT_SETUP_CMD` | — | Run in each worktree after creation (e.g. `pnpm install`, `uv sync`) |
| `AGENT_TEST_CMD` | — | Test/build command for merge workflow |
| `AGENT_MAX_CONCURRENT` | `3` | Parallel Claude sessions |
| `AGENT_TIMEOUT` | `3600` | Max seconds per Claude session |
| `GIT_AUTHOR_NAME` | — | Git commit author name |
| `GIT_AUTHOR_EMAIL` | — | Git commit author email |

### Agent target per stack

The `agent` target always follows the same pattern: extend your existing stage, add python3/venv, Claude CLI, and uv.

**Node/pnpm** (Astro, Next.js, etc.):

```dockerfile
FROM node:22-slim AS base
RUN corepack enable
RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

# ... your existing stages ...

FROM base AS agent
RUN apt-get update && apt-get install -y python3 python3-venv && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

**Python/uv** (Django, FastAPI, etc.):

```dockerfile
FROM python:3.12-slim AS base
RUN apt-get update && apt-get install -y git openssh-client curl && rm -rf /var/lib/apt/lists/*
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv

# ... your existing stages ...

FROM base AS agent
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

**Go:**

```dockerfile
FROM golang:1.23 AS base
RUN apt-get update && apt-get install -y git openssh-client && rm -rf /var/lib/apt/lists/*

# ... your existing stages ...

FROM base AS agent
RUN apt-get update && apt-get install -y python3 python3-venv nodejs npm && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

### Dockerfile.agent examples

If your repo has no Dockerfile, create a standalone `Dockerfile.agent` and set `dockerfile: Dockerfile.agent` in your docker-compose.yml.

**Node/pnpm:**

```dockerfile
FROM node:22-slim
RUN corepack enable
RUN apt-get update && apt-get install -y \
      git openssh-client python3 python3-venv \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

**Python/uv:**

```dockerfile
FROM python:3.12-slim
RUN apt-get update && apt-get install -y git openssh-client curl && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=ghcr.io/astral-sh/uv:latest /uv /usr/local/bin/uv
RUN useradd -m -s /bin/bash -u 1000 agent && mkdir -p /app/state && chown agent:agent /app/state
USER agent
WORKDIR /home/agent/repository
ENTRYPOINT ["uvx", "--from", "git+https://github.com/kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start", "--cwd", "/home/agent/repository"]
```

## Management

### View logs

```bash
docker compose logs -f
```

### Restart

```bash
docker compose restart
```

### Stop

```bash
docker compose down
```

### Rebuild after repo changes

```bash
docker compose build
docker compose up -d
```

### Shell access

```bash
docker compose run --rm --entrypoint sh agent
```

## Volumes

| Mount point | Purpose | Mode |
|---|---|---|
| `/home/agent/repository` | Base git repository | rw |
| `/home/agent/workspaces` | Worktrees (one per card) | rw |
| `/home/agent/.claude` | Claude CLI home (API creds, sessions) | rw |
| `/home/agent/.ssh/id_ed25519` | SSH private key | ro |

## Troubleshooting

### Agent can't push/pull

Check SSH connectivity from inside the container:

```bash
docker compose run --rm --entrypoint sh agent -c "ssh -T git@github.com"
```

If it fails, verify:

1. The deploy key is added to your git hosting with **write** access
2. The key file permissions are correct (`chmod 600 ssh/id_ed25519`)

### "Missing required config" on start

Ensure `KARDBRD_ID`, `KARDBRD_TOKEN`, and `KARDBRD_AGENT` are set in your `.env` file or exported as environment variables.

### Setup command fails

If `AGENT_SETUP_CMD` (e.g. `pnpm install`) fails in worktrees, make sure the base image includes the required toolchain. The whole point of the `agent` target (or `Dockerfile.agent`) is that your project's tools are baked into the image.

### Permission errors on volumes

The container runs as UID 1000 (`agent` user). Ensure local directories are writable:

```bash
sudo chown -R 1000:1000 workspaces claude
```
