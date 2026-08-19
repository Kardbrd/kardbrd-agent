package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
