package cli

import (
	"bytes"
	"testing"
)

func TestOutputTableTSVEncodesHeadersAndCells(t *testing.T) {
	table := outputTable{
		columns: []tableColumn{
			{header: "id", paths: [][]string{{"id"}}},
			{header: "name", paths: [][]string{{"name"}}},
			{header: "match_locations", paths: [][]string{{"match_locations"}}},
		},
		rows: []map[string]any{{
			"id":              "board1",
			"name":            "first\tline\n\"quoted\"",
			"match_locations": []any{"title", "description"},
		}},
	}

	var output bytes.Buffer
	if err := outputTableTSV(&output, table, false); err != nil {
		t.Fatal(err)
	}

	want := "id\tname\tmatch_locations\nboard1\t\"first\tline\n\"\"quoted\"\"\"\t\"[\"\"title\"\",\"\"description\"\"]\"\n"
	assertEqual(t, want, output.String())
}

func TestOutputTableTSVWithoutHeadersStillTerminates(t *testing.T) {
	table := outputTable{
		columns: []tableColumn{{header: "id", paths: [][]string{{"id"}}}},
		rows:    []map[string]any{{"id": "board1"}},
	}

	var output bytes.Buffer
	if err := outputTableTSV(&output, table, true); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "board1\n", output.String())
}

func TestOutputTableTSVIncludesHeaderForEmptyCollection(t *testing.T) {
	table := outputTable{
		columns: []tableColumn{
			{header: "id", paths: [][]string{{"id"}}},
			{header: "name", paths: [][]string{{"name"}}},
		},
	}

	var output bytes.Buffer
	if err := outputTableTSV(&output, table, false); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "id\tname\n", output.String())
}

func TestOutputTableMarkdownIncludesEmptyStateAndEscapesCells(t *testing.T) {
	table := outputTable{
		columns: []tableColumn{
			{header: "id", paths: [][]string{{"id"}}},
			{header: "name", paths: [][]string{{"name"}}},
		},
	}

	var output bytes.Buffer
	if err := outputTableMarkdown(&output, table); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "| id | name |\n| --- | --- |\n| _No results_ |  |\n", output.String())

	table.rows = []map[string]any{{"id": "board1", "name": "a|b\\c\r\nd"}}
	output.Reset()
	if err := outputTableMarkdown(&output, table); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "| id | name |\n| --- | --- |\n| board1 | a\\|b\\\\c<br>d |\n", output.String())
}

func TestTableValueNormalizesNullsAndScalars(t *testing.T) {
	assertEqual(t, "", tableValue(nil))
	assertEqual(t, "true", tableValue(true))
	assertEqual(t, "[\"one\",\"two\"]", tableValue([]any{"one", "two"}))
}
