# Deployment Overview

kardbrd-agent can be deployed in several ways depending on your infrastructure and isolation needs.

## Comparison

| | Docker | smolvm | uv (no Docker) | Linux (systemd) | macOS (launchd) |
|---|---|---|---|---|---|
| **Isolation** | Container (shared kernel) | Micro-VM (hypervisor) | Shares host | Container + systemd | Host process |
| **Toolchain** | Baked into image | Baked into VM | Host-installed | Baked into image | Host-installed |
| **Setup** | Dockerfile + Compose | Smolfile | One command (`uvx`) | Provisioning script | Install script |
| **Network control** | iptables/firewall | Built-in allowlist | None | iptables/firewall | None |
| **Auto-update** | Watchtower or rebuild | Restart fetches latest | Restart fetches latest | Timer-based pull | Git pull on restart |
| **Auto-restart** | Docker restart policy | Manual/systemd | systemd/launchd | systemd | launchd |
| **Best for** | Production | High-isolation production | Dev machines | Production (Linux) | Dev machines (Mac) |

## Choosing a deployment method

**[Docker](docker.md)** (recommended for production)

:   Your project's dev image is the base — kardbrd-agent and the executor CLI are injected into it. Full container isolation, Docker Compose support, auto-updates via Watchtower.

**[smolvm](smolvm.md)** (strongest isolation)

:   Run in a hardware-virtualized micro-VM with built-in network allowlisting and SSH agent forwarding. No daemon required. Best when you need stronger isolation than containers provide.

**[uv (no Docker)](uv.md)** (simplest)

:   Run directly on the host with `uvx`. No Docker needed. Best when your project's toolchain is already installed locally.

**[Linux (systemd)](linux.md)** (production on Linux)

:   systemd user service with Podman or Docker. Provisioning script handles setup, image builds, and auto-update timers.

**[macOS (launchd)](macos.md)** (persistent on Mac)

:   launchd daemon with auto-start, auto-restart, auto-update, and rollback on failure.

## Common requirements

All deployment methods need:

- A **kardbrd board** with a bot token
- An **LLM provider** API key (or local model for Goose)
- **Git SSH access** for pushing from worktrees (dedicated deploy key recommended)
- The target **repository** cloned locally or mounted as a volume
