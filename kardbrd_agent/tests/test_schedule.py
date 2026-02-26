"""Tests for the schedule feature (cron-based automation)."""

from datetime import UTC, datetime, timedelta
from unittest.mock import AsyncMock, MagicMock

import pytest

from kardbrd_agent.rules import (
    KNOWN_CONFIG_FIELDS,
    KNOWN_SCHEDULE_FIELDS,
    MODEL_MAP,
    BoardConfig,
    Schedule,
    load_rules,
    parse_schedules,
    validate_rules_file,
)
from kardbrd_agent.scheduler import ScheduleManager


class TestScheduleDataclass:
    """Tests for the Schedule dataclass."""

    def test_schedule_basic(self):
        """Test creating a basic schedule."""
        s = Schedule(name="Daily report", cron="0 9 * * *", action="Summarize activity")
        assert s.name == "Daily report"
        assert s.cron == "0 9 * * *"
        assert s.action == "Summarize activity"
        assert s.model is None
        assert s.assignee is None
        assert s.list is None

    def test_schedule_with_all_fields(self):
        """Test creating a schedule with all fields."""
        s = Schedule(
            name="Weekly cleanup",
            cron="0 0 * * 0",
            action="Archive old cards",
            model="haiku",
            assignee="user123",
            list="To Do",
        )
        assert s.model == "haiku"
        assert s.assignee == "user123"
        assert s.list == "To Do"

    def test_model_id_resolves_short_names(self):
        """Test model_id resolves haiku/sonnet/opus to full IDs."""
        for short, full in MODEL_MAP.items():
            s = Schedule(name="t", cron="* * * * *", action="a", model=short)
            assert s.model_id == full

    def test_model_id_none_when_no_model(self):
        """Test model_id returns None when model not set."""
        s = Schedule(name="t", cron="* * * * *", action="a")
        assert s.model_id is None

    def test_model_id_passthrough_unknown(self):
        """Test model_id passes through unknown model strings."""
        s = Schedule(name="t", cron="* * * * *", action="a", model="custom-model")
        assert s.model_id == "custom-model"


class TestParseSchedules:
    """Tests for parse_schedules function."""

    def test_parse_basic_schedule(self):
        """Test parsing a basic schedule."""
        data = [{"name": "Daily", "cron": "0 9 * * *", "action": "Do stuff"}]
        schedules = parse_schedules(data)
        assert len(schedules) == 1
        assert schedules[0].name == "Daily"
        assert schedules[0].cron == "0 9 * * *"

    def test_parse_schedule_with_model(self):
        """Test parsing a schedule with model."""
        data = [{"name": "Daily", "cron": "0 9 * * *", "action": "Do stuff", "model": "haiku"}]
        schedules = parse_schedules(data)
        assert schedules[0].model == "haiku"

    def test_parse_schedule_with_assignee(self):
        """Test parsing a schedule with assignee."""
        data = [{"name": "Daily", "cron": "0 9 * * *", "action": "Do stuff", "assignee": "usr123"}]
        schedules = parse_schedules(data)
        assert schedules[0].assignee == "usr123"

    def test_parse_schedule_with_list(self):
        """Test parsing a schedule with target list."""
        data = [{"name": "Daily", "cron": "0 9 * * *", "action": "Do stuff", "list": "To Do"}]
        schedules = parse_schedules(data)
        assert schedules[0].list == "To Do"

    def test_parse_missing_name_raises(self):
        """Test missing name raises ValueError."""
        with pytest.raises(ValueError, match="missing 'name'"):
            parse_schedules([{"cron": "0 9 * * *", "action": "Do stuff"}])

    def test_parse_missing_cron_raises(self):
        """Test missing cron raises ValueError."""
        with pytest.raises(ValueError, match="missing 'cron'"):
            parse_schedules([{"name": "Daily", "action": "Do stuff"}])

    def test_parse_missing_action_raises(self):
        """Test missing action raises ValueError."""
        with pytest.raises(ValueError, match="missing 'action'"):
            parse_schedules([{"name": "Daily", "cron": "0 9 * * *"}])

    def test_parse_invalid_cron_raises(self):
        """Test invalid cron expression raises ValueError."""
        with pytest.raises(ValueError, match="invalid cron"):
            parse_schedules(
                [{"name": "Bad", "cron": "not a cron expression", "action": "Do stuff"}]
            )

    def test_parse_multiple_schedules(self):
        """Test parsing multiple schedules."""
        data = [
            {"name": "Daily", "cron": "0 9 * * *", "action": "Do daily"},
            {"name": "Weekly", "cron": "0 0 * * 0", "action": "Do weekly"},
        ]
        schedules = parse_schedules(data)
        assert len(schedules) == 2
        assert schedules[0].name == "Daily"
        assert schedules[1].name == "Weekly"


class TestLoadRulesWithSchedules:
    """Tests for load_rules with schedules section."""

    def test_load_with_schedules(self, tmp_path):
        """Test loading kardbrd.yml with schedules."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily summary\n"
            '    cron: "0 9 * * *"\n'
            "    model: haiku\n"
            "    action: Summarize activity\n"
            "rules:\n"
            "  - name: test\n"
            "    event: card_moved\n"
            "    action: /ke\n"
        )
        engine, config = load_rules(rules_file)
        assert len(engine.rules) == 1
        assert len(config.schedules) == 1
        assert config.schedules[0].name == "Daily summary"
        assert config.schedules[0].cron == "0 9 * * *"

    def test_load_without_schedules(self, tmp_path):
        """Test loading kardbrd.yml without schedules section."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "rules:\n"
            "  - name: test\n"
            "    event: card_moved\n"
            "    action: /ke\n"
        )
        engine, config = load_rules(rules_file)
        assert config.schedules == []

    def test_load_schedules_not_list_raises(self, tmp_path):
        """Test schedules as non-list raises ValueError."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text("board_id: test\nagent: Bot\nschedules: not_a_list\nrules: []\n")
        with pytest.raises(ValueError, match="'schedules' must be a list"):
            load_rules(rules_file)

    def test_load_schedule_with_assignee(self, tmp_path):
        """Test loading schedule with assignee field."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Report\n"
            '    cron: "0 9 * * *"\n'
            "    assignee: E21K9jmv\n"
            "    action: Generate report\n"
            "rules: []\n"
        )
        engine, config = load_rules(rules_file)
        assert config.schedules[0].assignee == "E21K9jmv"

    def test_load_schedule_with_list(self, tmp_path):
        """Test loading schedule with list field."""
        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Report\n"
            '    cron: "0 9 * * *"\n'
            "    list: To Do\n"
            "    action: Generate report\n"
            "rules: []\n"
        )
        engine, config = load_rules(rules_file)
        assert config.schedules[0].list == "To Do"


class TestValidateSchedules:
    """Tests for schedule validation in validate_rules_file."""

    def test_valid_schedule(self, tmp_path):
        """Test valid schedule produces no issues."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_schedule_missing_name(self, tmp_path):
        """Test schedule missing name reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            '  - cron: "0 9 * * *"\n'
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("missing 'name'" in e.message for e in result.errors)

    def test_schedule_missing_cron(self, tmp_path):
        """Test schedule missing cron reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("missing 'cron'" in e.message for e in result.errors)

    def test_schedule_missing_action(self, tmp_path):
        """Test schedule missing action reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("missing 'action'" in e.message for e in result.errors)

    def test_schedule_invalid_cron(self, tmp_path):
        """Test invalid cron expression reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Bad\n"
            "    cron: not valid cron\n"
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("invalid cron" in e.message for e in result.errors)

    def test_schedule_unknown_field_warns(self, tmp_path):
        """Test unknown field in schedule produces warning."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "    action: Do stuff\n"
            "    unknown_field: value\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert any("unknown_field" in w.message for w in result.warnings)

    def test_schedule_unknown_model_warns(self, tmp_path):
        """Test unknown model in schedule produces warning."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "    model: gpt4\n"
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert any("gpt4" in w.message for w in result.warnings)

    def test_schedule_not_list_errors(self, tmp_path):
        """Test schedules as non-list reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("board_id: test\nagent: Bot\nschedules: not_a_list\nrules: []\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'schedules' must be a list" in e.message for e in result.errors)

    def test_schedules_not_in_unknown_top_level(self, tmp_path):
        """Test 'schedules' is recognized as a known top-level field."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "board_id: test\n"
            "agent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "    action: Do stuff\n"
            "rules: []\n"
        )
        result = validate_rules_file(f)
        # Should not have "Unknown top-level field(s): schedules" warning
        assert not any("schedules" in w.message for w in result.warnings)


class TestKnownScheduleFields:
    """Tests for KNOWN_SCHEDULE_FIELDS."""

    def test_known_schedule_fields_contents(self):
        """Test KNOWN_SCHEDULE_FIELDS contains expected fields."""
        assert {"name", "cron", "action", "model", "assignee", "list"} == KNOWN_SCHEDULE_FIELDS

    def test_known_schedule_fields_is_frozenset(self):
        """Test KNOWN_SCHEDULE_FIELDS is immutable."""
        assert isinstance(KNOWN_SCHEDULE_FIELDS, frozenset)


class TestKnownConfigFieldsIncludesSchedules:
    """Test KNOWN_CONFIG_FIELDS now includes 'schedules'."""

    def test_schedules_in_known_config_fields(self):
        """Test 'schedules' is in KNOWN_CONFIG_FIELDS."""
        assert "schedules" in KNOWN_CONFIG_FIELDS


class TestScheduleManager:
    """Tests for the ScheduleManager class."""

    def test_find_existing_card(self):
        """Test ScheduleManager finds an existing card by title."""
        schedule = Schedule(name="Daily Report", cron="0 9 * * *", action="Summarize")
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {
                    "id": "list1",
                    "name": "To Do",
                    "cards": [
                        {"public_id": "card123", "title": "Daily Report"},
                        {"public_id": "card456", "title": "Other Card"},
                    ],
                }
            ]
        }

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        card_id = mgr._find_or_create_card(schedule)
        assert card_id == "card123"
        # Should NOT call create_card
        client.create_card.assert_not_called()

    def test_find_card_case_insensitive(self):
        """Test card title matching is case-insensitive."""
        schedule = Schedule(name="daily report", cron="0 9 * * *", action="Summarize")
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {
                    "id": "list1",
                    "name": "To Do",
                    "cards": [{"public_id": "card123", "title": "Daily Report"}],
                }
            ]
        }

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        card_id = mgr._find_or_create_card(schedule)
        assert card_id == "card123"

    def test_create_card_when_not_found(self):
        """Test ScheduleManager creates a card when none exists."""
        schedule = Schedule(name="Daily Report", cron="0 9 * * *", action="Summarize")
        client = MagicMock()
        client.get_board.return_value = {"lists": [{"id": "list1", "name": "To Do", "cards": []}]}
        client.create_card.return_value = {"public_id": "new_card_id"}

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        card_id = mgr._find_or_create_card(schedule)
        assert card_id == "new_card_id"
        client.create_card.assert_called_once_with(
            board_id="board1", list_id="list1", title="Daily Report"
        )

    def test_create_card_in_specified_list(self):
        """Test card is created in the specified list."""
        schedule = Schedule(name="Report", cron="0 9 * * *", action="Do stuff", list="Plans")
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {"id": "list1", "name": "To Do", "cards": []},
                {"id": "list2", "name": "Plans", "cards": []},
            ]
        }
        client.create_card.return_value = {"public_id": "new_id"}

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        mgr._find_or_create_card(schedule)
        client.create_card.assert_called_once_with(
            board_id="board1", list_id="list2", title="Report"
        )

    def test_create_card_with_assignee(self):
        """Test card is assigned to the specified user."""
        schedule = Schedule(name="Report", cron="0 9 * * *", action="Do stuff", assignee="user123")
        client = MagicMock()
        client.get_board.return_value = {"lists": [{"id": "list1", "name": "To Do", "cards": []}]}
        client.create_card.return_value = {"public_id": "new_id"}

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        mgr._find_or_create_card(schedule)
        client.update_card.assert_called_once_with("new_id", assignee_id="user123")

    def test_no_assignee_when_existing_card(self):
        """Test assignee is NOT set when reusing an existing card."""
        schedule = Schedule(name="Report", cron="0 9 * * *", action="Do stuff", assignee="user123")
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {
                    "id": "list1",
                    "name": "To Do",
                    "cards": [{"public_id": "existing_id", "title": "Report"}],
                }
            ]
        }

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=AsyncMock(),
        )
        card_id = mgr._find_or_create_card(schedule)
        assert card_id == "existing_id"
        client.update_card.assert_not_called()

    @pytest.mark.asyncio
    async def test_check_schedules_fires_when_due(self):
        """Test _check_schedules fires a schedule when it's due."""
        schedule = Schedule(name="Every minute", cron="* * * * *", action="Do stuff")
        callback = AsyncMock()
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {
                    "id": "list1",
                    "name": "To Do",
                    "cards": [{"public_id": "card1", "title": "Every minute"}],
                }
            ]
        }

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=callback,
        )
        mgr._init_cron_iters()

        # Move next_time to the past so it fires
        mgr._next_times["Every minute"] = datetime.now(UTC) - timedelta(seconds=1)

        await mgr._check_schedules()
        callback.assert_called_once_with("card1", schedule)

    @pytest.mark.asyncio
    async def test_check_schedules_does_not_fire_before_due(self):
        """Test _check_schedules does not fire before schedule is due."""
        schedule = Schedule(name="Future", cron="0 0 1 1 *", action="Do stuff")
        callback = AsyncMock()
        client = MagicMock()

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=callback,
        )
        mgr._init_cron_iters()

        await mgr._check_schedules()
        callback.assert_not_called()

    @pytest.mark.asyncio
    async def test_start_with_no_schedules_returns(self):
        """Test start() returns immediately when no schedules configured."""
        mgr = ScheduleManager(
            schedules=[],
            board_id="board1",
            client=MagicMock(),
            process_callback=AsyncMock(),
        )
        # Should return immediately without looping
        await mgr.start()

    @pytest.mark.asyncio
    async def test_schedule_error_does_not_crash_loop(self):
        """Test that an error in one schedule doesn't crash the check loop."""
        schedule = Schedule(name="Failing", cron="* * * * *", action="Do stuff")
        callback = AsyncMock(side_effect=RuntimeError("test error"))
        client = MagicMock()
        client.get_board.return_value = {
            "lists": [
                {
                    "id": "list1",
                    "name": "To Do",
                    "cards": [{"public_id": "card1", "title": "Failing"}],
                }
            ]
        }

        mgr = ScheduleManager(
            schedules=[schedule],
            board_id="board1",
            client=client,
            process_callback=callback,
        )
        mgr._init_cron_iters()
        mgr._next_times["Failing"] = datetime.now(UTC) - timedelta(seconds=1)

        # Should not raise
        await mgr._check_schedules()
        callback.assert_called_once()


class TestReloadableRuleEngineSchedules:
    """Tests for ReloadableRuleEngine schedule support."""

    def test_schedules_exposed(self, tmp_path):
        """Test engine.schedules returns schedule list."""
        from kardbrd_agent.rules import ReloadableRuleEngine

        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: abc\nagent: Bot\n"
            "schedules:\n"
            "  - name: Daily\n"
            '    cron: "0 9 * * *"\n'
            "    action: Do stuff\n"
            "rules:\n"
            "  - name: test\n    event: card_moved\n    action: /ke\n"
        )
        engine = ReloadableRuleEngine(rules_file)
        assert len(engine.schedules) == 1
        assert engine.schedules[0].name == "Daily"

    def test_schedules_empty_when_none(self, tmp_path):
        """Test engine.schedules is empty when no schedules configured."""
        from kardbrd_agent.rules import ReloadableRuleEngine

        rules_file = tmp_path / "kardbrd.yml"
        rules_file.write_text(
            "board_id: abc\nagent: Bot\n"
            "rules:\n"
            "  - name: test\n    event: card_moved\n    action: /ke\n"
        )
        engine = ReloadableRuleEngine(rules_file)
        assert engine.schedules == []


class TestBoardConfigSchedules:
    """Tests for BoardConfig with schedules."""

    def test_board_config_default_empty_schedules(self):
        """Test BoardConfig defaults to empty schedules list."""
        config = BoardConfig(board_id="abc", agent_name="Bot")
        assert config.schedules == []

    def test_board_config_with_schedules(self):
        """Test BoardConfig stores schedules."""
        s = Schedule(name="Daily", cron="0 9 * * *", action="stuff")
        config = BoardConfig(board_id="abc", agent_name="Bot", schedules=[s])
        assert len(config.schedules) == 1
        assert config.schedules[0].name == "Daily"
