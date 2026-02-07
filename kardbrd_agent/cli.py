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
    port: int = typer.Option(
        None,
        "--port",
        "-p",
        envvar="AGENT_MCP_PORT",
        help="MCP server port (enables unified mode with HTTP server + WebSocket)",
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
    test_cmd: str = typer.Option(
        None,
        "--test-cmd",
        envvar="AGENT_TEST_CMD",
        help="Test command for merge workflow (default: make test)",
    ),
    merge_queue_list: str = typer.Option(
        None,
        "--merge-queue-list",
        envvar="AGENT_MERGE_QUEUE_LIST",
        help="List name that triggers merge workflow (disabled if not set)",
    ),
):
    """Start the proxy manager and listen for @mentions.

    In unified mode (--port), runs both an MCP HTTP server and WebSocket listener.
    Claude Code instances spawned by the proxy will connect to the local MCP server.
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

    if port:
        config_table.add_row("MCP port", str(port))
        config_table.add_row("Mode", "[cyan]unified[/cyan] (HTTP + WebSocket)")
    else:
        config_table.add_row("Mode", "WebSocket only")

    config_table.add_row("Setup command", setup_cmd or "[dim]none (skip)[/dim]")
    if merge_queue_list:
        config_table.add_row("Merge queue list", merge_queue_list)
        config_table.add_row("Test command", test_cmd or "make test")
    else:
        config_table.add_row("Merge queue", "[dim]disabled[/dim]")

    console.print(config_table)

    # Subscriptions section
    for board_id, sub in subscriptions.items():
        console.print(f"\nBoard: {board_id}")
        console.print(f"  Agent: @{sub.agent_name}")
        console.print(f"  API: {sub.api_url}")

    console.print("\n[green]Starting...[/green]\n")

    # Create and run the proxy manager
    manager = ProxyManager(
        state_manager=state_manager,
        cwd=cwd,
        timeout=timeout,
        mcp_port=port,
        max_concurrent=max_concurrent,
        worktrees_dir=worktrees_dir,
        setup_command=setup_cmd,
        test_command=test_cmd,
        merge_queue_list=merge_queue_list,
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


@app.command(name="proxy-mcp")
def proxy_mcp():
    """Start MCP proxy server (stdio transport) for Claude Code integration."""
    from .mcp_proxy import run_mcp_server

    run_mcp_server(state_dir=get_state_dir())


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


def main():
    """Main CLI entrypoint."""
    app()


if __name__ == "__main__":
    main()
