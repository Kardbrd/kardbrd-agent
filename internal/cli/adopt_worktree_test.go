package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentAdoptWorktreeVerifiesCanonicalAPICardIDBeforeRecordWrite(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		cardID     string
		wantErr    string
		wantRecord bool
	}{
		{
			name:    "canonical ID mismatch",
			status:  http.StatusOK,
			cardID:  "casecard",
			wantErr: "canonical API card ID",
		},
		{
			name:    "API read failure",
			status:  http.StatusServiceUnavailable,
			wantErr: "HTTP 503",
		},
		{
			name:       "exact canonical ID",
			status:     http.StatusOK,
			cardID:     "CaseCard",
			wantRecord: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearAgentEnv(t)
			base, worktrees, adopted, rulesPath, recordPath := setupAdoptionCommandFixture(t, "CaseCard")
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/api/cards/CaseCard/" {
					t.Fatalf("unexpected API request %s %s", r.Method, r.URL.Path)
				}
				if tc.status != http.StatusOK {
					http.Error(w, "synthetic API failure", tc.status)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":%q}`, tc.cardID)
			}))
			defer server.Close()

			t.Setenv("KARDBRD_API_URL", server.URL)
			t.Setenv("KARDBRD_TOKEN", "test-token")
			stdout, stderr, err := executeRoot("agent", "adopt-worktree", "CaseCard", "--cwd", base, "--worktrees-dir", worktrees, "--rules", rulesPath)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected adoption rejection, got success: stdout=%q stderr=%q", stdout, stderr)
				}
				assertCLIContains(t, stdout+stderr+err.Error(), tc.wantErr)
			} else if err != nil {
				t.Fatalf("unexpected adoption failure: %v\nstdout=%s\nstderr=%s", err, stdout, stderr)
			}
			if requests == 0 {
				t.Fatal("adoption did not fetch the card from the API")
			}
			_, recordErr := os.Stat(recordPath)
			if tc.wantRecord != (recordErr == nil) {
				t.Fatalf("record presence = %t (err=%v), want %t", recordErr == nil, recordErr, tc.wantRecord)
			}
			adoptionAssertPreservesSource(t, adopted)
		})
	}
}

func setupAdoptionCommandFixture(t *testing.T, cardID string) (base, worktrees, adopted, rulesPath, recordPath string) {
	t.Helper()
	base = filepath.Join(t.TempDir(), "base")
	worktrees = filepath.Join(t.TempDir(), "worktrees")
	adopted = filepath.Join(worktrees, "existing")
	cliGit(t, "", "init", base)
	cliGit(t, base, "config", "user.email", "adoption-test@example.test")
	cliGit(t, base, "config", "user.name", "Adoption Test")
	if err := os.WriteFile(filepath.Join(base, "source.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cliGit(t, base, "add", "source.txt")
	cliGit(t, base, "commit", "-m", "base")
	if err := os.MkdirAll(worktrees, 0o755); err != nil {
		t.Fatal(err)
	}
	cliGit(t, base, "worktree", "add", "-b", "fix/CaseCard-preserved", adopted)
	if err := os.WriteFile(filepath.Join(adopted, "source.txt"), []byte("dirty source must survive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	common := strings.TrimSpace(cliGitOutput(t, base, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	rulesPath = filepath.Join(t.TempDir(), "kardbrd.yml")
	rules := fmt.Sprintf("board_id: board1\nagent: Bot\nworktree:\n  checkout:\n    mode: full\n  sharing:\n    env: disabled\n    skills: disabled\n  adoptions:\n    - card_id: %s\n      path: %s\n      common_git_dir: %s\n      branch: fix/CaseCard-preserved\n", cardID, adopted, common)
	if err := os.WriteFile(rulesPath, []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	recordPath = strings.TrimSpace(cliGitOutput(t, adopted, "rev-parse", "--git-path", "kardbrd-lifecycle.json"))
	if !filepath.IsAbs(recordPath) {
		recordPath = filepath.Join(adopted, recordPath)
	}
	return base, worktrees, adopted, rulesPath, recordPath
}

func adoptionAssertPreservesSource(t *testing.T, adopted string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(adopted, "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "dirty source must survive\n", string(data))
	assertEqual(t, "fix/CaseCard-preserved\n", cliGitOutput(t, adopted, "branch", "--show-current"))
}

func cliGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func cliGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
