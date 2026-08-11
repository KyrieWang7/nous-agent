// Package community contains optional network tools configured by the
// production entrypoint. They remain outside builtin because deployments must
// opt in and provide provider credentials.
package community

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const maxResponseBytes = 4 << 20

type Options struct {
	Name       string
	APIKey     string
	MaxResults int
	Timeout    time.Duration
	Client     *http.Client
}

func Definition(opts Options) (tool.Definition, error) {
	switch opts.Name {
	case "web_search":
		return tavilySearch(opts, false), nil
	case "image_search":
		return tavilySearch(opts, true), nil
	case "web_fetch":
		return jinaFetch(opts), nil
	default:
		return tool.Definition{}, fmt.Errorf("community: unsupported tool %q", opts.Name)
	}
}

func tavilySearch(opts Options, images bool) tool.Definition {
	name := "web_search"
	description := "Search the web and return normalized titles, URLs, and snippets."
	if images {
		name = "image_search"
		description = "Search online images and return reference image URLs."
	}
	return tool.Definition{
		Name: name, Group: "web", Description: description,
		Parameters: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"max_results":{"type":"integer","minimum":1,"maximum":20}},"required":["query"]}`),
		Metadata:   tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errorResult(err), nil
			}
			if strings.TrimSpace(args.Query) == "" {
				return errorResult(fmt.Errorf("%s: query is required", name)), nil
			}
			if strings.TrimSpace(opts.APIKey) == "" {
				return errorResult(fmt.Errorf("%s: Tavily API key is not configured", name)), nil
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = opts.MaxResults
			}
			if limit <= 0 {
				limit = 5
			}
			if limit > 20 {
				limit = 20
			}
			payload := map[string]any{"api_key": opts.APIKey, "query": args.Query, "max_results": limit}
			if images {
				payload["include_images"] = true
				payload["max_results"] = 1
			}
			raw, err := postJSON(ctx, opts, "https://api.tavily.com/search", payload)
			if err != nil {
				return errorResult(err), nil
			}
			var response struct {
				Results []struct {
					Title   string `json:"title"`
					URL     string `json:"url"`
					Content string `json:"content"`
				} `json:"results"`
				Images []json.RawMessage `json:"images"`
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				return errorResult(fmt.Errorf("%s: decoding provider response: %w", name, err)), nil
			}
			var output any
			if images {
				normalized := make([]map[string]string, 0, len(response.Images))
				for _, item := range response.Images {
					var imageURL string
					if err := json.Unmarshal(item, &imageURL); err != nil {
						var object struct {
							URL string `json:"url"`
						}
						_ = json.Unmarshal(item, &object)
						imageURL = object.URL
					}
					if imageURL != "" {
						normalized = append(normalized, map[string]string{"image_url": imageURL, "thumbnail_url": imageURL})
					}
					if len(normalized) >= limit {
						break
					}
				}
				output = map[string]any{"query": args.Query, "total_results": len(normalized), "results": normalized}
			} else {
				output = response.Results
			}
			encoded, err := json.MarshalIndent(output, "", "  ")
			if err != nil {
				return nil, err
			}
			return &tool.Result{Content: string(encoded)}, nil
		},
	}
}

func jinaFetch(opts Options) tool.Definition {
	return tool.Definition{
		Name: "web_fetch", Group: "web",
		Description: "Fetch the readable contents of an exact public HTTP or HTTPS URL.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		Metadata:    tool.Metadata{IsReadOnly: true, IsConcurrencySafe: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return errorResult(err), nil
			}
			parsed, err := url.Parse(strings.TrimSpace(args.URL))
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
				return errorResult(fmt.Errorf("web_fetch: url must be an absolute public HTTP(S) URL without credentials")), nil
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://r.jina.ai/"+parsed.String(), nil)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Accept", "text/markdown")
			if opts.APIKey != "" {
				request.Header.Set("Authorization", "Bearer "+opts.APIKey)
			}
			response, err := httpClient(opts).Do(request)
			if err != nil {
				return errorResult(fmt.Errorf("web_fetch: %w", err)), nil
			}
			defer func() { _ = response.Body.Close() }()
			raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
			if err != nil {
				return nil, err
			}
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return errorResult(fmt.Errorf("web_fetch: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))), nil
			}
			if len(raw) > maxResponseBytes {
				raw = raw[:maxResponseBytes]
			}
			return &tool.Result{Content: string(raw)}, nil
		},
	}
}

func postJSON(ctx context.Context, opts Options, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpClient(opts).Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("community: provider HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(raw) > maxResponseBytes {
		return nil, errorsNewResponseTooLarge()
	}
	return raw, nil
}

func httpClient(opts Options) *http.Client {
	if opts.Client != nil {
		return opts.Client
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func errorResult(err error) *tool.Result {
	return &tool.Result{Content: "Error: " + err.Error(), IsError: true}
}

func errorsNewResponseTooLarge() error {
	return fmt.Errorf("community: provider response exceeds %d bytes", maxResponseBytes)
}
