// Package deepseek implements the Messages-only DeepSeek API used by upstream
// harness 0.2. Existing OpenAI-compatible configurations remain independent.
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/internal/streamhttp"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const Name = "deepseek"
const filesBeta = "files-api-2025-04-14"

type Client struct {
	http               *http.Client
	root, key, modelID string
	info               model.Info
	temperature        *float64
	idle               time.Duration
	useFiles           bool
	mu                 sync.Mutex
	files              map[string]File
}

func New(cfg model.ProviderConfig) (model.Model, error) {
	if len(cfg.ExtraBody) > 0 || len(cfg.ThinkingExtraBody) > 0 {
		return nil, errors.New("deepseek: use Messages options instead of OpenAI extra_body")
	}
	if cfg.Model == "" {
		return nil, errors.New("deepseek: model is required")
	}
	base := strings.TrimRight(cfg.BaseURL, "/")
	if base == "" {
		base = "https://api.deepseek.com/anthropic"
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("deepseek: base_url must be an HTTP(S) root without credentials, query or fragment")
	}
	if !strings.HasSuffix(u.Path, "/v1") {
		base += "/v1"
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	idle := time.Duration(cfg.StreamIdleTimeout) * time.Second
	if idle <= 0 {
		idle = 300 * time.Second
	}
	window, output := cfg.ContextLength, cfg.MaxTokens
	if window <= 0 {
		window = 1000000
	}
	if output <= 0 {
		output = 256000
	}
	return &Client{http: &http.Client{Timeout: timeout, Transport: http.DefaultTransport.(*http.Transport).Clone(), CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("deepseek: redirects are disabled") }}, root: base, key: cfg.APIKey, modelID: cfg.Model, temperature: cfg.Temperature, idle: idle, useFiles: cfg.UseFiles, files: map[string]File{}, info: model.Info{Name: cfg.Name, ContextLength: window, MaxOutputTokens: output, SupportsThinking: cfg.SupportsThinking, SupportsReasoningEffort: cfg.SupportsReasoningEffort, SupportsVision: cfg.SupportsVision, SupportsTools: true}}, nil
}
func (c *Client) Info() model.Info { return c.info }
func (c *Client) Close() error     { c.http.CloseIdleConnections(); return nil }
func (c *Client) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	stream, err := c.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	return stream.Result()
}
func (c *Client) Stream(ctx context.Context, req model.Request) (model.StreamReader, error) {
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := c.serialize(ctx, req)
		if err != nil {
			return nil, err
		}
		request, err := c.request(ctx, http.MethodPost, "/messages", bytes.NewReader(raw), "application/json")
		if err != nil {
			return nil, err
		}
		resp, err := streamhttp.Do(c.http, request, c.idle)
		if err != nil {
			return nil, transportError(err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return newStream(resp.Body, c.modelID), nil
		}
		rawErr, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		if attempt == 0 && c.useFiles && staleFile(resp.StatusCode, rawErr) {
			c.mu.Lock()
			clear(c.files)
			c.mu.Unlock()
			continue
		}
		return nil, providerError(resp.StatusCode, rawErr)
	}
	return nil, errors.New("deepseek: file recovery exhausted")
}
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.root+path, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("anthropic-version", "2023-06-01")
	if c.key != "" {
		r.Header.Set("x-api-key", c.key)
	}
	if c.useFiles || strings.HasPrefix(path, "/files") {
		r.Header.Set("anthropic-beta", filesBeta)
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r, nil
}
func transportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", model.ErrProviderUnavailable, err)
}
func providerError(status int, raw []byte) error {
	var e struct {
		Error struct{ Type, Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	detail := strings.TrimSpace(e.Error.Type + " " + e.Error.Code + " " + e.Error.Message)
	lower := strings.ToLower(detail)
	kind := model.ErrProviderUnavailable
	switch {
	case status == 401 || status == 403 || e.Error.Type == "authentication_error" || e.Error.Type == "permission_error":
		kind = model.ErrAuthFailed
	case status == 429 || e.Error.Type == "rate_limit_error":
		kind = model.ErrRateLimited
	case strings.Contains(lower, "context") || strings.Contains(lower, "too many tokens") || strings.Contains(lower, "prompt is too long"):
		kind = model.ErrContextOverflow
	case status >= 400 && status < 500 || e.Error.Type == "invalid_request_error":
		kind = model.ErrInvalidRequest
	}
	return fmt.Errorf("%w: deepseek HTTP %d: %s", kind, status, detail)
}

func staleFile(status int, raw []byte) bool {
	if status != 400 && status != 404 {
		return false
	}
	detail := strings.ToLower(string(raw))
	if !strings.Contains(detail, "file") {
		return false
	}
	for _, hint := range []string{"not found", "not_found", "expired", "invalid file", "deleted", "does not exist", "not created under this account"} {
		if strings.Contains(detail, hint) {
			return true
		}
	}
	return false
}
