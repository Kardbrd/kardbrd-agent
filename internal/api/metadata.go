package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// CardMetadata retains JSON numbers without converting them to floating point.
type CardMetadata struct {
	ID               string                     `json:"id"`
	Metadata         map[string]json.RawMessage `json:"metadata"`
	MetadataRevision int64                      `json:"metadata_revision"`
}

type MetadataPatch struct {
	Set              map[string]json.RawMessage `json:"set,omitempty"`
	Remove           []string                   `json:"remove,omitempty"`
	ExpectedRevision int64                      `json:"expected_revision"`
}

func (c *Client) GetCardMetadata(ctx context.Context, cardID string) (CardMetadata, error) {
	var result CardMetadata
	err := c.Request(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/metadata/", nil, &result)
	if err == nil && (result.Metadata == nil || result.MetadataRevision < 0) {
		err = fmt.Errorf("server returned an invalid card metadata response")
	}
	return result, err
}

func (c *Client) UpdateCardMetadata(ctx context.Context, cardID string, patch MetadataPatch) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/metadata/", patch)
}
