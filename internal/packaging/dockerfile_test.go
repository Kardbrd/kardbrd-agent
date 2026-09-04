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
		"ARG GH_VERSION=2.98.0",
		"ARG GH_AMD64_SHA256=3b8ac6b30336802fc1a858d7c084e11cdf24ac1a761ca90b68022d7d729208de",
		"ARG GH_ARM64_SHA256=cf689084f3a3618f7eae4a2420d335d74626d65f5e594b9828d125d69f800d86",
		"gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz",
		"gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh",
		"sha256sum -c -",
		"ARG PRE_COMMIT_VERSION=4.1.0",
		"pre-commit==${PRE_COMMIT_VERSION}",
		"npm install -g @openai/codex@0.144.5",
	} {
		assertContains(t, dockerfile, want)
	}

	smoke := "RUN kardbrd --version \\\n    && codex --version \\\n    && gh --version \\\n    && go version \\\n    && pre-commit --version"
	assertContains(t, dockerfile, smoke)
	if userIndex := strings.Index(dockerfile, "USER agent"); userIndex == -1 || userIndex > strings.Index(dockerfile, smoke) {
		t.Fatal("expected delivery-tooling smoke to run as the non-root agent user")
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected Dockerfile to contain %q", want)
	}
}
