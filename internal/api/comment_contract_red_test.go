package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
