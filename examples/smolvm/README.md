# smolvm Deployment

Run kardbrd-agent in a [smolvm](https://github.com/smol-machines/smolvm) micro-VM with hardware-level isolation, built-in network allowlisting, and SSH agent forwarding.

## Setup

```bash
# 1. Create project directory
mkdir kardbrd-project && cd kardbrd-project
mkdir -p workspaces

# 2. Clone your repo
git clone git@github.com:yourorg/yourrepo.git repository

# 3. Copy the Smolfile
cp path/to/kardbrd-agent/examples/smolvm/Smolfile .

# 4. Configure credentials
cat > .env << 'EOF'
KARDBRD_ID=<board-id>
KARDBRD_TOKEN=<bot-token>
KARDBRD_AGENT=<agent-name>
ANTHROPIC_API_KEY=sk-ant-...
EOF

# 5. Ensure your SSH agent has the deploy key
ssh-add path/to/deploy_key

# 6. Start
smolvm machine start --smolfile Smolfile --name kardbrd-agent
smolvm machine exec --name kardbrd-agent --env-file .env -- \
  uvx --from git+https://github.com/Kardbrd/kardbrd-agent.git \
  kardbrd-agent start --cwd /home/agent/repository
```

## Management

```bash
smolvm machine exec --name kardbrd-agent -- bash   # Shell access
smolvm machine stop --name kardbrd-agent           # Stop
smolvm machine rm --name kardbrd-agent             # Remove
smolvm machine list                                # List machines
```

## Customization

Edit the `Smolfile` to:

- Add hosts to `[network].allow_hosts` for your project's dependencies
- Adjust `[dev].init` commands for additional tooling (e.g., Node.js, Go)
- Add volume mounts for additional directories

See the [full documentation](https://kardbrd.github.io/kardbrd-agent/deployment/smolvm/) for details on resource limits, portable artifacts, and troubleshooting.
