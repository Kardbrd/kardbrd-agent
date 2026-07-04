# Deployment Overview

`kardbrd` can run as a containerized agent or as a host process.

| | Docker | Host binary | Linux systemd | macOS launchd | smolvm |
| --- | --- | --- | --- | --- | --- |
| Isolation | Container | Host process | Host or container | Host process | Micro-VM |
| Binary | Built into image | Local Go build | Local Go build or image | Local Go build | Built into VM |
| Best for | Production | Development | Long-running Linux agents | Long-running Mac agents | Strong isolation |

## Options

**[Docker](docker.md)**: Build the Go binary into an image and run `kardbrd agent start`.

**[Host binary](uv.md)**: Build `kardbrd` locally and run it directly on the host.

**[Linux](linux.md)**: systemd user service.

**[macOS](macos.md)**: launchd service.

**[smolvm](smolvm.md)**: hardware-virtualized deployment.

## Common Requirements

- A Kardbrd board with a bot token
- One executor CLI and its provider credentials
- The target repository cloned locally or mounted into the runtime
- Git credentials if the agent should push branches or commits
