package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDownloadAttachmentStreamsWithoutBoardCredentials(t *testing.T) {
	payload := []byte("\x89PNG\r\n\x1a\nimage bytes")
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("API credentials reached object storage")
		}
		_, _ = w.Write(payload)
	}))
	defer storage.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cards/card1/attachments/image1/download/" || r.Header.Get("Authorization") != "Bearer board-secret" {
			t.Errorf("unexpected API request: %s", r.URL.Path)
		}
		http.Redirect(w, r, storage.URL+"/object?signature=secret", http.StatusFound)
	}))
	defer server.Close()
	client := NewClient(server.URL, "board-secret")
	client.HTTPClient = storage.Client()
	jar, _ := cookiejar.New(nil)
	storageURL, _ := url.Parse(storage.URL)
	jar.SetCookies(storageURL, []*http.Cookie{{Name: "session", Value: "private"}})
	client.HTTPClient.Jar = jar
	var output bytes.Buffer
	if err := client.DownloadAttachment(context.Background(), "card1", "image1", &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), payload) {
		t.Fatalf("downloaded %q", output.Bytes())
	}
}

func TestDownloadAttachmentFailuresDoNotWriteResponseBodies(t *testing.T) {
	for _, mode := range []string{"api-failure", "storage-failure", "storage-redirect", "invalid-url", "no-retry", "transport-failure"} {
		t.Run(mode, func(t *testing.T) {
			storageCalls := 0
			storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				storageCalls++
				if mode == "storage-redirect" {
					http.Redirect(w, r, "/unexpected", http.StatusFound)
					return
				}
				http.Error(w, "private error body", http.StatusForbidden)
			}))
			defer storage.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "api-failure" {
					http.Error(w, "denied", http.StatusUnauthorized)
					return
				}
				location := storage.URL + "/?signature=secret"
				if mode == "invalid-url" {
					location = "http://untrusted.test/file"
				}
				http.Redirect(w, r, location, http.StatusFound)
			}))
			defer server.Close()
			client := NewClient(server.URL, "board-secret")
			client.HTTPClient = storage.Client()
			client.SetNoRetry(mode == "no-retry")
			if mode == "transport-failure" {
				storage.Close()
			}
			var output bytes.Buffer
			err := client.DownloadAttachment(context.Background(), "card1", "image1", &output)
			if err == nil || strings.Contains(err.Error(), "signature=secret") || output.Len() != 0 {
				t.Fatalf("err=%v output=%q", err, output.String())
			}
			want := 0
			if mode == "storage-failure" || mode == "storage-redirect" {
				want = 1
			}
			if storageCalls != want {
				t.Fatal(fmt.Sprintf("storage calls = %d, want %d", storageCalls, want))
			}
		})
	}
}
