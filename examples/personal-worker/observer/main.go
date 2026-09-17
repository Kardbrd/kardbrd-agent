// fixture-observer emits one read-only, synthetic suggestion event. It is a
// contract example, not an inbox, Gmail, Calendar, or browser integration.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	if _, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"events": []map[string]string{{
			"source_id": "fixture-source-1",
			"reference": "fixture://source-1",
			"title":     "Synthetic suggestion",
			"proposal":  "Review this synthetic proposal; it is not delegated work.",
		}},
	})
}
