// Package update retrieves and installs kardbrd releases.
package update

import "fmt"

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
