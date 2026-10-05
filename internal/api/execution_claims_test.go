package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecutionClaimContract(t *testing.T) {
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer authentication")
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"requests":[{"event_type":"comment_created","comment_id":"c1","author_name":"Paul","author_is_bot":false,"accepted_models":[{"id":"m","efforts":["high"]}]}],"next_after":"c1"}}`))
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 6 || body["instance_id"] != "socket" || body["comment_id"] != "c1" {
				t.Errorf("claim body = %+v", body)
			}
			_, _ = w.Write([]byte(`{"data":{"board_id":"board","card_id":"card","comment_id":"c1","instance_id":"socket","effective":{"model":"m","effort":null},"status":"claimed","receipt":null}}`))
		case http.MethodPut:
			_, _ = w.Write([]byte(`{"data":{"board_id":"board","card_id":"card","comment_id":"c1","instance_id":"socket","effective":{"model":"m","effort":null},"status":"completed","receipt":{"status":"completed","message":"done"}}}`))
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	page, err := client.GetExecutionRequests(context.Background(), "board", "c0")
	if err != nil || len(page.Requests) != 1 || page.NextAfter == nil || *page.NextAfter != "c1" || !strings.Contains(string(page.Requests[0]), `"author_name":"Paul"`) {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	model := "m"
	claim, err := client.ClaimExecution(context.Background(), ExecutionClaimRequest{Version: 1, BoardID: "board", CardID: "card", CommentID: "c1", InstanceID: "socket", Effective: ExecutionSelection{Model: &model}})
	if err != nil || claim.Status != "claimed" || claim.Effective.Model == nil || *claim.Effective.Model != model {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	receipt, err := client.PutExecutionReceipt(context.Background(), "c1", ExecutionReceiptRequest{Version: 1, BoardID: "board", CardID: "card", InstanceID: "socket", Status: "completed", Message: "done"})
	if err != nil || receipt.Status != "completed" || receipt.Receipt == nil || receipt.Receipt.Message != "done" {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if len(paths) != 3 || paths[0] != "/api/bots/execution-requests/?board_id=board&after=c0" || paths[1] != "/api/bots/execution-claims/" || paths[2] != "/api/bots/execution-claims/c1/receipt/" {
		t.Fatalf("paths=%v", paths)
	}
}

func TestExecutionClaimErrorsPreserveWebCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"CLAIM_UNCERTAIN","error":"another instance owns this claim"}`))
	}))
	defer server.Close()
	_, err := NewClient(server.URL, "token").ClaimExecution(context.Background(), ExecutionClaimRequest{Version: 1})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "CLAIM_UNCERTAIN" || apiErr.StatusCode != 409 {
		t.Fatalf("error=%v", err)
	}
}

func TestIdempotentOutcomeCommentSendsStableKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["content"] != "done" || body["client_request_id"] != "stable-key" {
			t.Errorf("body=%+v", body)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"comment-1"}}`))
	}))
	defer server.Close()
	if _, err := NewClient(server.URL, "token").AddCommentIdempotent(context.Background(), "card", "done", "stable-key"); err != nil {
		t.Fatal(err)
	}
}
