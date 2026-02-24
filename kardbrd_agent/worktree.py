"""Worktree manager for creating git worktrees per card."""

import logging
import subprocess
from pathlib import Path

logger = logging.getLogger("kardbrd_agent.worktree")


class WorktreeManager:
    """
    Manages git worktrees for kardbrd cards.

    Creates one worktree per card for isolated development.
    Worktrees are created as sibling directories to the base repo:
    ~/src/repo/ → ~/src/card-<short_id>/
    """

    def __init__(
        self,
        base_repo: Path,
        worktrees_dir: Path | None = None,
        setup_command: str | None = None,
        executor_type: str = "claude",
    ):
        """
        Initialize worktree manager.

        Args:
            base_repo: Path to the main git repository
            worktrees_dir: Optional directory for worktrees (defaults to base_repo parent)
            setup_command: Shell command to run in worktree after creation
                (e.g. "npm install", "uv sync"). None means skip setup.
            executor_type: The executor type ("claude" or "goose"). Controls
                which config symlinks are created.
        """
        self.base_repo = Path(base_repo).resolve()
        # Store worktrees in explicit dir or as sibling directories
        self.worktrees_base = (
            Path(worktrees_dir).resolve() if worktrees_dir else self.base_repo.parent
        )
        self.setup_command = setup_command
        self.executor_type = executor_type
        # Track active worktrees: card_id → worktree_path
        self.active_worktrees: dict[str, Path] = {}

    def _get_short_id(self, card_id: str) -> str:
        """Get truncated card ID (first 8 characters)."""
        return card_id[:8]

    def _get_worktree_path(self, card_id: str) -> Path:
        """Get the worktree path for a card (sibling directory)."""
        short_id = self._get_short_id(card_id)
        return self.worktrees_base / f"card-{short_id}"

    def _get_branch_name(self, card_id: str) -> str:
        """Get the branch name for a card."""
        short_id = self._get_short_id(card_id)
        return f"card/{short_id}"

    def _setup_symlinks(self, worktree_path: Path) -> None:
        """
        Set up symlinks for shared configuration files.

        Creates symlinks for .mcp.json, .env, and .claude/settings.local.json
        from the base repo to the worktree.
        """
        # Symlink shared configs
        for config_file in [".mcp.json", ".env"]:
            src = self.base_repo / config_file
            dst = worktree_path / config_file
            if src.exists() and not dst.exists():
                dst.symlink_to(src)
                logger.debug(f"Created symlink: {dst} -> {src}")

        # Claude settings (only for claude executor)
        if self.executor_type == "claude":
            claude_dir = worktree_path / ".claude"
            claude_dir.mkdir(exist_ok=True)
            settings_src = self.base_repo / ".claude" / "settings.local.json"
            settings_dst = claude_dir / "settings.local.json"
            if settings_src.exists() and not settings_dst.exists():
                settings_dst.symlink_to(settings_src)
                logger.debug(f"Created symlink: {settings_dst} -> {settings_src}")

    def _run_setup_command(self, worktree_path: Path) -> None:
        """
        Run the configured setup command in the worktree.

        Skips if no setup_command is configured.

        Args:
            worktree_path: Path to the worktree

        Raises:
            subprocess.CalledProcessError: If the setup command fails
        """
        if not self.setup_command:
            logger.debug(f"No setup command configured, skipping setup in {worktree_path}")
            return

        logger.info(f"Running setup command in {worktree_path}: {self.setup_command}")
        subprocess.run(
            self.setup_command,
            shell=True,
            cwd=worktree_path,
            check=True,
            capture_output=True,
        )

    def _update_main_branch(self) -> bool:
        """Fetch and fast-forward main before creating a new worktree."""
        try:
            # Fetch just main from origin
            subprocess.run(
                ["git", "fetch", "origin", "main"],
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            )

            # Get current branch to restore later
            current = subprocess.run(
                ["git", "rev-parse", "--abbrev-ref", "HEAD"],
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            ).stdout.strip()

            # Update main via fast-forward
            subprocess.run(
                ["git", "checkout", "main"],
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            )
            subprocess.run(
                ["git", "pull", "--ff-only"],
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            )

            # Restore previous branch if needed
            if current != "main":
                subprocess.run(
                    ["git", "checkout", current],
                    cwd=self.base_repo,
                    check=True,
                    capture_output=True,
                    text=True,
                )

            logger.info("Updated main branch from origin")
            return True
        except subprocess.CalledProcessError as e:
            logger.warning(f"Failed to update main branch: {e.stderr}")
            return False

    def create_worktree(self, card_id: str, branch_name: str | None = None) -> Path:
        """
        Create a worktree for a card.

        Creates worktree as a sibling directory: ~/src/repo/ → ~/src/card-<short_id>/
        Also sets up symlinks for shared configs and runs setup command if configured.

        Args:
            card_id: The card ID (used for worktree directory name)
            branch_name: Optional branch name (defaults to card/<short_id>)

        Returns:
            Path to the created worktree

        Raises:
            RuntimeError: If worktree creation fails
        """
        if not branch_name:
            branch_name = self._get_branch_name(card_id)

        worktree_path = self._get_worktree_path(card_id)

        # Check if worktree already exists
        if worktree_path.exists():
            logger.info(f"Worktree already exists: {worktree_path}")
            self.active_worktrees[card_id] = worktree_path
            return worktree_path

        logger.info(f"Creating worktree for card {card_id} at {worktree_path}")

        # Update main before creating worktree from latest
        self._update_main_branch()

        try:
            # Create worktree with new branch
            subprocess.run(
                ["git", "worktree", "add", "-b", branch_name, str(worktree_path)],
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            )
            logger.info(f"Created worktree: {worktree_path} (branch: {branch_name})")

        except subprocess.CalledProcessError as e:
            # If branch already exists, try without -b
            if "already exists" in e.stderr.lower():
                logger.debug(f"Branch {branch_name} exists, creating worktree without -b")
                try:
                    subprocess.run(
                        ["git", "worktree", "add", str(worktree_path), branch_name],
                        cwd=self.base_repo,
                        check=True,
                        capture_output=True,
                        text=True,
                    )
                    logger.info(
                        f"Created worktree: {worktree_path} (existing branch: {branch_name})"
                    )
                except subprocess.CalledProcessError as e2:
                    raise RuntimeError(f"Failed to create worktree: {e2.stderr}") from e2
            else:
                raise RuntimeError(f"Failed to create worktree: {e.stderr}") from e

        # Setup symlinks for shared configs
        self._setup_symlinks(worktree_path)

        # Run setup command (e.g. "npm install", "uv sync") if configured
        self._run_setup_command(worktree_path)

        # Track active worktree
        self.active_worktrees[card_id] = worktree_path

        return worktree_path

    def remove_worktree(self, card_id: str, force: bool = False) -> None:
        """
        Remove a worktree for a card.

        Args:
            card_id: The card ID
            force: Force removal even if worktree has uncommitted changes

        Raises:
            RuntimeError: If worktree removal fails
        """
        worktree_path = self._get_worktree_path(card_id)

        # Remove from tracking
        self.active_worktrees.pop(card_id, None)

        if not worktree_path.exists():
            logger.debug(f"Worktree does not exist: {worktree_path}")
            return

        logger.info(f"Removing worktree for card {card_id}")

        try:
            cmd = ["git", "worktree", "remove", str(worktree_path)]
            if force:
                cmd.append("--force")

            subprocess.run(
                cmd,
                cwd=self.base_repo,
                check=True,
                capture_output=True,
                text=True,
            )
            logger.info(f"Removed worktree: {worktree_path}")

        except subprocess.CalledProcessError as e:
            raise RuntimeError(f"Failed to remove worktree: {e.stderr}") from e

    def get_worktree_path(self, card_id: str) -> Path | None:
        """
        Get the path to a worktree for a card.

        Args:
            card_id: The card ID

        Returns:
            Path to the worktree if it exists, None otherwise
        """
        worktree_path = self._get_worktree_path(card_id)
        return worktree_path if worktree_path.exists() else None

    def list_worktrees(self) -> list[tuple[str, Path]]:
        """
        List all active worktrees managed by this manager.

        Returns:
            List of (card_id, worktree_path) tuples
        """
        # Return tracked worktrees that still exist
        worktrees = []
        for card_id, path in self.active_worktrees.items():
            if path.exists() and (path / ".git").exists():
                worktrees.append((card_id, path))

        return worktrees
