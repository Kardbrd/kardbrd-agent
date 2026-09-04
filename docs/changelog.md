# Changelog

## 0.10.0

### Changed

- Collection, search, and activity commands now default to stable TSV with
  headers. Use `--no-headers` for headerless TSV, `--format json` for the
  previous lossless JSON output, or `--format md` for Markdown tables. (#58)
- The non-root agent image now includes Go, GitHub CLI, and pre-commit, and
  pins Codex CLI 0.144.5. (#59)

### Added

- Added `kardbrd self-update`, which selects the current platform's GitHub
  release archive, verifies its SHA-256 checksum, validates its contents, and
  atomically replaces the installed executable. (#60)
- Added release archives for Linux and macOS on amd64 and arm64. (#60)
- Added `kardbrd card update --clear-labels` for removing every label from a
  card. (#55)

### Fixed

- Fixed `card update --label` and `--label-ids` so they validate,
  de-duplicate, and reconcile a card's complete label set. (#55)
- Fixed board labels JSON and Markdown output to use the board's label
  catalog. (#56)
- Restored the agent runtime's ability to create pull requests, inspect CI,
  and push workflow-file changes. (#59)

## 0.1.0

### Added

- Bot resilience: catch unhandled exceptions that kill WebSocket loop ([#40](https://github.com/Kardbrd/kardbrd-agent/pull/40))
- Retry-via-resume for recoverable Claude exit code failures ([#39](https://github.com/Kardbrd/kardbrd-agent/pull/39))
- Diagnostic error reporting and Claude log extraction ([#38](https://github.com/Kardbrd/kardbrd-agent/pull/38))
- Resolve full CLI path with `shutil.which` before spawning executors ([#37](https://github.com/Kardbrd/kardbrd-agent/pull/37))
- CodexExecutor for OpenAI Codex CLI integration ([#36](https://github.com/Kardbrd/kardbrd-agent/pull/36))
- Watchtower and uv deployment examples ([#35](https://github.com/Kardbrd/kardbrd-agent/pull/35))
- Replace MCP tool references with kardbrd CLI commands in skills ([#33](https://github.com/Kardbrd/kardbrd-agent/pull/33))
- Migrate commands to skills directory format ([#32](https://github.com/Kardbrd/kardbrd-agent/pull/32))
- Replace MCP server with kardbrd CLI for board operations
- SOUL.md, RULES.md, and SKILL.md frontmatter for GitAgent standards
- `__self__` support for `assignee` condition in rule engine
- `comment_author` condition with `__self__` support ([#30](https://github.com/Kardbrd/kardbrd-agent/pull/30))
- `assignee` condition with strict YAML list validation ([#27](https://github.com/Kardbrd/kardbrd-agent/pull/27))
- On-demand WebSocket streaming for agent subprocess output ([#28](https://github.com/Kardbrd/kardbrd-agent/pull/28))
- Register bot skills via PUT /api/bots/skills/ on startup ([#29](https://github.com/Kardbrd/kardbrd-agent/pull/29))
- kardbrd API token validation on startup ([#23](https://github.com/Kardbrd/kardbrd-agent/pull/23))
- Skip worktree management when cwd is not a git repository ([#22](https://github.com/Kardbrd/kardbrd-agent/pull/22))
- Enhanced bot card with executor, auth, skills, schedules, and version info ([#25](https://github.com/Kardbrd/kardbrd-agent/pull/25))
- Fix `[Errno 7] Argument list too long` by piping prompts via stdin ([#24](https://github.com/Kardbrd/kardbrd-agent/pull/24))
- Cron schedule support for time-based automation ([#21](https://github.com/Kardbrd/kardbrd-agent/pull/21))
- Bot card creation/update on startup with triggers display ([#20](https://github.com/Kardbrd/kardbrd-agent/pull/20))
- Wizard card auto-creation on startup when bot is unconfigured ([#19](https://github.com/Kardbrd/kardbrd-agent/pull/19))
- Executor abstraction for multi-agent support — Claude + Goose ([#18](https://github.com/Kardbrd/kardbrd-agent/pull/18))
- Claude CLI auth check before spawning sessions ([#17](https://github.com/Kardbrd/kardbrd-agent/pull/17))
- Deterministic stop confirmation when agent is stopped via reaction ([#16](https://github.com/Kardbrd/kardbrd-agent/pull/16))
- Environment variable support for `start` command ([#15](https://github.com/Kardbrd/kardbrd-agent/pull/15))
- Top-level config in `kardbrd.yml`, eliminate statefile ([#14](https://github.com/Kardbrd/kardbrd-agent/pull/14))
- `require_label`/`require_user` conditions and Agent-scoped workflows ([#13](https://github.com/Kardbrd/kardbrd-agent/pull/13))
- `emoji` condition for reaction-based triggers ([#11](https://github.com/Kardbrd/kardbrd-agent/pull/11))
- yamllint and `kardbrd.yml` validation to pre-commit hooks ([#12](https://github.com/Kardbrd/kardbrd-agent/pull/12))
- `kardbrd.yml` validation tool with CLI command ([#10](https://github.com/Kardbrd/kardbrd-agent/pull/10))
- `exclude_label` condition for rule engine ([#9](https://github.com/Kardbrd/kardbrd-agent/pull/9))
- `kardbrd.yml` rule engine for declarative board automation ([#8](https://github.com/Kardbrd/kardbrd-agent/pull/8))
- `--strict-mcp-config` to prevent agent using wrong MCP server ([#7](https://github.com/Kardbrd/kardbrd-agent/pull/7))
- Labels support: prompt context and session tracking ([#6](https://github.com/Kardbrd/kardbrd-agent/pull/6))
- Replace FastMCP in-process proxy with kardbrd-mcp stdio subprocess
- Reaction-based stop replacing word-based stop command ([#4](https://github.com/Kardbrd/kardbrd-agent/pull/4))
- Fetch and update main branch before creating worktrees ([#1](https://github.com/Kardbrd/kardbrd-agent/pull/1))
- Initial release: WebSocket listener, worktree management, Claude executor, MCP proxy
