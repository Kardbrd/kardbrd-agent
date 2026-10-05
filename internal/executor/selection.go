package executor

import "fmt"

func validateEffort(executor, effort string) error {
	if effort == "" {
		return nil
	}
	if executor != "codex" && executor != "claude" {
		return fmt.Errorf("reasoning effort is not supported by the %s executor", executor)
	}
	switch effort {
	case "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return fmt.Errorf("unsupported reasoning effort %q; use low, medium, high, xhigh, or max", effort)
	}
}
