package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Kardbrd/kardbrd-agent/internal/rules"
)

const lifecycleRecordName = "kardbrd-lifecycle.json"

type LifecycleReason string

const (
	LifecycleCreate LifecycleReason = "create"
	LifecycleReuse  LifecycleReason = "reuse"
)

type LifecycleRequest struct {
	CardID  string
	BoardID string
	Reason  LifecycleReason
}

// LifecycleManager implements the opt-in lifecycle contract. The older
// Manager remains intentionally untouched for configurations without worktree.
type LifecycleManager struct {
	BaseRepo      string
	WorktreesBase string
	ExecutorType  string
	Config        rules.WorktreeConfig

	baseCommonGitDir string
	gitMu            sync.Mutex
}

type lifecycleRecord struct {
	CardID       string `json:"card_id"`
	Path         string `json:"path"`
	CommonGitDir string `json:"common_git_dir"`
	Branch       string `json:"branch"`
	Origin       string `json:"origin"`
	Stage        string `json:"stage"`
	FailedPhase  string `json:"failed_phase,omitempty"`
	Materialized bool   `json:"materialized"`
	Shared       bool   `json:"shared"`
}

func NewLifecycleManager(baseRepo, worktreesDir, executorType string, config rules.WorktreeConfig) (*LifecycleManager, error) {
	base, err := canonicalExistingPath(baseRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve base repository: %w", err)
	}
	worktreesBase := worktreesDir
	if worktreesBase == "" {
		worktreesBase = filepath.Dir(base)
	}
	worktreesBase = cleanAbs(worktreesBase)
	if executorType == "" {
		executorType = "claude"
	}
	manager := &LifecycleManager{
		BaseRepo:      base,
		WorktreesBase: worktreesBase,
		ExecutorType:  strings.ToLower(executorType),
		Config:        config,
	}
	common, err := manager.commonGitDir(context.Background(), base)
	if err != nil {
		return nil, fmt.Errorf("resolve base Git common directory: %w", err)
	}
	manager.baseCommonGitDir = common
	if config.Checkout.Bootstrap != nil {
		if err := manager.verifyHook(*config.Checkout.Bootstrap); err != nil {
			return nil, err
		}
	}
	if config.Prepare != nil {
		if err := manager.verifyHook(config.Prepare.Hook); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func (m *LifecycleManager) WorktreePath(cardID string) string {
	return filepath.Clean(filepath.Join(m.WorktreesBase, "card-"+cardID))
}

func (m *LifecycleManager) BranchName(cardID string) string {
	return "card/" + cardID
}

// Remove intentionally does not implement a second Done-retirement path. The
// validated direct cleanup rule introduced by #67 remains responsible for
// retirement when lifecycle configuration is enabled.
func (m *LifecycleManager) Remove(_ string, _ bool) error {
	return nil
}

// Prepare creates or verifies a worktree, then runs only the configured
// materialization, sharing, and preparation phases within the caller deadline.
func (m *LifecycleManager) Prepare(ctx context.Context, request LifecycleRequest) (string, error) {
	if err := validLifecycleRequest(request); err != nil {
		return "", err
	}
	record, path, reason, created, err := m.acquireOrCreate(ctx, request)
	if err != nil {
		return "", err
	}
	if created && m.Config.Checkout.Mode == rules.CheckoutFull {
		record.Materialized = true
		if err := m.writeRecord(ctx, path, record); err != nil {
			return "", err
		}
	}

	if !record.Materialized {
		if m.Config.Checkout.Mode != rules.CheckoutDelegated || m.Config.Checkout.Bootstrap == nil {
			return "", fmt.Errorf("worktree %q is not materialized", path)
		}
		record.Stage, record.FailedPhase = "bootstrapping", ""
		if err := m.writeRecord(ctx, path, record); err != nil {
			return "", err
		}
		if err := m.runHook(ctx, path, request, reason, "bootstrap", *m.Config.Checkout.Bootstrap); err != nil {
			record.Stage, record.FailedPhase = "failed", "bootstrap"
			_ = m.writeRecord(context.Background(), path, record)
			return "", err
		}
		record.Materialized = true
		if err := m.writeRecord(ctx, path, record); err != nil {
			return "", err
		}
	}

	if !record.Shared && record.Origin == "created" {
		if err := m.verifyOwned(ctx, request.CardID, path, record); err != nil {
			return "", err
		}
		if err := m.applySharing(ctx, path); err != nil {
			record.Stage, record.FailedPhase = "failed", "sharing"
			_ = m.writeRecord(context.Background(), path, record)
			return "", err
		}
		record.Shared = true
		if err := m.writeRecord(ctx, path, record); err != nil {
			return "", err
		}
	}

	prepare := m.Config.Prepare
	needsPrepare := prepare != nil && (record.FailedPhase == "prepare" || (reason == LifecycleCreate && prepare.OnCreate) || (reason == LifecycleReuse && prepare.OnReuse))
	if needsPrepare {
		record.Stage, record.FailedPhase = "preparing", ""
		if err := m.writeRecord(ctx, path, record); err != nil {
			return "", err
		}
		if err := m.runHook(ctx, path, request, reason, "prepare", prepare.Hook); err != nil {
			record.Stage, record.FailedPhase = "failed", "prepare"
			_ = m.writeRecord(context.Background(), path, record)
			return "", err
		}
	}
	record.Stage, record.FailedPhase = "ready", ""
	if err := m.writeRecord(ctx, path, record); err != nil {
		return "", err
	}
	return path, nil
}

// ExistingOrBase deliberately performs no create, hook, link, setup, prune,
// repair, or record mutation. It is used by safe stop actions.
func (m *LifecycleManager) ExistingOrBase(ctx context.Context, request LifecycleRequest) (string, error) {
	if err := validLifecycleRequest(request); err != nil {
		return "", err
	}
	paths := m.candidatePaths(request.CardID)
	present := make([]string, 0, len(paths))
	for _, path := range paths {
		_, err := os.Lstat(path)
		if err == nil {
			present = append(present, path)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect worktree path %q: %w", path, err)
		}
	}
	if len(present) == 0 {
		return m.BaseRepo, nil
	}
	if len(present) != 1 {
		return "", fmt.Errorf("ambiguous present worktrees for card %q", request.CardID)
	}
	record, err := m.readRecord(ctx, present[0])
	if err != nil {
		return "", err
	}
	if err := m.verifyOwned(ctx, request.CardID, present[0], record); err != nil {
		return "", err
	}
	if record.Stage == "ready" {
		return present[0], nil
	}
	return m.BaseRepo, nil
}

// Adopt is an explicit administrator preflight. It only writes a verified
// ownership record and never renames, resets, cleans, or prepares source.
func (m *LifecycleManager) Adopt(ctx context.Context, cardID string) error {
	adoption, ok := m.adoption(cardID)
	if !ok {
		return fmt.Errorf("card %q is not in worktree.adoptions", cardID)
	}
	path := cleanAbs(adoption.Path)
	if !within(m.WorktreesBase, path) {
		return fmt.Errorf("adoption path %q escapes worktree root", path)
	}
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("adoption path %q: %w", path, err)
	}
	if _, err := m.readRecord(ctx, path); err == nil {
		return fmt.Errorf("adoption path %q already has a lifecycle record", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record := lifecycleRecord{CardID: cardID, Path: path, CommonGitDir: m.baseCommonGitDir, Branch: adoption.Branch, Origin: "adopted", Stage: "created", Materialized: true, Shared: true}
	if err := m.verifyOwnershipWithoutRecord(ctx, cardID, path, record); err != nil {
		return err
	}
	declaredCommon, err := canonicalExistingPath(adoption.CommonGitDir)
	if err != nil || declaredCommon != m.baseCommonGitDir {
		return fmt.Errorf("adoption common_git_dir does not match base repository")
	}
	return m.writeRecord(ctx, path, record)
}

func (m *LifecycleManager) acquireOrCreate(ctx context.Context, request LifecycleRequest) (lifecycleRecord, string, LifecycleReason, bool, error) {
	m.gitMu.Lock()
	defer m.gitMu.Unlock()
	paths := m.candidatePaths(request.CardID)
	var present []string
	for _, path := range paths {
		if _, err := os.Lstat(path); err == nil {
			present = append(present, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return lifecycleRecord{}, "", "", false, err
		}
	}
	if len(present) > 1 {
		return lifecycleRecord{}, "", "", false, fmt.Errorf("ambiguous present worktrees for card %q", request.CardID)
	}
	if len(present) == 1 {
		record, err := m.readRecord(ctx, present[0])
		if err != nil {
			return lifecycleRecord{}, "", "", false, err
		}
		if err := m.verifyOwned(ctx, request.CardID, present[0], record); err != nil {
			return lifecycleRecord{}, "", "", false, err
		}
		return record, present[0], LifecycleReuse, false, nil
	}

	path := m.WorktreePath(request.CardID)
	if err := os.MkdirAll(m.WorktreesBase, 0o755); err != nil {
		return lifecycleRecord{}, "", "", false, fmt.Errorf("create worktree root: %w", err)
	}
	oid, err := m.resolveBaseOID(ctx)
	if err != nil {
		return lifecycleRecord{}, "", "", false, err
	}
	branch := m.BranchName(request.CardID)
	args := []string{"git", "worktree", "add", "-b", branch}
	if m.Config.Checkout.Mode == rules.CheckoutDelegated {
		// Delegated mode must not let a fallback-link decision observe source
		// before the administrator-provided materializer has populated it.
		args = append(args, "--no-checkout")
	}
	args = append(args, path, oid)
	if _, err := m.run(ctx, m.BaseRepo, args); err != nil {
		return lifecycleRecord{}, "", "", false, fmt.Errorf("create worktree: %w", err)
	}
	record := lifecycleRecord{CardID: request.CardID, Path: path, CommonGitDir: m.baseCommonGitDir, Branch: branch, Origin: "created", Stage: "created"}
	if err := m.writeRecord(ctx, path, record); err != nil {
		return lifecycleRecord{}, "", "", false, err
	}
	return record, path, LifecycleCreate, true, nil
}

func (m *LifecycleManager) resolveBaseOID(ctx context.Context) (string, error) {
	ref := m.Config.Base.Ref
	if ref == "" {
		result, err := m.run(ctx, m.BaseRepo, []string{"git", "ls-remote", "--symref", m.Config.Base.Remote, "HEAD"})
		if err != nil {
			return "", fmt.Errorf("resolve remote HEAD: %w", err)
		}
		for _, line := range strings.Split(result.Stdout, "\n") {
			parts := strings.Fields(line)
			if len(parts) == 3 && parts[0] == "ref:" && parts[2] == "HEAD" && strings.HasPrefix(parts[1], "refs/heads/") {
				ref = parts[1]
				break
			}
		}
		if ref == "" {
			return "", errors.New("remote HEAD is missing or ambiguous")
		}
	}
	branch := strings.TrimPrefix(ref, "refs/heads/")
	remoteRef := "refs/remotes/" + m.Config.Base.Remote + "/" + branch
	if _, err := m.run(ctx, m.BaseRepo, []string{"git", "fetch", m.Config.Base.Remote, ref + ":" + remoteRef}); err != nil {
		return "", fmt.Errorf("fetch selected base ref: %w", err)
	}
	result, err := m.run(ctx, m.BaseRepo, []string{"git", "rev-parse", remoteRef + "^{commit}"})
	if err != nil {
		return "", fmt.Errorf("resolve fetched base object: %w", err)
	}
	oid := strings.TrimSpace(result.Stdout)
	if oid == "" {
		return "", errors.New("fetched base ref has no object ID")
	}
	return oid, nil
}

func (m *LifecycleManager) candidatePaths(cardID string) []string {
	paths := []string{m.WorktreePath(cardID)}
	if adoption, ok := m.adoption(cardID); ok {
		adopted := cleanAbs(adoption.Path)
		if adopted != paths[0] {
			paths = append(paths, adopted)
		}
	}
	return paths
}

func (m *LifecycleManager) adoption(cardID string) (rules.WorktreeAdoption, bool) {
	for _, adoption := range m.Config.Adoptions {
		if adoption.CardID == cardID {
			return adoption, true
		}
	}
	return rules.WorktreeAdoption{}, false
}

func (m *LifecycleManager) verifyOwned(ctx context.Context, cardID, path string, record lifecycleRecord) error {
	if record.CardID != cardID || record.Path != path || record.CommonGitDir != m.baseCommonGitDir || record.Branch == "" || (record.Origin != "created" && record.Origin != "adopted") || !validLifecycleStage(record.Stage) {
		return fmt.Errorf("invalid lifecycle ownership record for %q", path)
	}
	return m.verifyOwnershipWithoutRecord(ctx, cardID, path, record)
}

func validLifecycleStage(stage string) bool {
	switch stage {
	case "created", "bootstrapping", "preparing", "ready", "failed":
		return true
	default:
		return false
	}
}

func (m *LifecycleManager) verifyOwnershipWithoutRecord(ctx context.Context, cardID, path string, record lifecycleRecord) error {
	if !within(m.WorktreesBase, path) {
		return fmt.Errorf("worktree path %q escapes worktree root", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect worktree path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("worktree path %q must not be a symlink", path)
	}
	canonical, err := canonicalExistingPath(path)
	if err != nil || canonical != path {
		return fmt.Errorf("worktree path %q is not canonically owned", path)
	}
	registered, err := m.isRegistered(ctx, path)
	if err != nil {
		return err
	}
	if !registered {
		return fmt.Errorf("worktree path %q is not registered", path)
	}
	common, err := m.commonGitDir(ctx, path)
	if err != nil || common != m.baseCommonGitDir {
		return fmt.Errorf("worktree path %q has a different common Git directory", path)
	}
	branch, err := m.currentBranch(ctx, path)
	if err != nil || branch != record.Branch {
		return fmt.Errorf("worktree path %q has unexpected branch", path)
	}
	if record.CardID != cardID {
		return fmt.Errorf("worktree path %q belongs to another card", path)
	}
	return nil
}

func (m *LifecycleManager) isRegistered(ctx context.Context, path string) (bool, error) {
	result, err := m.run(ctx, m.BaseRepo, []string{"git", "worktree", "list", "--porcelain"})
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.HasPrefix(line, "worktree ") && cleanAbs(strings.TrimPrefix(line, "worktree ")) == path {
			return true, nil
		}
	}
	return false, nil
}

func (m *LifecycleManager) currentBranch(ctx context.Context, path string) (string, error) {
	result, err := m.run(ctx, path, []string{"git", "symbolic-ref", "--quiet", "--short", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func (m *LifecycleManager) commonGitDir(ctx context.Context, path string) (string, error) {
	result, err := m.run(ctx, path, []string{"git", "rev-parse", "--git-common-dir"})
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(result.Stdout)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(path, dir)
	}
	return canonicalExistingPath(dir)
}

func (m *LifecycleManager) recordPath(ctx context.Context, path string) (string, error) {
	result, err := m.run(ctx, path, []string{"git", "rev-parse", "--git-path", lifecycleRecordName})
	if err != nil {
		return "", err
	}
	recordPath := strings.TrimSpace(result.Stdout)
	if !filepath.IsAbs(recordPath) {
		recordPath = filepath.Join(path, recordPath)
	}
	return filepath.Clean(recordPath), nil
}

func (m *LifecycleManager) readRecord(ctx context.Context, path string) (lifecycleRecord, error) {
	recordPath, err := m.recordPath(ctx, path)
	if err != nil {
		return lifecycleRecord{}, err
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return lifecycleRecord{}, err
	}
	var record lifecycleRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return lifecycleRecord{}, fmt.Errorf("decode lifecycle record: %w", err)
	}
	return record, nil
}

func (m *LifecycleManager) writeRecord(ctx context.Context, path string, record lifecycleRecord) error {
	recordPath, err := m.recordPath(ctx, path)
	if err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(recordPath), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(recordPath), ".kardbrd-lifecycle-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, recordPath)
}

func (m *LifecycleManager) applySharing(ctx context.Context, path string) error {
	if m.Config.Sharing.Env == rules.SharingEnvLink {
		if err := m.linkIfFallback(ctx, path, ".env"); err != nil {
			return err
		}
	}
	if m.Config.Sharing.Skills == rules.SharingSkillsFallback && m.ExecutorType == "codex" {
		for _, skillPath := range []string{filepath.Join(".agents", "skills"), filepath.Join(".codex", "skills")} {
			if err := m.linkIfFallback(ctx, path, skillPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *LifecycleManager) linkIfFallback(ctx context.Context, worktreePath, relative string) error {
	tracked, err := m.trackedPath(ctx, worktreePath, relative)
	if err != nil {
		return err
	}
	if tracked {
		return nil
	}
	source := filepath.Join(m.BaseRepo, relative)
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	destination := filepath.Join(worktreePath, relative)
	if _, err := os.Lstat(destination); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.Symlink(source, destination)
}

func (m *LifecycleManager) trackedPath(ctx context.Context, path, relative string) (bool, error) {
	_, err := m.run(ctx, path, []string{"git", "ls-files", "--error-unmatch", "--", relative})
	if err == nil {
		return true, nil
	}
	var commandErr commandError
	if errors.As(err, &commandErr) {
		return false, nil
	}
	return false, err
}

func (m *LifecycleManager) runHook(ctx context.Context, path string, request LifecycleRequest, reason LifecycleReason, phase string, hook rules.Hook) error {
	if err := m.verifyHook(hook); err != nil {
		return err
	}
	hookCtx, cancel := boundedHookContext(ctx, time.Duration(hook.TimeoutSeconds)*time.Second)
	defer cancel()
	_, err := m.runWithEnv(hookCtx, path, hook.Argv, lifecycleHookEnvironment(request, path, reason, phase, m.Config.Environment.Passthrough))
	if err != nil {
		return fmt.Errorf("%s hook failed: %s", phase, redactLifecycleDiagnostic(err.Error()))
	}
	return nil
}

func redactLifecycleDiagnostic(value string) string {
	for _, entry := range os.Environ() {
		key, secret, found := strings.Cut(entry, "=")
		if found && secret != "" && sensitiveLifecycleValueName(key) {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = strings.ReplaceAll(value, "```", "'''")
	if len(value) > 2000 {
		value = value[:2000] + "\n... (truncated)"
	}
	return strings.TrimSpace(value)
}

func sensitiveLifecycleValueName(name string) bool {
	upper := strings.ToUpper(name)
	return strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "CREDENTIAL") || strings.HasSuffix(upper, "_KEY")
}

func (m *LifecycleManager) verifyHook(hook rules.Hook) error {
	root, err := canonicalExistingPath(m.Config.Helpers.StableRoot)
	if err != nil {
		return fmt.Errorf("resolve stable helper root: %w", err)
	}
	helper, err := canonicalExistingPath(hook.Argv[0])
	if err != nil || !within(root, helper) {
		return fmt.Errorf("helper %q is not beneath stable_root", hook.Argv[0])
	}
	info, err := os.Stat(helper)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("helper %q must resolve to an executable regular file", hook.Argv[0])
	}
	return nil
}

func lifecycleHookEnvironment(request LifecycleRequest, path string, reason LifecycleReason, phase string, passthrough []string) []string {
	allowed := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true, "TZ": true, "LANG": true}
	for _, name := range passthrough {
		allowed[name] = true
	}
	env := make([]string, 0, len(allowed)+5)
	for _, value := range os.Environ() {
		key, _, found := strings.Cut(value, "=")
		if found && (allowed[key] || strings.HasPrefix(key, "LC_")) {
			env = append(env, value)
		}
	}
	return append(env,
		"KARDBRD_CARD_ID="+request.CardID,
		"KARDBRD_BOARD_ID="+request.BoardID,
		"KARDBRD_WORKTREE_PATH="+path,
		"KARDBRD_WORKTREE_REASON="+string(reason),
		"KARDBRD_WORKTREE_PHASE="+phase,
	)
}

func boundedHookContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := parent.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < timeout {
			timeout = remaining
		}
	}
	return context.WithTimeout(parent, timeout)
}

func (m *LifecycleManager) run(ctx context.Context, dir string, args []string) (RunResult, error) {
	return m.runWithEnv(ctx, dir, args, nil)
}

func (m *LifecycleManager) runWithEnv(ctx context.Context, dir string, args, environment []string) (RunResult, error) {
	if len(args) == 0 {
		return RunResult{}, errors.New("missing command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	if environment != nil {
		cmd.Env = environment
	}
	configureLifecycleProcessGroup(cmd)
	output := &limitedLifecycleOutput{limit: 64 * 1024}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return RunResult{Stderr: output.String()}, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			terminateLifecycleProcessGroup(cmd)
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	terminateLifecycleProcessGroup(cmd)
	result := RunResult{Stdout: output.String(), Stderr: output.String()}
	if err != nil {
		return result, commandError{args: args, stderr: result.Stderr, err: err}
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	return result, nil
}

type limitedLifecycleOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (o *limitedLifecycleOutput) Write(value []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if remaining := o.limit - o.buffer.Len(); remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		_, _ = o.buffer.Write(value[:remaining])
		if remaining < len(value) {
			o.truncated = true
		}
	} else if len(value) > 0 {
		o.truncated = true
	}
	return len(value), nil
}

func (o *limitedLifecycleOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	value := o.buffer.String()
	if o.truncated {
		value += "\n... (output truncated)"
	}
	return value
}

func validLifecycleRequest(request LifecycleRequest) error {
	if strings.TrimSpace(request.CardID) == "" || strings.TrimSpace(request.BoardID) == "" {
		return errors.New("lifecycle card and board IDs are required")
	}
	if strings.ContainsAny(request.CardID, string(filepath.Separator)+"\\") || request.CardID == "." || request.CardID == ".." {
		return errors.New("lifecycle card ID is not a path-safe canonical ID")
	}
	return nil
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

var _ io.Writer = (*limitedLifecycleOutput)(nil)
