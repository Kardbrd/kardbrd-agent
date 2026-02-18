"""Tests for the kardbrd.yml rule engine."""

import pytest

from kardbrd_agent.rules import (
    MODEL_MAP,
    Rule,
    RuleEngine,
    load_rules,
    parse_rules,
)


class TestRule:
    """Tests for the Rule dataclass."""

    def test_rule_basic(self):
        """Test creating a basic rule."""
        rule = Rule(name="test", events=["card_moved"], action="/ke")
        assert rule.name == "test"
        assert rule.events == ["card_moved"]
        assert rule.action == "/ke"
        assert rule.model is None
        assert rule.list is None

    def test_rule_with_all_fields(self):
        """Test creating a rule with all fields."""
        rule = Rule(
            name="full rule",
            events=["card_moved", "card_created"],
            action="implement the card",
            model="haiku",
            list="In Progress",
            title="📦",
            label="exploration",
            content_contains="@claude",
        )
        assert rule.model == "haiku"
        assert rule.list == "In Progress"
        assert rule.title == "📦"
        assert rule.label == "exploration"
        assert rule.content_contains == "@claude"

    def test_model_id_resolves_short_names(self):
        """Test model_id resolves haiku/sonnet/opus to full IDs."""
        for short, full in MODEL_MAP.items():
            rule = Rule(name="t", events=["card_moved"], action="a", model=short)
            assert rule.model_id == full

    def test_model_id_case_insensitive(self):
        """Test model_id resolution is case-insensitive."""
        rule = Rule(name="t", events=["card_moved"], action="a", model="Haiku")
        assert rule.model_id == MODEL_MAP["haiku"]

    def test_model_id_none_when_no_model(self):
        """Test model_id returns None when model not set."""
        rule = Rule(name="t", events=["card_moved"], action="a")
        assert rule.model_id is None

    def test_model_id_passthrough_unknown(self):
        """Test model_id passes through unknown model strings."""
        rule = Rule(name="t", events=["card_moved"], action="a", model="claude-custom-123")
        assert rule.model_id == "claude-custom-123"


class TestRuleEngine:
    """Tests for the RuleEngine matching logic."""

    def test_match_card_moved_by_list(self):
        """Test matching card_moved event by list name."""
        engine = RuleEngine(
            rules=[
                Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas"),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_title": "Test"},
        )
        assert len(matches) == 1
        assert matches[0].name == "ideas"

    def test_match_card_moved_list_case_insensitive(self):
        """Test list matching is case-insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(name="ideas", events=["card_moved"], action="/ke", list="ideas"),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_title": "Test"},
        )
        assert len(matches) == 1

    def test_no_match_wrong_list(self):
        """Test no match when list doesn't match."""
        engine = RuleEngine(
            rules=[
                Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas"),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Done", "card_title": "Test"},
        )
        assert len(matches) == 0

    def test_no_match_wrong_event(self):
        """Test no match when event type doesn't match."""
        engine = RuleEngine(
            rules=[
                Rule(name="ideas", events=["card_moved"], action="/ke", list="Ideas"),
            ]
        )
        matches = engine.match(
            "comment_created",
            {"card_id": "abc", "content": "hello"},
        )
        assert len(matches) == 0

    def test_match_multiple_events(self):
        """Test rule with multiple events matches both."""
        rule = Rule(name="multi", events=["card_moved", "card_created"], action="/ke")
        engine = RuleEngine(rules=[rule])

        assert len(engine.match("card_moved", {"card_id": "a"})) == 1
        assert len(engine.match("card_created", {"card_id": "a"})) == 1
        assert len(engine.match("comment_created", {"card_id": "a"})) == 0

    def test_match_title_condition(self):
        """Test matching by card title substring."""
        engine = RuleEngine(
            rules=[
                Rule(name="box", events=["card_created"], action="deploy", title="📦"),
            ]
        )
        matches = engine.match(
            "card_created",
            {"card_id": "abc", "card_title": "📦 Release v2.0"},
        )
        assert len(matches) == 1

    def test_no_match_title_missing(self):
        """Test no match when title doesn't contain pattern."""
        engine = RuleEngine(
            rules=[
                Rule(name="box", events=["card_created"], action="deploy", title="📦"),
            ]
        )
        matches = engine.match(
            "card_created",
            {"card_id": "abc", "card_title": "Normal Card"},
        )
        assert len(matches) == 0

    def test_match_content_contains(self):
        """Test matching comment by content substring."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="mention",
                    events=["comment_created"],
                    action="respond",
                    content_contains="@claude",
                ),
            ]
        )
        matches = engine.match(
            "comment_created",
            {"card_id": "abc", "content": "Hey @Claude, fix this"},
        )
        assert len(matches) == 1

    def test_content_contains_case_insensitive(self):
        """Test content_contains matching is case-insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="mention",
                    events=["comment_created"],
                    action="respond",
                    content_contains="@CLAUDE",
                ),
            ]
        )
        matches = engine.match(
            "comment_created",
            {"card_id": "abc", "content": "Hey @claude, fix this"},
        )
        assert len(matches) == 1

    def test_match_multiple_rules(self):
        """Test multiple rules can match the same event."""
        engine = RuleEngine(
            rules=[
                Rule(name="rule1", events=["card_moved"], action="/ke", list="Ideas"),
                Rule(name="rule2", events=["card_moved"], action="/kp", list="Ideas"),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas"},
        )
        assert len(matches) == 2

    def test_match_no_conditions_matches_all_events(self):
        """Test rule with no conditions matches all events of that type."""
        engine = RuleEngine(
            rules=[
                Rule(name="all_moves", events=["card_moved"], action="/ke"),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Anything"},
        )
        assert len(matches) == 1

    def test_event_map_label_added(self):
        """Test 'label_added' maps to 'label_updated' event type."""
        engine = RuleEngine(
            rules=[
                Rule(name="label", events=["label_added"], action="/ke"),
            ]
        )
        matches = engine.match("label_updated", {"card_id": "abc"})
        assert len(matches) == 1

    def test_empty_rules_no_matches(self):
        """Test empty rule engine returns no matches."""
        engine = RuleEngine()
        assert engine.match("card_moved", {"card_id": "abc"}) == []


class TestParseRules:
    """Tests for parse_rules function."""

    def test_parse_basic_rules(self):
        """Test parsing basic rule dicts."""
        data = [
            {"name": "test", "event": "card_moved", "action": "/ke"},
            {"name": "test2", "event": "comment_created", "action": "respond"},
        ]
        rules = parse_rules(data)
        assert len(rules) == 2
        assert rules[0].name == "test"
        assert rules[0].events == ["card_moved"]
        assert rules[1].events == ["comment_created"]

    def test_parse_comma_separated_events(self):
        """Test parsing comma-separated event strings."""
        data = [{"name": "multi", "event": "card_moved, card_created", "action": "/ke"}]
        rules = parse_rules(data)
        assert rules[0].events == ["card_moved", "card_created"]

    def test_parse_with_conditions(self):
        """Test parsing rules with condition fields."""
        data = [
            {
                "name": "test",
                "event": "card_moved",
                "action": "/ke",
                "list": "Ideas",
                "title": "📦",
                "model": "haiku",
            }
        ]
        rules = parse_rules(data)
        assert rules[0].list == "Ideas"
        assert rules[0].title == "📦"
        assert rules[0].model == "haiku"

    def test_parse_missing_name_raises(self):
        """Test that missing name raises ValueError."""
        with pytest.raises(ValueError, match="missing 'name'"):
            parse_rules([{"event": "card_moved", "action": "/ke"}])

    def test_parse_missing_event_raises(self):
        """Test that missing event raises ValueError."""
        with pytest.raises(ValueError, match="missing 'event'"):
            parse_rules([{"name": "test", "action": "/ke"}])

    def test_parse_missing_action_raises(self):
        """Test that missing action raises ValueError."""
        with pytest.raises(ValueError, match="missing 'action'"):
            parse_rules([{"name": "test", "event": "card_moved"}])

    def test_parse_multiline_action(self):
        """Test parsing a rule with multiline action."""
        data = [
            {
                "name": "deploy",
                "event": "card_created",
                "action": "Run `make test-all`\nthen deploy if green",
            }
        ]
        rules = parse_rules(data)
        assert "\n" in rules[0].action


class TestLoadRules:
    """Tests for load_rules function."""

    def test_load_valid_yaml(self, tmp_path):
        """Test loading a valid kardbrd.yml file."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            """
- name: Auto-explore
  event: card_moved
  list: Ideas
  action: /ke

- name: Deploy box
  event: card_created
  title: "📦"
  model: haiku
  action: "Run make test-all"
"""
        )
        engine = load_rules(rules_file)
        assert len(engine.rules) == 2
        assert engine.rules[0].name == "Auto-explore"
        assert engine.rules[0].list == "Ideas"
        assert engine.rules[1].model == "haiku"

    def test_load_empty_yaml(self, tmp_path):
        """Test loading an empty YAML file returns empty engine."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("")
        engine = load_rules(rules_file)
        assert len(engine.rules) == 0

    def test_load_nonexistent_file_raises(self, tmp_path):
        """Test loading nonexistent file raises FileNotFoundError."""
        with pytest.raises(FileNotFoundError):
            load_rules(tmp_path / "missing.yml")

    def test_load_invalid_format_raises(self, tmp_path):
        """Test loading non-list YAML raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("key: value\n")
        with pytest.raises(ValueError, match="must be a YAML list"):
            load_rules(rules_file)

    def test_load_from_card_examples(self, tmp_path):
        """Test loading the example rules from the card description."""
        rules_file = tmp_path / "kardbrd.yml"
        yaml_content = (
            "- name: Explore ideas\n"
            "  event: card_created, card_moved\n"
            "  list: ideas\n"
            "  action: /ke\n"
            "\n"
            "- name: Box card ships\n"
            "  event: card_created\n"
            "  model: haiku\n"
            '  title: "\U0001f4e6"\n'
            "  action: Run tests and deploy\n"
            "\n"
            "- name: In progress starts coding\n"
            "  event: card_moved\n"
            "  list: in progress\n"
            "  action: implement and commit\n"
            "\n"
            "- name: Exploration label\n"
            "  event: label_added\n"
            "  action: /ke\n"
        )
        rules_file.write_text(yaml_content)
        engine = load_rules(rules_file)
        assert len(engine.rules) == 4
        assert engine.rules[0].events == ["card_created", "card_moved"]
        assert engine.rules[1].model == "haiku"
        assert engine.rules[1].title == "📦"
        assert engine.rules[2].list == "in progress"
        assert engine.rules[3].events == ["label_added"]
