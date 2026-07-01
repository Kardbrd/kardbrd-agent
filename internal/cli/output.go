package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

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
