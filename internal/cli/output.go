package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type tableColumn struct {
	header string
	paths  [][]string
}

type outputTable struct {
	columns []tableColumn
	rows    []map[string]any
}

func outputRawJSON(w io.Writer, raw json.RawMessage) error {
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w, indented.String())
	return err
}

func outputMarkdown(w io.Writer, markdown string) error {
	_, err := fmt.Fprint(w, markdown)
	return err
}

func tableFromJSON(raw json.RawMessage, schema []tableColumn) (outputTable, error) {
	rows, err := decodeCollection(raw)
	if err != nil {
		return outputTable{}, err
	}
	return outputTable{columns: append([]tableColumn(nil), schema...), rows: rows}, nil
}

func decodeCollection(raw json.RawMessage) ([]map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("expected one JSON value")
		}
		return nil, err
	}

	if value == nil {
		return []map[string]any{}, nil
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"results", "items", "activities"} {
			if items, ok := object[key]; ok {
				value = items
				break
			}
		}
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected collection response, got %T", value)
	}

	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("expected collection item object, got %T", item)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func tableColumnValue(row map[string]any, column tableColumn) (any, bool) {
	for _, path := range column.paths {
		value, ok := nestedTableValue(row, path)
		if ok {
			return value, true
		}
	}
	return nil, false
}

func nestedTableValue(row map[string]any, path []string) (any, bool) {
	var value any = row
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func outputTableTSV(w io.Writer, table outputTable, noHeaders bool) error {
	if noHeaders && len(table.rows) == 0 {
		_, err := fmt.Fprintln(w)
		return err
	}

	writer := csv.NewWriter(w)
	writer.Comma = '\t'
	if !noHeaders {
		headers := make([]string, len(table.columns))
		for i, column := range table.columns {
			headers[i] = column.header
		}
		if err := writer.Write(headers); err != nil {
			return err
		}
	}
	for _, row := range table.rows {
		cells := tableRow(row, table.columns)
		for i, cell := range cells {
			cells[i] = tsvTableCell(cell)
		}
		if err := writer.Write(cells); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func outputTableMarkdown(w io.Writer, table outputTable) error {
	headers := make([]string, len(table.columns))
	separator := make([]string, len(table.columns))
	for i, column := range table.columns {
		headers[i] = markdownTableCell(column.header)
		separator[i] = "---"
	}
	if _, err := fmt.Fprintf(w, "| %s |\n| %s |\n", strings.Join(headers, " | "), strings.Join(separator, " | ")); err != nil {
		return err
	}
	if len(table.rows) == 0 {
		cells := make([]string, len(table.columns))
		if len(cells) > 0 {
			cells[0] = "_No results_"
		}
		_, err := fmt.Fprintf(w, "| %s |\n", strings.Join(cells, " | "))
		return err
	}
	for _, row := range table.rows {
		cells := tableRow(row, table.columns)
		for i, cell := range cells {
			cells[i] = markdownTableCell(cell)
		}
		if _, err := fmt.Fprintf(w, "| %s |\n", strings.Join(cells, " | ")); err != nil {
			return err
		}
	}
	return nil
}

func tableRow(row map[string]any, columns []tableColumn) []string {
	cells := make([]string, len(columns))
	for i, column := range columns {
		value, _ := tableColumnValue(row, column)
		cells[i] = tableValue(value)
	}
	return cells
}

func tableValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return visibleTableControls(typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case json.Number:
		return typed.String()
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func tsvTableCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func visibleTableControls(value string) string {
	var out strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' && character != '\n' && character != '\r' {
			fmt.Fprintf(&out, "\\u%04x", character)
			continue
		}
		out.WriteRune(character)
	}
	return out.String()
}

func markdownTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", "<br>")
}
