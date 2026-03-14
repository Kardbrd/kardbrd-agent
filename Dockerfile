# Stage 1: uv binary
FROM ghcr.io/astral-sh/uv:latest AS uv

# Stage 2: Runtime
FROM python:3.12-slim AS agent

RUN apt-get update && apt-get install -y --no-install-recommends git openssh-client curl && rm -rf /var/lib/apt/lists/*

# uv + uvx
COPY --from=uv /uv /uvx /usr/local/bin/

# Non-root user
RUN groupadd -g 1000 agent && useradd -u 1000 -g agent -m agent \
    && mkdir -p /home/agent/.ssh /home/agent/workspaces /app/state \
    && chown -R agent:agent /home/agent /app

# SSH defaults
RUN echo "Host *\n    StrictHostKeyChecking accept-new" > /etc/ssh/ssh_config.d/defaults.conf

WORKDIR /app

ENV PATH="/home/agent/.local/bin:/app/.venv/bin:$PATH"

# Everything below runs as agent
USER agent

# Claude CLI (installed as agent user)
RUN curl -fsSL https://claude.ai/install.sh | bash
ENV AGENT_STATE_DIR=/app/state LOG_LEVEL=INFO PYTHONUNBUFFERED=1

ENTRYPOINT ["uvx", "--from", "git+https://github.com/Kardbrd/kardbrd-agent.git", "kardbrd-agent"]
CMD ["start"]
