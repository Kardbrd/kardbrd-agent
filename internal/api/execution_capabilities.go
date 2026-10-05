package api

import (
	"context"
	"net/url"
	"time"
)

type ExecutionModel struct {
	ID      string   `json:"id"`
	Efforts []string `json:"efforts"`
}

type ExecutionDefaults struct {
	Model  *string `json:"model"`
	Effort *string `json:"effort"`
}

type ExecutionProvenance struct {
	Source            string `json:"source"`
	ConfigFingerprint string `json:"config_fingerprint"`
	VerifiedAt        string `json:"verified_at"`
}

type ExecutionCapabilities struct {
	Version    int                 `json:"version"`
	InstanceID string              `json:"instance_id"`
	Executor   string              `json:"executor"`
	BoardID    string              `json:"board_id"`
	Models     []ExecutionModel    `json:"models"`
	Defaults   ExecutionDefaults   `json:"defaults"`
	Provenance ExecutionProvenance `json:"provenance"`
}

type ExecutionCapabilityRegistration struct {
	Revision    string    `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (c *Client) RegisterExecutionCapabilities(ctx context.Context, payload ExecutionCapabilities) (ExecutionCapabilityRegistration, error) {
	var result ExecutionCapabilityRegistration
	err := c.Request(ctx, "PUT", "/api/bots/execution-capabilities/", payload, &result)
	return result, err
}

type ExecutionCapabilityView struct {
	ExecutionCapabilities
	Revision    string    `json:"revision"`
	PublishedAt time.Time `json:"published_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (c *Client) GetExecutionCapabilities(ctx context.Context, botID, boardID string) (ExecutionCapabilityView, error) {
	var result ExecutionCapabilityView
	path := "/api/bots/" + url.PathEscape(botID) + "/execution-capabilities/?board_id=" + url.QueryEscape(boardID)
	err := c.Request(ctx, "GET", path, nil, &result)
	return result, err
}
