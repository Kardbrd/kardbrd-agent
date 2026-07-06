package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentDockerfileInstallsCodexCLI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)

	assertContains(t, dockerfile, "nodejs")
	assertContains(t, dockerfile, "npm")
	assertContains(t, dockerfile, "npm install -g @openai/codex")
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected Dockerfile to contain %q", want)
	}
}
