package executor

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxCodexFinalMessageBytes = 1 << 20

type codexFinalMessageFile struct {
	path     string
	dir      string
	dirFile  *os.File
	file     *os.File
	sentinel []byte
}

func createCodexFinalMessageFile() (*codexFinalMessageFile, error) {
	dir, err := os.MkdirTemp("", "kardbrd-codex-final-*")
	if err != nil {
		return nil, fmt.Errorf("create private Codex final-output directory: %w", err)
	}
	dirFile, err := os.Open(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("open private Codex final-output directory: %w", err)
	}
	path := filepath.Join(dir, "final.txt")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = dirFile.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("create Codex final output: %w", err)
	}
	sentinel := make([]byte, 32)
	if _, err := rand.Read(sentinel); err != nil {
		_ = file.Close()
		_ = dirFile.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("seed Codex final-output sentinel: %w", err)
	}
	if _, err := file.Write(sentinel); err != nil {
		_ = file.Close()
		_ = dirFile.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("initialize Codex final output: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = dirFile.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("sync Codex final output: %w", err)
	}
	return &codexFinalMessageFile{path: path, dir: dir, dirFile: dirFile, file: file, sentinel: sentinel}, nil
}

func (output *codexFinalMessageFile) read() (string, error) {
	if output == nil || output.file == nil {
		return "", errors.New("Codex final output is unavailable")
	}
	info, err := output.file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat Codex final output: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Codex final output is not a regular file")
	}
	if info.Size() > maxCodexFinalMessageBytes {
		return "", fmt.Errorf("Codex final output exceeds %d bytes", maxCodexFinalMessageBytes)
	}
	if _, err := output.file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek Codex final output: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(output.file, maxCodexFinalMessageBytes+1))
	if err != nil {
		return "", fmt.Errorf("read Codex final output: %w", err)
	}
	if len(data) > maxCodexFinalMessageBytes {
		return "", fmt.Errorf("Codex final output exceeds %d bytes", maxCodexFinalMessageBytes)
	}
	if bytes.Equal(data, output.sentinel) {
		return "", errors.New("Codex did not write final output")
	}
	return strings.TrimSpace(string(data)), nil
}

func (output *codexFinalMessageFile) remove() error {
	if output == nil {
		return nil
	}
	var err error
	if output.file != nil {
		err = output.file.Close()
		output.file = nil
	}
	if output.dirFile != nil {
		// The child may have made its private directory non-searchable. Restore
		// the mode and verify directory identity before unlinking the expected
		// final file. Linux unlinks through the held directory descriptor.
		if chmodErr := output.dirFile.Chmod(0o700); chmodErr != nil {
			err = errors.Join(err, fmt.Errorf("restore private Codex output directory: %w", chmodErr))
		}
		originalInfo, statErr := output.dirFile.Stat()
		if statErr != nil {
			err = errors.Join(err, fmt.Errorf("stat private Codex output directory: %w", statErr))
		} else if pathInfo, pathErr := os.Stat(output.dir); pathErr != nil {
			err = errors.Join(err, fmt.Errorf("verify private Codex output directory: %w", pathErr))
		} else if !os.SameFile(originalInfo, pathInfo) {
			err = errors.Join(err, errors.New("private Codex output directory identity changed during cleanup"))
		} else if removeErr := removeCodexFinalOutputEntry(output.dirFile, output.path); removeErr != nil {
			err = errors.Join(err, fmt.Errorf("remove private Codex final output: %w", removeErr))
		} else if pathInfo, pathErr := os.Stat(output.dir); pathErr != nil {
			err = errors.Join(err, fmt.Errorf("reverify private Codex output directory: %w", pathErr))
		} else if !os.SameFile(originalInfo, pathInfo) {
			err = errors.Join(err, errors.New("private Codex output directory identity changed during cleanup"))
		} else if removeErr := os.Remove(output.dir); removeErr != nil {
			// Never use RemoveAll here. If the path changes after the identity
			// check, os.Remove can only remove an empty attacker-controlled
			// directory; it cannot recursively remove unrelated content.
			err = errors.Join(err, fmt.Errorf("remove private Codex output directory: %w", removeErr))
		}
		if closeErr := output.dirFile.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		output.dirFile = nil
	}
	return err
}
