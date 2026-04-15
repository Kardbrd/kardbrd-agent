# Deployment Overview

kardbrd-agent can be deployed in several ways depending on your infrastructure and isolation needs.

## Comparison

| | Docker | uv (no Docker) | Linux (systemd) | macOS (launchd) |
|---|---|---|---|---|
| **Isolation** | Full container | Shares host | Container + systemd | Host process |
| **Toolchain** | Baked into image | Host-installed | Baked into image | Host-installed |
| **Setup** | Dockerfile + Compose | One command (`uvx`) | Provisioning script | Install script |
| **Auto-update** | Watchtower or rebuild | Restart fetches latest | Timer-based pull | Git pull on restart |
| **Auto-restart** | Docker restart policy | systemd/launchd | systemd | launchd |
| **Best for** | Production | Dev machines | Production (Linux) | Dev machines (Mac) |

## Choosing a deployment method

**[Docker](docker.md)** (recommended for production)

:   Your project's dev image is the base — kardbrd-agent and the executor CLI are injected into it. Full container isolation, Docker Compose support, auto-updates via Watchtower.

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
