package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Kardbrd/kardbrd-agent/internal/update"
)

func TestSelfUpdateCommandPrintsInstalledRelease(t *testing.T) {
	fake := &fakeSelfUpdater{result: update.Result{Tag: "v9.9.9"}}
	previous := newSelfUpdater
	newSelfUpdater = func() selfUpdater { return fake }
	t.Cleanup(func() { newSelfUpdater = previous })

	stdout, stderr, err := executeRoot("self-update")
	if err != nil {
		t.Fatalf("self-update failed: %v\nstderr: %s", err, stderr)
	}
	if stdout != "Updated kardbrd to v9.9.9.\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if fake.calls != 1 {
		t.Fatalf("Update calls = %d, want 1", fake.calls)
	}
	if fake.ctx == nil {
		t.Fatal("Update received a nil context")
	}
}

func TestSelfUpdateCommandWrapsUpdaterError(t *testing.T) {
	sentinel := errors.New("release unavailable")
	previous := newSelfUpdater
	newSelfUpdater = func() selfUpdater { return &fakeSelfUpdater{err: sentinel} }
	t.Cleanup(func() { newSelfUpdater = previous })

	_, _, err := executeRoot("self-update")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want wrapped %v", err, sentinel)
	}
	if !strings.Contains(err.Error(), "self-update: release unavailable") {
		t.Fatalf("error = %q, want self-update context", err)
	}
}

type fakeSelfUpdater struct {
	result update.Result
	err    error
	calls  int
	ctx    context.Context
}

func (u *fakeSelfUpdater) Update(ctx context.Context) (update.Result, error) {
	u.calls++
	u.ctx = ctx
	return u.result, u.err
}
