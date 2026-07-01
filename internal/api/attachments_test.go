package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadAttachmentPresignsPutsAndConfirms(t *testing.T) {
	var uploadedBody string
	var uploadedContentType string
	var presignSeen bool
	var confirmSeen bool

	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertEqual(t, "PUT", r.Method)
		uploadedContentType = r.Header.Get("Content-Type")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		uploadedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cards/card1/attachments/presign/":
			presignSeen = true
			assertEqual(t, "POST", r.Method)
			writeJSON(t, w, map[string]any{
				"data": map[string]string{
					"upload_url": uploadServer.URL,
					"s3_key":     "uploads/file.txt",
				},
			})
		case "/api/cards/card1/attachments/confirm/":
			confirmSeen = true
			assertEqual(t, "POST", r.Method)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			assertEqual(t, "uploads/file.txt", payload["s3_key"])
			assertEqual(t, "file.txt", payload["filename"])
			writeJSON(t, w, map[string]any{"data": map[string]string{"id": "att1"}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := NewClient(apiServer.URL, "tok")
	raw, err := client.UploadAttachment(context.Background(), "card1", filePath)
	if err != nil {
		t.Fatal(err)
	}

	var result map[string]string
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, true, presignSeen)
	assertEqual(t, true, confirmSeen)
	assertEqual(t, "hello", uploadedBody)
	assertEqual(t, "text/plain; charset=utf-8", uploadedContentType)
	assertEqual(t, "att1", result["id"])
}
