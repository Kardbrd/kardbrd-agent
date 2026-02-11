# Stage 1: uv binary
FROM ghcr.io/astral-sh/uv:latest AS uv

# Stage 2: Node.js + Claude CLI
FROM node:22-slim AS node
RUN npm install -g @anthropic-ai/claude-code

# Stage 3: Build Python environment
FROM python:3.12-slim AS builder

# git needed for kardbrd-client git dependency
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*

COPY --from=uv /uv /usr/local/bin/uv

WORKDIR /app

# Two-step install for layer caching:
# 1) deps only (cached unless pyproject.toml/uv.lock change)
COPY pyproject.toml uv.lock README.md LICENSE ./
RUN uv sync --frozen --no-install-project

# 2) project source
COPY kardbrd_agent/ kardbrd_agent/
RUN uv sync --frozen

# Stage 4: Runtime
FROM python:3.12-slim AS agent

RUN apt-get update && apt-get install -y --no-install-recommends git openssh-client && rm -rf /var/lib/apt/lists/*

# Node.js + Claude CLI from stage 2
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/@anthropic-ai /usr/local/lib/node_modules/@anthropic-ai
RUN ln -s /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js /usr/local/bin/claude

# uv + uvx (used to build kardbrd-agent itself; NOT required for target repo worktrees)
COPY --from=uv /uv /uvx /usr/local/bin/

# Python venv from builder
COPY --from=builder /app/.venv /app/.venv

# Non-root user
RUN groupadd -g 1000 agent && useradd -u 1000 -g agent -m agent \
    && mkdir -p /home/agent/.ssh /home/agent/workspaces /app/state \
    && chown -R agent:agent /home/agent /app/state

# SSH defaults
RUN echo "Host *\n    StrictHostKeyChecking accept-new" > /etc/ssh/ssh_config.d/defaults.conf

WORKDIR /app
COPY pyproject.toml uv.lock ./
COPY kardbrd_agent/ kardbrd_agent/

ENV PATH="/app/.venv/bin:$PATH"
ENV AGENT_STATE_DIR=/app/state LOG_LEVEL=INFO PYTHONUNBUFFERED=1

USER agent

ENTRYPOINT ["kardbrd-agent"]
CMD ["start"]
