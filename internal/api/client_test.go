package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestUnwrapsDataAndSendsAuth(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		assertEqual(t, "GET", r.Method)
		assertEqual(t, "/api/boards/", r.URL.Path)
		writeJSON(t, w, map[string]any{"data": []map[string]string{{"id": "board1"}}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	raw, err := client.RequestRaw(context.Background(), "GET", "/api/boards/", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "Bearer tok", authHeader)

	var boards []map[string]string
	if err := json.Unmarshal(raw, &boards); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "board1", boards[0]["id"])
}

func TestRequestMarkdownSetsAcceptHeader(t *testing.T) {
	var accept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept = r.Header.Get("Accept")
		assertEqual(t, "/api/cards/card1/", r.URL.Path)
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Card\n"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	markdown, err := client.RequestMarkdown(context.Background(), "/api/cards/card1/")
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "text/markdown", accept)
	assertEqual(t, "# Card\n", markdown)
}

func TestRequestReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(t, w, map[string]any{"error": "bad token", "code": "auth"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	_, err := client.RequestRaw(context.Background(), "GET", "/api/boards/", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	assertEqual(t, "bad token", apiErr.Message)
	assertEqual(t, "auth", apiErr.Code)
	assertEqual(t, http.StatusUnauthorized, apiErr.StatusCode)
}

func TestRequestRetriesServerErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("try again"))
			return
		}
		writeJSON(t, w, map[string]any{"data": map[string]string{"ok": "yes"}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	raw, err := client.RequestRaw(context.Background(), "GET", "/api/boards/", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 3, attempts)

	var result map[string]string
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, "yes", result["ok"])
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func assertEqual[T comparable](t *testing.T, want T, got T) {
	t.Helper()
	if got != want {
		t.Fatalf("want %#v, got %#v", want, got)
	}
}
