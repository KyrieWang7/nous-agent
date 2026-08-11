// Package acp invokes Agent Client Protocol subprocesses over newline-delimited
// JSON-RPC 2.0. It implements the stable ACP v1 handshake and prompt flow.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const protocolVersion = 1

type AgentConfig struct {
	Command                string
	Args                   []string
	Env                    map[string]string
	Description            string
	Model                  string
	AutoApprovePermissions bool
}

type Options struct {
	Agents   map[string]AgentConfig
	WorkRoot string
	Timeout  time.Duration
}

func Tool(opts Options) (tool.Definition, error) {
	if len(opts.Agents) == 0 {
		return tool.Definition{}, errors.New("acp: at least one agent is required")
	}
	names := make([]string, 0, len(opts.Agents))
	for name, cfg := range opts.Agents {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(cfg.Command) == "" || strings.TrimSpace(cfg.Description) == "" {
			return tool.Definition{}, fmt.Errorf("acp: agent %q requires command and description", name)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	var descriptions []string
	for _, name := range names {
		descriptions = append(descriptions, fmt.Sprintf("- %s: %s", name, opts.Agents[name].Description))
	}
	description := "Invoke an external ACP-compatible agent and return its final response. Available agents:\n" + strings.Join(descriptions, "\n")
	return tool.Definition{
		Name: "invoke_acp_agent", Group: "acp", Description: description,
		Parameters: json.RawMessage(`{"type":"object","properties":{"agent":{"type":"string"},"prompt":{"type":"string"}},"required":["agent","prompt"]}`),
		Metadata:   tool.Metadata{IsAgentState: true},
		Handler: func(ctx context.Context, call tool.Call) (*tool.Result, error) {
			var args struct {
				Agent  string `json:"agent"`
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return acpError(err), nil
			}
			cfg, ok := opts.Agents[args.Agent]
			if !ok {
				return acpError(fmt.Errorf("unknown ACP agent %q; available: %v", args.Agent, names)), nil
			}
			if strings.TrimSpace(args.Prompt) == "" {
				return acpError(errors.New("ACP prompt is required")), nil
			}
			workDir, err := workDirectory(opts.WorkRoot, ctx)
			if err != nil {
				return acpError(err), nil
			}
			timeout := opts.Timeout
			if timeout <= 0 {
				timeout = 15 * time.Minute
			}
			runCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			output, err := invoke(runCtx, cfg, workDir, args.Prompt)
			if err != nil {
				return acpError(fmt.Errorf("invoking ACP agent %q: %w", args.Agent, err)), nil
			}
			if strings.TrimSpace(output) == "" {
				output = "(no response)"
			}
			return &tool.Result{Content: output}, nil
		},
	}, nil
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type connection struct {
	stdin  io.WriteCloser
	mu     sync.Mutex
	nextID int
	waits  map[int]chan rpcMessage
	chunks strings.Builder
	auto   bool
	done   chan error
}

func invoke(ctx context.Context, cfg AgentConfig, workDir, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = workDir
	cmd.Env = os.Environ()
	for key, value := range cfg.Env {
		if strings.HasPrefix(value, "$") {
			value = os.Getenv(strings.TrimPrefix(value, "$"))
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitWriter{writer: &stderr, remaining: 8 << 10}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	conn := &connection{stdin: stdin, waits: make(map[int]chan rpcMessage), auto: cfg.AutoApprovePermissions, done: make(chan error, 1)}
	go conn.readLoop(stdout)

	if _, err := conn.request(ctx, "initialize", map[string]any{
		"protocolVersion":    protocolVersion,
		"clientCapabilities": map[string]any{},
		"clientInfo":         map[string]any{"name": "nous-agent-go", "title": "Nous Agent", "version": "0.1.0"},
	}); err != nil {
		terminate(cmd, stdin)
		return "", fmt.Errorf("initialize: %w: %s", err, stderr.String())
	}
	newSession := map[string]any{"cwd": workDir, "mcpServers": []any{}}
	if cfg.Model != "" {
		newSession["model"] = cfg.Model
	}
	result, err := conn.request(ctx, "session/new", newSession)
	if err != nil {
		terminate(cmd, stdin)
		return "", fmt.Errorf("session/new: %w", err)
	}
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &session); err != nil || session.SessionID == "" {
		terminate(cmd, stdin)
		return "", errors.New("session/new returned no sessionId")
	}
	_, err = conn.request(ctx, "session/prompt", map[string]any{
		"sessionId": session.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	})
	terminate(cmd, stdin)
	if err != nil {
		return "", fmt.Errorf("session/prompt: %w", err)
	}
	conn.mu.Lock()
	output := conn.chunks.String()
	conn.mu.Unlock()
	return output, nil
}

func (c *connection) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	wait := make(chan rpcMessage, 1)
	c.waits[id] = wait
	err := c.writeLocked(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-c.done:
		return nil, err
	case response := <-wait:
		if response.Error != nil {
			return nil, fmt.Errorf("RPC %d: %s", response.Error.Code, response.Error.Message)
		}
		return response.Result, nil
	}
}

func (c *connection) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.Method != "" {
			c.handleIncoming(message)
			continue
		}
		var id int
		if err := json.Unmarshal(message.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		wait := c.waits[id]
		delete(c.waits, id)
		c.mu.Unlock()
		if wait != nil {
			wait <- message
		}
	}
	c.done <- scanner.Err()
}

func (c *connection) handleIncoming(message rpcMessage) {
	if message.Method == "session/update" {
		var notification struct {
			Update struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(message.Params, &notification) == nil && notification.Update.SessionUpdate == "agent_message_chunk" && notification.Update.Content.Type == "text" {
			c.mu.Lock()
			c.chunks.WriteString(notification.Update.Content.Text)
			c.mu.Unlock()
		}
		return
	}
	if len(message.ID) == 0 {
		return
	}
	var result any
	if message.Method == "session/request_permission" {
		result = map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
		if c.auto {
			var params struct {
				Options []struct {
					ID   string `json:"optionId"`
					Kind string `json:"kind"`
				} `json:"options"`
			}
			_ = json.Unmarshal(message.Params, &params)
			for _, option := range params.Options {
				if option.Kind == "allow_once" || option.Kind == "allow_always" {
					result = map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option.ID}}
					break
				}
			}
		}
		c.mu.Lock()
		_ = c.writeLocked(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "result": result})
		c.mu.Unlock()
		return
	}
	c.mu.Lock()
	_ = c.writeLocked(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "error": map[string]any{"code": -32601, "message": "Method not found"}})
	c.mu.Unlock()
}

func (c *connection) writeLocked(message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

func workDirectory(root string, ctx context.Context) (string, error) {
	if root == "" {
		root = filepath.Join(os.TempDir(), "nous-agent-acp")
	}
	threadID := "global"
	if run, ok := runtime.RunContextFrom(ctx); ok && strings.TrimSpace(run.ThreadID) != "" {
		threadID = sanitize(run.ThreadID)
	}
	path := filepath.Join(root, threadID)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

func sanitize(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
}

func terminate(cmd *exec.Cmd, stdin io.Closer) {
	_ = stdin.Close()
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func acpError(err error) *tool.Result {
	return &tool.Result{Content: "Error: " + err.Error(), IsError: true}
}

type limitWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitWriter) Write(data []byte) (int, error) {
	original := len(data)
	if w.remaining <= 0 {
		return original, nil
	}
	if int64(len(data)) > w.remaining {
		data = data[:w.remaining]
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return original, err
}
