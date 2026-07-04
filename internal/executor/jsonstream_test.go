package executor

import "testing"

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

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
