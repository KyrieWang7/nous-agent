package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func TestCompactionReservesModelOutputInProductionAssembly(t *testing.T) {
	var summaries atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if !req.Stream {
			summaries.Add(1)
			fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"previous context summary"}}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer provider.Close()
	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "test", Provider: "openai-compatible", Model: "test", BaseURL: provider.URL, ContextLength: 10000, MaxTokens: 6000}}
	cfg.DefaultModel = "test"
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = false
	cfg.Title.Enabled = false
	cfg.Summarization.KeepMessages = 1
	built, err := buildAgent(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "budget-run", ThreadID: "budget-thread"})
	result, err := built.agent.Run(ctx, runmanager.AgentRequest{
		RunID: "budget-run", ThreadID: "budget-thread", Prompt: "latest question",
		History: []message.Message{{Role: message.RoleUser, Content: strings.Repeat("x", 14000)}, {Role: message.RoleAssistant, Content: "old answer"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Compacted || summaries.Load() != 1 {
		t.Fatalf("compacted=%v, summary calls=%d; 3500 input tokens should exceed 75%% of the 4000-token message budget", result.Compacted, summaries.Load())
	}
}

func TestCompactionRejectsExhaustedMessageBudget(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "test", Provider: "openai-compatible", Model: "test", ContextLength: 10000, MaxTokens: 10000}}
	cfg.DefaultModel = "test"
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = false
	built, err := buildAgent(cfg, nil, nil)
	if err == nil {
		built.Close()
		t.Fatal("accepted output reservation that leaves no message budget")
	}
	if !strings.Contains(err.Error(), "message budget") {
		t.Fatalf("unexpected error: %v", err)
	}
}
