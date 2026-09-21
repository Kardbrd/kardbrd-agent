package executor

import (
	"strings"
	"testing"
)

func TestParseClaudeOutput(t *testing.T) {
	result := parseClaudeOutput(`{"type":"result","result":"done","session_id":"s1","cost_usd":1.25,"duration_ms":42}`+"\n", "", 0, []string{"claude"})
	assertEqual(t, true, result.Success)
	assertEqual(t, "done", result.ResultText)
	assertEqual(t, "s1", result.SessionID)
	if result.CostUSD == nil || *result.CostUSD != 1.25 {
		t.Fatalf("unexpected cost: %#v", result.CostUSD)
	}
	if result.DurationMS == nil || *result.DurationMS != 42 {
		t.Fatalf("unexpected duration: %#v", result.DurationMS)
	}
}

func TestParseCodexOutputAggregatesMessages(t *testing.T) {
	stdout := `{"type":"item.message","content":[{"type":"text","text":"hello "}]}` + "\n" +
		`{"type":"response.message","content":"world"}` + "\n"
	result := parseCodexOutput(stdout, "", 0, []string{"codex"})
	assertEqual(t, true, result.Success)
	assertEqual(t, "hello world", result.ResultText)
}

func TestParseCodexOutputCurrentJSONL(t *testing.T) {
	stdout := `{"type":"thread.started","thread_id":"synthetic-diagnostic-thread"}` + "\n" +
		`{"type":"turn.started"}` + "\n" +
		`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Synthetic final response"}}` + "\n" +
		`{"type":"turn.completed"}` + "\n"

	result := parseCodexOutput(stdout, "", 0, []string{"codex"})

	assertEqual(t, true, result.Success)
	assertEqual(t, "Synthetic final response", result.ResultText)
	assertEqual(t, "synthetic-diagnostic-thread", result.SessionID)
}

func TestParseCodexOutputKeepsOnlyLatestCompletedMessageAsFallback(t *testing.T) {
	stdout := `{"type":"turn.started"}` + "\n" +
		`{"type":"item.started","item":{"id":"progress","type":"agent_message","text":"draft"}}` + "\n" +
		`{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}` + "\n" +
		`{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"progress","type":"agent_message","phase":"commentary","text":"progress complete"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"final answer"}}` + "\n" +
		`{"type":"turn.completed"}` + "\n"

	result := parseCodexOutput(stdout, "", 0, []string{"codex"})

	assertEqual(t, true, result.Success)
	assertEqual(t, "final answer", result.ResultText)
}

func TestParseCodexOutputFailsForTurnAndJSONLErrors(t *testing.T) {
	tests := []struct {
		name       string
		stdout     string
		stderr     string
		returnCode int
		wantError  string
	}{
		{
			name:      "failed turn after text",
			stdout:    `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"partial"}}` + "\n" + `{"type":"turn.failed","error":{"message":"synthetic failure"}}` + "\n",
			wantError: "synthetic failure",
		},
		{
			name:      "top level object error",
			stdout:    `{"type":"error","error":{"message":"transport failed"}}` + "\n",
			wantError: "transport failed",
		},
		{
			name:      "malformed jsonl",
			stdout:    `{"type":"turn.started"}` + "\n" + `{"type":"item.completed"` + "\n",
			wantError: "malformed Codex JSONL",
		},
		{
			name:      "truncated after complete item",
			stdout:    `{"type":"thread.started","thread_id":"thread"}` + "\n" + `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"partial"}}` + "\n",
			wantError: "ended before turn.completed",
		},
		{
			name:       "nonzero exit",
			stderr:     "bad exit",
			returnCode: 2,
			wantError:  "Codex exited with code 2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := parseCodexOutput(test.stdout, test.stderr, test.returnCode, []string{"codex"})
			assertEqual(t, false, result.Success)
			assertContains(t, result.Error, test.wantError)
		})
	}
}

func TestParseGooseOutputAggregatesChunks(t *testing.T) {
	result := parseGooseOutput(`{"type":"AgentMessageChunk","content":"hello"}`+"\n", "", 0, []string{"goose"})
	assertEqual(t, true, result.Success)
	assertEqual(t, "hello", result.ResultText)
}

func TestParsePiOutputTracksSession(t *testing.T) {
	stdout := `{"type":"session","id":"s1"}` + "\n" +
		`{"type":"message_end","message":{"content":"done"}}` + "\n"
	result := parsePiOutput(stdout, "", 0, []string{"pi"})
	assertEqual(t, true, result.Success)
	assertEqual(t, "s1", result.SessionID)
	assertEqual(t, "done", result.ResultText)
}

func TestEmitChunksForAssistantAndToolEvents(t *testing.T) {
	var chunks []string
	onChunk := func(content string, chunkType string) {
		chunks = append(chunks, chunkType+":"+content)
	}

	emitChunks(`{"type":"assistant","content":"hello"}`+"\n"+`{"type":"tool_use","tool":"Read"}`+"\n", "claude", onChunk)
	emitChunks(`{"type":"AgentMessageChunk","content":"goose"}`+"\n"+`{"type":"ToolCallUpdate","tool":"shell"}`+"\n", "goose", onChunk)

	assertEqual(t, "assistant:hello", chunks[0])
	assertEqual(t, "tool_use:", chunks[1][:9])
	assertEqual(t, "assistant:goose", chunks[2])
	assertEqual(t, "tool_use:", chunks[3][:9])
}

func TestEmitCodexChunksStreamsOnlyChangedAssistantMessages(t *testing.T) {
	var chunks []string
	emitChunks(
		`{"type":"item.started","item":{"id":"progress","type":"agent_message","text":"draft"}}`+"\n"+
			`{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}`+"\n"+
			`{"type":"item.updated","item":{"id":"progress","type":"agent_message","text":"working"}}`+"\n"+
			`{"type":"item.completed","item":{"id":"progress","type":"agent_message","text":"done"}}`+"\n"+
			`{"type":"item.completed","item":{"id":"reasoning","type":"reasoning","text":"private chain"}}`+"\n"+
			`{"type":"item.completed","item":{"id":"tool","type":"command_execution","command":"secret command"}}`+"\n"+
			`{"type":"tool.message","content":"secret legacy tool output"}`+"\n"+
			`{"type":"item.completed","item":{"id":"final","type":"agent_message","text":"final answer"}}`+"\n",
		"codex",
		func(content string, chunkType string) { chunks = append(chunks, chunkType+":"+content) },
	)

	assertEqual(t, 4, len(chunks))
	assertEqual(t, "assistant:draft", chunks[0])
	assertEqual(t, "assistant:working", chunks[1])
	assertEqual(t, "assistant:done", chunks[2])
	assertEqual(t, "assistant:final answer", chunks[3])
	if strings.Contains(strings.Join(chunks, "\n"), "secret") || strings.Contains(strings.Join(chunks, "\n"), "private") {
		t.Fatalf("Codex stream leaked non-assistant content: %v", chunks)
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
