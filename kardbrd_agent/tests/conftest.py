"""Shared fixtures for kardbrd-agent tests."""

from pathlib import Path
from unittest.mock import MagicMock

import pytest

from kardbrd_agent.executor import ClaudeResult


@pytest.fixture
def git_repo(tmp_path: Path) -> Path:
    """Create a mock git repository with config files."""
    repo = tmp_path / "kbn"
    repo.mkdir()
    (repo / ".git").mkdir()
    (repo / ".mcp.json").write_text('{"servers": {}}')
    (repo / ".env").write_text("SECRET=value")

    claude_dir = repo / ".claude"
    claude_dir.mkdir()
    (claude_dir / "settings.local.json").write_text("{}")

    return repo


@pytest.fixture
def mock_claude_result() -> ClaudeResult:
    """Create a successful ClaudeResult."""
    return ClaudeResult(
        success=True,
        result_text="Task completed successfully",
        session_id="session-123",
    )


@pytest.fixture
def mock_kardbrd_client() -> MagicMock:
    """Create a mock KardbrdClient."""
    client = MagicMock()
    client.get_card_markdown.return_value = "# Card\n\nDescription"
    client.add_comment = MagicMock()
    client.toggle_reaction = MagicMock()
    return client
