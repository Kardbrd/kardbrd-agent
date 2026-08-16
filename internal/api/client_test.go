package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCardLabelEndpointsEscapeIDs(t *testing.T) {
	var paths []string
	var addBody struct {
		LabelID string `json:"label_id"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		switch r.Method {
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&addBody); err != nil {
				t.Fatal(err)
			}
		case http.MethodDelete:
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
		writeJSON(t, w, map[string]any{"data": map[string]any{}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	if err := client.AddCardLabel(context.Background(), "card/one", "label/two"); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveCardLabel(context.Background(), "card/one", "label/two"); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "label/two", addBody.LabelID)
	if want := []string{"/api/cards/card%2Fone/labels/", "/api/cards/card%2Fone/labels/label%2Ftwo/"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %#v, want %#v", paths, want)
	}
}

func TestGetBoardLabelsExtractsCatalogFromBoardDetail(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assertEqual(t, http.MethodGet, r.Method)
		assertEqual(t, "/api/boards/board1/", r.URL.Path)
		writeJSON(t, w, map[string]any{"data": map[string]any{
			"id":     "board1",
			"labels": []map[string]string{{"id": "label1", "name": "One", "color": "blue"}},
		}})
	}))
	defer server.Close()

	raw, err := NewClient(server.URL, "tok").GetBoardLabels(context.Background(), "board1")
	if err != nil {
		t.Fatal(err)
	}
	var labels []Label
	if err := json.Unmarshal(raw, &labels); err != nil {
		t.Fatal(err)
	}
	assertEqual(t, 1, requests)
	if !reflect.DeepEqual(labels, []Label{{ID: "label1", Name: "One", Color: "blue"}}) {
		t.Fatalf("labels = %#v", labels)
	}
}

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

func TestRequestErrorsRemainControlled(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantCode   string
		wantMsg    string
		forbidText string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"error":"bad token","code":"auth"}`, wantCode: "auth", wantMsg: "bad token"},
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"forbidden","code":"permission"}`, wantCode: "permission", wantMsg: "forbidden"},
		{name: "not found", status: http.StatusNotFound, body: `{"error":"missing","code":"not_found"}`, wantCode: "not_found", wantMsg: "missing"},
		{name: "html not found", status: http.StatusNotFound, body: "<html><body>missing</body></html>", wantMsg: "API returned an HTML error response", forbidText: "<html>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := NewClient(server.URL, "tok").RequestRaw(context.Background(), http.MethodGet, "/api/boards/board1/", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %v", err)
			}
			assertEqual(t, tt.status, apiErr.StatusCode)
			assertEqual(t, tt.wantCode, apiErr.Code)
			assertEqual(t, tt.wantMsg, apiErr.Message)
			if tt.forbidText != "" && strings.Contains(apiErr.Error(), tt.forbidText) {
				t.Fatalf("error leaked response body: %q", apiErr.Error())
			}
		})
	}
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

func TestNoRetryMakesCommittedJSONWriteOneAttempt(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		// Reading the body models a server that committed the mutation before
		// reporting its failure.
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"committed but failed"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	client.SetNoRetry(true)
	_, err := client.RequestRaw(context.Background(), http.MethodPost, "/api/cards/", map[string]string{"title": "one"})
	if err == nil {
		t.Fatal("expected committed write failure")
	}
	assertEqual(t, 1, attempts)
}

func TestNoRetryDoesNotFollowJSONRedirect(t *testing.T) {
	redirectedRequests := 0
	var redirectedAuthorization string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests++
		redirectedAuthorization = r.Header.Get("Authorization")
		writeJSON(t, w, map[string]any{"data": map[string]string{"unexpected": "redirect"}})
	}))
	defer target.Close()

	originalRequests := 0
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalRequests++
		w.Header().Set("Location", target.URL+"/redirected")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	client := NewClient(origin.URL, "tok")
	client.SetNoRetry(true)
	_, err := client.RequestRaw(context.Background(), http.MethodPost, "/api/cards/", map[string]string{"title": "one"})
	if err == nil {
		t.Fatal("expected redirect failure")
	}
	assertEqual(t, 1, originalRequests)
	assertEqual(t, 0, redirectedRequests)
	assertEqual(t, "", redirectedAuthorization)
}

func TestNoRetryMakesMarkdownReadOneAttempt(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "tok")
	client.SetNoRetry(true)
	_, err := client.RequestMarkdown(context.Background(), "/api/cards/card1/")
	if err == nil {
		t.Fatal("expected markdown request failure")
	}
	assertEqual(t, 1, attempts)
}

func TestAddCommentOnceDoesNotRetryServerErrors(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		assertEqual(t, http.MethodPost, r.Method)
		assertEqual(t, "/api/cards/card1/comments/", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("try again"))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "tok").AddCommentOnce(context.Background(), "card1", "terminal summary")
	if err == nil {
		t.Fatal("expected comment publication error")
	}
	assertEqual(t, 1, attempts)
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
