package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type RunResult struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(dir string, args []string) (RunResult, error)
}

type Manager struct {
	BaseRepo      string
	WorktreesBase string
	SetupCommand  string
	ExecutorType  string
	Runner        Runner

	active map[string]string
}

func NewManager(baseRepo string, worktreesDir string, setupCommand string, executorType string) *Manager {
	base := cleanAbs(baseRepo)
	worktreesBase := worktreesDir
	if worktreesBase == "" {
		worktreesBase = filepath.Dir(base)
	}
	if executorType == "" {
		executorType = "claude"
	}

	return &Manager{
		BaseRepo:      base,
		WorktreesBase: cleanAbs(worktreesBase),
		SetupCommand:  setupCommand,
		ExecutorType:  executorType,
		Runner:        commandRunner{},
		active:        map[string]string{},
	}
}

func (m *Manager) WorktreePath(cardID string) string {
	return filepath.Clean(filepath.Join(m.WorktreesBase, "card-"+shortID(cardID)))
}

func (m *Manager) BranchName(cardID string) string {
	return "card/" + shortID(cardID)
}

func (m *Manager) Create(cardID string, branchName string) (string, error) {
	if branchName == "" {
		branchName = m.BranchName(cardID)
	}
	path := m.WorktreePath(cardID)

	if exists(path) {
		m.active[cardID] = path
		return path, nil
	}
	if err := os.MkdirAll(m.WorktreesBase, 0o755); err != nil {
		return "", fmt.Errorf("create worktrees directory: %w", err)
	}

	_ = m.updateMainBranch()

	_, err := m.run("git", "worktree", "add", "-b", branchName, path)
	if err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return "", fmt.Errorf("failed to create worktree: %w", err)
		}
		if _, fallbackErr := m.run("git", "worktree", "add", path, branchName); fallbackErr != nil {
			return "", fmt.Errorf("failed to create worktree: %w", fallbackErr)
		}
	}

	if err := m.SetupSymlinks(path); err != nil {
		return "", err
	}
	if err := m.runSetupCommand(path); err != nil {
		return "", err
	}

	m.active[cardID] = path
	return path, nil
}

func (m *Manager) Remove(cardID string, force bool) error {
	path := m.WorktreePath(cardID)
	delete(m.active, cardID)

	if !exists(path) {
		return nil
	}

	args := []string{"git", "worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	if _, err := m.run(args...); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}
	return nil
}

func (m *Manager) Get(cardID string) (string, bool) {
	path := m.WorktreePath(cardID)
	return path, exists(path)
}

func (m *Manager) ListActive() []string {
	var paths []string
	for _, path := range m.active {
		if exists(path) && exists(filepath.Join(path, ".git")) {
			paths = append(paths, path)
		}
	}
	return paths
}

func (m *Manager) SetupSymlinks(worktreePath string) error {
	if err := m.symlinkIfPresent(filepath.Join(m.BaseRepo, ".env"), filepath.Join(worktreePath, ".env")); err != nil {
		return err
	}

	switch strings.ToLower(m.ExecutorType) {
	case "claude":
		if err := os.MkdirAll(filepath.Join(worktreePath, ".claude"), 0o755); err != nil {
			return err
		}
		return m.symlinkIfPresent(
			filepath.Join(m.BaseRepo, ".claude", "settings.local.json"),
			filepath.Join(worktreePath, ".claude", "settings.local.json"),
		)
	case "codex":
		if err := m.symlinkDirIfPresent(filepath.Join(m.BaseRepo, ".agents", "skills"), filepath.Join(worktreePath, ".agents", "skills")); err != nil {
			return err
		}
		return m.symlinkDirIfPresent(filepath.Join(m.BaseRepo, ".codex", "skills"), filepath.Join(worktreePath, ".codex", "skills"))
	default:
		return nil
	}
}

func (m *Manager) updateMainBranch() error {
	if _, err := m.run("git", "fetch", "origin", "main"); err != nil {
		return err
	}
	result, err := m.run("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	current := strings.TrimSpace(result.Stdout)
	if _, err := m.run("git", "checkout", "main"); err != nil {
		return err
	}
	if _, err := m.run("git", "pull", "--ff-only"); err != nil {
		return err
	}
	if current != "" && current != "main" {
		if _, err := m.run("git", "checkout", current); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) runSetupCommand(worktreePath string) error {
	if strings.TrimSpace(m.SetupCommand) == "" {
		return nil
	}
	if _, err := m.Runner.Run(worktreePath, []string{"sh", "-c", m.SetupCommand}); err != nil {
		return fmt.Errorf("setup command failed: %w", err)
	}
	return nil
}

func (m *Manager) symlinkIfPresent(src string, dst string) error {
	if !exists(src) || exists(dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(src, dst)
}

func (m *Manager) symlinkDirIfPresent(src string, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() || exists(dst) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(src, dst)
}

func (m *Manager) run(args ...string) (RunResult, error) {
	return m.Runner.Run(m.BaseRepo, args)
}

type commandRunner struct{}

func (commandRunner) Run(dir string, args []string) (RunResult, error) {
	if len(args) == 0 {
		return RunResult{}, errors.New("missing command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := RunResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return result, commandError{args: args, stderr: result.Stderr, err: err}
	}
	return result, nil
}

type commandError struct {
	args   []string
	stderr string
	err    error
}

func (e commandError) Error() string {
	if strings.TrimSpace(e.stderr) != "" {
		return strings.TrimSpace(e.stderr)
	}
	return fmt.Sprintf("%s: %v", strings.Join(e.args, " "), e.err)
}

func (e commandError) Unwrap() error {
	return e.err
}

func shortID(cardID string) string {
	if len(cardID) <= 8 {
		return cardID
	}
	return cardID[:8]
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cleanAbs(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
