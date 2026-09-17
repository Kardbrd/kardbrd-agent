// fixture-runner is a synthetic personal-worker bridge for local acceptance
// tests. It never contacts email, calendars, browsers, or external providers.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	var packet struct {
		RunID  string `json:"run_id"`
		Action *struct {
			ID string `json:"id"`
		} `json:"action"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&packet); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	result := map[string]any{"run_id": packet.RunID, "status": "completed", "summary": "Synthetic fixture completed.", "receipt_id": "fixture-" + packet.RunID}
	mode := ""
	if len(os.Args) > 1 {
		mode = os.Args[len(os.Args)-1]
	}
	switch mode {
	case "scheduled":
		result = map[string]any{"run_id": packet.RunID, "status": "scheduled", "summary": "Synthetic future step.", "wake_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)}
	case "waiting_event":
		result = map[string]any{"run_id": packet.RunID, "status": "waiting_event", "summary": "Synthetic event wait.", "event_id": "fixture-event-1"}
	case "waiting_user":
		result = map[string]any{"run_id": packet.RunID, "status": "waiting_user", "summary": "Synthetic decision wait.", "decision_prompt": "Approve the synthetic fixture?"}
	case "needs_review":
		result = map[string]any{"run_id": packet.RunID, "status": "needs_review", "review_note": "Synthetic uncertainty for operator review."}
	}
	if packet.Action != nil {
		switch mode {
		case "scheduled", "waiting_event", "waiting_user":
			// These fixture transitions intentionally defer the predeclared
			// outward action until a later, explicitly delegated step.
			result["action_status"] = "not_started"
		case "needs_review":
			result["action_status"] = "uncertain"
		default:
			result["action_status"] = "performed"
			result["action_receipt"] = map[string]string{"action_id": packet.Action.ID, "receipt_id": "fixture-action-" + packet.RunID}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
