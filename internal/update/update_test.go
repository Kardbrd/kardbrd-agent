package update

import "testing"

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
