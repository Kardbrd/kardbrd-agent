package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentSupervisorDocumentedEmptyPassthrough(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kardbrd.yml")
	data := []byte("board_id: testboard\nagent: TestBot\nworktree:\n  environment:\n    passthrough: []\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("documented empty passthrough must load: %v", err)
	}
}
