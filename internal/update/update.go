// Package update retrieves and installs kardbrd releases.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

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
