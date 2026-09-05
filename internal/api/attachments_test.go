package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

func TestNoRetryAttachmentUploadDoesNotReplayCommittedOrLostWrites(t *testing.T) {
	tests := []struct {
		name          string
		uploadHandler func(t *testing.T, w http.ResponseWriter, r *http.Request)
	}{
		{
			name: "committed 500",
			uploadHandler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Helper()
				if _, err := io.ReadAll(r.Body); err != nil {
					t.Fatal(err)
				}
				w.WriteHeader(http.StatusInternalServerError)
			},
		},
		{
			name: "lost response",
			uploadHandler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Helper()
				if _, err := io.ReadAll(r.Body); err != nil {
					t.Fatal(err)
				}
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("response writer does not support hijacking")
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var uploads int32
			uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&uploads, 1)
				if r.Method != http.MethodPut {
					t.Fatalf("method = %s, want PUT", r.Method)
				}
				tt.uploadHandler(t, w, r)
			}))
			defer uploadServer.Close()

			confirmations := 0
			apiServer := attachmentAPIServer(t, uploadServer.URL, &confirmations)
			defer apiServer.Close()

			filePath := writeAttachmentFixture(t)
			client := NewClient(apiServer.URL, "tok")
			client.SetNoRetry(true)
			_, err := client.UploadAttachment(context.Background(), "card1", filePath)
			if err == nil {
				t.Fatal("expected upload failure")
			}
			assertEqual(t, int32(1), atomic.LoadInt32(&uploads))
			assertEqual(t, 0, confirmations)
		})
	}
}

func TestNoRetryAttachmentUploadDoesNotFollowRedirect(t *testing.T) {
	var redirectedUploads int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&redirectedUploads, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	var uploads int32
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&uploads, 1)
		w.Header().Set("Location", target.URL+"/replayed")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer uploadServer.Close()

	confirmations := 0
	apiServer := attachmentAPIServer(t, uploadServer.URL, &confirmations)
	defer apiServer.Close()

	client := NewClient(apiServer.URL, "tok")
	client.SetNoRetry(true)
	_, err := client.UploadAttachment(context.Background(), "card1", writeAttachmentFixture(t))
	if err == nil {
		t.Fatal("expected redirect failure")
	}
	assertEqual(t, int32(1), atomic.LoadInt32(&uploads))
	assertEqual(t, int32(0), atomic.LoadInt32(&redirectedUploads))
	assertEqual(t, 0, confirmations)
}

func TestAttachmentUploadRetriesServerErrorsByDefault(t *testing.T) {
	var uploads int32
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&uploads, 1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer uploadServer.Close()

	confirmations := 0
	apiServer := attachmentAPIServer(t, uploadServer.URL, &confirmations)
	defer apiServer.Close()

	_, err := NewClient(apiServer.URL, "tok").UploadAttachment(context.Background(), "card1", writeAttachmentFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, int32(3), atomic.LoadInt32(&uploads))
	assertEqual(t, 1, confirmations)
}

func attachmentAPIServer(t *testing.T, uploadURL string, confirmations *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cards/card1/attachments/presign/":
			writeJSON(t, w, map[string]any{"data": map[string]string{"upload_url": uploadURL, "s3_key": "uploads/file.txt"}})
		case "/api/cards/card1/attachments/confirm/":
			*confirmations++
			writeJSON(t, w, map[string]any{"data": map[string]string{"id": "att1"}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func writeAttachmentFixture(t *testing.T) string {
	t.Helper()
	filePath := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filePath
}
