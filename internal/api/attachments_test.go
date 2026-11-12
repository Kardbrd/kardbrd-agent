package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestUploadAttachmentUsesPresignedContentType(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		content    []byte
		signedType string
	}{
		{"markdown", "notes.md", []byte("# Héllo\n"), "text/markdown; charset=utf-8"},
		{"json", "data.json", []byte(`{"ok":true}`), "application/json; charset=utf-8"},
		{"explicit charset", "notes.txt", []byte("hello"), "text/plain; charset=iso-8859-1"},
		{"binary", "image.png", []byte{0x89, 'P', 'N', 'G', 0, 0xff}, "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			puts, confirms := 0, 0
			uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				puts++
				if r.Method != http.MethodPut {
					t.Errorf("method = %s, want PUT", r.Method)
				}
				if got := r.Header.Get("Content-Type"); got != tt.signedType {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if !bytes.Equal(body, tt.content) {
					t.Errorf("PUT body = %q, want %q", body, tt.content)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer uploadServer.Close()
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/cards/card1/attachments/presign/":
					var payload struct {
						ContentType string `json:"content_type"`
						FileSize    int64  `json:"file_size"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.ContentType == "" {
						t.Error("missing requested content type")
					}
					if payload.FileSize != int64(len(tt.content)) {
						t.Errorf("size = %d", payload.FileSize)
					}
					writeJSON(t, w, map[string]any{"data": map[string]string{"upload_url": uploadServer.URL, "s3_key": "uploads/file", "content_type": tt.signedType}})
				case "/api/cards/card1/attachments/confirm/":
					confirms++
					var payload struct {
						ContentType string `json:"content_type"`
						FileSize    int64  `json:"file_size"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
					}
					if payload.ContentType != tt.signedType {
						t.Errorf("confirmed type = %q, want %q", payload.ContentType, tt.signedType)
					}
					if payload.FileSize != int64(len(tt.content)) {
						t.Errorf("confirmed size = %d", payload.FileSize)
					}
					writeJSON(t, w, map[string]any{"data": map[string]string{"id": "att1"}})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer apiServer.Close()
			filePath := filepath.Join(t.TempDir(), tt.filename)
			if err := os.WriteFile(filePath, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			client := NewClient(apiServer.URL, "tok")
			client.SetNoRetry(true)
			if _, err := client.UploadAttachment(context.Background(), "card1", filePath); err != nil {
				t.Fatal(err)
			}
			if puts != 1 || confirms != 1 {
				t.Errorf("puts = %d, confirms = %d; want 1 each", puts, confirms)
			}
		})
	}
}

func TestUploadAttachmentRejectsMissingOrInvalidPresignedContentType(t *testing.T) {
	for _, signedType := range []string{"", "not a media type"} {
		t.Run(signedType, func(t *testing.T) {
			puts, confirms := 0, 0
			uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				puts++
				w.WriteHeader(http.StatusOK)
			}))
			defer uploadServer.Close()
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/cards/card1/attachments/presign/":
					writeJSON(t, w, map[string]any{"data": map[string]string{"upload_url": uploadServer.URL, "s3_key": "uploads/file", "content_type": signedType}})
				case "/api/cards/card1/attachments/confirm/":
					confirms++
					writeJSON(t, w, map[string]any{"data": map[string]string{"id": "unexpected"}})
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer apiServer.Close()
			client := NewClient(apiServer.URL, "tok")
			client.SetNoRetry(true)
			if _, err := client.UploadAttachment(context.Background(), "card1", writeAttachmentFixture(t)); err == nil {
				t.Error("expected invalid presign content type error")
			}
			if puts != 0 || confirms != 0 {
				t.Errorf("puts = %d, confirms = %d; want none", puts, confirms)
			}
		})
	}
}

func TestUploadAttachmentCancellationDuringPutDoesNotConfirmOrRetry(t *testing.T) {
	var puts, confirms int32
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	uploadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&puts, 1)
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer uploadServer.Close()
	defer close(release)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/cards/card1/attachments/presign/":
			writeJSON(t, w, map[string]any{"data": map[string]string{"upload_url": uploadServer.URL, "s3_key": "uploads/file", "content_type": "text/plain; charset=utf-8"}})
		case "/api/cards/card1/attachments/confirm/":
			atomic.AddInt32(&confirms, 1)
			writeJSON(t, w, map[string]any{"data": map[string]string{"id": "unexpected"}})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer apiServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	filePath := writeAttachmentFixture(t)
	result := make(chan error, 1)
	go func() {
		_, err := NewClient(apiServer.URL, "tok").UploadAttachment(ctx, "card1", filePath)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("PUT did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("upload error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not stop after cancellation")
	}
	if got := atomic.LoadInt32(&puts); got != 1 {
		t.Errorf("PUT attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&confirms); got != 0 {
		t.Errorf("confirmations = %d, want 0", got)
	}
}

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
					"upload_url":   uploadServer.URL,
					"s3_key":       "uploads/file.txt",
					"content_type": "text/plain; charset=utf-8",
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
			writeJSON(t, w, map[string]any{"data": map[string]string{"upload_url": uploadURL, "s3_key": "uploads/file.txt", "content_type": "text/plain; charset=utf-8"}})
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
