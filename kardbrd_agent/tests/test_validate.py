"""Tests for kardbrd.yml validation."""

import pytest

from kardbrd_agent.rules import Severity, ValidationResult, validate_rules_file


class TestValidateRulesFile:
    """Tests for the validate_rules_file function."""

    def test_valid_file(self, tmp_path):
        """Test a fully valid file produces no issues."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n  list: Ideas\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_valid_file_multiple_rules(self, tmp_path):
        """Test multiple valid rules produce no issues."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "- name: rule1\n"
            "  event:\n"
            "    - card_moved\n"
            "    - card_created\n"
            "  list: Ideas\n"
            "  action: /ke\n"
            "- name: rule2\n"
            "  event: comment_created\n"
            "  content_contains: '@claude'\n"
            "  model: haiku\n"
            "  action: respond\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_empty_file_is_valid(self, tmp_path):
        """Test empty file is valid."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("")
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_file_not_found(self, tmp_path):
        """Test missing file reports error."""
        result = validate_rules_file(tmp_path / "missing.yml")
        assert not result.is_valid
        assert len(result.errors) == 1
        assert "not found" in result.errors[0].message

    def test_invalid_yaml_syntax(self, tmp_path):
        """Test invalid YAML syntax reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: [broken\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert len(result.errors) == 1
        assert "YAML syntax" in result.errors[0].message

    def test_not_a_list(self, tmp_path):
        """Test non-list YAML reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("key: value\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert len(result.errors) == 1
        assert "YAML list" in result.errors[0].message

    def test_rule_not_a_dict(self, tmp_path):
        """Test rule that isn't a mapping reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- just a string\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert "mapping" in result.errors[0].message

    def test_missing_name(self, tmp_path):
        """Test missing name field reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- event: card_moved\n  action: /ke\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'name'" in e.message for e in result.errors)

    def test_missing_event(self, tmp_path):
        """Test missing event field reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  action: /ke\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'event'" in e.message for e in result.errors)

    def test_missing_action(self, tmp_path):
        """Test missing action field reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'action'" in e.message for e in result.errors)

    def test_multiple_missing_fields(self, tmp_path):
        """Test all missing required fields reported at once."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- list: Ideas\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        messages = [e.message for e in result.errors]
        assert any("'name'" in m for m in messages)
        assert any("'event'" in m for m in messages)
        assert any("'action'" in m for m in messages)

    def test_unknown_event_warns(self, tmp_path):
        """Test unknown event produces warning, not error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: made_up_event\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert len(result.warnings) == 1
        assert "made_up_event" in result.warnings[0].message

    def test_unknown_model_warns(self, tmp_path):
        """Test unknown model produces warning, not error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n  model: gpt4\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid
        assert len(result.warnings) == 1
        assert "gpt4" in result.warnings[0].message

    def test_unknown_fields_warn(self, tmp_path):
        """Test unknown fields produce warning."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "- name: test\n  event: card_moved\n  action: /ke\n  priority: high\n  timeout: 30\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert len(result.warnings) == 1
        assert "priority" in result.warnings[0].message
        assert "timeout" in result.warnings[0].message

    def test_event_yaml_list_is_valid(self, tmp_path):
        """Test event field as a YAML list is valid."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "- name: test\n  event:\n    - card_moved\n    - card_created\n  action: /ke\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_event_yaml_list_single_item(self, tmp_path):
        """Test event field as a single-item YAML list is valid."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event:\n    - card_moved\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid

    def test_event_yaml_list_unknown_warns(self, tmp_path):
        """Test unknown event names inside a YAML list still produce warnings."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event:\n    - card_moved\n    - fake_event\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert len(result.warnings) == 1
        assert "fake_event" in result.warnings[0].message

    def test_event_invalid_type_errors(self, tmp_path):
        """Test non-string, non-list event type reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: 123\n  action: /ke\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("must be a string or list" in e.message for e in result.errors)

    def test_comma_in_event_string_is_unknown(self, tmp_path):
        """Test comma-separated string is treated as a single unknown event name."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: 'card_moved, card_created'\n  action: /ke\n")
        result = validate_rules_file(f)
        assert result.is_valid  # warnings don't invalidate
        assert len(result.warnings) == 1
        assert "card_moved, card_created" in result.warnings[0].message

    def test_model_not_string_errors(self, tmp_path):
        """Test model field that isn't a string reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n  model: 123\n  action: /ke\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'model' must be a string" in e.message for e in result.errors)

    def test_action_not_string_errors(self, tmp_path):
        """Test action field that isn't a string reports error."""
        f = tmp_path / "kardbrd.yml"
        f.write_text("- name: test\n  event: card_moved\n  action: 123\n")
        result = validate_rules_file(f)
        assert not result.is_valid
        assert any("'action' must be a string" in e.message for e in result.errors)

    def test_known_models_accepted(self, tmp_path):
        """Test known model names don't produce warnings."""
        for model in ("haiku", "sonnet", "opus"):
            f = tmp_path / "kardbrd.yml"
            f.write_text(f"- name: test\n  event: card_moved\n  model: {model}\n  action: /ke\n")
            result = validate_rules_file(f)
            assert result.is_valid
            assert len(result.warnings) == 0, f"Unexpected warning for model '{model}'"

    def test_all_condition_fields_accepted(self, tmp_path):
        """Test all condition fields are accepted without warnings."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "- name: test\n"
            "  event: card_moved\n"
            "  list: Ideas\n"
            "  title: Test\n"
            "  label: Bug\n"
            "  content_contains: hello\n"
            "  action: /ke\n"
        )
        result = validate_rules_file(f)
        assert result.is_valid
        assert result.issues == []

    def test_multiple_rules_multiple_issues(self, tmp_path):
        """Test issues from multiple rules are all reported."""
        f = tmp_path / "kardbrd.yml"
        f.write_text(
            "- name: rule1\n"
            "  event: fake_event\n"
            "  action: /ke\n"
            "- event: card_moved\n"
            "  action: /kp\n"
        )
        result = validate_rules_file(f)
        assert not result.is_valid
        assert len(result.warnings) == 1  # unknown event
        assert len(result.errors) == 1  # missing name

    def test_validates_example_file(self):
        """Test the kardbrd.yml.example file passes validation."""
        from pathlib import Path

        example = Path(__file__).parent.parent.parent / "kardbrd.yml.example"
        if not example.exists():
            pytest.skip("kardbrd.yml.example not found")
        result = validate_rules_file(example)
        assert result.is_valid, f"Example file has errors: {result.errors}"
        assert len(result.warnings) == 0, f"Example file has warnings: {result.warnings}"


class TestValidationResult:
    """Tests for the ValidationResult dataclass."""

    def test_empty_result_is_valid(self):
        result = ValidationResult()
        assert result.is_valid
        assert result.errors == []
        assert result.warnings == []

    def test_result_with_only_warnings_is_valid(self):
        from kardbrd_agent.rules import ValidationIssue

        result = ValidationResult(
            issues=[ValidationIssue(Severity.WARNING, 0, "test", "some warning")]
        )
        assert result.is_valid

    def test_result_with_errors_is_invalid(self):
        from kardbrd_agent.rules import ValidationIssue

        result = ValidationResult(issues=[ValidationIssue(Severity.ERROR, 0, "test", "some error")])
        assert not result.is_valid

    def test_issue_str_with_name(self):
        from kardbrd_agent.rules import ValidationIssue

        issue = ValidationIssue(Severity.ERROR, 0, "my rule", "bad field")
        assert str(issue) == "ERROR: Rule 'my rule': bad field"

    def test_issue_str_with_index(self):
        from kardbrd_agent.rules import ValidationIssue

        issue = ValidationIssue(Severity.WARNING, 2, None, "something off")
        assert str(issue) == "WARNING: Rule 2: something off"

    def test_issue_str_file_level(self):
        from kardbrd_agent.rules import ValidationIssue

        issue = ValidationIssue(Severity.ERROR, None, None, "bad yaml")
        assert str(issue) == "ERROR: bad yaml"
