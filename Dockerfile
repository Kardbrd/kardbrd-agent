# Stage 1: uv binary
FROM ghcr.io/astral-sh/uv:latest AS uv

# Stage 2: Node.js + Claude CLI
FROM node:22-slim AS node
RUN npm install -g @anthropic-ai/claude-code

# Stage 3: Runtime
FROM python:3.12-slim AS agent

RUN apt-get update && apt-get install -y --no-install-recommends git openssh-client && rm -rf /var/lib/apt/lists/*

# Node.js + Claude CLI from stage 2
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules/@anthropic-ai /usr/local/lib/node_modules/@anthropic-ai
RUN ln -s /usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js /usr/local/bin/claude

# uv + uvx
COPY --from=uv /uv /uvx /usr/local/bin/

# Non-root user
RUN groupadd -g 1000 agent && useradd -u 1000 -g agent -m agent \
    && mkdir -p /home/agent/.ssh /home/agent/workspaces /app/state \
    && chown -R agent:agent /home/agent /app

# SSH defaults
RUN echo "Host *\n    StrictHostKeyChecking accept-new" > /etc/ssh/ssh_config.d/defaults.conf

WORKDIR /app

ENV PATH="/app/.venv/bin:$PATH"
ENV AGENT_STATE_DIR=/app/state LOG_LEVEL=INFO PYTHONUNBUFFERED=1

USER agent

ENTRYPOINT ["uvx", "--from", "git+https://github.com/Kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start"]
