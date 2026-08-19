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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectAssetsForSupportedPlatforms(t *testing.T) {
	t.Parallel()

	const tag = "v9.9.9"
	release := release{
		TagName: tag,
		Assets: []releaseAsset{
			{Name: "checksums.txt", BrowserDownloadURL: "https://example.test/checksums.txt"},
			{Name: "kardbrd_v9.9.9_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.test/linux-amd64"},
			{Name: "kardbrd_v9.9.9_linux_arm64.tar.gz", BrowserDownloadURL: "https://example.test/linux-arm64"},
			{Name: "kardbrd_v9.9.9_darwin_amd64.tar.gz", BrowserDownloadURL: "https://example.test/darwin-amd64"},
			{Name: "kardbrd_v9.9.9_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.test/darwin-arm64"},
		},
	}

	tests := []struct {
		name    string
		goos    string
		goarch  string
		wantURL string
	}{
		{name: "Linux AMD64", goos: "linux", goarch: "amd64", wantURL: "https://example.test/linux-amd64"},
		{name: "Linux ARM64", goos: "linux", goarch: "arm64", wantURL: "https://example.test/linux-arm64"},
		{name: "macOS Intel", goos: "darwin", goarch: "amd64", wantURL: "https://example.test/darwin-amd64"},
		{name: "macOS Apple Silicon", goos: "darwin", goarch: "arm64", wantURL: "https://example.test/darwin-arm64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive, checksums, err := selectAssets(release, tt.goos, tt.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if archive.BrowserDownloadURL != tt.wantURL {
				t.Fatalf("archive URL = %q, want %q", archive.BrowserDownloadURL, tt.wantURL)
			}
			if checksums.Name != "checksums.txt" {
				t.Fatalf("checksum asset = %q, want checksums.txt", checksums.Name)
			}
		})
	}
}

func TestSelectAssetsRejectsMissingPlatformAsset(t *testing.T) {
	t.Parallel()

	_, _, err := selectAssets(release{
		TagName: "v9.9.9",
		Assets:  []releaseAsset{{Name: "checksums.txt", BrowserDownloadURL: "https://example.test/checksums.txt"}},
	}, "darwin", "arm64")
	if err == nil {
		t.Fatal("selectAssets succeeded without a platform archive")
	}
}

func TestSelectAssetsRejectsMissingChecksumAsset(t *testing.T) {
	t.Parallel()

	_, _, err := selectAssets(release{
		TagName: "v9.9.9",
		Assets:  []releaseAsset{{Name: "kardbrd_v9.9.9_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.test/linux-amd64"}},
	}, "linux", "amd64")
	if err == nil {
		t.Fatal("selectAssets succeeded without checksums.txt")
	}
}

func TestChecksumFor(t *testing.T) {
	t.Parallel()

	const asset = "kardbrd_v9.9.9_darwin_arm64.tar.gz"
	const digest = "74c1b23bf9fcd5e8a0742b77a6637b2ea688d0a6ce93ca3a4b8598f8c7495101"
	tests := []struct {
		name     string
		manifest string
		want     string
		wantErr  bool
	}{
		{name: "valid", manifest: digest + "  " + asset + "\n", want: digest},
		{name: "missing", manifest: digest + "  another-file.tar.gz\n", wantErr: true},
		{name: "duplicate", manifest: digest + "  " + asset + "\n" + digest + "  " + asset + "\n", wantErr: true},
		{name: "short digest", manifest: "abc  " + asset + "\n", wantErr: true},
		{name: "non hexadecimal digest", manifest: strings.Repeat("g", 64) + "  " + asset + "\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checksumFor([]byte(tt.manifest), asset)
			if tt.wantErr {
				if err == nil {
					t.Fatal("checksumFor succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("checksum = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractBinary(t *testing.T) {
	t.Parallel()

	const archiveDir = "kardbrd_v9.9.9_linux_amd64"
	tests := []struct {
		name    string
		entries []tarEntry
		want    string
		wantErr bool
	}{
		{
			name:    "expected binary",
			entries: []tarEntry{{name: archiveDir + "/kardbrd", mode: 0o755, body: "new executable"}},
			want:    "new executable",
		},
		{
			name:    "path traversal",
			entries: []tarEntry{{name: "../kardbrd", mode: 0o755, body: "bad"}},
			wantErr: true,
		},
		{
			name:    "absolute path",
			entries: []tarEntry{{name: "/kardbrd", mode: 0o755, body: "bad"}},
			wantErr: true,
		},
		{
			name:    "symlink binary",
			entries: []tarEntry{{name: archiveDir + "/kardbrd", typeflag: tar.TypeSymlink, linkname: "/bin/sh"}},
			wantErr: true,
		},
		{
			name:    "duplicate binary",
			entries: []tarEntry{{name: archiveDir + "/kardbrd", mode: 0o755, body: "first"}, {name: archiveDir + "/kardbrd", mode: 0o755, body: "second"}},
			wantErr: true,
		},
		{
			name:    "unexpected file",
			entries: []tarEntry{{name: archiveDir + "/README", mode: 0o644, body: "bad"}},
			wantErr: true,
		},
		{
			name:    "missing binary",
			entries: []tarEntry{{name: archiveDir + "/", typeflag: tar.TypeDir, mode: 0o755}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "kardbrd")
			err := extractBinary(makeArchive(t, tt.entries), archiveDir, destination)
			if tt.wantErr {
				if err == nil {
					t.Fatal("extractBinary succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(destination)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("binary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpdateReplacesExecutableFromVerifiedPlatformRelease(t *testing.T) {
	const tag = "v9.9.9"
	const assetName = "kardbrd_v9.9.9_darwin_arm64.tar.gz"
	archive := makeArchive(t, []tarEntry{{name: "kardbrd_v9.9.9_darwin_arm64/kardbrd", mode: 0o755, body: "new executable"}})
	server := newReleaseServer(t, tag, assetName, archive, checksumManifest(assetName, archive), http.StatusOK, http.StatusOK)

	target := filepath.Join(t.TempDir(), "kardbrd")
	if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	updater := New(Config{
		Client:           server.Client(),
		LatestReleaseURL: server.URL + "/latest",
		ExecutablePath:   func() (string, error) { return target, nil },
		GOOS:             "darwin",
		GOARCH:           "arm64",
	})
	result, err := updater.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Tag != tag {
		t.Fatalf("tag = %q, want %q", result.Tag, tag)
	}
	if result.Executable != target {
		t.Fatalf("executable = %q, want %q", result.Executable, target)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new executable" {
		t.Fatalf("executable = %q, want replacement", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %o, want 755", info.Mode().Perm())
	}
}

func TestUpdateLeavesExecutableUntouchedOnFailure(t *testing.T) {
	const tag = "v9.9.9"
	const assetName = "kardbrd_v9.9.9_linux_amd64.tar.gz"
	validArchive := makeArchive(t, []tarEntry{{name: "kardbrd_v9.9.9_linux_amd64/kardbrd", mode: 0o755, body: "new executable"}})
	tests := []struct {
		name          string
		archive       []byte
		manifest      string
		releaseStatus int
		archiveStatus int
	}{
		{name: "release error", archive: validArchive, manifest: checksumManifest(assetName, validArchive), releaseStatus: http.StatusInternalServerError, archiveStatus: http.StatusOK},
		{name: "archive error", archive: validArchive, manifest: checksumManifest(assetName, validArchive), releaseStatus: http.StatusOK, archiveStatus: http.StatusBadGateway},
		{name: "checksum mismatch", archive: validArchive, manifest: strings.Repeat("0", 64) + "  " + assetName + "\n", releaseStatus: http.StatusOK, archiveStatus: http.StatusOK},
		{name: "unsafe archive", archive: makeArchive(t, []tarEntry{{name: "../kardbrd", mode: 0o755, body: "bad"}}), releaseStatus: http.StatusOK, archiveStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := tt.manifest
			if manifest == "" {
				manifest = checksumManifest(assetName, tt.archive)
			}
			server := newReleaseServer(t, tag, assetName, tt.archive, manifest, tt.releaseStatus, tt.archiveStatus)
			target := filepath.Join(t.TempDir(), "kardbrd")
			if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
				t.Fatal(err)
			}

			_, err := New(Config{
				Client:           server.Client(),
				LatestReleaseURL: server.URL + "/latest",
				ExecutablePath:   func() (string, error) { return target, nil },
				GOOS:             "linux",
				GOARCH:           "amd64",
			}).Update(context.Background())
			if err == nil {
				t.Fatal("Update succeeded, want error")
			}
			got, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(got) != "old executable" {
				t.Fatalf("executable changed to %q after failed update", got)
			}
		})
	}
}

func TestUpdateResolvesExecutableSymlink(t *testing.T) {
	const tag = "v9.9.9"
	const assetName = "kardbrd_v9.9.9_linux_arm64.tar.gz"
	archive := makeArchive(t, []tarEntry{{name: "kardbrd_v9.9.9_linux_arm64/kardbrd", mode: 0o755, body: "new executable"}})
	server := newReleaseServer(t, tag, assetName, archive, checksumManifest(assetName, archive), http.StatusOK, http.StatusOK)

	dir := t.TempDir()
	target := filepath.Join(dir, "kardbrd-real")
	link := filepath.Join(dir, "kardbrd")
	if err := os.WriteFile(target, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := New(Config{
		Client:           server.Client(),
		LatestReleaseURL: server.URL + "/latest",
		ExecutablePath:   func() (string, error) { return link, nil },
		GOOS:             "linux",
		GOARCH:           "arm64",
	}).Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("executable symlink was replaced")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new executable" {
		t.Fatalf("resolved executable = %q, want replacement", got)
	}
}

type tarEntry struct {
	name     string
	mode     int64
	body     string
	typeflag byte
	linkname string
}

func makeArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Size:     int64(len(entry.body)),
			Typeflag: typeflag,
			Linkname: entry.linkname,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func newReleaseServer(t *testing.T, tag, assetName string, archive []byte, manifest string, releaseStatus, archiveStatus int) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			if releaseStatus != http.StatusOK {
				http.Error(w, "release unavailable", releaseStatus)
				return
			}
			if err := json.NewEncoder(w).Encode(release{
				TagName: tag,
				Assets: []releaseAsset{
					{Name: assetName, BrowserDownloadURL: server.URL + "/archive"},
					{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"},
				},
			}); err != nil {
				t.Errorf("encode release: %v", err)
			}
		case "/archive":
			if archiveStatus != http.StatusOK {
				http.Error(w, "archive unavailable", archiveStatus)
				return
			}
			_, _ = w.Write(archive)
		case "/checksums":
			_, _ = fmt.Fprint(w, manifest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func checksumManifest(assetName string, archive []byte) string {
	digest := sha256.Sum256(archive)
	return hex.EncodeToString(digest[:]) + "  " + assetName + "\n"
}
