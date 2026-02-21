"""Command-line interface for the proxy manager."""

import asyncio
import logging
import sys
from pathlib import Path

import typer
from kardbrd_client import configure_logging
from rich.console import Console
from rich.table import Table

from .manager import ProxyManager
from .rules import ReloadableRuleEngine, RuleEngine, Severity, validate_rules_file

configure_logging()
logger = logging.getLogger("kardbrd_agent.cli")

app = typer.Typer(
    name="kardbrd-agent",
    help="Proxy agent that spawns Claude CLI to work on kardbrd cards.",
    add_completion=False,
)
console = Console()


@app.command()
def start(
    board_id: str = typer.Option(
        None,
        "--board-id",
        envvar="KARDBRD_ID",
        help="Board ID (bypasses subscription file when set)",
    ),
    bot_token: str = typer.Option(
        None,
        "--token",
        envvar="KARDBRD_TOKEN",
        help="Bot authentication token",
    ),
    agent_name: str = typer.Option(
        None,
        "--name",
        "-n",
        envvar="KARDBRD_AGENT",
        help="Agent name for @mentions",
    ),
    api_url: str = typer.Option(
        "https://app.kardbrd.com",
        "--api-url",
        envvar="KARDBRD_URL",
        help="API base URL",
    ),
    cwd: Path = typer.Option(
        None,
        "--cwd",
        "-C",
        envvar="AGENT_CWD",
        help="Working directory for Claude (defaults to current directory)",
    ),
    timeout: int = typer.Option(
        3600,
        "--timeout",
        "-t",
        envvar="AGENT_TIMEOUT",
        help="Maximum execution time in seconds for Claude (default 1 hour)",
    ),
    max_concurrent: int = typer.Option(
        3,
        "--max-concurrent",
        "-c",
        envvar="AGENT_MAX_CONCURRENT",
        help="Maximum number of concurrent Claude sessions (default 3)",
    ),
    worktrees_dir: Path = typer.Option(
        None,
        "--worktrees-dir",
        "-w",
        envvar="AGENT_WORKTREES_DIR",
        help="Directory for worktrees (defaults to parent of --cwd)",
    ),
    setup_cmd: str = typer.Option(
        None,
        "--setup-cmd",
        envvar="AGENT_SETUP_CMD",
        help="Setup command to run in worktrees after creation (e.g. 'npm install', 'uv sync')",
    ),
    rules_file: Path = typer.Option(
        None,
        "--rules",
        "-r",
        envvar="AGENT_RULES_FILE",
        help="Path to kardbrd.yml rules file (defaults to <cwd>/kardbrd.yml)",
    ),
):
    """Start the proxy manager and listen for @mentions.

    Each Claude CLI session spawns its own kardbrd-mcp subprocess for MCP tools.

    Board config comes from env vars (KARDBRD_ID, KARDBRD_TOKEN, KARDBRD_AGENT)
    or equivalent CLI flags.
    """
    # Validate required board config
    missing = []
    if not board_id:
        missing.append("KARDBRD_ID (--board-id)")
    if not bot_token:
        missing.append("KARDBRD_TOKEN (--token)")
    if not agent_name:
        missing.append("KARDBRD_AGENT (--name)")
    if missing:
        console.print(f"[red]Error: missing required config: {', '.join(missing)}[/red]")
        sys.exit(1)

    # Display startup info
    console.print("\n[bold]Proxy Manager[/bold]")
    console.print()

    config_table = Table(show_header=False, box=None, padding=(0, 2))
    config_table.add_column("Key", style="dim")
    config_table.add_column("Value")

    config_table.add_row("Working directory", str(cwd or Path.cwd()))
    if worktrees_dir:
        config_table.add_row("Worktrees directory", str(worktrees_dir))
    config_table.add_row("Timeout", f"{timeout}s")
    config_table.add_row("Max concurrent", str(max_concurrent))
    config_table.add_row("MCP", "kardbrd-mcp (stdio per session)")
    config_table.add_row("Setup command", setup_cmd or "[dim]none (skip)[/dim]")

    console.print(config_table)

    console.print(f"\nBoard: {board_id}")
    console.print(f"  Agent: @{agent_name}")
    console.print(f"  API: {api_url}")

    # Load kardbrd.yml rules (with hot reload every 60s)
    rules_path = rules_file or (cwd or Path.cwd()) / "kardbrd.yml"
    if rules_path.exists():
        # Validate before loading — fail fast with clear error messages
        validation = validate_rules_file(rules_path)
        if validation.warnings:
            for issue in validation.warnings:
                console.print(f"  [yellow]warning[/yellow]: {issue}")
        if not validation.is_valid:
            console.print("\n[red]kardbrd.yml has errors:[/red]\n")
            for issue in validation.errors:
                console.print(f"  [red]error[/red]: {issue}")
            console.print(f"\n[dim]Fix the errors above in {rules_path} and restart.[/dim]")
            sys.exit(1)

        try:
            rule_engine = ReloadableRuleEngine(rules_path)
            console.print(
                f"\nRules: loaded {len(rule_engine.rules)} from {rules_path} (hot reload: 60s)"
            )
            for rule in rule_engine.rules:
                events = ", ".join(rule.events)
                console.print(f"  - {rule.name} ({events} → {rule.action[:40]})")
        except Exception as e:
            console.print(f"\n[red]Error loading rules: {e}[/red]")
            sys.exit(1)
    else:
        rule_engine = RuleEngine()
        console.print(f"\nRules: [dim]no kardbrd.yml found at {rules_path}[/dim]")

    console.print("\n[green]Starting...[/green]\n")

    # Create and run the proxy manager
    manager = ProxyManager(
        board_id=board_id,
        api_url=api_url,
        bot_token=bot_token,
        agent_name=agent_name,
        cwd=cwd,
        timeout=timeout,
        max_concurrent=max_concurrent,
        worktrees_dir=worktrees_dir,
        setup_command=setup_cmd,
        rule_engine=rule_engine,
    )

    try:
        asyncio.run(manager.start())
    except KeyboardInterrupt:
        console.print("\n[yellow]Shutting down...[/yellow]")
    except Exception as e:
        console.print(f"\n[red]Error: {e}[/red]")
        sys.exit(1)


@app.command()
def validate(
    rules_file: Path = typer.Argument(
        None,
        help="Path to kardbrd.yml file (defaults to ./kardbrd.yml)",
    ),
):
    """Validate a kardbrd.yml rules file.

    Checks YAML syntax, schema structure, required fields, event names,
    and model names. Reports all issues with severity levels.
    """
    path = rules_file or Path.cwd() / "kardbrd.yml"
    console.print(f"\nValidating [bold]{path}[/bold]\n")

    result = validate_rules_file(path)

    if not result.issues:
        console.print("[green]Valid[/green] — no issues found\n")
        sys.exit(0)

    # Print all issues grouped by severity
    for issue in result.issues:
        if issue.severity == Severity.ERROR:
            console.print(f"  [red]error[/red]: {issue}")
        else:
            console.print(f"  [yellow]warning[/yellow]: {issue}")

    console.print()

    error_count = len(result.errors)
    warning_count = len(result.warnings)
    parts = []
    if error_count:
        parts.append(f"{error_count} error(s)")
    if warning_count:
        parts.append(f"{warning_count} warning(s)")

    if result.is_valid:
        console.print(f"[green]Valid[/green] with {', '.join(parts)}\n")
        sys.exit(0)
    else:
        console.print(f"[red]Invalid[/red] — {', '.join(parts)}\n")
        sys.exit(1)


def main():
    """Main CLI entrypoint."""
    app()


if __name__ == "__main__":
    main()
