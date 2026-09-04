// Package update retrieves and installs kardbrd releases.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultLatestReleaseURL        = "https://api.github.com/repos/Kardbrd/kardbrd-agent/releases/latest"
	defaultMaxReleaseMetadataSize  = 1 << 20
	defaultMaxChecksumManifestSize = 1 << 20
	defaultMaxArchiveSize          = 100 << 20
	defaultMaxExtractedBinarySize  = 100 << 20
)

// Config contains the updater's external dependencies. The fields exist so
// callers can test updates without contacting GitHub or replacing themselves.
type Config struct {
	Client           *http.Client
	LatestReleaseURL string
	ExecutablePath   func() (string, error)
	GOOS             string
	GOARCH           string
}

// Updater downloads and atomically installs a compatible kardbrd release.
type Updater struct {
	client                  *http.Client
	latestReleaseURL        string
	executablePath          func() (string, error)
	goos                    string
	goarch                  string
	maxReleaseMetadataSize  int64
	maxChecksumManifestSize int64
	maxArchiveSize          int64
	maxExtractedBinarySize  int64
}

// Result describes a successfully installed release.
type Result struct {
	Tag        string
	Executable string
}

// New constructs an updater with production defaults for omitted dependencies.
func New(config Config) *Updater {
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	latestReleaseURL := config.LatestReleaseURL
	if latestReleaseURL == "" {
		latestReleaseURL = defaultLatestReleaseURL
	}
	executablePath := config.ExecutablePath
	if executablePath == nil {
		executablePath = os.Executable
	}
	goos := config.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := config.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return &Updater{
		client:                  client,
		latestReleaseURL:        latestReleaseURL,
		executablePath:          executablePath,
		goos:                    goos,
		goarch:                  goarch,
		maxReleaseMetadataSize:  defaultMaxReleaseMetadataSize,
		maxChecksumManifestSize: defaultMaxChecksumManifestSize,
		maxArchiveSize:          defaultMaxArchiveSize,
		maxExtractedBinarySize:  defaultMaxExtractedBinarySize,
	}
}

// Update retrieves, verifies, and atomically installs the latest matching release.
func (u *Updater) Update(ctx context.Context) (Result, error) {
	release, err := u.fetchRelease(ctx)
	if err != nil {
		return Result{}, err
	}
	archiveAsset, checksumAsset, err := selectAssets(release, u.goos, u.goarch)
	if err != nil {
		return Result{}, err
	}
	checksums, err := u.download(ctx, checksumAsset.BrowserDownloadURL, u.maxChecksumManifestSize)
	if err != nil {
		return Result{}, fmt.Errorf("download checksums.txt: %w", err)
	}
	archive, err := u.download(ctx, archiveAsset.BrowserDownloadURL, u.maxArchiveSize)
	if err != nil {
		return Result{}, fmt.Errorf("download %s: %w", archiveAsset.Name, err)
	}
	expectedChecksum, err := checksumFor(checksums, archiveAsset.Name)
	if err != nil {
		return Result{}, err
	}
	if err := verifyChecksum(archive, expectedChecksum); err != nil {
		return Result{}, fmt.Errorf("verify %s: %w", archiveAsset.Name, err)
	}

	executable, err := u.executablePath()
	if err != nil {
		return Result{}, fmt.Errorf("find executable: %w", err)
	}
	resolvedExecutable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return Result{}, fmt.Errorf("resolve executable: %w", err)
	}
	targetInfo, err := os.Stat(resolvedExecutable)
	if err != nil {
		return Result{}, fmt.Errorf("stat executable: %w", err)
	}
	if !targetInfo.Mode().IsRegular() {
		return Result{}, fmt.Errorf("executable %q is not a regular file", resolvedExecutable)
	}

	workDir, err := os.MkdirTemp("", "kardbrd-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create update workspace: %w", err)
	}
	defer os.RemoveAll(workDir)
	extractedBinary := filepath.Join(workDir, "kardbrd")
	archiveDir := strings.TrimSuffix(archiveAsset.Name, ".tar.gz")
	if err := extractBinaryWithLimit(archive, archiveDir, extractedBinary, u.maxExtractedBinarySize); err != nil {
		return Result{}, err
	}

	staged, err := os.CreateTemp(filepath.Dir(resolvedExecutable), ".kardbrd-update-*")
	if err != nil {
		return Result{}, fmt.Errorf("create replacement: %w", err)
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)

	newBinary, err := os.Open(extractedBinary)
	if err != nil {
		_ = staged.Close()
		return Result{}, fmt.Errorf("open extracted binary: %w", err)
	}
	_, copyErr := io.Copy(staged, newBinary)
	closeBinaryErr := newBinary.Close()
	syncErr := staged.Sync()
	closeStagedErr := staged.Close()
	if copyErr != nil {
		return Result{}, fmt.Errorf("stage replacement: %w", copyErr)
	}
	if closeBinaryErr != nil {
		return Result{}, fmt.Errorf("close extracted binary: %w", closeBinaryErr)
	}
	if syncErr != nil {
		return Result{}, fmt.Errorf("sync replacement: %w", syncErr)
	}
	if closeStagedErr != nil {
		return Result{}, fmt.Errorf("close replacement: %w", closeStagedErr)
	}
	if err := os.Chmod(stagedPath, targetInfo.Mode().Perm()); err != nil {
		return Result{}, fmt.Errorf("set replacement permissions: %w", err)
	}
	if err := os.Rename(stagedPath, resolvedExecutable); err != nil {
		return Result{}, fmt.Errorf("replace executable: %w", err)
	}
	return Result{Tag: release.TagName, Executable: resolvedExecutable}, nil
}

type release struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func selectAssets(release release, goos, goarch string) (releaseAsset, releaseAsset, error) {
	archiveName := fmt.Sprintf("kardbrd_%s_%s_%s.tar.gz", release.TagName, goos, goarch)
	var archive, checksums releaseAsset
	for _, asset := range release.Assets {
		switch asset.Name {
		case archiveName:
			archive = asset
		case "checksums.txt":
			checksums = asset
		}
	}
	if archive.BrowserDownloadURL == "" {
		return releaseAsset{}, releaseAsset{}, fmt.Errorf("release %s has no asset for %s/%s (%s)", release.TagName, goos, goarch, archiveName)
	}
	if checksums.BrowserDownloadURL == "" {
		return releaseAsset{}, releaseAsset{}, fmt.Errorf("release %s has no checksums.txt asset", release.TagName)
	}
	return archive, checksums, nil
}

func checksumFor(manifest []byte, assetName string) (string, error) {
	var checksum string
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != assetName {
			continue
		}
		if checksum != "" {
			return "", fmt.Errorf("checksums.txt has duplicate entries for %s", assetName)
		}
		if len(fields[0]) != 64 {
			return "", fmt.Errorf("checksums.txt has an invalid SHA-256 for %s", assetName)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("checksums.txt has an invalid SHA-256 for %s: %w", assetName, err)
		}
		checksum = fields[0]
	}
	if checksum == "" {
		return "", fmt.Errorf("checksums.txt has no entry for %s", assetName)
	}
	return checksum, nil
}

func extractBinary(archive []byte, archiveDir, destination string) (err error) {
	return extractBinaryWithLimit(archive, archiveDir, destination, defaultMaxExtractedBinarySize)
}

func extractBinaryWithLimit(archive []byte, archiveDir, destination string, maxBinarySize int64) (err error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	expectedBinary := archiveDir + "/kardbrd"
	foundBinary := false
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		if path.IsAbs(header.Name) || path.Clean(header.Name) != strings.TrimSuffix(header.Name, "/") {
			return fmt.Errorf("archive has unsafe path %q", header.Name)
		}

		switch header.Name {
		case archiveDir, archiveDir + "/":
			if header.Typeflag != tar.TypeDir {
				return fmt.Errorf("archive directory %q is not a directory", archiveDir)
			}
		case expectedBinary:
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				return fmt.Errorf("archive binary %q is not a regular file", expectedBinary)
			}
			if header.Size < 0 || header.Size > maxBinarySize {
				return fmt.Errorf("archive binary %q exceeds maximum size of %d bytes", expectedBinary, maxBinarySize)
			}
			if foundBinary {
				return fmt.Errorf("archive has duplicate binary %q", expectedBinary)
			}
			file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
			if err != nil {
				return fmt.Errorf("create extracted binary: %w", err)
			}
			_, copyErr := io.Copy(file, tarReader)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract binary: %w", copyErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close extracted binary: %w", closeErr)
			}
			foundBinary = true
		default:
			return fmt.Errorf("archive has unexpected entry %q", header.Name)
		}
	}
	if !foundBinary {
		return fmt.Errorf("archive has no binary %q", expectedBinary)
	}
	return nil
}

func (u *Updater) fetchRelease(ctx context.Context) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.latestReleaseURL, nil)
	if err != nil {
		return release{}, fmt.Errorf("create latest-release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kardbrd-self-update")
	resp, err := u.client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return release{}, fmt.Errorf("fetch latest release: HTTP %d", resp.StatusCode)
	}
	contents, err := readLimited(resp.Body, u.maxReleaseMetadataSize)
	if err != nil {
		return release{}, fmt.Errorf("read latest release: %w", err)
	}
	var releaseValue release
	if err := json.Unmarshal(contents, &releaseValue); err != nil {
		return release{}, fmt.Errorf("decode latest release: %w", err)
	}
	if releaseValue.TagName == "" {
		return release{}, fmt.Errorf("latest release has no tag name")
	}
	return releaseValue, nil
}

func (u *Updater) download(ctx context.Context, url string, maxSize int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create download request: %w", err)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	contents, err := readLimited(resp.Body, maxSize)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return contents, nil
}

func readLimited(reader io.Reader, maxSize int64) ([]byte, error) {
	contents, err := io.ReadAll(io.LimitReader(reader, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maxSize {
		return nil, fmt.Errorf("response exceeds maximum size of %d bytes", maxSize)
	}
	return contents, nil
}

func verifyChecksum(archive []byte, expected string) error {
	want, err := hex.DecodeString(expected)
	if err != nil {
		return fmt.Errorf("decode expected SHA-256: %w", err)
	}
	got := sha256.Sum256(archive)
	if !bytes.Equal(got[:], want) {
		return fmt.Errorf("SHA-256 mismatch")
	}
	return nil
}
