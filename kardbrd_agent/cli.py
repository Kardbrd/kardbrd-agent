"""Command-line interface for the proxy manager."""

import asyncio
import logging
import os
import sys
from pathlib import Path

import typer
from kardbrd_client import (
    BoardSubscription,
    DirectoryStateManager,
    configure_logging,
    display_subscription_info,
    fetch_setup_url,
    is_setup_url,
    validate_manual_subscription,
)
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


def get_state_dir() -> str:
    """Get state directory from environment or default."""
    return os.getenv("AGENT_STATE_DIR", "state")


def get_state_manager() -> DirectoryStateManager:
    """Get the directory-based state manager."""
    return DirectoryStateManager(get_state_dir())


@app.command()
def sub(
    board_id_or_url: str = typer.Argument(
        ...,
        help="Setup URL or Board ID (UUID)",
    ),
    token: str = typer.Argument(
        None,
        help="Bot authentication token (required if using board ID)",
    ),
    name: str = typer.Option(
        None,
        "--name",
        "-n",
        help="Agent name for @mentions (required if using board ID)",
    ),
    api_url: str = typer.Option(
        "http://localhost:8000",
        "--api-url",
        help="API base URL",
    ),
):
    """Subscribe to a board using setup URL or manual credentials."""
    state_manager = get_state_manager()

    # Check if it's a URL or board_id
    if is_setup_url(board_id_or_url):
        # Fetch credentials from setup URL
        credentials = fetch_setup_url(board_id_or_url)
        board_id = credentials["board_id"]
        bot_token = credentials["token"]
        agent_name = credentials["agent_name"]
        api_base_url = credentials["api_url"]
    else:
        # Manual board_id + token
        board_id = board_id_or_url
        bot_token = token
        api_base_url = api_url
        agent_name = name

        # Validate manual inputs
        validate_manual_subscription(board_id, bot_token, agent_name)

    # Create subscription
    subscription = BoardSubscription(
        board_id=board_id,
        api_url=api_base_url,
        bot_token=bot_token,
        agent_name=agent_name,
    )

    # Save subscription
    state_manager.add_subscription(subscription)

    # Display confirmation
    display_subscription_info(
        board_id=board_id,
        agent_name=agent_name,
        api_url=api_base_url,
        token=bot_token,
        state_file=str(state_manager.state_dir),
    )


@app.command()
def start(
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
    """
    state_manager = get_state_manager()

    subscriptions = state_manager.get_all_subscriptions()
    if not subscriptions:
        console.print("[red]Error: No subscriptions configured[/red]")
        console.print("Use 'kardbrd-agent sub <setup-url>' to add a subscription")
        sys.exit(1)

    # Display startup info
    console.print("\n[bold]Proxy Manager[/bold]")
    console.print()

    # Configuration section
    config_table = Table(show_header=False, box=None, padding=(0, 2))
    config_table.add_column("Key", style="dim")
    config_table.add_column("Value")

    config_table.add_row("Config path", str(state_manager.state_dir))
    config_table.add_row("Working directory", str(cwd or Path.cwd()))
    if worktrees_dir:
        config_table.add_row("Worktrees directory", str(worktrees_dir))
    config_table.add_row("Timeout", f"{timeout}s")
    config_table.add_row("Max concurrent", str(max_concurrent))
    config_table.add_row("MCP", "kardbrd-mcp (stdio per session)")

    config_table.add_row("Setup command", setup_cmd or "[dim]none (skip)[/dim]")

    console.print(config_table)

    # Subscriptions section
    for board_id, sub in subscriptions.items():
        console.print(f"\nBoard: {board_id}")
        console.print(f"  Agent: @{sub.agent_name}")
        console.print(f"  API: {sub.api_url}")

    # Load kardbrd.yml rules (with hot reload every 60s)
    rules_path = rules_file or (cwd or Path.cwd()) / "kardbrd.yml"
    if rules_path.exists():
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
        state_manager=state_manager,
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
def status():
    """Show current subscription status."""
    state_manager = get_state_manager()
    subscriptions = state_manager.get_all_subscriptions()

    if not subscriptions:
        console.print("\n[yellow]Not subscribed to any board[/yellow]")
        console.print("Use 'kardbrd-agent sub <setup-url>' to subscribe\n")
        sys.exit(0)

    console.print("\n[bold]Proxy Manager Status[/bold]\n")
    console.print(f"State directory: {state_manager.state_dir}\n")

    table = Table(show_header=True, header_style="bold")
    table.add_column("Board ID")
    table.add_column("Agent Name")
    table.add_column("API URL")

    for board_id, sub in subscriptions.items():
        table.add_row(
            board_id,
            sub.agent_name,
            sub.api_url,
        )

    console.print(table)
    console.print(f"\nTotal: {len(subscriptions)} subscription(s)\n")


@app.command()
def unsub(
    yes: bool = typer.Option(
        False,
        "--yes",
        "-y",
        help="Skip confirmation prompt",
    ),
):
    """Unsubscribe from all boards."""
    state_manager = get_state_manager()
    subscriptions = state_manager.get_all_subscriptions()

    if not subscriptions:
        console.print("Not currently subscribed to any board")
        sys.exit(0)

    console.print(f"\nCurrently subscribed to {len(subscriptions)} board(s):")
    for board_id, sub in subscriptions.items():
        console.print(f"  - {board_id} ({sub.agent_name})")

    # Ask for confirmation
    if not yes:
        response = typer.prompt("\nRemove ALL subscriptions? [y/N]", default="n")
        if response.lower() not in ("y", "yes"):
            console.print("Cancelled")
            sys.exit(0)

    # Remove all subscriptions
    removed_count = 0
    for board_id in list(subscriptions.keys()):
        if state_manager.remove_subscription(board_id):
            removed_count += 1

    if removed_count > 0:
        console.print(f"[green]Removed {removed_count} subscription(s)[/green]\n")
    else:
        console.print("No subscriptions found to remove\n")


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
