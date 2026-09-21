//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package worktree

import "context"

// Platforms without flock retain the manager-level serialization. The direct
// argv/process lifecycle remains portable; platform-native advisory locking can
// be supplied here without changing lifecycle semantics.
func lockLifecycleGitAdministration(_ context.Context, _ string) (func(), error) {
	return func() {}, nil
}
