FROM golang:1.24-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/kardbrd ./cmd/kardbrd

FROM golang:1.24-bookworm AS agent

ARG TARGETARCH
ARG GH_VERSION=2.100.0
ARG GH_AMD64_SHA256=e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be
ARG GH_ARM64_SHA256=ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961
ARG PRE_COMMIT_VERSION=4.6.2

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git openssh-client curl bash nodejs npm python3-pip \
    && case "$TARGETARCH" in \
        amd64) gh_sha256="$GH_AMD64_SHA256" ;; \
        arm64) gh_sha256="$GH_ARM64_SHA256" ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl --fail --location --silent --show-error \
        "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz" \
        -o /tmp/gh.tar.gz \
    && echo "${gh_sha256}  /tmp/gh.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/gh.tar.gz -C /tmp \
    && install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh" /usr/local/bin/gh \
    && python3 -m pip install --no-cache-dir --break-system-packages "pre-commit==${PRE_COMMIT_VERSION}" \
    && rm -rf /var/lib/apt/lists/* /tmp/gh.tar.gz "/tmp/gh_${GH_VERSION}_linux_${TARGETARCH}"

RUN groupadd -g 1000 agent \
    && useradd -u 1000 -g agent -m agent \
    && mkdir -p /home/agent/.ssh /home/agent/workspaces /app \
    && chown -R agent:agent /home/agent /app

RUN echo "Host *\n    StrictHostKeyChecking accept-new" > /etc/ssh/ssh_config.d/defaults.conf

COPY --from=build /out/kardbrd /usr/local/bin/kardbrd

RUN npm install -g @openai/codex@0.144.5

WORKDIR /app
ENV PATH="/usr/local/bin:/home/agent/.local/bin:$PATH"

USER agent
RUN curl -fsSL https://claude.ai/install.sh | bash
RUN kardbrd --version \
    && codex --version \
    && gh --version \
    && go version \
    && pre-commit --version

ENTRYPOINT ["kardbrd"]
CMD ["agent", "start"]
