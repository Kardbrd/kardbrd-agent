# macOS (launchd)

Run `kardbrd agent start` as a launchd service.

## Build

```bash
git clone https://github.com/kardbrd/kardbrd-agent.git
cd kardbrd-agent
go build -o ~/.local/bin/kardbrd ./cmd/kardbrd
```

## Environment

Create `~/Library/Application Support/kardbrd-agent/agent.env` and load it from a wrapper script, or set environment variables directly in the plist.

## Plist

Save as `~/Library/LaunchAgents/com.kardbrd.agent.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.kardbrd.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/you/.local/bin/kardbrd</string>
    <string>agent</string>
    <string>start</string>
    <string>--cwd</string>
    <string>/Users/you/projects/repo</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>KARDBRD_API_URL</key><string>https://app.kardbrd.com</string>
    <key>KARDBRD_TOKEN</key><string>your-token</string>
    <key>KARDBRD_AGENT_BOARD_ID</key><string>your-board-id</string>
    <key>KARDBRD_AGENT_NAME</key><string>your-agent-name</string>
    <key>ANTHROPIC_API_KEY</key><string>your-api-key</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

## Manage

```bash
launchctl load ~/Library/LaunchAgents/com.kardbrd.agent.plist
launchctl kickstart -k gui/$(id -u)/com.kardbrd.agent
launchctl unload ~/Library/LaunchAgents/com.kardbrd.agent.plist
```
