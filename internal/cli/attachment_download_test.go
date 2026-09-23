package cli

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachmentDownloadPublishesOnlyCompleteNewFiles(t *testing.T) {
	for _, mode := range []string{"success", "existing", "incomplete"} {
		t.Run(mode, func(t *testing.T) {
			storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "incomplete" {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = w.Write([]byte("image bytes"))
			}))
			defer storage.Close()
			previous := http.DefaultTransport
			http.DefaultTransport = storage.Client().Transport
			defer func() { http.DefaultTransport = previous }()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, storage.URL+"/file", http.StatusFound)
			}))
			defer server.Close()
			dir := t.TempDir()
			path := filepath.Join(dir, "image.png")
			if mode == "existing" {
				if err := os.WriteFile(path, []byte("keep me"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stdout, _, err := executeRoot("--api-url", server.URL, "--token", "secret", "attachment", "download", "card1", "image1", "--output", path)
			if mode == "success" {
				if err != nil || !strings.Contains(stdout, "image.png") {
					t.Fatalf("%s: %v", stdout, err)
				}
				data, _ := os.ReadFile(path)
				if string(data) != "image bytes" {
					t.Fatalf("bytes=%q", data)
				}
			} else {
				if err == nil {
					t.Fatal("expected failure")
				}
				data, readErr := os.ReadFile(path)
				if mode == "existing" && string(data) != "keep me" {
					t.Fatal("existing file changed")
				}
				if mode == "incomplete" && !os.IsNotExist(readErr) {
					t.Fatal("partial download was published")
				}
			}
			files, _ := filepath.Glob(filepath.Join(dir, ".kardbrd-download-*"))
			if len(files) != 0 {
				t.Fatal("temporary downloads leaked")
			}
		})
	}
}
