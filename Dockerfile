FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kardbrd ./cmd/kardbrd

FROM debian:bookworm-slim AS agent

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git openssh-client curl bash \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -g 1000 agent \
    && useradd -u 1000 -g agent -m agent \
    && mkdir -p /home/agent/.ssh /home/agent/workspaces /app \
    && chown -R agent:agent /home/agent /app

RUN echo "Host *\n    StrictHostKeyChecking accept-new" > /etc/ssh/ssh_config.d/defaults.conf

COPY --from=build /out/kardbrd /usr/local/bin/kardbrd

WORKDIR /app
ENV PATH="/home/agent/.local/bin:$PATH"

USER agent
RUN curl -fsSL https://claude.ai/install.sh | bash

ENTRYPOINT ["kardbrd"]
CMD ["agent", "start"]
