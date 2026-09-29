package deepseek

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"time"
)

const maxUploadBytes = 128 * 1024 * 1024

// File contains validated provider metadata. ExpiresAt is only known on upload.
type File struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"-"`
}

func (f File) validate() error {
	if f.ID == "" || f.Type != "file" || f.Filename == "" || f.MimeType == "" || f.SizeBytes < 0 || f.CreatedAt.IsZero() {
		return fmt.Errorf("deepseek: invalid Files metadata")
	}
	return nil
}
func (c *Client) filesRequest(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := c.request(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return providerError(resp.StatusCode, raw)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("deepseek: invalid Files response: %w", err)
	}
	return nil
}

// UploadFile uploads image bytes with a bounded provider-side expiry. The caller
// controls the filename; credentials never follow HTTP redirects.
func (c *Client) UploadFile(ctx context.Context, data []byte, media, filename string, expirySeconds int) (File, error) {
	if len(data) == 0 || len(data) > maxUploadBytes {
		return File{}, fmt.Errorf("%w: file must contain 1..128 MiB", model.ErrInvalidRequest)
	}
	if expirySeconds < 3600 || expirySeconds > 2592000 {
		return File{}, fmt.Errorf("%w: file expiry must be 3600..2592000 seconds", model.ErrInvalidRequest)
	}
	if media != "image/png" && media != "image/jpeg" && media != "image/webp" && media != "image/gif" {
		return File{}, fmt.Errorf("%w: unsupported image type", model.ErrInvalidRequest)
	}
	var b bytes.Buffer
	form := multipart.NewWriter(&b)
	if err := form.WriteField("expires_after[anchor]", "created_at"); err != nil {
		return File{}, err
	}
	if err := form.WriteField("expires_after[seconds]", strconv.Itoa(expirySeconds)); err != nil {
		return File{}, err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	h.Set("Content-Type", media)
	part, err := form.CreatePart(h)
	if err != nil {
		return File{}, err
	}
	if _, err = part.Write(data); err != nil {
		return File{}, err
	}
	if err = form.Close(); err != nil {
		return File{}, err
	}
	var f File
	if err = c.filesRequest(ctx, http.MethodPost, "/files", &b, form.FormDataContentType(), &f); err != nil {
		return File{}, err
	}
	if err = f.validate(); err != nil {
		return File{}, err
	}
	f.ExpiresAt = f.CreatedAt.Add(time.Duration(expirySeconds) * time.Second)
	return f, nil
}

type FilePage struct {
	Data    []File `json:"data"`
	FirstID string `json:"first_id"`
	LastID  string `json:"last_id"`
	HasMore bool   `json:"has_more"`
}

func (c *Client) ListFiles(ctx context.Context, after string, limit int) (FilePage, error) {
	q := url.Values{}
	if after != "" {
		q.Set("after_id", after)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var p FilePage
	err := c.filesRequest(ctx, http.MethodGet, "/files?"+q.Encode(), nil, "", &p)
	if err != nil {
		return p, err
	}
	if p.Data == nil {
		return p, fmt.Errorf("deepseek: invalid Files page")
	}
	for _, f := range p.Data {
		if err = f.validate(); err != nil {
			return p, err
		}
	}
	return p, nil
}
func (c *Client) RetrieveFile(ctx context.Context, id string) (File, error) {
	var f File
	err := c.filesRequest(ctx, http.MethodGet, "/files/"+url.PathEscape(id), nil, "", &f)
	if err != nil {
		return f, err
	}
	if err = f.validate(); err != nil {
		return f, err
	}
	if f.ID != id {
		return f, fmt.Errorf("deepseek: mismatched file identity")
	}
	return f, nil
}
func (c *Client) DeleteFile(ctx context.Context, id string) error {
	var result struct{ ID, Type string }
	if err := c.filesRequest(ctx, http.MethodDelete, "/files/"+url.PathEscape(id), nil, "", &result); err != nil {
		return err
	}
	if result.ID != id || result.Type != "file_deleted" {
		return fmt.Errorf("deepseek: invalid file deletion response")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.files {
		if v.ID == id {
			delete(c.files, k)
		}
	}
	return nil
}
func (c *Client) imageFile(ctx context.Context, data []byte, media string) (string, error) {
	key := fmt.Sprintf("%x:%s", sha256.Sum256(data), media)
	c.mu.Lock()
	f, ok := c.files[key]
	c.mu.Unlock()
	if ok && time.Until(f.ExpiresAt) > time.Hour {
		return f.ID, nil
	}
	// The canonical transcript retains source bytes; provider ids only live in
	// this generation's bounded, credential-scoped cache and may be reuploaded.
	f, err := c.UploadFile(ctx, data, media, fmt.Sprintf("nous-%x", sha256.Sum256(data)), 7*24*60*60)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	if len(c.files) >= 256 {
		for k := range c.files {
			delete(c.files, k)
			break
		}
	}
	c.files[key] = f
	c.mu.Unlock()
	return f.ID, nil
}
