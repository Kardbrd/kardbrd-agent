"""Rule engine for kardbrd.yml automation workflows."""

import logging
import time
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path

import yaml

logger = logging.getLogger("kardbrd_agent")

# Reserved action keyword for deterministic stop behavior.
# When a rule uses this action, the agent kills the active session
# instead of spawning Claude.
STOP_ACTION = "__stop__"

# Map of model short names to Claude CLI model identifiers
MODEL_MAP = {
    "opus": "claude-opus-4-6",
    "sonnet": "claude-sonnet-4-5-20250929",
    "haiku": "claude-haiku-4-5-20251001",
}

# All known WebSocket event types from the server spec.
# Used for validation only — rules match event names directly.
KNOWN_EVENTS = frozenset(
    {
        # Card events
        "card_created",
        "card_moved",
        "card_archived",
        "card_unarchived",
        "card_deleted",
        # Comment events
        "comment_created",
        "comment_deleted",
        # Reaction events
        "reaction_added",
        # Checklist events
        "checklist_created",
        "checklist_deleted",
        # Todo item events
        "todo_item_created",
        "todo_item_completed",
        "todo_item_reopened",
        "todo_item_deleted",
        "todo_item_assigned",
        "todo_item_unassigned",
        # Attachment events
        "attachment_created",
        "attachment_deleted",
        # Link events
        "card_link_created",
        "card_link_deleted",
        # Label events
        "label_added",
        "label_removed",
        # List events
        "list_created",
        "list_deleted",
    }
)

# All known fields in a rule entry
KNOWN_FIELDS = frozenset(
    {
        "name",
        "event",
        "action",
        "model",
        "list",
        "title",
        "label",
        "content_contains",
        "exclude_label",
        "require_label",
        "emoji",
        "require_user",
    }
)


class Severity(Enum):
    """Severity level for validation issues."""

    ERROR = "error"
    WARNING = "warning"


@dataclass
class ValidationIssue:
    """A single validation issue found in kardbrd.yml."""

    severity: Severity
    rule_index: int | None  # None for file-level issues
    rule_name: str | None
    message: str

    def __str__(self) -> str:
        prefix = self.severity.value.upper()
        if self.rule_name:
            return f"{prefix}: Rule '{self.rule_name}': {self.message}"
        if self.rule_index is not None:
            return f"{prefix}: Rule {self.rule_index}: {self.message}"
        return f"{prefix}: {self.message}"


@dataclass
class ValidationResult:
    """Result of validating a kardbrd.yml file."""

    issues: list[ValidationIssue] = field(default_factory=list)

    @property
    def errors(self) -> list[ValidationIssue]:
        return [i for i in self.issues if i.severity == Severity.ERROR]

    @property
    def warnings(self) -> list[ValidationIssue]:
        return [i for i in self.issues if i.severity == Severity.WARNING]

    @property
    def is_valid(self) -> bool:
        """True if there are no errors (warnings are OK)."""
        return len(self.errors) == 0


@dataclass
class Rule:
    """A single automation rule from kardbrd.yml."""

    name: str
    events: list[str]
    action: str
    model: str | None = None
    # Conditions
    list: str | None = None
    title: str | None = None
    label: str | None = None
    content_contains: str | None = None
    exclude_label: str | None = None
    require_label: str | None = None
    emoji: str | None = None
    require_user: str | None = None

    @property
    def is_stop(self) -> bool:
        """True if this rule triggers the deterministic stop action."""
        return self.action == STOP_ACTION

    @property
    def model_id(self) -> str | None:
        """Resolve short model name to full Claude CLI model ID."""
        if self.model is None:
            return None
        return MODEL_MAP.get(self.model.lower(), self.model)


@dataclass
class RuleEngine:
    """Matches incoming WebSocket events against kardbrd.yml rules."""

    rules: list[Rule] = field(default_factory=list)

    def match(self, event_type: str, message: dict) -> list[Rule]:
        """
        Find all rules that match the given event and message.

        Args:
            event_type: The WebSocket event type (e.g. "card_moved")
            message: The full WebSocket message payload

        Returns:
            List of matching Rule objects
        """
        matched = []
        for rule in self.rules:
            if self._matches(rule, event_type, message):
                matched.append(rule)
        return matched

    def _matches(self, rule: Rule, event_type: str, message: dict) -> bool:
        """Check if a single rule matches the event."""
        # Check event type
        if not self._event_matches(rule, event_type):
            return False

        # Check conditions
        if rule.list is not None:
            list_name = message.get("list_name", "")
            if list_name.lower() != rule.list.lower():
                return False

        if rule.title is not None:
            card_title = message.get("card_title", "")
            if card_title.lower() != rule.title.lower():
                return False

        if rule.label is not None:
            label_name = message.get("label_name", "")
            if label_name.lower() != rule.label.lower():
                return False

        if rule.content_contains is not None:
            content = message.get("content", "")
            if rule.content_contains.lower() not in content.lower():
                return False

        if rule.exclude_label is not None:
            card_labels = message.get("card_labels", [])
            for lbl in card_labels:
                if lbl.lower() == rule.exclude_label.lower():
                    return False

        if rule.require_label is not None:
            card_labels = message.get("card_labels", [])
            if rule.require_label.lower() not in [lbl.lower() for lbl in card_labels]:
                return False

        if rule.emoji is not None:
            msg_emoji = message.get("emoji", "")
            if msg_emoji != rule.emoji:
                return False

        if rule.require_user is not None:
            user_id = message.get("user_id", "")
            if user_id != rule.require_user:
                return False

        return True

    def _event_matches(self, rule: Rule, event_type: str) -> bool:
        """Check if the event type matches any of the rule's events."""
        return event_type in rule.events


def parse_rules(data: list[dict]) -> list[Rule]:
    """
    Parse a list of rule dicts (from YAML) into Rule objects.

    Args:
        data: List of rule dictionaries from YAML

    Returns:
        List of validated Rule objects

    Raises:
        ValueError: If a rule is missing required fields
    """
    rules = []
    for i, entry in enumerate(data):
        name = entry.get("name")
        if not name:
            raise ValueError(f"Rule {i} is missing 'name'")

        event_raw = entry.get("event")
        if not event_raw:
            raise ValueError(f"Rule '{name}' is missing 'event'")

        action = entry.get("action")
        if not action:
            raise ValueError(f"Rule '{name}' is missing 'action'")

        # Parse events: accept YAML lists or comma-separated strings
        if isinstance(event_raw, list):
            events = [str(e).strip() for e in event_raw]
        elif isinstance(event_raw, str):
            events = [e.strip() for e in event_raw.split(",")]
        else:
            raise ValueError(
                f"Rule '{name}': 'event' must be a string or list, got {type(event_raw).__name__}"
            )

        # Warn on unknown event names
        for ev in events:
            if ev not in KNOWN_EVENTS:
                logger.warning(
                    f"Rule '{name}': unknown event '{ev}', "
                    f"expected one of the known WebSocket events"
                )

        # Validate model if provided
        model = entry.get("model")
        if model and model.lower() not in MODEL_MAP:
            logger.warning(
                f"Rule '{name}': unknown model '{model}', expected one of {list(MODEL_MAP.keys())}"
            )

        rule = Rule(
            name=name,
            events=events,
            action=action,
            model=model,
            list=entry.get("list"),
            title=entry.get("title"),
            label=entry.get("label"),
            content_contains=entry.get("content_contains"),
            exclude_label=entry.get("exclude_label"),
            require_label=entry.get("require_label"),
            emoji=entry.get("emoji"),
            require_user=entry.get("require_user"),
        )
        rules.append(rule)

    return rules


def load_rules(path: Path) -> RuleEngine:
    """
    Load kardbrd.yml from the given path and return a RuleEngine.

    Args:
        path: Path to kardbrd.yml file

    Returns:
        Configured RuleEngine instance

    Raises:
        FileNotFoundError: If the file doesn't exist
        ValueError: If the file is invalid
    """
    if not path.exists():
        raise FileNotFoundError(f"Rules file not found: {path}")

    with open(path) as f:
        data = yaml.safe_load(f)

    if data is None:
        return RuleEngine(rules=[])

    if not isinstance(data, list):
        raise ValueError(f"kardbrd.yml must be a YAML list, got {type(data).__name__}")

    rules = parse_rules(data)
    logger.info(f"Loaded {len(rules)} rules from {path}")
    return RuleEngine(rules=rules)


def validate_rules_file(path: Path) -> ValidationResult:
    """
    Validate a kardbrd.yml file and return all issues found.

    Unlike load_rules/parse_rules which raise on the first error,
    this collects all errors and warnings for reporting.

    Args:
        path: Path to kardbrd.yml file

    Returns:
        ValidationResult with all issues found
    """
    result = ValidationResult()

    # Check file exists
    if not path.exists():
        result.issues.append(ValidationIssue(Severity.ERROR, None, None, f"File not found: {path}"))
        return result

    # Check YAML is parseable
    try:
        with open(path) as f:
            raw = f.read()
    except OSError as e:
        result.issues.append(ValidationIssue(Severity.ERROR, None, None, f"Cannot read file: {e}"))
        return result

    try:
        data = yaml.safe_load(raw)
    except yaml.YAMLError as e:
        result.issues.append(
            ValidationIssue(Severity.ERROR, None, None, f"Invalid YAML syntax: {e}")
        )
        return result

    # Empty file is valid
    if data is None:
        return result

    # Must be a list
    if not isinstance(data, list):
        result.issues.append(
            ValidationIssue(
                Severity.ERROR,
                None,
                None,
                f"File must contain a YAML list, got {type(data).__name__}",
            )
        )
        return result

    # Validate each rule
    for i, entry in enumerate(data):
        if not isinstance(entry, dict):
            result.issues.append(
                ValidationIssue(
                    Severity.ERROR,
                    i,
                    None,
                    f"Rule must be a mapping, got {type(entry).__name__}",
                )
            )
            continue

        name = entry.get("name")

        # Required fields
        if not name:
            result.issues.append(
                ValidationIssue(Severity.ERROR, i, None, "Missing required field 'name'")
            )
        if not entry.get("event"):
            result.issues.append(
                ValidationIssue(Severity.ERROR, i, name, "Missing required field 'event'")
            )
        if not entry.get("action"):
            result.issues.append(
                ValidationIssue(Severity.ERROR, i, name, "Missing required field 'action'")
            )

        # Check for unknown fields
        unknown = set(entry.keys()) - KNOWN_FIELDS
        if unknown:
            result.issues.append(
                ValidationIssue(
                    Severity.WARNING,
                    i,
                    name,
                    f"Unknown field(s): {', '.join(sorted(unknown))}",
                )
            )

        # Validate event field
        event_raw = entry.get("event")
        if event_raw:
            if isinstance(event_raw, list):
                events = [str(e).strip() for e in event_raw]
            elif isinstance(event_raw, str):
                events = [e.strip() for e in event_raw.split(",")]
            else:
                result.issues.append(
                    ValidationIssue(
                        Severity.ERROR,
                        i,
                        name,
                        f"'event' must be a string or list, got {type(event_raw).__name__}",
                    )
                )
                events = []

            for ev in events:
                if not ev:
                    result.issues.append(
                        ValidationIssue(
                            Severity.ERROR,
                            i,
                            name,
                            "Empty event name (trailing or double comma in event list)",
                        )
                    )
                elif ev not in KNOWN_EVENTS:
                    result.issues.append(
                        ValidationIssue(
                            Severity.WARNING,
                            i,
                            name,
                            f"Unknown event '{ev}'",
                        )
                    )

        # Validate model
        model = entry.get("model")
        if model:
            if not isinstance(model, str):
                result.issues.append(
                    ValidationIssue(
                        Severity.ERROR,
                        i,
                        name,
                        f"'model' must be a string, got {type(model).__name__}",
                    )
                )
            elif model.lower() not in MODEL_MAP:
                result.issues.append(
                    ValidationIssue(
                        Severity.WARNING,
                        i,
                        name,
                        f"Unknown model '{model}', expected one of: {', '.join(MODEL_MAP.keys())}",
                    )
                )

        # Validate action is a string
        action = entry.get("action")
        if action and not isinstance(action, str):
            result.issues.append(
                ValidationIssue(
                    Severity.ERROR,
                    i,
                    name,
                    f"'action' must be a string, got {type(action).__name__}",
                )
            )

    return result


class ReloadableRuleEngine:
    """
    Wraps a RuleEngine and hot-reloads kardbrd.yml when the file changes.

    Checks the file's mtime every `reload_interval` seconds (default 60).
    On error during reload, the previous rules remain active.
    """

    def __init__(self, path: Path, reload_interval: float = 60.0):
        self._path = path
        self._reload_interval = reload_interval
        self._engine = RuleEngine()
        self._last_mtime: float = 0.0
        self._last_check: float = 0.0
        # Initial load
        self._try_reload()

    @property
    def rules(self) -> list[Rule]:
        """Return the current rules, reloading if needed."""
        self._maybe_reload()
        return self._engine.rules

    def match(self, event_type: str, message: dict) -> list[Rule]:
        """Match an event against the current rules, reloading if needed."""
        self._maybe_reload()
        return self._engine.match(event_type, message)

    def _maybe_reload(self) -> None:
        """Check if enough time has passed and reload if file changed."""
        now = time.monotonic()
        if now - self._last_check < self._reload_interval:
            return
        self._last_check = now
        self._try_reload()

    def _try_reload(self) -> None:
        """Reload rules from disk if the file's mtime has changed."""
        try:
            if not self._path.exists():
                return
            mtime = self._path.stat().st_mtime
            if mtime == self._last_mtime:
                return
            self._last_mtime = mtime
            self._engine = load_rules(self._path)
            logger.info(f"Hot-reloaded {len(self._engine.rules)} rules from {self._path}")
        except Exception:
            logger.exception(f"Failed to reload rules from {self._path}")
