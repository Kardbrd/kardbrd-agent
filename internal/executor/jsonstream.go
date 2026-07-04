package executor

import (
	"encoding/json"
	"fmt"
	"strings"
)

func emitChunks(stdout string, executorName string, onChunk func(content string, chunkType string)) {
	if onChunk == nil {
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
	eventType := stringFromAny(item["type"])
	if strings.Contains(eventType, "message") {
		var text strings.Builder
		appendContent(&text, item["content"])
		safeChunk(onChunk, text.String(), "assistant")
	}
	if strings.Contains(eventType, "tool") {
		safeChunk(onChunk, mustJSON(item), "tool_use")
	}
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
		eventType, _ := item["type"].(string)
		if strings.Contains(eventType, "message") {
			appendContent(&text, item["content"])
		}
		if eventType == "error" {
			result.Success = false
			result.Error = stringFromAny(item["message"])
			if result.Error == "" {
				result.Error = stringFromAny(item["error"])
			}
		}
	}
	result.ResultText = strings.TrimSpace(text.String())
	if returnCode != 0 && result.Error == "" {
		result.Success = false
		result.Error = exitError("Codex", returnCode, stderr)
	}
	return result
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
