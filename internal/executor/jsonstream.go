package executor

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	maxCodexProgressTextBytes   = 1 << 20
	maxCodexProgressSnapshots   = 256
	maxCodexProgressItemIDBytes = 1024
	maxCodexSessionIDBytes      = 1024
)

func emitChunks(stdout string, executorName string, onChunk func(content string, chunkType string)) {
	if onChunk == nil {
		return
	}
	if executorName == "codex" {
		emit := newCodexChunkEmitter(onChunk)
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			if line != "" {
				emit(line)
			}
		}
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		emitChunkLine(line, executorName, onChunk)
	}
}

func emitChunkLine(line string, executorName string, onChunk func(content string, chunkType string)) {
	if onChunk == nil || line == "" {
		return
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(line), &item); err != nil {
		return
	}
	switch executorName {
	case "claude":
		emitClaudeChunk(item, onChunk)
	case "codex":
		emitCodexChunk(item, onChunk)
	case "goose":
		emitGooseChunk(item, onChunk)
	case "pi":
		emitPiChunk(item, onChunk)
	}
}

func emitClaudeChunk(item map[string]any, onChunk func(content string, chunkType string)) {
	switch item["type"] {
	case "assistant":
		safeChunk(onChunk, stringFromAny(item["content"]), "assistant")
	case "tool_use":
		safeChunk(onChunk, mustJSON(item), "tool_use")
	}
}

func emitCodexChunk(item map[string]any, onChunk func(content string, chunkType string)) {
	emitCodexAssistantChunk(item, onChunk, nil, nil)
}

func newCodexChunkEmitter(onChunk func(content string, chunkType string)) func(string) {
	seenByItemID := map[string]codexProgressSnapshot{}
	lastAnonymousText := codexProgressSnapshot{}
	return func(line string) {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return
		}
		emitCodexAssistantChunk(event, onChunk, seenByItemID, &lastAnonymousText)
	}
}

func emitCodexAssistantChunk(event map[string]any, onChunk func(content string, chunkType string), seenByItemID map[string]codexProgressSnapshot, lastAnonymousText *codexProgressSnapshot) {
	eventType := stringFromAny(event["type"])
	if nested, ok := event["item"].(map[string]any); ok {
		if eventType != "item.started" && eventType != "item.updated" && eventType != "item.completed" {
			return
		}
		if stringFromAny(nested["type"]) != "agent_message" {
			return
		}
		text := codexMessageText(nested)
		if text == "" {
			return
		}
		if !emitCodexText(text, stringFromAny(nested["id"]), onChunk, seenByItemID, lastAnonymousText) {
			return
		}
		return
	}

	// Keep legacy top-level assistant events working, but do not stream raw tool,
	// reasoning, or other non-assistant events from Codex.
	if !isLegacyCodexMessageEvent(eventType) {
		return
	}
	text := codexMessageText(event)
	if text == "" {
		return
	}
	_ = emitCodexText(text, stringFromAny(event["id"]), onChunk, seenByItemID, lastAnonymousText)
}

type codexProgressSnapshot struct {
	length int
	digest [sha256.Size]byte
}

func emitCodexText(text, itemID string, onChunk func(content string, chunkType string), seenByItemID map[string]codexProgressSnapshot, lastAnonymousText *codexProgressSnapshot) bool {
	if len(text) > maxCodexProgressTextBytes {
		return false
	}
	snapshot := codexProgressSnapshot{length: len(text), digest: sha256.Sum256([]byte(text))}
	if seenByItemID != nil && itemID != "" {
		if len(itemID) > maxCodexProgressItemIDBytes {
			return false
		}
		if seenByItemID[itemID] == snapshot {
			return false
		}
		if _, exists := seenByItemID[itemID]; !exists && len(seenByItemID) >= maxCodexProgressSnapshots {
			clear(seenByItemID)
		}
		seenByItemID[itemID] = snapshot
	} else if lastAnonymousText != nil {
		if *lastAnonymousText == snapshot {
			return false
		}
		*lastAnonymousText = snapshot
	}
	safeChunk(onChunk, text, "assistant")
	return true
}

func emitGooseChunk(item map[string]any, onChunk func(content string, chunkType string)) {
	switch item["type"] {
	case "AgentMessageChunk":
		safeChunk(onChunk, stringFromAny(item["content"]), "assistant")
	case "ToolCallUpdate":
		safeChunk(onChunk, mustJSON(item), "tool_use")
	}
}

func emitPiChunk(item map[string]any, onChunk func(content string, chunkType string)) {
	switch item["type"] {
	case "message_delta", "message_end":
		if text := stringFromAny(item["message"]); text != "" {
			safeChunk(onChunk, text, "assistant")
			return
		}
		var builder strings.Builder
		appendContent(&builder, item["content"])
		if builder.Len() > 0 {
			safeChunk(onChunk, builder.String(), "assistant")
		}
	case "tool_use":
		safeChunk(onChunk, mustJSON(item), "tool_use")
	}
}

func safeChunk(onChunk func(content string, chunkType string), content string, chunkType string) {
	defer func() { _ = recover() }()
	onChunk(content, chunkType)
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func parseClaudeOutput(stdout string, stderr string, returnCode int, cmd []string) Result {
	result := Result{Success: returnCode == 0, ReturnCode: &returnCode, Stderr: emptyToNone(stderr), Command: cmd}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		switch item["type"] {
		case "result":
			result.ResultText, _ = item["result"].(string)
			result.SessionID, _ = item["session_id"].(string)
			if cost, ok := item["cost_usd"].(float64); ok {
				result.CostUSD = &cost
			}
			if duration, ok := item["duration_ms"].(float64); ok {
				value := int64(duration)
				result.DurationMS = &value
			}
		case "error":
			result.Success = false
			if errObj, ok := item["error"].(map[string]any); ok {
				result.Error, _ = errObj["message"].(string)
			}
		}
	}
	if returnCode != 0 && result.Error == "" {
		result.Success = false
		result.Error = exitError("Claude", returnCode, stderr)
	}
	return result
}

func parseCodexOutput(stdout string, stderr string, returnCode int, cmd []string) Result {
	stream := newCodexOutputState(true)
	for _, line := range strings.Split(stdout, "\n") {
		stream.consume(line)
	}
	return stream.result(stderr, returnCode, cmd)
}

// codexOutputState holds only the protocol state needed to determine success.
// Codex execution feeds it directly from complete stdout records, so bounded
// retained diagnostics can never make a later JSONL record appear malformed or
// hide a terminal turn failure.
type codexOutputState struct {
	mu sync.Mutex

	retainFallback   bool
	sessionID        string
	legacyText       strings.Builder
	terminalFallback string
	fallbackTooLarge bool
	sawModernEvent   bool
	turnCompleted    bool
	parseError       string
	failed           bool
	failureError     string
}

func newCodexOutputState(retainFallback bool) *codexOutputState {
	return &codexOutputState{retainFallback: retainFallback}
}

func (stream *codexOutputState) consume(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	stream.consumeLocked(line)
}

func (stream *codexOutputState) consumeLocked(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		if stream.parseError == "" {
			stream.parseError = boundedCodexDiagnostic("malformed Codex JSONL: " + err.Error())
		}
		return
	}
	eventType := stringFromAny(event["type"])
	switch eventType {
	case "thread.started":
		stream.sawModernEvent = true
		if threadID := stringFromAny(event["thread_id"]); threadID != "" {
			if len(threadID) > maxCodexSessionIDBytes {
				if stream.parseError == "" {
					stream.parseError = fmt.Sprintf("Codex thread ID exceeds %d bytes", maxCodexSessionIDBytes)
				}
			} else {
				stream.sessionID = threadID
			}
		}
	case "turn.started":
		stream.sawModernEvent = true
	case "turn.completed":
		stream.sawModernEvent = true
		stream.turnCompleted = true
	case "item.started", "item.updated":
		if _, ok := event["item"].(map[string]any); ok {
			stream.sawModernEvent = true
		}
	case "item.completed":
		nested, nestedItem := event["item"].(map[string]any)
		if nestedItem {
			stream.sawModernEvent = true
		}
		if stream.retainFallback && nestedItem && stringFromAny(nested["type"]) == "agent_message" && isCodexTerminalPhase(event, nested) {
			if text := codexMessageText(nested); text != "" {
				// The documented JSONL stream has no phase. Its completed agent
				// message is a compatibility fallback only; Execute replaces it
				// with --output-last-message for real subprocesses.
				if len(text) > maxCodexFinalMessageBytes {
					stream.fallbackTooLarge = true
				} else {
					stream.terminalFallback = text
				}
			}
		}
	case "turn.failed", "error":
		stream.sawModernEvent = true
		stream.failed = true
		if message := codexEventError(event); message != "" {
			stream.failureError = message
		}
	default:
		if stream.retainFallback && isLegacyCodexMessageEvent(eventType) && event["item"] == nil && !stream.fallbackTooLarge {
			appendContent(&stream.legacyText, event["content"])
			if stream.legacyText.Len() > maxCodexFinalMessageBytes {
				stream.legacyText.Reset()
				stream.fallbackTooLarge = true
			}
		}
	}
}

func (stream *codexOutputState) result(stderr string, returnCode int, cmd []string) Result {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	result := Result{Success: returnCode == 0, ReturnCode: &returnCode, Stderr: emptyToNone(stderr), Command: cmd, SessionID: stream.sessionID}
	if stream.parseError != "" {
		result.Success = false
		result.Error = stream.parseError
	}
	if stream.fallbackTooLarge {
		result.Success = false
		if result.Error == "" {
			result.Error = fmt.Sprintf("Codex JSONL fallback exceeds %d bytes", maxCodexFinalMessageBytes)
		}
	}
	if stream.failed {
		result.Success = false
		if stream.failureError != "" {
			result.Error = stream.failureError
		} else if result.Error == "" {
			result.Error = "Codex reported a failed turn"
		}
	}
	if stream.terminalFallback != "" {
		result.ResultText = strings.TrimSpace(stream.terminalFallback)
	} else {
		result.ResultText = strings.TrimSpace(stream.legacyText.String())
	}
	if returnCode != 0 && result.Error == "" {
		result.Success = false
		result.Error = exitError("Codex", returnCode, stderr)
	}
	if result.Success && stream.sawModernEvent && !stream.turnCompleted {
		result.Success = false
		result.Error = "Codex JSONL ended before turn.completed"
	}
	return result
}

func isLegacyCodexMessageEvent(eventType string) bool {
	return eventType == "item.message" || eventType == "response.message" || eventType == "message"
}

func codexMessageText(item map[string]any) string {
	if text := stringFromAny(item["text"]); text != "" {
		return text
	}
	var text strings.Builder
	appendContent(&text, item["content"])
	return text.String()
}

func isCodexTerminalPhase(event, item map[string]any) bool {
	phase := stringFromAny(item["phase"])
	if phase == "" {
		phase = stringFromAny(event["phase"])
	}
	return phase == "" || phase == "final"
}

func codexEventError(event map[string]any) string {
	message := stringFromAny(event["message"])
	if message == "" {
		switch errValue := event["error"].(type) {
		case string:
			message = errValue
		case map[string]any:
			message = stringFromAny(errValue["message"])
		}
	}
	return boundedCodexDiagnostic(message)
}

func boundedCodexDiagnostic(message string) string {
	const maxDiagnosticLength = 512
	message = strings.TrimSpace(message)
	if len(message) <= maxDiagnosticLength {
		return message
	}
	return message[:maxDiagnosticLength] + "... (truncated)"
}

func parseGooseOutput(stdout string, stderr string, returnCode int, cmd []string) Result {
	result := Result{Success: returnCode == 0, ReturnCode: &returnCode, Stderr: emptyToNone(stderr), Command: cmd}
	var text strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		switch item["type"] {
		case "AgentMessageChunk":
			text.WriteString(stringFromAny(item["content"]))
		case "error":
			result.Success = false
			result.Error = stringFromAny(item["message"])
		}
	}
	result.ResultText = strings.TrimSpace(text.String())
	if returnCode != 0 && result.Error == "" {
		result.Success = false
		result.Error = exitError("Goose", returnCode, stderr)
	}
	return result
}

func parsePiOutput(stdout string, stderr string, returnCode int, cmd []string) Result {
	result := Result{Success: returnCode == 0, ReturnCode: &returnCode, Stderr: emptyToNone(stderr), Command: cmd}
	var text strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		switch item["type"] {
		case "session":
			result.SessionID = stringFromAny(item["id"])
		case "message_end":
			switch msg := item["message"].(type) {
			case string:
				text.WriteString(msg)
			case map[string]any:
				appendContent(&text, msg["content"])
			}
		case "error":
			result.Success = false
			result.Error = stringFromAny(item["message"])
		}
	}
	result.ResultText = strings.TrimSpace(text.String())
	if returnCode != 0 && result.Error == "" {
		result.Success = false
		result.Error = exitError("Pi", returnCode, stderr)
	}
	return result
}

func appendContent(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case string:
		builder.WriteString(typed)
	case []any:
		for _, part := range typed {
			if obj, ok := part.(map[string]any); ok && obj["type"] == "text" {
				builder.WriteString(stringFromAny(obj["text"]))
			}
		}
	}
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func emptyToNone(value string) string {
	return value
}

func exitError(name string, code int, stderr string) string {
	if stderr != "" {
		return fmt.Sprintf("%s exited with code %d: %s", name, code, stderr)
	}
	return fmt.Sprintf("%s exited with code %d", name, code)
}
