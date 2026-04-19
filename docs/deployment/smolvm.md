# smolvm Deployment

Run kardbrd-agent inside a [smolvm](https://github.com/smol-machines/smolvm) micro-VM for hardware-level isolation. Unlike Docker containers (which share the host kernel), smolvm uses real hardware virtualization (KVM on Linux, Hypervisor.framework on macOS) to provide a stronger security boundary around each agent.

## Why smolvm?

| | Docker | smolvm |
|---|---|---|
| **Isolation** | Linux namespaces (shared kernel) | Hardware virtualization (hypervisor) |
| **Network control** | Requires iptables/firewall rules | `--allow-host` built-in allowlist |
| **Credential isolation** | Env vars visible to all processes | SSH agent forwarding — keys never enter VM |
| **Startup time** | ~2-5s (cached image) | ~200ms cold boot |
| **Daemon required** | Yes (dockerd) | No — single binary |
| **Image format** | Dockerfile | Smolfile (TOML) or OCI images |

smolvm is a good fit when you want stronger isolation than containers provide, particularly for untrusted or semi-trusted agent workloads where network egress control and credential separation matter.

## Prerequisites

- **Linux**: KVM support (`/dev/kvm` must be accessible)
- **macOS**: Apple Hypervisor.framework (macOS 11+, Apple Silicon or Intel with HV support)
- [smolvm CLI](https://github.com/smol-machines/smolvm) installed
- An SSH deploy key (for git push/pull from inside the VM)

### Install smolvm

```bash
# macOS
brew install smol-machines/tap/smolvm

# Linux (download binary)
curl -fsSL https://github.com/smol-machines/smolvm/releases/latest/download/smolvm-linux-amd64 \
  -o /usr/local/bin/smolvm && chmod +x /usr/local/bin/smolvm
```

## Quick start

### 1. Create the project directory

```bash
mkdir kardbrd-project && cd kardbrd-project
mkdir -p workspaces
```

### 2. Clone your repo

```bash
git clone git@github.com:yourorg/yourrepo.git repository
```

### 3. Create a Smolfile

Create a `Smolfile` in the project directory:

```toml
image = "python:3.12-slim"
net = true

[network]
allow_hosts = [
  "api.anthropic.com",
  "app.kardbrd.com",
  "github.com",
  "pypi.org",
  "files.pythonhosted.org",
]

[dev]
init = [
  "apt-get update && apt-get install -y git openssh-client curl",
  "curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg && echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main' > /etc/apt/sources.list.d/github-cli.list && apt-get update && apt-get install -y gh",
  "curl -LsSf https://astral.sh/uv/install.sh | sh",
  "curl -fsSL https://claude.ai/install.sh | bash",
]
volumes = [
  "./repository:/home/agent/repository",
  "./workspaces:/home/agent/workspaces",
]

[auth]
ssh_agent = true
```

See the [`examples/smolvm/`](https://github.com/Kardbrd/kardbrd-agent/tree/main/examples/smolvm) directory for a ready-to-use Smolfile.

### 4. Configure environment

Create a `.env` file with your credentials:

```bash
cat > .env << 'EOF'
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>
ANTHROPIC_API_KEY=sk-ant-...
# For Goose: AGENT_EXECUTOR=goose, GOOSE_PROVIDER=anthropic
EOF
```

### 5. Start the VM and run the agent

```bash
# Create and start the machine
smolvm machine start --smolfile Smolfile --name kardbrd-agent

# Load environment variables and run the agent
smolvm machine exec --name kardbrd-agent --env-file .env -- \
  uvx --from git+https://github.com/Kardbrd/kardbrd-agent.git \
  kardbrd-agent start --cwd /home/agent/repository
```

## Directory structure

```
kardbrd-project/
├── Smolfile                # VM configuration
├── .env                    # Credentials (gitignored)
├── repository/             # Your cloned git repo
└── workspaces/             # Worktrees (created automatically)
```

!!! note "No SSH key directory needed"
    Unlike Docker, smolvm uses SSH agent forwarding — your host's SSH agent is available inside the VM, and private keys never enter the guest. Make sure your SSH agent has the deploy key loaded: `ssh-add path/to/deploy_key`.

## Network allowlisting

smolvm disables networking by default. The `[network].allow_hosts` list in the Smolfile explicitly permits outbound connections to only the hosts the agent needs:

```toml
[network]
allow_hosts = [
  "api.anthropic.com",     # Claude API
  "app.kardbrd.com",       # kardbrd API + WebSocket
  "github.com",            # Git operations
  "pypi.org",              # Python packages
  "files.pythonhosted.org", # Python package downloads
]
```

Add additional hosts as needed for your project (e.g., `registry.npmjs.org` for Node projects, your private package registry, etc.).

You can also control network access per-invocation with CLI flags:

```bash
smolvm machine start --net --allow-host api.anthropic.com --allow-host app.kardbrd.com ...
```

## Resource limits

Cap CPU and memory to prevent runaway agents from starving the host:

```bash
smolvm machine start --smolfile Smolfile --name kardbrd-agent \
  --cpus 2 --mem 4096
```

## Management

```bash
# View logs (follow agent stdout/stderr)
smolvm machine exec --name kardbrd-agent -- journalctl -f

# Shell into the VM
smolvm machine exec --name kardbrd-agent -- bash

# Stop the VM
smolvm machine stop --name kardbrd-agent

# Remove the VM
smolvm machine rm --name kardbrd-agent

# List running machines
smolvm machine list
```

## Portable artifacts

smolvm can bundle a fully configured VM into a single `.smolmachine` file that can be distributed and run on any compatible host:

```bash
# Export a configured machine as a portable artifact
smolvm machine export --name kardbrd-agent -o kardbrd-agent.smolmachine

# Run it anywhere
smolvm run kardbrd-agent.smolmachine
```

This is useful for distributing pre-configured agent environments to team members or deploying to new hosts without repeating setup.

## Comparison with Docker deployment

Both Docker and smolvm are valid deployment options. Choose based on your needs:

**Use smolvm when:**

- You need stronger isolation (hypervisor boundary vs. shared kernel)
- Network egress control is important (built-in allowlisting)
- You want SSH agent forwarding instead of mounting key files
- You prefer a daemon-less setup (no dockerd)
- You're running on bare metal or cloud VMs with KVM/Hypervisor support

**Use Docker when:**

- You need multi-service stacks (Compose, Watchtower, databases)
- Your CI/CD pipeline is Docker-native
- You want mature build caching and layer reuse
- Your host doesn't support KVM or Hypervisor.framework

## Deployment targets

| Host | smolvm Support | Notes |
|---|---|---|
| Bare metal Linux | Best fit | Direct KVM access |
| macOS (Apple Silicon) | Good | Native Hypervisor.framework |
| Cloud VM (EC2, GCE) | Good | Most support nested KVM |
| Docker container | Requires `/dev/kvm` passthrough | Nested virtualization |
| Kubernetes | Complex | Needs device plugin for `/dev/kvm` |

## Troubleshooting

**"KVM not available"** — Check that `/dev/kvm` exists and is accessible:

```bash
ls -la /dev/kvm
# If missing, load the module:
sudo modprobe kvm_intel  # or kvm_amd
```

**Agent can't reach APIs** — Verify the host is in your allowlist. Check connectivity from inside the VM:

```bash
smolvm machine exec --name kardbrd-agent -- curl -s https://api.anthropic.com/
```

**SSH auth fails** — Make sure your SSH agent is running and has the deploy key loaded:

```bash
ssh-add -l  # Should show your deploy key
```

**Slow init** — The `[dev].init` commands run from scratch on each machine creation. To avoid this, export a configured machine as a `.smolmachine` artifact and reuse it.
