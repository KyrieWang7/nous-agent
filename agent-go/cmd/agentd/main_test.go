package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/transport/httpapi"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/harness"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	lifecyclehandlers "github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle/handlers"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/subagent"
)

func TestAgentHTTPStreamEndToEnd(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"call-1\",\"model\":\"stub\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"id\":\"call-1\",\"model\":\"stub\",\"choices\":[{\"finish_reason\":\"stop\",\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()

	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "stub", Provider: "openai-compatible", Model: "stub", BaseURL: provider.URL, ContextLength: 1000}}
	cfg.DefaultModel = "stub"
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = false
	cfg.Title.Enabled = false
	cfg.Summarization.Enabled = false

	built, err := buildAgent(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	wantAfterModel := []string{lifecyclehandlers.NameSafetyFinishReason, lifecyclehandlers.NameLoopDetection, lifecyclehandlers.NameSubagentLimit, "telemetry"}
	if got := built.chain.StageOrder(lifecycle.StageAfterModel); !slices.Equal(got, wantAfterModel) {
		t.Fatalf("after-model order = %v, want %v", got, wantAfterModel)
	}
	api, err := httpapi.New(httpapi.Options{Agent: built.agent, AllowedTools: built.tools})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	server := httptest.NewServer(api)
	defer server.Close()

	postJSON(t, server.URL+"/api/v1/threads", `{"thread_id":"e2e-thread"}`)
	resp, err := http.Post(server.URL+"/api/v1/threads/e2e-thread/runs", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"say hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var messageChunks []string
	var events []string
	scanner := bufio.NewScanner(resp.Body)
	var event string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			events = append(events, event)
		case event == "messages" && strings.HasPrefix(line, "data:"):
			var data []map[string]any
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data); err != nil {
				t.Fatal(err)
			}
			if len(data) > 0 {
				if content, ok := data[0]["content"].(string); ok {
					messageChunks = append(messageChunks, content)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(messageChunks, ""); got != "hello" {
		t.Fatalf("streamed content = %q, events = %v", got, events)
	}
	for _, required := range []string{"metadata", "messages", "values", "custom", "end"} {
		if !contains(events, required) {
			t.Fatalf("missing event %q in %v", required, events)
		}
	}
}

func TestChildModelStreamUsesOnlyTaskProgressChannel(t *testing.T) {
	t.Parallel()

	var published []runtime.Event
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID:          "parent-run:task-1",
		ParentRunID:    "parent-run",
		EventRunID:     "parent-run",
		ThreadID:       "parent-thread",
		SubagentTaskID: "task-1",
		Publish: func(_ context.Context, event runtime.Event) (int64, error) {
			published = append(published, event)
			return int64(len(published)), nil
		},
	})
	st := lifecycle.NewState(lifecycle.StateInit{
		RunID:    "parent-run:task-1",
		ThreadID: "child-state-thread",
	})
	st.Iteration = 2

	publishModelStreamEvent(ctx, st, model.StreamEvent{Type: model.StreamTextDelta, Delta: "hello"})
	if len(published) != 0 {
		t.Fatalf("child delta leaked into the lead stream: %#v", published)
	}

	st.ModelOutput = &model.Response{Message: message.Message{
		Role:    message.RoleAssistant,
		Content: "checking files",
		ToolCalls: []message.ToolCall{{
			ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`),
		}},
	}}
	subagentProgressPublisher{}.PublishReply(ctx, st, true)
	if len(published) != 1 {
		t.Fatalf("progress events = %d, want 1", len(published))
	}
	event := published[0]
	if event.Type != runtime.EventSubagentProgress || event.RunID != "parent-run" || event.ThreadID != "parent-thread" {
		t.Fatalf("event = %#v", event)
	}
	var payload runtime.SubagentProgress
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TaskID != "task-1" || payload.MessageID != "parent-run:task-1:2" || payload.MessageIndex != 3 {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Message.Content != "checking files" || len(payload.Message.ToolCalls) != 1 {
		t.Fatalf("message = %#v", payload.Message)
	}
}

func TestBuildAgentRegistersRuntimeCapabilitiesAndBuildsPromptPerRun(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "stub", Provider: "openai-compatible", Model: "stub", BaseURL: "http://127.0.0.1:1", ContextLength: 1000}}
	cfg.DefaultModel = "stub"
	cfg.Sandbox.Enabled = false
	cfg.Subagents.Enabled = false
	cfg.Swarm.Enabled = false
	cfg.Title.Enabled = false
	cfg.Summarization.Enabled = false

	built, err := buildAgent(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer built.Close()
	if !slices.Contains(built.tools, "task") {
		t.Fatalf("registered tools = %v; task must remain a server capability", built.tools)
	}
	agent, ok := built.agent.(harness.Agent)
	if !ok || agent.SystemPromptBuilder == nil {
		t.Fatalf("agent = %T, want HarnessAgent with a prompt builder", built.agent)
	}
	for _, name := range []string{"model.default", "model.stub", "policy.tools", "agent.subagents", "tool.task"} {
		if _, err := agent.Capabilities.Get(name); err != nil {
			t.Fatalf("production generation is missing capability %q: %v", name, err)
		}
	}
	if agent.GenerationID == "" {
		t.Fatal("production agent has no immutable generation id")
	}
	if got := agent.RunBudgetLimits(); got.Tokens != int64(cfg.Loop.TokenBudget) || got.CostMicros != cfg.Loop.CostBudgetMicros {
		t.Fatalf("production budget limits = %+v", got)
	}
	plain := agent.SystemPromptBuilder(map[string]any{valueSubagentEnabled: false, valueSwarmEnabled: false})
	if strings.Contains(plain, "<available_subagents>") || strings.Contains(plain, "Swarm Mode") {
		t.Fatalf("plain prompt exposes disabled orchestration:\n%s", plain)
	}
	swarmPrompt := agent.SystemPromptBuilder(map[string]any{valueSwarmEnabled: true})
	for _, section := range []string{"<available_subagents>", "Swarm Mode"} {
		if !strings.Contains(swarmPrompt, section) {
			t.Fatalf("swarm prompt is missing %q:\n%s", section, swarmPrompt)
		}
	}
}

func TestBuildAgentFailsFastWhenDefaultSwarmHasNoDatabase(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = []config.ModelConfig{{Name: "stub", Provider: "openai-compatible", Model: "stub", BaseURL: "http://127.0.0.1:1", ContextLength: 1000}}
	cfg.DefaultModel = "stub"
	cfg.Sandbox.Enabled = false
	cfg.Swarm.Enabled = true
	cfg.Title.Enabled = false
	cfg.Summarization.Enabled = false

	_, err := buildAgent(cfg, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "runtime.database_url") {
		t.Fatalf("error = %v, want missing swarm database failure", err)
	}
}

func TestSubagentTimeoutUsesProfileOverrideAndSwarmPolicy(t *testing.T) {
	cfg := config.Defaults()
	cfg.Subagents.Agents = map[string]config.SubagentProfileConfig{
		"explore": {Timeout: 5 * time.Minute},
	}
	req := subagent.DispatchRequest{SubagentType: "explore"}
	if got := subagentTimeout(cfg, runtime.RunContext{}, req); got != 5*time.Minute {
		t.Fatalf("profile timeout = %s", got)
	}
	parent := runtime.RunContext{Values: map[string]any{valueSwarmEnabled: true}}
	if got := subagentTimeout(cfg, parent, req); got != cfg.Swarm.TeammateTimeout {
		t.Fatalf("swarm timeout = %s", got)
	}
}

func TestBuildPricerUsesConfiguredAliasAndProviderModelID(t *testing.T) {
	pricer := buildPricer([]config.ModelConfig{{
		Name:  "fast",
		Model: "provider-model-v1",
		Pricing: config.ModelPricingConfig{
			InputPerMillionMicros:  1_000_000,
			OutputPerMillionMicros: 2_000_000,
		},
	}})
	usage := model.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	for _, name := range []string{"fast", "provider-model-v1"} {
		if got := pricer.CostMicros(name, usage); got != 3_000_000 {
			t.Fatalf("CostMicros(%q) = %d, want 3000000", name, got)
		}
	}
}

func postJSON(t *testing.T, url, body string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		t.Fatalf("POST %s returned %s", url, resp.Status)
	}
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
