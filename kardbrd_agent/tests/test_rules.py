"""Tests for the kardbrd.yml rule engine."""

import time

import pytest

from kardbrd_agent.rules import (
    KNOWN_CONFIG_FIELDS,
    KNOWN_EVENTS,
    KNOWN_FIELDS,
    MODEL_MAP,
    STOP_ACTION,
    BoardConfig,
    ReloadableRuleEngine,
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
            exclude_label="Agent",
        )
        assert rule.model == "haiku"
        assert rule.list == "In Progress"
        assert rule.title == "📦"
        assert rule.label == "exploration"
        assert rule.content_contains == "@claude"
        assert rule.exclude_label == "Agent"

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

    def test_emoji_field_default_none(self):
        """Test emoji defaults to None."""
        rule = Rule(name="t", events=["reaction_added"], action="a")
        assert rule.emoji is None

    def test_emoji_field_set(self):
        """Test emoji can be set on a rule."""
        rule = Rule(name="t", events=["reaction_added"], action="ship", emoji="📦")
        assert rule.emoji == "📦"

    def test_is_stop_true_for_stop_action(self):
        """Test is_stop returns True for __stop__ action."""
        rule = Rule(name="stop", events=["reaction_added"], action=STOP_ACTION, emoji="🛑")
        assert rule.is_stop is True

    def test_is_stop_false_for_normal_action(self):
        """Test is_stop returns False for normal actions."""
        rule = Rule(name="ship", events=["reaction_added"], action="/ship", emoji="📦")
        assert rule.is_stop is False


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

    def test_match_title_exact(self):
        """Test matching by exact card title."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="box",
                    events=["card_created"],
                    action="deploy",
                    title="📦",
                ),
            ]
        )
        matches = engine.match(
            "card_created",
            {"card_id": "abc", "card_title": "📦"},
        )
        assert len(matches) == 1

    def test_match_title_case_insensitive(self):
        """Test title matching is case insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="deploy",
                    events=["card_created"],
                    action="deploy",
                    title="Deploy",
                ),
            ]
        )
        for card_title in ["Deploy", "deploy", "DEPLOY", "dEpLoY"]:
            matches = engine.match(
                "card_created",
                {"card_id": "abc", "card_title": card_title},
            )
            assert len(matches) == 1, f"Should match '{card_title}'"

    def test_no_match_title_substring(self):
        """Test no match when title is only a substring (exact match required)."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="box",
                    events=["card_created"],
                    action="deploy",
                    title="📦",
                ),
            ]
        )
        matches = engine.match(
            "card_created",
            {"card_id": "abc", "card_title": "📦 Release v2.0"},
        )
        assert len(matches) == 0

    def test_no_match_title_different(self):
        """Test no match when title doesn't match."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="box",
                    events=["card_created"],
                    action="deploy",
                    title="📦",
                ),
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

    def test_event_label_added_direct_match(self):
        """Test 'label_added' matches directly (no mapping)."""
        engine = RuleEngine(
            rules=[
                Rule(name="label", events=["label_added"], action="/ke"),
            ]
        )
        # Direct match
        matches = engine.match("label_added", {"card_id": "abc"})
        assert len(matches) == 1
        # Should NOT match label_removed
        matches = engine.match("label_removed", {"card_id": "abc"})
        assert len(matches) == 0

    def test_match_label_condition(self):
        """Test matching by label name on label_added event."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="bug label",
                    events=["label_added"],
                    action="/ke",
                    label="Bug",
                ),
            ]
        )
        matches = engine.match(
            "label_added",
            {
                "card_id": "abc",
                "label_id": "lbl1",
                "label_name": "Bug",
                "label_color": "red",
                "board_id": "board1",
            },
        )
        assert len(matches) == 1

    def test_label_condition_case_insensitive(self):
        """Test label matching is case-insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="bug label",
                    events=["label_added"],
                    action="/ke",
                    label="bug",
                ),
            ]
        )
        matches = engine.match(
            "label_added",
            {"card_id": "abc", "label_name": "Bug"},
        )
        assert len(matches) == 1

    def test_no_match_wrong_label(self):
        """Test no match when label name doesn't match."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="bug label",
                    events=["label_added"],
                    action="/ke",
                    label="Bug",
                ),
            ]
        )
        matches = engine.match(
            "label_added",
            {"card_id": "abc", "label_name": "Feature"},
        )
        assert len(matches) == 0

    def test_empty_rules_no_matches(self):
        """Test empty rule engine returns no matches."""
        engine = RuleEngine()
        assert engine.match("card_moved", {"card_id": "abc"}) == []

    def test_match_emoji_condition(self):
        """Test matching reaction_added by emoji."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship",
                    events=["reaction_added"],
                    action="ship the card",
                    emoji="📦",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "📦", "comment_id": "c1"},
        )
        assert len(matches) == 1
        assert matches[0].name == "ship"

    def test_no_match_wrong_emoji(self):
        """Test no match when emoji doesn't match."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship",
                    events=["reaction_added"],
                    action="ship the card",
                    emoji="📦",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "👍", "comment_id": "c1"},
        )
        assert len(matches) == 0

    def test_match_emoji_exact_unicode(self):
        """Test emoji matching is exact (no normalization)."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="stop",
                    events=["reaction_added"],
                    action=STOP_ACTION,
                    emoji="🛑",
                ),
            ]
        )
        # Match
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "🛑"},
        )
        assert len(matches) == 1

        # No match — different emoji
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "⛔"},
        )
        assert len(matches) == 0

    def test_reaction_rule_without_emoji_matches_all(self):
        """Test a reaction_added rule with no emoji condition matches all reactions."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="any reaction",
                    events=["reaction_added"],
                    action="log it",
                ),
            ]
        )
        for emoji in ["📦", "🛑", "👍", "🔄"]:
            matches = engine.match(
                "reaction_added",
                {"card_id": "abc", "emoji": emoji},
            )
            assert len(matches) == 1, f"Should match {emoji}"

    def test_multiple_emoji_rules_match_independently(self):
        """Test multiple rules with different emojis match independently."""
        engine = RuleEngine(
            rules=[
                Rule(name="ship", events=["reaction_added"], action="ship", emoji="📦"),
                Rule(name="stop", events=["reaction_added"], action=STOP_ACTION, emoji="🛑"),
                Rule(name="review", events=["reaction_added"], action="/kr", emoji="🔄"),
            ]
        )
        # Only the ship rule matches
        matches = engine.match("reaction_added", {"card_id": "abc", "emoji": "📦"})
        assert len(matches) == 1
        assert matches[0].name == "ship"

        # Only the stop rule matches
        matches = engine.match("reaction_added", {"card_id": "abc", "emoji": "🛑"})
        assert len(matches) == 1
        assert matches[0].name == "stop"

        # Unregistered emoji matches nothing
        matches = engine.match("reaction_added", {"card_id": "abc", "emoji": "😀"})
        assert len(matches) == 0


class TestExcludeLabel:
    """Tests for exclude_label condition."""

    def test_exclude_label_skips_card_with_label(self):
        """Test rule with exclude_label skips cards that have the excluded label."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    exclude_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {
                "card_id": "abc",
                "list_name": "Ideas",
                "card_labels": ["Agent", "Bug"],
            },
        )
        assert len(matches) == 0

    def test_exclude_label_matches_card_without_label(self):
        """Test rule with exclude_label matches cards that don't have the excluded label."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    exclude_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {
                "card_id": "abc",
                "list_name": "Ideas",
                "card_labels": ["Bug", "Feature"],
            },
        )
        assert len(matches) == 1

    def test_exclude_label_case_insensitive(self):
        """Test exclude_label matching is case-insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    exclude_label="agent",
                ),
            ]
        )
        # "Agent" on card should match "agent" in rule (case-insensitive)
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "card_labels": ["Agent"]},
        )
        assert len(matches) == 0

        # Reverse: "AGENT" on card should match "agent" in rule
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "card_labels": ["AGENT"]},
        )
        assert len(matches) == 0

    def test_exclude_label_with_empty_card_labels(self):
        """Test exclude_label matches when card has no labels."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    exclude_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "card_labels": []},
        )
        assert len(matches) == 1

    def test_exclude_label_with_missing_card_labels(self):
        """Test exclude_label matches when card_labels is not in message."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    exclude_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc"},
        )
        assert len(matches) == 1

    def test_exclude_label_combined_with_list_condition(self):
        """Test exclude_label works alongside other conditions."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="auto",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    exclude_label="Agent",
                ),
            ]
        )
        # Matches: right list, no excluded label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Bug"]},
        )
        assert len(matches) == 1

        # No match: right list, but has excluded label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Agent"]},
        )
        assert len(matches) == 0

        # No match: wrong list, no excluded label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Done", "card_labels": ["Bug"]},
        )
        assert len(matches) == 0

    def test_exclude_label_default_none(self):
        """Test exclude_label defaults to None."""
        rule = Rule(name="test", events=["card_moved"], action="/ke")
        assert rule.exclude_label is None


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

    def test_parse_string_event_is_single(self):
        """Test a plain string event is treated as a single event (no comma splitting)."""
        data = [{"name": "single", "event": "card_moved", "action": "/ke"}]
        rules = parse_rules(data)
        assert rules[0].events == ["card_moved"]

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

    def test_parse_with_exclude_label(self):
        """Test parsing rules with exclude_label field."""
        data = [
            {
                "name": "test",
                "event": "card_moved",
                "action": "/ke",
                "exclude_label": "Agent",
            }
        ]
        rules = parse_rules(data)
        assert rules[0].exclude_label == "Agent"

    def test_parse_with_emoji(self):
        """Test parsing rules with emoji field."""
        data = [
            {
                "name": "ship reaction",
                "event": "reaction_added",
                "action": "ship the card",
                "emoji": "📦",
            }
        ]
        rules = parse_rules(data)
        assert rules[0].emoji == "📦"
        assert rules[0].events == ["reaction_added"]

    def test_parse_stop_rule(self):
        """Test parsing a stop rule with __stop__ action."""
        data = [
            {
                "name": "stop agent",
                "event": "reaction_added",
                "action": "__stop__",
                "emoji": "🛑",
            }
        ]
        rules = parse_rules(data)
        assert rules[0].is_stop is True
        assert rules[0].emoji == "🛑"

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

    def test_parse_yaml_list_events(self):
        """Test parsing YAML list events produces correct events list."""
        data = [
            {
                "name": "multi",
                "event": ["card_moved", "card_created"],
                "action": "/ke",
            }
        ]
        rules = parse_rules(data)
        assert rules[0].events == ["card_moved", "card_created"]

    def test_parse_yaml_list_single_event(self):
        """Test parsing a single-item YAML list event."""
        data = [{"name": "single", "event": ["card_moved"], "action": "/ke"}]
        rules = parse_rules(data)
        assert rules[0].events == ["card_moved"]

    def test_parse_event_invalid_type_raises(self):
        """Test that non-string, non-list event type raises ValueError."""
        with pytest.raises(ValueError, match="must be a string or list"):
            parse_rules([{"name": "test", "event": 123, "action": "/ke"}])

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
            "board_id: test123\n"
            "agent: TestBot\n"
            "rules:\n"
            "  - name: Auto-explore\n"
            "    event: card_moved\n"
            "    list: Ideas\n"
            "    action: /ke\n"
            "  - name: Deploy box\n"
            "    event: card_created\n"
            '    title: "\U0001f4e6"\n'
            "    model: haiku\n"
            '    action: "Run make test-all"\n'
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 2
        assert engine.rules[0].name == "Auto-explore"
        assert engine.rules[0].list == "Ideas"
        assert engine.rules[1].model == "haiku"
        assert config.board_id == "test123"

    def test_load_empty_yaml_raises(self, tmp_path):
        """Test loading an empty YAML file raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("")
        with pytest.raises(ValueError, match="empty"):
            load_rules(rules_file)

    def test_load_nonexistent_file_raises(self, tmp_path):
        """Test loading nonexistent file raises FileNotFoundError."""
        with pytest.raises(FileNotFoundError):
            load_rules(tmp_path / "missing.yml")

    def test_load_bare_list_raises(self, tmp_path):
        """Test loading a bare list raises ValueError (old format not supported)."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("- name: test\n  event: card_moved\n  action: /ke\n")
        with pytest.raises(ValueError, match="dict"):
            load_rules(rules_file)

    def test_load_from_card_examples(self, tmp_path):
        """Test loading the example rules from the card description."""
        rules_file = tmp_path / "kardbrd.yml"
        yaml_content = (
            "board_id: test123\n"
            "agent: TestBot\n"
            "rules:\n"
            "  - name: Explore ideas\n"
            "    event:\n"
            "      - card_created\n"
            "      - card_moved\n"
            "    list: ideas\n"
            "    action: /ke\n"
            "  - name: Box card ships\n"
            "    event: card_created\n"
            "    model: haiku\n"
            '    title: "\U0001f4e6"\n'
            "    action: Run tests and deploy\n"
            "  - name: In progress starts coding\n"
            "    event: card_moved\n"
            "    list: in progress\n"
            "    action: implement and commit\n"
            "  - name: Exploration label\n"
            "    event: label_added\n"
            "    action: /ke\n"
        )
        rules_file.write_text(yaml_content)
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 4
        assert engine.rules[0].events == ["card_created", "card_moved"]
        assert engine.rules[1].model == "haiku"
        assert engine.rules[1].title == "\U0001f4e6"
        assert engine.rules[2].list == "in progress"
        assert engine.rules[3].events == ["label_added"]
        assert config.board_id == "test123"

    def test_load_yaml_list_events(self, tmp_path):
        """Test loading a YAML file with list-style events works end-to-end."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "rules:\n"
            "  - name: Explore ideas\n"
            "    event:\n"
            "      - card_created\n"
            "      - card_moved\n"
            "    list: Ideas\n"
            "    action: /ke\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 1
        assert engine.rules[0].events == ["card_created", "card_moved"]

    def test_load_own_kardbrd_yml(self):
        """Test the repo's own kardbrd.yml loads without errors."""
        from pathlib import Path

        own = Path(__file__).parent.parent.parent / "kardbrd.yml"
        if not own.exists():
            pytest.skip("kardbrd.yml not found")
        engine, config = load_rules(own)
        assert len(engine.rules) > 0, "kardbrd.yml should contain at least one rule"
        assert config.board_id == "0gl5MlBZ"
        assert config.agent_name == "KABot"

    def test_load_mbpbot_kardbrd_yml(self):
        """Test MBPBot's kardbrd.yml fixture loads without errors."""
        from pathlib import Path

        fixture = Path(__file__).parent / "fixtures" / "mbpbot_kardbrd.yml"
        if not fixture.exists():
            pytest.skip("mbpbot_kardbrd.yml fixture not found")
        engine, config = load_rules(fixture)
        assert len(engine.rules) > 0, "MBPBot kardbrd.yml should contain at least one rule"
        # Verify specific rules are loaded
        rule_names = [r.name for r in engine.rules]
        assert "Explore new cards in Ideas" in rule_names
        assert "Box card plans deployment" in rule_names
        assert config.board_id == "0gl5MlBZ"
        assert config.agent_name == "MBPBot"

    def test_load_reaction_rules_yaml(self, tmp_path):
        """Test loading reaction-based rules from kardbrd.yml."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "rules:\n"
            "  - name: Ship via reaction\n"
            "    event: reaction_added\n"
            '    emoji: "\U0001f4e6"\n'
            "    model: sonnet\n"
            "    action: Ship this card\n"
            "  - name: Stop agent\n"
            "    event: reaction_added\n"
            '    emoji: "\U0001f6d1"\n'
            "    action: __stop__\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 2
        assert engine.rules[0].emoji == "\U0001f4e6"
        assert engine.rules[0].model == "sonnet"
        assert not engine.rules[0].is_stop
        assert engine.rules[1].emoji == "\U0001f6d1"
        assert engine.rules[1].is_stop


class TestKnownFields:
    """Tests for KNOWN_FIELDS validation."""

    def test_emoji_is_known_field(self):
        """Test emoji is in KNOWN_FIELDS."""
        assert "emoji" in KNOWN_FIELDS

    def test_exclude_label_is_known_field(self):
        """Test exclude_label is in KNOWN_FIELDS."""
        assert "exclude_label" in KNOWN_FIELDS

    def test_known_fields_includes_all_conditions(self):
        """Test KNOWN_FIELDS includes every condition field."""
        expected = {
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
        assert expected == KNOWN_FIELDS

    def test_known_fields_is_frozenset(self):
        """Test KNOWN_FIELDS is immutable."""
        assert isinstance(KNOWN_FIELDS, frozenset)


class TestKnownEvents:
    """Tests for KNOWN_EVENTS and event validation."""

    def test_known_events_has_24_events(self):
        """Test KNOWN_EVENTS contains all 24 events from the spec."""
        assert len(KNOWN_EVENTS) == 24

    @pytest.mark.parametrize(
        "event",
        [
            "card_created",
            "card_moved",
            "card_archived",
            "card_unarchived",
            "card_deleted",
            "comment_created",
            "comment_deleted",
            "reaction_added",
            "checklist_created",
            "checklist_deleted",
            "todo_item_created",
            "todo_item_completed",
            "todo_item_reopened",
            "todo_item_deleted",
            "todo_item_assigned",
            "todo_item_unassigned",
            "attachment_created",
            "attachment_deleted",
            "card_link_created",
            "card_link_deleted",
            "label_added",
            "label_removed",
            "list_created",
            "list_deleted",
        ],
    )
    def test_known_event(self, event):
        """Test each spec event is in KNOWN_EVENTS."""
        assert event in KNOWN_EVENTS

    def test_parse_warns_on_unknown_event(self, caplog):
        """Test parse_rules warns on unknown event names."""
        import logging

        with caplog.at_level(logging.WARNING, logger="kardbrd_agent"):
            parse_rules(
                [
                    {
                        "name": "test",
                        "event": "made_up_event",
                        "action": "/ke",
                    }
                ]
            )
        assert "unknown event 'made_up_event'" in caplog.text

    def test_parse_no_warning_on_known_event(self, caplog):
        """Test parse_rules does not warn on known event names."""
        import logging

        with caplog.at_level(logging.WARNING, logger="kardbrd_agent"):
            parse_rules(
                [
                    {
                        "name": "test",
                        "event": "card_moved",
                        "action": "/ke",
                    }
                ]
            )
        assert "unknown event" not in caplog.text


class TestEventMatching:
    """Tests for direct event matching across all spec event types."""

    @pytest.mark.parametrize(
        "event_type",
        [
            "card_created",
            "card_moved",
            "card_archived",
            "card_unarchived",
            "card_deleted",
            "comment_created",
            "comment_deleted",
            "reaction_added",
            "checklist_created",
            "checklist_deleted",
            "todo_item_created",
            "todo_item_completed",
            "todo_item_reopened",
            "todo_item_deleted",
            "todo_item_assigned",
            "todo_item_unassigned",
            "attachment_created",
            "attachment_deleted",
            "card_link_created",
            "card_link_deleted",
            "label_added",
            "label_removed",
            "list_created",
            "list_deleted",
        ],
    )
    def test_rule_matches_own_event_type(self, event_type):
        """Test a rule with a given event matches that event directly."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="test",
                    events=[event_type],
                    action="do stuff",
                ),
            ]
        )
        matches = engine.match(event_type, {"card_id": "abc"})
        assert len(matches) == 1

    def test_label_added_does_not_match_label_removed(self):
        """Test label_added rule does NOT match label_removed event."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="label",
                    events=["label_added"],
                    action="/ke",
                ),
            ]
        )
        matches = engine.match("label_removed", {"card_id": "abc"})
        assert len(matches) == 0

    def test_card_archived_does_not_match_card_created(self):
        """Test card_archived rule does NOT match card_created event."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="archive",
                    events=["card_archived"],
                    action="cleanup",
                ),
            ]
        )
        matches = engine.match("card_created", {"card_id": "abc"})
        assert len(matches) == 0


class TestSpecEventPayloads:
    """Tests matching with realistic event payloads from the spec."""

    def test_card_created_payload(self):
        """Test matching card_created with full spec payload."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="new card",
                    events=["card_created"],
                    action="/ke",
                    list="Backlog",
                ),
            ]
        )
        matches = engine.match(
            "card_created",
            {
                "event_type": "card_created",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "card_title": "Implement dark mode",
                "list_id": "01JM9F2A5P3NXQK7D8HMCY6W1B",
                "list_name": "Backlog",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1

    def test_comment_created_payload(self):
        """Test matching comment_created with full spec payload."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="mention",
                    events=["comment_created"],
                    action="respond",
                    content_contains="@MBPBot",
                ),
            ]
        )
        matches = engine.match(
            "comment_created",
            {
                "event_type": "comment_created",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "comment_id": "01JMDG7Y8L0RASMT3U6CJPQ9W5",
                "content": "@MBPBot please review this card",
                "author_id": "01JM5K2B3N4PXQR7D8HMFY6W1A",
                "author_name": "Paul",
                "created_at": "2026-02-18T14:30:00.000000+00:00",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1

    def test_label_added_payload(self):
        """Test matching label_added with full spec payload."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="bug label",
                    events=["label_added"],
                    action="/ke",
                    label="Bug",
                ),
            ]
        )
        matches = engine.match(
            "label_added",
            {
                "event_type": "label_added",
                "label_id": "01JMDL1D3R5WFXSZ8A1HPUT2V0",
                "label_name": "Bug",
                "label_color": "red",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1

    def test_reaction_added_payload_no_board_id(self):
        """Test reaction_added has no board_id per spec."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="react",
                    events=["reaction_added"],
                    action="acknowledge",
                ),
            ]
        )
        # reaction_added does NOT include board_id per spec
        matches = engine.match(
            "reaction_added",
            {
                "event_type": "reaction_added",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "comment_id": "01JMDG7Y8L0RASMT3U6CJPQ9W5",
                "emoji": "\U0001f44d",
                "user_id": "01JM5K2B3N4PXQR7D8HMFY6W1A",
                "user_name": "Paul",
            },
        )
        assert len(matches) == 1

    def test_card_moved_payload(self):
        """Test matching card_moved with full spec payload."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="in progress",
                    events=["card_moved"],
                    action="implement",
                    list="In Progress",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {
                "event_type": "card_moved",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "card_title": "Implement dark mode",
                "list_id": "01JM9F4B6Q4RYSL8E9JNDZ7W2C",
                "list_name": "In Progress",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1

    def test_todo_item_completed_payload(self):
        """Test matching todo_item_completed event."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="todo done",
                    events=["todo_item_completed"],
                    action="check progress",
                ),
            ]
        )
        matches = engine.match(
            "todo_item_completed",
            {
                "event_type": "todo_item_completed",
                "todo_item_id": "01JMDH8A0N2TCUPW5X8ELQR9S7",
                "todo_item_title": "Write unit tests",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1

    def test_attachment_created_payload(self):
        """Test matching attachment_created event."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="new attachment",
                    events=["attachment_created"],
                    action="process attachment",
                ),
            ]
        )
        matches = engine.match(
            "attachment_created",
            {
                "event_type": "attachment_created",
                "attachment_id": "01JMDJ9B1P3UDVQX6Y9FMRS0T8",
                "filename": "implementation-plan.md",
                "card_id": "01JMDC3X7K9QZJVW2T5BHFN8R4",
                "board_id": "eykqek0W",
            },
        )
        assert len(matches) == 1


class TestReloadableRuleEngine:
    """Tests for ReloadableRuleEngine hot reload."""

    def _write_yml(self, path, rules_yaml, board_id="test", agent="Bot"):
        """Helper to write a valid kardbrd.yml with config."""
        path.write_text(f"board_id: {board_id}\nagent: {agent}\nrules:\n{rules_yaml}")

    def test_initial_load(self, tmp_path):
        """Test initial load reads rules from file."""
        rules_file = tmp_path / "kardbrd.yml"
        self._write_yml(rules_file, "  - name: test\n    event: card_moved\n    action: /ke\n")
        engine = ReloadableRuleEngine(rules_file)
        assert len(engine.rules) == 1
        assert engine.rules[0].name == "test"

    def test_match_delegates_to_engine(self, tmp_path):
        """Test match() works through ReloadableRuleEngine."""
        rules_file = tmp_path / "kardbrd.yml"
        self._write_yml(
            rules_file, "  - name: test\n    event: card_moved\n    list: Ideas\n    action: /ke\n"
        )
        engine = ReloadableRuleEngine(rules_file)
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas"},
        )
        assert len(matches) == 1

    def test_reload_on_file_change(self, tmp_path):
        """Test rules are reloaded when file changes."""
        rules_file = tmp_path / "kardbrd.yml"
        self._write_yml(rules_file, "  - name: rule1\n    event: card_moved\n    action: /ke\n")
        engine = ReloadableRuleEngine(rules_file, reload_interval=0)
        assert len(engine.rules) == 1

        # Modify the file
        time.sleep(0.05)  # Ensure mtime changes
        self._write_yml(
            rules_file,
            "  - name: rule1\n    event: card_moved\n    action: /ke\n"
            "  - name: rule2\n    event: card_created\n    action: /kp\n",
        )

        # Access rules — should trigger reload
        assert len(engine.rules) == 2

    def test_no_reload_within_interval(self, tmp_path):
        """Test rules are NOT reloaded within the reload interval."""
        rules_file = tmp_path / "kardbrd.yml"
        self._write_yml(rules_file, "  - name: rule1\n    event: card_moved\n    action: /ke\n")
        # Use a very long interval
        engine = ReloadableRuleEngine(rules_file, reload_interval=9999)
        assert len(engine.rules) == 1

        # Modify the file
        time.sleep(0.05)
        self._write_yml(
            rules_file,
            "  - name: rule1\n    event: card_moved\n    action: /ke\n"
            "  - name: rule2\n    event: card_created\n    action: /kp\n",
        )

        # Should NOT reload yet (interval hasn't passed)
        assert len(engine.rules) == 1

    def test_survives_invalid_yaml_on_reload(self, tmp_path):
        """Test engine keeps old rules when reload encounters bad YAML."""
        rules_file = tmp_path / "kardbrd.yml"
        self._write_yml(rules_file, "  - name: rule1\n    event: card_moved\n    action: /ke\n")
        engine = ReloadableRuleEngine(rules_file, reload_interval=0)
        assert len(engine.rules) == 1

        # Write file missing required field (agent without board_id)
        time.sleep(0.05)
        rules_file.write_text("agent: Bot\nrules: []\n")

        # Should keep old rules
        assert len(engine.rules) == 1

    def test_missing_file_on_init(self, tmp_path):
        """Test engine starts empty if file doesn't exist."""
        engine = ReloadableRuleEngine(tmp_path / "missing.yml", reload_interval=0)
        assert len(engine.rules) == 0

    def test_file_appears_after_init(self, tmp_path):
        """Test engine picks up file that appears after init."""
        rules_file = tmp_path / "kardbrd.yml"
        engine = ReloadableRuleEngine(rules_file, reload_interval=0)
        assert len(engine.rules) == 0

        # Create the file
        self._write_yml(rules_file, "  - name: rule1\n    event: card_moved\n    action: /ke\n")

        # Should pick it up on next access
        assert len(engine.rules) == 1


class TestRequireLabel:
    """Tests for the require_label condition."""

    def test_require_label_matches_when_present(self):
        """Test rule matches when card has the required label."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    require_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Agent", "Workflow"]},
        )
        assert len(matches) == 1

    def test_require_label_no_match_when_missing(self):
        """Test rule doesn't match when card lacks the required label."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    require_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Workflow"]},
        )
        assert len(matches) == 0

    def test_require_label_no_match_when_card_labels_empty(self):
        """Test rule doesn't match when card has no labels."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    require_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": []},
        )
        assert len(matches) == 0

    def test_require_label_no_match_when_card_labels_absent(self):
        """Test rule doesn't match when card_labels key is not in message."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    require_label="Agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas"},
        )
        assert len(matches) == 0

    def test_require_label_case_insensitive(self):
        """Test require_label matching is case-insensitive."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent only",
                    events=["card_moved"],
                    action="/ke",
                    require_label="agent",
                ),
            ]
        )
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "card_labels": ["Agent"]},
        )
        assert len(matches) == 1

    def test_require_label_combined_with_list(self):
        """Test require_label works together with list condition."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="agent ideas",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                    require_label="Agent",
                ),
            ]
        )
        # Matches: right list + right label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Agent"]},
        )
        assert len(matches) == 1

        # No match: right list, wrong label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Workflow"]},
        )
        assert len(matches) == 0

        # No match: wrong list, right label
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Done", "card_labels": ["Agent"]},
        )
        assert len(matches) == 0

    def test_rule_without_require_label_ignores_card_labels(self):
        """Test rules without require_label match regardless of card labels."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="all cards",
                    events=["card_moved"],
                    action="/ke",
                    list="Ideas",
                ),
            ]
        )
        # Matches without card_labels
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas"},
        )
        assert len(matches) == 1

        # Also matches with card_labels
        matches = engine.match(
            "card_moved",
            {"card_id": "abc", "list_name": "Ideas", "card_labels": ["Agent"]},
        )
        assert len(matches) == 1

    def test_parse_rules_with_require_label(self):
        """Test require_label is parsed from YAML rule data."""
        rules = parse_rules(
            [
                {
                    "name": "agent only",
                    "event": "card_moved",
                    "action": "/ke",
                    "require_label": "Agent",
                }
            ]
        )
        assert len(rules) == 1
        assert rules[0].require_label == "Agent"

    def test_parse_rules_without_require_label(self):
        """Test require_label defaults to None when not specified."""
        rules = parse_rules(
            [
                {
                    "name": "all cards",
                    "event": "card_moved",
                    "action": "/ke",
                }
            ]
        )
        assert len(rules) == 1
        assert rules[0].require_label is None

    def test_load_rules_with_require_label(self, tmp_path):
        """Test loading kardbrd.yml with require_label rules."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "rules:\n"
            "  - name: agent only\n"
            "    event: card_moved\n"
            "    list: Ideas\n"
            "    require_label: Agent\n"
            "    action: /ke\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 1
        assert engine.rules[0].require_label == "Agent"


class TestEmoji:
    """Tests for the emoji condition."""

    def test_emoji_field_default_none(self):
        """Test emoji defaults to None."""
        rule = Rule(name="t", events=["reaction_added"], action="a")
        assert rule.emoji is None

    def test_emoji_field_set(self):
        """Test emoji can be set on a rule."""
        rule = Rule(name="t", events=["reaction_added"], action="ship", emoji="📦")
        assert rule.emoji == "📦"

    def test_emoji_matches(self):
        """Test rule matches when emoji in message matches rule emoji."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship",
                    events=["reaction_added"],
                    action="ship",
                    emoji="📦",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "comment_id": "c1", "emoji": "📦", "user_id": "u1"},
        )
        assert len(matches) == 1

    def test_emoji_no_match_different(self):
        """Test rule doesn't match when emoji differs."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship",
                    events=["reaction_added"],
                    action="ship",
                    emoji="📦",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "comment_id": "c1", "emoji": "🔄", "user_id": "u1"},
        )
        assert len(matches) == 0

    def test_emoji_no_match_missing(self):
        """Test rule doesn't match when emoji is missing from message."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship",
                    events=["reaction_added"],
                    action="ship",
                    emoji="📦",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "comment_id": "c1"},
        )
        assert len(matches) == 0

    def test_rule_without_emoji_matches_all_reactions(self):
        """Test rule without emoji condition matches all reaction_added events."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="any reaction",
                    events=["reaction_added"],
                    action="log",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "🎉"},
        )
        assert len(matches) == 1

    def test_multiple_emoji_rules(self):
        """Test different emoji rules match their respective reactions."""
        engine = RuleEngine(
            rules=[
                Rule(name="ship", events=["reaction_added"], action="ship", emoji="📦"),
                Rule(name="stop", events=["reaction_added"], action="__stop__", emoji="🛑"),
                Rule(name="review", events=["reaction_added"], action="/kr", emoji="🔄"),
            ]
        )
        matches = engine.match("reaction_added", {"card_id": "abc", "emoji": "🛑"})
        assert len(matches) == 1
        assert matches[0].name == "stop"

    def test_parse_emoji_from_yaml(self):
        """Test emoji is parsed from YAML rule data."""
        rules = parse_rules(
            [
                {
                    "name": "ship",
                    "event": "reaction_added",
                    "emoji": "📦",
                    "action": "ship it",
                }
            ]
        )
        assert len(rules) == 1
        assert rules[0].emoji == "📦"


class TestIsStop:
    """Tests for the is_stop property and STOP_ACTION."""

    def test_is_stop_true(self):
        """Test is_stop returns True for __stop__ action."""
        rule = Rule(name="stop", events=["reaction_added"], action=STOP_ACTION, emoji="🛑")
        assert rule.is_stop is True

    def test_is_stop_false_for_normal_action(self):
        """Test is_stop returns False for normal actions."""
        rule = Rule(name="ship", events=["reaction_added"], action="/ki", emoji="📦")
        assert rule.is_stop is False

    def test_is_stop_false_for_similar_string(self):
        """Test is_stop is exact match, not substring."""
        rule = Rule(name="t", events=["reaction_added"], action="__stop__extra")
        assert rule.is_stop is False

    def test_stop_action_constant(self):
        """Test STOP_ACTION constant value."""
        assert STOP_ACTION == "__stop__"


class TestRequireUser:
    """Tests for the require_user condition."""

    def test_require_user_matches(self):
        """Test rule matches when user_id matches require_user."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="paul only",
                    events=["reaction_added"],
                    action="ship",
                    emoji="✅",
                    require_user="E21K9jmv",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "✅", "user_id": "E21K9jmv"},
        )
        assert len(matches) == 1

    def test_require_user_no_match_wrong_user(self):
        """Test rule doesn't match when user_id differs."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="paul only",
                    events=["reaction_added"],
                    action="ship",
                    emoji="✅",
                    require_user="E21K9jmv",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "✅", "user_id": "kw4DANjz"},
        )
        assert len(matches) == 0

    def test_require_user_no_match_missing_user_id(self):
        """Test rule doesn't match when user_id is absent from message."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="paul only",
                    events=["reaction_added"],
                    action="ship",
                    require_user="E21K9jmv",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "✅"},
        )
        assert len(matches) == 0

    def test_require_user_exact_match(self):
        """Test require_user is exact match (not case-insensitive like labels)."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="paul only",
                    events=["reaction_added"],
                    action="ship",
                    require_user="E21K9jmv",
                ),
            ]
        )
        # User IDs are case-sensitive — lowercase should NOT match
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "user_id": "e21k9jmv"},
        )
        assert len(matches) == 0

    def test_require_user_combined_with_emoji_and_label(self):
        """Test require_user works with emoji and require_label conditions."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="ship approval",
                    events=["reaction_added"],
                    action="ship",
                    emoji="✅",
                    require_label="Agent",
                    require_user="E21K9jmv",
                ),
            ]
        )
        # All conditions met
        matches = engine.match(
            "reaction_added",
            {
                "card_id": "abc",
                "emoji": "✅",
                "user_id": "E21K9jmv",
                "card_labels": ["Agent", "Workflow"],
            },
        )
        assert len(matches) == 1

        # Wrong user
        matches = engine.match(
            "reaction_added",
            {
                "card_id": "abc",
                "emoji": "✅",
                "user_id": "kw4DANjz",
                "card_labels": ["Agent"],
            },
        )
        assert len(matches) == 0

        # Wrong emoji
        matches = engine.match(
            "reaction_added",
            {
                "card_id": "abc",
                "emoji": "📦",
                "user_id": "E21K9jmv",
                "card_labels": ["Agent"],
            },
        )
        assert len(matches) == 0

        # Missing required label
        matches = engine.match(
            "reaction_added",
            {
                "card_id": "abc",
                "emoji": "✅",
                "user_id": "E21K9jmv",
                "card_labels": ["Workflow"],
            },
        )
        assert len(matches) == 0

    def test_rule_without_require_user_matches_any_user(self):
        """Test rules without require_user match regardless of user_id."""
        engine = RuleEngine(
            rules=[
                Rule(
                    name="anyone",
                    events=["reaction_added"],
                    action="stop",
                    emoji="🛑",
                ),
            ]
        )
        matches = engine.match(
            "reaction_added",
            {"card_id": "abc", "emoji": "🛑", "user_id": "anyone123"},
        )
        assert len(matches) == 1

    def test_parse_require_user_from_yaml(self):
        """Test require_user is parsed from YAML rule data."""
        rules = parse_rules(
            [
                {
                    "name": "paul ship",
                    "event": "reaction_added",
                    "emoji": "✅",
                    "require_user": "E21K9jmv",
                    "action": "ship",
                }
            ]
        )
        assert len(rules) == 1
        assert rules[0].require_user == "E21K9jmv"

    def test_parse_without_require_user(self):
        """Test require_user defaults to None when not specified."""
        rules = parse_rules([{"name": "t", "event": "card_moved", "action": "/ke"}])
        assert rules[0].require_user is None


class TestBoardConfig:
    """Tests for the BoardConfig dataclass."""

    def test_board_config_basic(self):
        """Test creating BoardConfig with required fields."""
        config = BoardConfig(board_id="abc123", agent_name="TestBot")
        assert config.board_id == "abc123"
        assert config.agent_name == "TestBot"
        assert config.api_url is None
        assert config.executor is None

    def test_board_config_with_api_url(self):
        """Test creating BoardConfig with api_url."""
        config = BoardConfig(
            board_id="abc123",
            agent_name="TestBot",
            api_url="http://app.kardbrd.com",
        )
        assert config.api_url == "http://app.kardbrd.com"

    def test_board_config_with_executor(self):
        """Test creating BoardConfig with executor field."""
        config = BoardConfig(
            board_id="abc123",
            agent_name="TestBot",
            executor="goose",
        )
        assert config.executor == "goose"


class TestLoadRulesDictFormat:
    """Tests for loading kardbrd.yml dict format."""

    def test_load_dict_format_with_config(self, tmp_path):
        """Test loading dict format with top-level config and rules."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: 0gl5MlBZ\n"
            "agent: TestBot\n"
            "api_url: http://app.kardbrd.com\n"
            "rules:\n"
            "  - name: test\n"
            "    event: card_moved\n"
            "    action: /ke\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 1
        assert config.board_id == "0gl5MlBZ"
        assert config.agent_name == "TestBot"
        assert config.api_url == "http://app.kardbrd.com"

    def test_load_dict_format_rules_parsed(self, tmp_path):
        """Test rules are correctly parsed in dict format."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: abc\n"
            "agent: Bot\n"
            "rules:\n"
            "  - name: rule1\n"
            "    event: card_moved\n"
            "    list: Ideas\n"
            "    action: /ke\n"
            "  - name: rule2\n"
            "    event: card_created\n"
            "    action: /kp\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 2
        assert engine.rules[0].name == "rule1"
        assert engine.rules[0].list == "Ideas"
        assert engine.rules[1].name == "rule2"

    def test_load_dict_missing_rules_key(self, tmp_path):
        """Test dict with only config, no rules key."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nagent: Bot\n")
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 0
        assert config.board_id == "abc"

    def test_load_dict_config_missing_board_id_raises(self, tmp_path):
        """Test missing board_id raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("agent: Bot\nrules: []\n")
        with pytest.raises(ValueError, match="board_id"):
            load_rules(rules_file)

    def test_load_dict_config_missing_agent_raises(self, tmp_path):
        """Test missing agent raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nrules: []\n")
        with pytest.raises(ValueError, match="agent"):
            load_rules(rules_file)

    def test_load_dict_api_url_optional(self, tmp_path):
        """Test api_url defaults to None when not specified."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nagent: Bot\nrules: []\n")
        engine, config = load_rules(rules_file)
        assert config.api_url is None

    def test_load_empty_file_raises(self, tmp_path):
        """Test empty file raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("")
        with pytest.raises(ValueError, match="empty"):
            load_rules(rules_file)


class TestValidateDictFormat:
    """Tests for validating dict format kardbrd.yml."""

    def test_validate_dict_format_valid(self, tmp_path):
        """Test valid dict format produces no issues."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: abc\n"
            "agent: Bot\n"
            "api_url: http://example.com\n"
            "rules:\n"
            "  - name: test\n"
            "    event: card_moved\n"
            "    action: /ke\n"
        )
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_validate_dict_format_missing_agent(self, tmp_path):
        """Test dict with board_id but no agent reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("agent" in e.message for e in result.errors)

    def test_validate_dict_format_missing_board_id(self, tmp_path):
        """Test dict with agent but no board_id reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("agent: Bot\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("board_id" in e.message for e in result.errors)

    def test_validate_dict_format_unknown_config_field(self, tmp_path):
        """Test unknown top-level field produces warning."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nagent: Bot\nextra_field: value\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert len(result.warnings) == 1
        assert "extra_field" in result.warnings[0].message

    def test_validate_bare_list_errors(self, tmp_path):
        """Test bare list format reports error (old format not supported)."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n  action: /ke\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("dict" in e.message for e in result.errors)

    def test_validate_dict_rules_not_list_errors(self, tmp_path):
        """Test dict with rules as non-list reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nagent: Bot\nrules: not_a_list\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'rules' must be a list" in e.message for e in result.errors)

    def test_validate_executor_valid(self, tmp_path):
        """Test valid executor field produces no issues."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nagent: Bot\nexecutor: goose\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_validate_executor_unknown_warns(self, tmp_path):
        """Test unknown executor produces warning."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nagent: Bot\nexecutor: unknown_executor\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert any("unknown_executor" in w.message for w in result.warnings)

    def test_validate_executor_not_string_errors(self, tmp_path):
        """Test non-string executor produces error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: abc\nagent: Bot\nexecutor: 123\nrules: []\n")
        from kardbrd_agent.rules import validate_rules_file

        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'executor' must be a string" in e.message for e in result.errors)


class TestLoadRulesExecutor:
    """Tests for loading executor config from kardbrd.yml."""

    def test_load_executor_field(self, tmp_path):
        """Test executor field is parsed from kardbrd.yml."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nagent: Bot\nexecutor: goose\nrules: []\n")
        engine, config = load_rules(rules_file)
        assert config.executor == "goose"

    def test_load_executor_default_none(self, tmp_path):
        """Test executor defaults to None when not specified."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nagent: Bot\nrules: []\n")
        engine, config = load_rules(rules_file)
        assert config.executor is None

    def test_load_executor_case_insensitive(self, tmp_path):
        """Test executor is lowercased."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: abc\nagent: Bot\nexecutor: Goose\nrules: []\n")
        engine, config = load_rules(rules_file)
        assert config.executor == "goose"


class TestKnownConfigFields:
    """Tests for KNOWN_CONFIG_FIELDS."""

    def test_known_config_fields_contents(self):
        """Test KNOWN_CONFIG_FIELDS contains expected fields."""
        expected = {"board_id", "agent", "api_url", "rules", "executor", "schedules"}
        assert expected == KNOWN_CONFIG_FIELDS

    def test_known_config_fields_is_frozenset(self):
        """Test KNOWN_CONFIG_FIELDS is immutable."""
        assert isinstance(KNOWN_CONFIG_FIELDS, frozenset)


class TestReloadableRuleEngineConfig:
    """Tests for ReloadableRuleEngine config support."""

    def test_config_exposed(self, tmp_path):
        """Test engine.config returns BoardConfig."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: abc\nagent: Bot\nrules:\n"
            "  - name: test\n    event: card_moved\n    action: /ke\n"
        )
        engine = ReloadableRuleEngine(rules_file)
        assert engine.config.board_id == "abc"
        assert engine.config.agent_name == "Bot"

    def test_config_reloaded_on_file_change(self, tmp_path):
        """Test config updates on hot reload."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: abc\nagent: Bot1\nrules:\n"
            "  - name: test\n    event: card_moved\n    action: /ke\n"
        )
        engine = ReloadableRuleEngine(rules_file, reload_interval=0)
        assert engine.config.agent_name == "Bot1"

        # Modify the file
        time.sleep(0.05)
        rules_file.write_text(
            "board_id: abc\nagent: Bot2\nrules:\n"
            "  - name: test\n    event: card_moved\n    action: /ke\n"
        )

        # Access config — should trigger reload
        assert engine.config.agent_name == "Bot2"
