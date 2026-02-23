"""Proxy Manager - Spawns agent executors to work on kardbrd cards."""

from .executor import AuthStatus, ClaudeExecutor, Executor, ExecutorResult
from .goose_executor import GooseExecutor
from .manager import ProxyManager
from .mcp_proxy import ProxySession

__all__ = [
    "AuthStatus",
    "ClaudeExecutor",
    "Executor",
    "ExecutorResult",
    "GooseExecutor",
    "ProxyManager",
    "ProxySession",
]
