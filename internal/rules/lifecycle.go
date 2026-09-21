package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// LifecycleFingerprint identifies restart-only policy: the lifecycle block and
// complete exact command rules. Ordinary fuzzy rules and schedules are omitted
// so they can retain validated reload support.
func LifecycleFingerprint(config Config) string {
	commands := make([]Rule, 0)
	for _, rule := range config.Rules {
		if rule.CommentCommand != "" || rule.Execution != "" {
			commands = append(commands, rule)
		}
	}
	payload, _ := json.Marshal(struct {
		Worktree *WorktreeConfig `json:"worktree"`
		Commands []Rule          `json:"commands"`
	}{Worktree: config.Worktree, Commands: commands})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// validateLoadedLifecycle rejects invalid opt-in fields before yaml.v3 can
// coerce them. Legacy configuration retains its historical permissive scope.
func validateLoadedLifecycle(data []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	top := mapping(root.Content[0])
	if node, ok := top["worktree"]; ok {
		if err := validateWorktreeNode(node); err != nil {
			return fmt.Errorf("worktree: %w", err)
		}
	}
	rulesNode := top["rules"]
	if rulesNode == nil || rulesNode.Kind != yaml.SequenceNode {
		return nil
	}
	for index, rule := range rulesNode.Content {
		if rule.Kind != yaml.MappingNode {
			continue
		}
		fields := mapping(rule)
		_, command := fields["comment_command"]
		_, execution := fields["execution"]
		if !command && !execution {
			continue
		}
		for key := range fields {
			if !knownRuleFields[key] {
				return fmt.Errorf("rule %d: unknown command-rule field %q", index, key)
			}
		}
		if command {
			if err := exactString(fields["comment_command"], "comment_command"); err != nil {
				return fmt.Errorf("rule %d: %w", index, err)
			}
		}
		if execution {
			if err := exactString(fields["execution"], "execution"); err != nil {
				return fmt.Errorf("rule %d: %w", index, err)
			}
		}
	}
	return nil
}

func validateWorktreeNode(node *yaml.Node) error {
	if err := mappingOnly(node, "worktree"); err != nil {
		return err
	}
	fields := mapping(node)
	known := set("base", "helpers", "checkout", "prepare", "sharing", "environment", "adoptions")
	if err := rejectUnknown(fields, known); err != nil {
		return err
	}
	if base := fields["base"]; base != nil {
		if err := validateBaseNode(base); err != nil {
			return err
		}
	}
	if helpers := fields["helpers"]; helpers != nil {
		if err := mappingOnly(helpers, "helpers"); err != nil {
			return err
		}
		values := mapping(helpers)
		if err := rejectUnknown(values, set("stable_root")); err != nil {
			return fmt.Errorf("helpers: %w", err)
		}
		if stableRoot := values["stable_root"]; stableRoot != nil {
			if err := exactString(stableRoot, "stable_root"); err != nil {
				return fmt.Errorf("helpers: %w", err)
			}
		}
	}
	if checkout := fields["checkout"]; checkout != nil {
		if err := validateCheckoutNode(checkout); err != nil {
			return err
		}
	}
	if prepare := fields["prepare"]; prepare != nil {
		if err := validatePrepareNode(prepare); err != nil {
			return err
		}
	}
	if sharing := fields["sharing"]; sharing != nil {
		if err := mappingOnly(sharing, "sharing"); err != nil {
			return err
		}
		values := mapping(sharing)
		if err := rejectUnknown(values, set("env", "skills")); err != nil {
			return fmt.Errorf("sharing: %w", err)
		}
		for _, key := range []string{"env", "skills"} {
			if value := values[key]; value != nil {
				if err := exactString(value, key); err != nil {
					return fmt.Errorf("sharing: %w", err)
				}
			}
		}
	}
	if environment := fields["environment"]; environment != nil {
		if err := mappingOnly(environment, "environment"); err != nil {
			return err
		}
		values := mapping(environment)
		if err := rejectUnknown(values, set("passthrough")); err != nil {
			return fmt.Errorf("environment: %w", err)
		}
		if passthrough := values["passthrough"]; passthrough != nil {
			if err := stringListAllowEmpty(passthrough, "passthrough"); err != nil {
				return fmt.Errorf("environment: %w", err)
			}
		}
	}
	if adoptions := fields["adoptions"]; adoptions != nil {
		if adoptions.Kind != yaml.SequenceNode {
			return fmt.Errorf("adoptions must be a YAML list")
		}
		for index, adoption := range adoptions.Content {
			if err := mappingOnly(adoption, "adoption"); err != nil {
				return fmt.Errorf("adoptions[%d]: %w", index, err)
			}
			values := mapping(adoption)
			if err := rejectUnknown(values, set("card_id", "path", "common_git_dir", "branch")); err != nil {
				return fmt.Errorf("adoptions[%d]: %w", index, err)
			}
			for _, key := range []string{"card_id", "path", "common_git_dir", "branch"} {
				if err := exactString(values[key], key); err != nil {
					return fmt.Errorf("adoptions[%d]: %w", index, err)
				}
			}
		}
	}
	return nil
}

func validateBaseNode(node *yaml.Node) error {
	if err := mappingOnly(node, "base"); err != nil {
		return err
	}
	fields := mapping(node)
	if err := rejectUnknown(fields, set("remote", "ref")); err != nil {
		return fmt.Errorf("base: %w", err)
	}
	for _, key := range []string{"remote", "ref"} {
		if value := fields[key]; value != nil {
			if err := exactString(value, key); err != nil {
				return fmt.Errorf("base: %w", err)
			}
		}
	}
	return nil
}

func validateCheckoutNode(node *yaml.Node) error {
	if err := mappingOnly(node, "checkout"); err != nil {
		return err
	}
	fields := mapping(node)
	if err := rejectUnknown(fields, set("mode", "bootstrap")); err != nil {
		return fmt.Errorf("checkout: %w", err)
	}
	if mode := fields["mode"]; mode != nil {
		if err := exactString(mode, "mode"); err != nil {
			return fmt.Errorf("checkout: %w", err)
		}
	}
	if bootstrap := fields["bootstrap"]; bootstrap != nil {
		if err := validateHookNode(bootstrap, "bootstrap"); err != nil {
			return fmt.Errorf("checkout: %w", err)
		}
	}
	return nil
}

func validatePrepareNode(node *yaml.Node) error {
	if err := mappingOnly(node, "prepare"); err != nil {
		return err
	}
	fields := mapping(node)
	if err := rejectUnknown(fields, set("argv", "timeout_seconds", "on_create", "on_reuse")); err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	if err := stringList(fields["argv"], "argv"); err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	if timeout := fields["timeout_seconds"]; timeout != nil && (timeout.Kind != yaml.ScalarNode || timeout.Tag != "!!int") {
		return fmt.Errorf("prepare: timeout_seconds must be an integer")
	}
	for _, key := range []string{"on_create", "on_reuse"} {
		if value := fields[key]; value != nil && (value.Kind != yaml.ScalarNode || value.Tag != "!!bool") {
			return fmt.Errorf("prepare: %s must be a boolean", key)
		}
	}
	return nil
}

func validateHookNode(node *yaml.Node, name string) error {
	if err := mappingOnly(node, name); err != nil {
		return err
	}
	fields := mapping(node)
	if err := rejectUnknown(fields, set("argv", "timeout_seconds")); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := stringList(fields["argv"], "argv"); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if timeout := fields["timeout_seconds"]; timeout != nil && (timeout.Kind != yaml.ScalarNode || timeout.Tag != "!!int") {
		return fmt.Errorf("%s: timeout_seconds must be an integer", name)
	}
	return nil
}

func mappingOnly(node *yaml.Node, name string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a YAML mapping", name)
	}
	return nil
}

func rejectUnknown(values map[string]*yaml.Node, known map[string]bool) error {
	for key := range values {
		if !known[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	return nil
}

func exactString(node *yaml.Node, name string) error {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" || strings.TrimSpace(node.Value) == "" {
		return fmt.Errorf("%s must be a non-empty string", name)
	}
	return nil
}

func stringList(node *yaml.Node, name string) error {
	if node == nil || node.Kind != yaml.SequenceNode || len(node.Content) == 0 {
		return fmt.Errorf("%s must be a non-empty YAML list", name)
	}
	return validateStringListEntries(node, name)
}

// stringListAllowEmpty is for declarative collections whose empty value is
// meaningful. Hook argv remains intentionally non-empty through stringList.
func stringListAllowEmpty(node *yaml.Node, name string) error {
	if node == nil || node.Kind != yaml.SequenceNode {
		return fmt.Errorf("%s must be a YAML list", name)
	}
	return validateStringListEntries(node, name)
}

func validateStringListEntries(node *yaml.Node, name string) error {
	for _, value := range node.Content {
		if err := exactString(value, name+" entries"); err != nil {
			return err
		}
	}
	return nil
}

var portableEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var gitRemoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateWorktreeConfig(config WorktreeConfig) error {
	if !gitRemoteName.MatchString(config.Base.Remote) {
		return fmt.Errorf("worktree.base.remote must be a non-empty remote name")
	}
	if ref := config.Base.Ref; ref != "" && !validBranchRef(ref) {
		return fmt.Errorf("worktree.base.ref must be refs/heads/<name>")
	}
	if config.Checkout.Mode != CheckoutFull && config.Checkout.Mode != CheckoutDelegated {
		return fmt.Errorf("worktree.checkout.mode must be full or delegated")
	}
	if config.Checkout.Mode == CheckoutDelegated && config.Checkout.Bootstrap == nil {
		return fmt.Errorf("worktree.checkout.bootstrap is required for delegated mode")
	}
	if config.Checkout.Mode == CheckoutFull && config.Checkout.Bootstrap != nil {
		return fmt.Errorf("worktree.checkout.bootstrap is only allowed for delegated mode")
	}
	if config.Checkout.Bootstrap != nil {
		if err := validateHookConfig("bootstrap", *config.Checkout.Bootstrap); err != nil {
			return err
		}
	}
	if config.Prepare != nil {
		if err := validateHookConfig("prepare", config.Prepare.Hook); err != nil {
			return err
		}
	}
	if config.Checkout.Bootstrap != nil || config.Prepare != nil {
		if !filepath.IsAbs(config.Helpers.StableRoot) || filepath.Clean(config.Helpers.StableRoot) != config.Helpers.StableRoot {
			return fmt.Errorf("worktree.helpers.stable_root must be an absolute clean path")
		}
	}
	if config.Sharing.Env != SharingEnvDisabled && config.Sharing.Env != SharingEnvLink {
		return fmt.Errorf("worktree.sharing.env must be disabled or link")
	}
	if config.Sharing.Skills != SharingSkillsFallback && config.Sharing.Skills != SharingSkillsDisabled {
		return fmt.Errorf("worktree.sharing.skills must be fallback or disabled")
	}
	for _, name := range config.Environment.Passthrough {
		if !portableEnvironmentName.MatchString(name) || strings.HasPrefix(name, "KARDBRD_") || sensitiveLifecycleEnvironmentName(name) {
			return fmt.Errorf("worktree.environment.passthrough contains reserved name %q", name)
		}
	}
	ids := map[string]bool{}
	paths := map[string]bool{}
	for _, adoption := range config.Adoptions {
		if adoption.CardID == "" || adoption.Path == "" || adoption.CommonGitDir == "" || adoption.Branch == "" {
			return fmt.Errorf("worktree.adoptions entries require card_id, path, common_git_dir, and branch")
		}
		path := filepath.Clean(adoption.Path)
		if !filepath.IsAbs(path) || paths[path] {
			return fmt.Errorf("worktree.adoptions paths must be unique absolute paths")
		}
		if ids[adoption.CardID] {
			return fmt.Errorf("worktree.adoptions card IDs must be unique")
		}
		ids[adoption.CardID] = true
		paths[path] = true
	}
	return nil
}

func validBranchRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/heads/") {
		return false
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	if branch == "" || strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.Contains(branch, "//") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.ContainsAny(branch, " ~^:?*[\\") {
		return false
	}
	for _, character := range branch {
		if character <= 0x20 || character == 0x7f {
			return false
		}
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" || component == "." || component == ".." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func validateHookConfig(name string, hook Hook) error {
	if len(hook.Argv) == 0 || !filepath.IsAbs(hook.Argv[0]) {
		return fmt.Errorf("worktree.%s.argv must begin with an absolute helper path", name)
	}
	for _, argument := range hook.Argv {
		if strings.TrimSpace(argument) == "" {
			return fmt.Errorf("worktree.%s.argv arguments must be non-empty", name)
		}
	}
	if hook.TimeoutSeconds <= 0 {
		return fmt.Errorf("worktree.%s.timeout_seconds must be positive", name)
	}
	return nil
}

func sensitiveLifecycleEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	return upper == "KARDBRD_TOKEN" || upper == "KARDBRD_API_URL" || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "CREDENTIAL") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "SECRET") || strings.HasSuffix(upper, "_KEY")
}
