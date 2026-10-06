package agent

import (
	"fmt"
	"strings"
)

// mentionDispatch keeps the comment body separate from executor selection.
// Only a directive on the addressed comment's first line has dispatch meaning.
type mentionDispatch struct {
	Content         string
	Model           string
	ReasoningEffort string
	ModelSource     string
	EffortSource    string
}

func parseMentionDispatch(content, mention string) (mentionDispatch, error) {
	plain := mentionDispatch{Content: content}
	lineEnd := strings.IndexByte(content, '\n')
	if lineEnd < 0 {
		lineEnd = len(content)
	}
	line := content[:lineEnd]
	start := len(line) - len(strings.TrimLeft(line, " \t"))
	if len(line[start:]) < len(mention) || !strings.EqualFold(line[start:start+len(mention)], mention) {
		return plain, nil
	}
	after := line[start+len(mention):]
	if after == "" || (after[0] != ' ' && after[0] != '\t') {
		return plain, nil
	}
	after = strings.TrimRight(strings.TrimLeft(after, " \t"), " \t\r")
	if !strings.HasPrefix(after, "[dispatch") {
		return plain, nil
	}
	if !strings.HasPrefix(after, "[dispatch ") {
		return plain, fmt.Errorf("dispatch directive must be [dispatch model=MODEL effort=LEVEL]")
	}
	if !strings.HasSuffix(after, "]") {
		return plain, fmt.Errorf("close dispatch directive with ] on the first line")
	}
	fields := strings.Fields(strings.TrimSuffix(strings.TrimPrefix(after, "[dispatch "), "]"))
	selected := mentionDispatch{Content: content[:start+len(mention)] + content[lineEnd:]}
	seen := map[string]bool{}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || value == "" {
			return plain, fmt.Errorf("malformed dispatch field %q; use model=MODEL effort=LEVEL", field)
		}
		if key != "model" && key != "effort" {
			return plain, fmt.Errorf("unknown dispatch field %q; use model and effort", key)
		}
		if seen[key] {
			return plain, fmt.Errorf("duplicate %s in dispatch directive", key)
		}
		seen[key] = true
		switch key {
		case "model":
			selected.Model = value
		case "effort":
			selected.ReasoningEffort = value
		}
	}
	if selected.Model == "" || selected.ReasoningEffort == "" {
		return plain, fmt.Errorf("dispatch directive requires model and effort; use [dispatch model=gpt-6.1-sol effort=high]")
	}
	if !supportedCommentModels[selected.Model] {
		return plain, fmt.Errorf("unsupported model %q for direct dispatch; supported: gpt-6.1-sol, gpt-6-sol, gpt-6-astra, gpt-6-luna", selected.Model)
	}
	if !supportedEfforts[selected.ReasoningEffort] {
		return plain, fmt.Errorf("unsupported effort %q; use low, medium, high, xhigh, or max", selected.ReasoningEffort)
	}
	selected.ModelSource = "directive"
	selected.EffortSource = "directive"
	return selected, nil
}

var supportedCommentModels = map[string]bool{
	"gpt-6.1-sol": true,
	"gpt-6-sol":   true,
	"gpt-6-astra": true,
	"gpt-6-luna":  true,
}

var supportedEfforts = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

func validateReasoning(executorType, effort string) error {
	if effort == "" {
		return nil
	}
	if !supportedEfforts[effort] {
		return fmt.Errorf("unsupported reasoning effort %q; use low, medium, high, xhigh, or max", effort)
	}
	if executorType != "codex" && executorType != "claude" {
		return fmt.Errorf("reasoning effort requires the codex or claude executor; current executor is %q", executorType)
	}
	return nil
}
