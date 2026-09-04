package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentDockerfileProvidesDeliveryTooling(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	dockerfile := string(data)

	for _, want := range []string{
		"FROM golang:1.24-bookworm AS agent",
		"ARG TARGETARCH",
		"ARG GH_VERSION=",
		"gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz",
		"gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh",
		"ARG PRE_COMMIT_VERSION=",
		"pre-commit==${PRE_COMMIT_VERSION}",
		"npm install -g @openai/codex@0.144.5",
		"USER agent",
		"RUN kardbrd --version",
		"codex --version",
		"gh --version",
		"go version",
		"pre-commit --version",
	} {
		assertContains(t, dockerfile, want)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected Dockerfile to contain %q", want)
	}
}
