// Package mcp adapts external Model Context Protocol tools to tool.Definition.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type Transport interface {
	Call(context.Context, Request) (Response, error)
	Close() error
}
type Client struct {
	transport Transport
	mu        sync.Mutex
	next      int64
	prefix    string
}

func New(transport Transport, prefix string) *Client {
	return &Client{transport: transport, prefix: prefix}
}
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.transport == nil {
		return nil, errors.New("mcp: transport is nil")
	}
	c.mu.Lock()
	c.next++
	id := c.next
	c.mu.Unlock()
	resp, err := c.transport.Call(ctx, Request{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("mcp: %s (%d)", resp.Error.Message, resp.Error.Code)
	}
	return resp.Result, nil
}

type remoteTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func (c *Client) ListTools(ctx context.Context) ([]tool.Definition, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result struct {
		Tools []remoteTool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	defs := make([]tool.Definition, 0, len(result.Tools))
	for _, rt := range result.Tools {
		name := rt.Name
		if c.prefix != "" {
			name = c.prefix + "_" + name
		}
		defs = append(defs, tool.Definition{Name: name, Group: "mcp", Description: rt.Description, Parameters: rt.InputSchema, Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args any
			if len(call.Args) > 0 {
				if err := json.Unmarshal(call.Args, &args); err != nil {
					return nil, err
				}
			}
			raw, err := c.call(ctx, "tools/call", map[string]any{"name": rt.Name, "arguments": args})
			if err != nil {
				return &tool.Result{Content: err.Error(), IsError: true}, nil //nolint:nilerr // remote tool errors are model-visible results
			}
			var result struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return nil, err
			}
			var parts []string
			for _, p := range result.Content {
				if p.Type == "text" {
					parts = append(parts, p.Text)
				}
			}
			return &tool.Result{Content: strings.Join(parts, "\n"), IsError: result.IsError}, nil
		}})
	}
	return defs, nil
}
func (c *Client) RegisterAvailable(ctx context.Context, registry *tool.Registry, logger *slog.Logger) error {
	defs, err := c.ListTools(ctx)
	if err != nil {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("MCP server unavailable; tools skipped", "error", err)
		return nil
	}
	return registry.RegisterAll(defs...)
}
func (c *Client) Close() error {
	if c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

type HTTPTransport struct {
	URL     string
	Client  *http.Client
	Headers map[string]string
}

func (h *HTTPTransport) Call(ctx context.Context, req Request) (Response, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range h.Headers {
		r.Header.Set(name, value)
	}
	resp, err := client.Do(r)
	if err != nil {
		return Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return Response{}, fmt.Errorf("mcp: HTTP %d: %s", resp.StatusCode, detail)
	}
	var raw []byte
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if data, ok := strings.CutPrefix(line, "data:"); ok {
				raw = []byte(strings.TrimSpace(data))
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return Response{}, err
		}
	} else {
		raw, err = io.ReadAll(resp.Body)
		if err != nil {
			return Response{}, err
		}
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Response{}, err
	}
	return out, nil
}
func (h *HTTPTransport) Close() error { return nil }

type StdioTransport struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	mu  sync.Mutex
}

func NewStdioTransport(ctx context.Context, command string, args ...string) (*StdioTransport, error) {
	return NewStdioTransportWithEnv(ctx, command, nil, args...)
}

func NewStdioTransportWithEnv(ctx context.Context, command string, environment map[string]string, args ...string) (*StdioTransport, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for name, value := range environment {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &StdioTransport{cmd: cmd, in: in, out: bufio.NewReader(out)}, nil
}
func (s *StdioTransport) Call(_ context.Context, req Request) (Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err = s.in.Write(append(raw, '\n')); err != nil {
		return Response{}, err
	}
	line, err := s.out.ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var resp Response
	err = json.Unmarshal(line, &resp)
	return resp, err
}
func (s *StdioTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.in.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return s.cmd.Wait()
}
