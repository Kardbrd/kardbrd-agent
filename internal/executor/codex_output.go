package executor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxCodexFinalMessageBytes = 1 << 20

func createCodexFinalMessageFile() (string, error) {
	dir, err := os.MkdirTemp("", "kardbrd-codex-final-*")
	if err != nil {
		return "", fmt.Errorf("create private Codex final-output directory: %w", err)
	}
	return filepath.Join(dir, "final.txt"), nil
}

func removeCodexFinalMessageFile(path string) {
	_ = os.RemoveAll(filepath.Dir(path))
}

func readCodexFinalMessage(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("Codex did not write final output")
		}
		return "", fmt.Errorf("read Codex final output: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Codex final output is not a regular file")
	}
	if info.Size() > maxCodexFinalMessageBytes {
		return "", fmt.Errorf("Codex final output exceeds %d bytes", maxCodexFinalMessageBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open Codex final output: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCodexFinalMessageBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Codex final output: %w", err)
	}
	if len(data) > maxCodexFinalMessageBytes {
		return "", fmt.Errorf("Codex final output exceeds %d bytes", maxCodexFinalMessageBytes)
	}
	return strings.TrimSpace(string(data)), nil
}
