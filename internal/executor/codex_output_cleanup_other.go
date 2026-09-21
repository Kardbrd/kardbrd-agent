//go:build !linux

package executor

import "os"

// Other platforms retain the directory identity checks and never recursively
// remove a mutable path. Linux additionally unlinks through the held FD.
func removeCodexFinalOutputEntry(_ *os.File, path string) error { return os.Remove(path) }
