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

func TestOutputTableTSVWithoutHeadersForEmptyCollectionPrintsOneNewline(t *testing.T) {
	table := outputTable{columns: []tableColumn{{header: "id", paths: [][]string{{"id"}}}}}

	var output bytes.Buffer
	if err := outputTableTSV(&output, table, true); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "\n", output.String())
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

func TestTableFromJSONKeepsTheSchemaForMissingOptionalValues(t *testing.T) {
	schema := []tableColumn{
		{header: "id", paths: [][]string{{"id"}}},
		{header: "board_name", paths: [][]string{{"board_name"}}},
	}
	table, err := tableFromJSON([]byte(`[{"id":"activity1"}]`), schema)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := outputTableTSV(&output, table, false); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "id\tboard_name\nactivity1\t\n", output.String())
}

func TestTableFromJSONRejectsTrailingData(t *testing.T) {
	_, err := tableFromJSON([]byte(`[] trailing`), nil)
	if err == nil {
		t.Fatal("expected trailing JSON data to fail")
	}
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
	assertEqual(t, `\u001b[31mred`, tableValue("\x1b[31mred"))
}

func TestTableOutputSanitizesTerminalControlsAndTSVFormulas(t *testing.T) {
	table := outputTable{
		columns: []tableColumn{{header: "name", paths: [][]string{{"name"}}}},
		rows:    []map[string]any{{"name": "=SUM(A1:A2)"}},
	}
	var output bytes.Buffer
	if err := outputTableTSV(&output, table, false); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "name\n'=SUM(A1:A2)\n", output.String())

	table.rows = []map[string]any{{"name": "\x1b[31mred"}}
	output.Reset()
	if err := outputTableMarkdown(&output, table); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "| name |\n| --- |\n| \\\\u001b\\[31mred |\n", output.String())
}

func TestTSVTableCellProtectsFormulasAfterLeadingRecordControls(t *testing.T) {
	for _, value := range []string{
		"\t=SUM(A1:A2)",
		"\r+1",
		"\n-1",
		"\t\r\n@cmd",
	} {
		if got, want := tsvTableCell(value), "'"+value; got != want {
			t.Errorf("tsvTableCell(%q) = %q, want %q", value, got, want)
		}
	}
	assertEqual(t, "\t\r\n", tsvTableCell("\t\r\n"))
}

func TestMarkdownTableCellEscapesAPISuppliedMarkup(t *testing.T) {
	got := markdownTableCell("<script>alert('x')</script> *bold* _em_ [link](https://example.com) `code` ~strike~ &")
	want := "&lt;script&gt;alert('x')&lt;/script&gt; \\*bold\\* \\_em\\_ \\[link\\](https\\://example\\.com) \\`code\\` \\~strike\\~ &amp;"
	assertEqual(t, want, got)
	assertEqual(t, "https\\://example\\.com", markdownTableCell("https://example.com"))
	assertEqual(t, "www\\.example\\.com user\\@example\\.com \\#123", markdownTableCell("www.example.com user@example.com #123"))
}
