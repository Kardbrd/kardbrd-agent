package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRESTBotWordLimitError(t *testing.T) {
	err := parseAPIError(400, []byte(`{"error":"Validation failed","code":"VALIDATION_ERROR","errors":{"content":[{"message":"Bot comments are limited to 250 words (received 251). Shorten and retry.","code":"max_words"}]}}`))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "VALIDATION_ERROR" || apiErr.FieldCode != "max_words" || apiErr.Message != "Bot comments are limited to 250 words (received 251). Shorten and retry." {
		t.Fatalf("lost actionable field error: %#v", err)
	}
}

func TestREDCommentPostDoesNotRetryUncertainWrite(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewClient(server.URL, "tok")
	if _, err := client.AddCommentOnce(context.Background(), "card1", "decision retained"); err == nil {
		t.Fatal("expected publish error")
	}
	if posts != 1 {
		t.Fatalf("non-idempotent POST retried %d times", posts)
	}
}
