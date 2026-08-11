package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

func TestInvokeACPAgentProtocolFlow(t *testing.T) {
	definition, err := Tool(Options{
		WorkRoot: t.TempDir(),
		Agents: map[string]AgentConfig{
			"helper": {
				Command: os.Args[0], Args: []string{"-test.run=TestACPHelperProcess"},
				Env: map[string]string{"GO_WANT_ACP_HELPER": "1"}, Description: "test ACP agent",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := definition.Handler(context.Background(), tool.Call{Args: []byte(`{"agent":"helper","prompt":"inspect repository"}`)})
	if err != nil || result.IsError || result.Content != "completed: inspect repository" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestACPWorkDirectoryIsThreadScoped(t *testing.T) {
	root := t.TempDir()
	path, err := workDirectory(root, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "global") {
		t.Fatalf("path = %q", path)
	}
}

func TestACPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ACP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		var result any = map[string]any{}
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}
		case "session/new":
			result = map[string]any{"sessionId": "session-1"}
		case "session/prompt":
			prompt := request.Params["prompt"].([]any)[0].(map[string]any)["text"].(string)
			notification := map[string]any{
				"jsonrpc": "2.0", "method": "session/update",
				"params": map[string]any{"sessionId": "session-1", "update": map[string]any{
					"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "completed: " + prompt},
				}},
			}
			writeHelperMessage(notification)
			result = map[string]any{"stopReason": "end_turn"}
		default:
			writeHelperMessage(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32601, "message": "unknown"}})
			continue
		}
		writeHelperMessage(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
	os.Exit(0)
}

func writeHelperMessage(value any) {
	raw, _ := json.Marshal(value)
	_, _ = fmt.Fprintln(os.Stdout, string(raw))
}
