package agent

import (
	"strings"
	"testing"
)

func TestParseExecutionRequestPresence(t *testing.T) {
	valid := `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1"}`
	request, err := parseExecutionRequest([]byte(valid))
	if err != nil || request.Model.Present || request.Effort.Present {
		t.Fatalf("request = %+v, %v", request, err)
	}
	partial, err := parseExecutionRequest([]byte(`{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":"high"}`))
	if err != nil || !partial.Effort.Present || partial.Effort.Value != "high" || partial.Model.Present {
		t.Fatalf("partial = %+v, %v", partial, err)
	}
	for _, tc := range []struct{ name, raw, want string }{
		{"null model", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":null}`, "model"},
		{"empty model", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","model":""}`, "model"},
		{"wrong effort", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","effort":2}`, "effort"},
		{"missing revision", `{"version":1,"target_bot_id":"bot-1"}`, "capability_revision"},
		{"unknown", `{"version":1,"target_bot_id":"bot-1","capability_revision":"rev-1","x":1}`, "x"},
		{"duplicate", `{"version":1,"target_bot_id":"bot-1","target_bot_id":"bot-2","capability_revision":"rev-1"}`, "duplicate"},
		{"version", `{"version":2,"target_bot_id":"bot-1","capability_revision":"rev-1"}`, "version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseExecutionRequest([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseAcceptedDefaults(t *testing.T) {
	defaults, err := parseAcceptedDefaults([]byte(`{"model":null,"effort":"high"}`))
	if err != nil || defaults.Model != nil || defaults.Effort == nil || *defaults.Effort != "high" {
		t.Fatalf("defaults = %+v, %v", defaults, err)
	}
	for _, raw := range []string{`null`, `{}`, `{"model":"","effort":null}`, `{"model":null,"effort":null,"x":1}`} {
		if _, err := parseAcceptedDefaults([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
