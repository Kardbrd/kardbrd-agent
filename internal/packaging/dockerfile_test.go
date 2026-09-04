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
		"ARG GH_VERSION=2.100.0",
		"ARG GH_AMD64_SHA256=e4d4bb4498e8d007abe545b6568926793ace1b6447da598294a610018cb164be",
		"ARG GH_ARM64_SHA256=ea4e7a581a32ccad6cc7923cb1576ac5859ba4b9a16ab22eb8f8a96e78e2e961",
		"gh_${GH_VERSION}_linux_${TARGETARCH}.tar.gz",
		"gh_${GH_VERSION}_linux_${TARGETARCH}/bin/gh",
		"sha256sum -c -",
		"ARG PRE_COMMIT_VERSION=4.6.2",
		"pre-commit==${PRE_COMMIT_VERSION}",
		"npm install -g @openai/codex@0.144.5",
	} {
		assertContains(t, dockerfile, want)
	}

	smoke := "RUN kardbrd --version \\\n    && codex --version \\\n    && gh --version \\\n    && go version \\\n    && pre-commit --version"
	assertContains(t, dockerfile, smoke)
	beforeSmoke := dockerfile[:strings.Index(dockerfile, smoke)]
	if userIndex := strings.LastIndex(beforeSmoke, "USER "); userIndex == -1 || !strings.HasPrefix(beforeSmoke[userIndex:], "USER agent\n") {
		t.Fatal("expected delivery-tooling smoke to run as the non-root agent user")
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("expected Dockerfile to contain %q", want)
	}
}
