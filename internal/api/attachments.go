package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (c *Client) UploadAttachment(ctx context.Context, cardID, filePath string) (json.RawMessage, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	filename := filepath.Base(filePath)
	contentType := detectContentType(filePath)

	presignRaw, err := c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/attachments/presign/", map[string]any{
		"filename":     filename,
		"content_type": contentType,
		"file_size":    info.Size(),
	})
	if err != nil {
		return nil, err
	}
	var presign struct {
		UploadURL string `json:"upload_url"`
		S3Key     string `json:"s3_key"`
	}
	if err := json.Unmarshal(presignRaw, &presign); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	if err := uploadToPresignedURL(ctx, presign.UploadURL, content, contentType); err != nil {
		return nil, err
	}

	return c.RequestRaw(ctx, "POST", "/api/cards/"+url.PathEscape(cardID)+"/attachments/confirm/", map[string]any{
		"s3_key":       presign.S3Key,
		"filename":     filename,
		"file_size":    info.Size(),
		"content_type": contentType,
	})
}

func (c *Client) UploadMarkdownContent(ctx context.Context, cardID, filename, content string) (json.RawMessage, error) {
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}
	return c.UploadFileContent(ctx, cardID, filename, []byte(content), "text/markdown")
}

func (c *Client) UploadFileContent(ctx context.Context, cardID, filename string, content []byte, contentType string) (json.RawMessage, error) {
	tempDir, err := os.MkdirTemp("", "kardbrd-upload-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, filename)
	if err := os.WriteFile(filePath, content, 0o600); err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = detectContentType(filePath)
	}
	return c.UploadAttachment(ctx, cardID, filePath)
}

func (c *Client) ListAttachments(ctx context.Context, cardID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/attachments/", nil)
}

func (c *Client) GetAttachment(ctx context.Context, cardID, attachmentID string) (json.RawMessage, error) {
	return c.RequestRaw(ctx, "GET", "/api/cards/"+url.PathEscape(cardID)+"/attachments/"+url.PathEscape(attachmentID)+"/", nil)
}

func uploadToPresignedURL(ctx context.Context, uploadURL string, content []byte, contentType string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(content))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode < 500 {
			if resp.StatusCode >= 400 {
				return &APIError{Message: fmt.Sprintf("Upload failed with HTTP %d", resp.StatusCode), StatusCode: resp.StatusCode}
			}
			return nil
		}
		lastErr = &APIError{Message: fmt.Sprintf("Upload failed with HTTP %d", resp.StatusCode), StatusCode: resp.StatusCode}
	}
	return lastErr
}

func detectContentType(filePath string) string {
	if fromExt := mime.TypeByExtension(filepath.Ext(filePath)); fromExt != "" {
		return fromExt
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "application/octet-stream"
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return "application/octet-stream"
	}
	return http.DetectContentType(buf[:n])
}
