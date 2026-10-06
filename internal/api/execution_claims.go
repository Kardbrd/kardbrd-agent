package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// ExecutionRequestPage preserves canonical event JSON for the existing event decoder.
type ExecutionRequestPage struct {
	Requests  []json.RawMessage `json:"requests"`
	NextAfter *string           `json:"next_after"`
}

func (c *Client) GetExecutionRequests(ctx context.Context, boardID, after string) (ExecutionRequestPage, error) {
	var page ExecutionRequestPage
	path := "/api/bots/execution-requests/?board_id=" + url.QueryEscape(boardID)
	if after != "" {
		path += "&after=" + url.QueryEscape(after)
	}
	err := c.Request(ctx, "GET", path, nil, &page)
	return page, err
}

type ExecutionSelection struct {
	Model  *string `json:"model"`
	Effort *string `json:"effort"`
}

type ExecutionClaimRequest struct {
	Version    int                `json:"version"`
	BoardID    string             `json:"board_id"`
	CardID     string             `json:"card_id"`
	CommentID  string             `json:"comment_id"`
	InstanceID string             `json:"instance_id"`
	Effective  ExecutionSelection `json:"effective"`
}

type ExecutionReceipt struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type ExecutionClaim struct {
	BoardID    string             `json:"board_id"`
	CardID     string             `json:"card_id"`
	CommentID  string             `json:"comment_id"`
	InstanceID string             `json:"instance_id"`
	Effective  ExecutionSelection `json:"effective"`
	Status     string             `json:"status"`
	Receipt    *ExecutionReceipt  `json:"receipt"`
}

func (c *Client) ClaimExecution(ctx context.Context, request ExecutionClaimRequest) (ExecutionClaim, error) {
	var claim ExecutionClaim
	err := c.Request(ctx, "POST", "/api/bots/execution-claims/", request, &claim)
	return claim, err
}

type ExecutionReceiptRequest struct {
	Version    int    `json:"version"`
	BoardID    string `json:"board_id"`
	CardID     string `json:"card_id"`
	InstanceID string `json:"instance_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

func (c *Client) PutExecutionReceipt(ctx context.Context, commentID string, request ExecutionReceiptRequest) (ExecutionClaim, error) {
	var claim ExecutionClaim
	path := "/api/bots/execution-claims/" + url.PathEscape(commentID) + "/receipt/"
	err := c.Request(ctx, "PUT", path, request, &claim)
	return claim, err
}
