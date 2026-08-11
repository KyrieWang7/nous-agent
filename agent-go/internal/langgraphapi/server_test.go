package langgraphapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type agentFunc func(context.Context, AgentRequest) (AgentResult, error)

type wireProtocolContract struct {
	SSESuccessOrder []string `json:"sse_success_order"`
	SSEFailureOrder []string `json:"sse_failure_order"`
}

func loadWireProtocolContract(t *testing.T) wireProtocolContract {
	t.Helper()
	path := filepath.Join("..", "..", "..", "contracts", "harness_protocol_contract.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contract wireProtocolContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}

func (f agentFunc) Run(ctx context.Context, req AgentRequest) (AgentResult, error) {
	return f(ctx, req)
}

func TestAgentPanicBecomesTerminalErrorEvents(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateThread(context.Background(), newThread("thread-panic", nil), false); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{
		Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
			panic("model adapter panic")
		}),
		Store:             store,
		HeartbeatInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	request := httptest.NewRequest(http.MethodPost, "/threads/thread-panic/runs/stream", strings.NewReader(`{
		"input":{"messages":[{"type":"human","content":"hello"}]},
		"on_disconnect":"continue"
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, request)

	events := readSSE(t, io.NopCloser(strings.NewReader(response.Body.String())))
	assertEventOrder(t, events, loadWireProtocolContract(t).SSEFailureOrder...)
	runs, err := store.ListRuns(context.Background(), "thread-panic")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "error" || runs[0].RiskLevel != "unknown" {
		t.Fatalf("runs = %#v", runs)
	}
}

func TestThreadStateAndHistoryContract(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, agentFunc(func(_ context.Context, req AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{
			{Role: message.RoleUser, Content: req.Prompt},
			{Role: message.RoleAssistant, Content: "hello from go"},
		}, Usage: Usage{InputTokens: 11, OutputTokens: 3}}, nil
	}))

	created := requestJSON(t, s.URL+"/threads", http.MethodPost, `{"thread_id":"thread-1","metadata":{"source":"test"}}`)
	if created["thread_id"] != "thread-1" {
		t.Fatalf("thread_id = %v", created["thread_id"])
	}

	body := `{"assistant_id":"lead_agent","input":{"messages":[{"type":"human","content":"hello"}]},"stream_mode":["values","messages","custom"],"on_disconnect":"continue"}`
	resp, err := http.Post(s.URL+"/threads/thread-1/runs/stream", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, loadWireProtocolContract(t).SSESuccessOrder...)
	var endPayload map[string]any
	for _, event := range events {
		if event.Event == "end" {
			if err := json.Unmarshal(event.Data, &endPayload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if endPayload["risk_level"] != "pass" {
		t.Fatalf("end risk_level = %v, want pass", endPayload["risk_level"])
	}

	state := requestJSON(t, s.URL+"/threads/thread-1/state", http.MethodGet, "")
	next, nextOK := state["next"].([]any)
	tasks, tasksOK := state["tasks"].([]any)
	if !nextOK || !tasksOK || len(next) != 0 || len(tasks) != 0 {
		t.Fatalf("state next/tasks must be empty: %#v", state)
	}
	values, valuesOK := state["values"].(map[string]any)
	msgs, messagesOK := values["messages"].([]any)
	if !valuesOK || !messagesOK || len(msgs) != 2 {
		t.Fatalf("message count = %d, want 2", len(msgs))
	}
	last, lastOK := msgs[1].(map[string]any)
	if !lastOK || last["content"] != "hello from go" {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	tokenUsage, ok := state["token_usage"].(map[string]any)
	if !ok || tokenUsage["input_tokens"] != float64(11) || tokenUsage["output_tokens"] != float64(3) || tokenUsage["total_tokens"] != float64(14) {
		t.Fatalf("token_usage not restored from completion: %#v", state["token_usage"])
	}

	update := requestJSON(t, s.URL+"/threads/thread-1/state", http.MethodPost, `{"values":{"title":"Go thread"}}`)
	updatedValues, ok := update["values"].(map[string]any)
	if !ok || updatedValues["title"] != "Go thread" {
		t.Fatalf("title not updated: %#v", update)
	}
}

func TestInputFromMessagesAcceptsStringAndComplexContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      map[string]any
		wantPrompt string
		wantBlocks []message.ContentBlock
	}{
		{
			name: "string content",
			input: map[string]any{"messages": []any{
				map[string]any{"type": "human", "content": "hello"},
			}},
			wantPrompt: "hello",
		},
		{
			name: "multimodal content",
			input: map[string]any{"messages": []any{
				map[string]any{"type": "human", "content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,aGk="}},
					map[string]any{"type": "text", "text": "describe this"},
				}},
			}},
			wantPrompt: "describe this",
			wantBlocks: []message.ContentBlock{{Type: "image", MimeType: "image/png", Data: "aGk="}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt, blocks, err := inputFromMessages(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if prompt != test.wantPrompt {
				t.Fatalf("prompt = %q, want %q", prompt, test.wantPrompt)
			}
			if !reflect.DeepEqual(blocks, test.wantBlocks) {
				t.Fatalf("blocks = %#v, want %#v", blocks, test.wantBlocks)
			}
		})
	}
}

func TestInputFromMessagesRejectsRemoteImageURL(t *testing.T) {
	t.Parallel()

	_, _, err := inputFromMessages(map[string]any{"messages": []any{
		map[string]any{"type": "human", "content": []any{
			map[string]any{"type": "image_url", "image_url": "https://example.com/image.png"},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "only data URLs are supported") {
		t.Fatalf("error = %v, want unsupported data URL error", err)
	}
}

func TestWireMessagePreservesComplexContent(t *testing.T) {
	t.Parallel()

	wire := toWireMessage(message.Message{
		Role:    message.RoleUser,
		Content: "describe this",
		ContentBlocks: []message.ContentBlock{
			{Type: "image", MimeType: "image/png", Data: "aGk="},
		},
	})
	blocks, ok := wire.Content.([]map[string]any)
	if !ok || len(blocks) != 2 {
		t.Fatalf("content = %#v, want text and image blocks", wire.Content)
	}
	image, ok := blocks[1]["image_url"].(map[string]any)
	if !ok || image["url"] != "data:image/png;base64,aGk=" {
		t.Fatalf("image block = %#v", blocks[1])
	}
}

func TestWireMessagePreservesSubagentStatus(t *testing.T) {
	t.Parallel()

	wire := toWireMessage(message.Message{
		Role:    message.RoleTool,
		Name:    "task",
		Content: "Task failed. Error: child crashed",
		AdditionalKwargs: map[string]any{
			message.SubagentStatusKey: message.SubagentFailed,
			message.SubagentErrorKey:  "child crashed",
		},
	})
	if got := wire.AdditionalKwargs[message.SubagentStatusKey]; got != message.SubagentFailed {
		t.Fatalf("status = %v, want %q", got, message.SubagentFailed)
	}
	if got := wire.AdditionalKwargs[message.SubagentErrorKey]; got != "child crashed" {
		t.Fatalf("error = %v, want child crashed", got)
	}
}

func TestWireMessageStampsLegacyTaskResult(t *testing.T) {
	t.Parallel()

	wire := toWireMessage(message.Message{
		Role:    message.RoleTool,
		Name:    "task",
		Content: "Task Succeeded. Result: child output",
	})
	if got := wire.AdditionalKwargs[message.SubagentStatusKey]; got != message.SubagentCompleted {
		t.Fatalf("status = %v, want %q", got, message.SubagentCompleted)
	}
}

func TestNormalizeTaskMessagesStampsPersistedTranscript(t *testing.T) {
	t.Parallel()
	in := []message.Message{{
		Role:    message.RoleTool,
		Name:    "task",
		Content: "Task failed. Error: persisted child failure",
	}}
	out := normalizeTaskMessages(in)
	if got := out[0].AdditionalKwargs[message.SubagentStatusKey]; got != message.SubagentFailed {
		t.Fatalf("status = %v, want %q", got, message.SubagentFailed)
	}
	if got := out[0].AdditionalKwargs[message.SubagentErrorKey]; got != "persisted child failure" {
		t.Fatalf("error = %v, want persisted child failure", got)
	}
	if in[0].AdditionalKwargs != nil {
		t.Fatalf("normalization mutated caller input: %#v", in[0])
	}
}

func TestNativeRoutesUseTheSameEventTypes(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, agentFunc(func(_ context.Context, req AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "native"}}}, nil
	}))
	requestJSON(t, s.URL+"/api/v1/threads", http.MethodPost, `{"thread_id":"native-thread"}`)
	resp, err := http.Post(s.URL+"/api/v1/threads/native-thread/runs", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, "metadata", "values", "messages", "custom", "end")
}

func TestStreamedRunDoesNotRepublishFinalToolMessages(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, agentFunc(func(_ context.Context, req AgentRequest) (AgentResult, error) {
		return AgentResult{
			Streamed: true,
			Messages: []message.Message{
				{Role: message.RoleUser, Content: req.Prompt},
				{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "call-1", Name: "ask_clarification"}}},
				{Role: message.RoleTool, ToolCallID: "call-1", Name: "ask_clarification", Content: "Which environment?"},
			},
		}, nil
	}))

	resp, err := http.Post(s.URL+"/threads/thread-streamed-tool/runs/stream", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"help"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	for _, event := range events {
		if event.Event == "messages" {
			t.Fatalf("streamed final tool message was republished: %s", event.Data)
		}
	}
	assertEventOrder(t, events, "metadata", "values", "custom", "end")
}

func TestRuntimeDeltaProjectsToMessagesEvent(t *testing.T) {
	e := runtime.MustEvent("run-1", "thread-1", runtime.EventContentDelta, runtime.ContentDelta{
		Delta:     "hello",
		MessageID: "run-1:0",
	})
	w, err := decodeWireEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if w.Event != "messages" {
		t.Fatalf("event = %q", w.Event)
	}
	var data []map[string]any
	if err := json.Unmarshal(w.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 2 || data[0]["id"] != "run-1:0" || data[0]["content"] != "hello" {
		t.Fatalf("data = %#v", data)
	}
}

func TestSubagentStartProjectsToFrontendTaskStartedEvent(t *testing.T) {
	t.Parallel()
	e := runtime.MustEvent("run-1", "thread-1", runtime.EventSubagentStart, map[string]any{
		"task_id":       "task-1",
		"description":   "Inspect repository",
		"subagent_type": "explore",
	})
	w, err := decodeWireEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if w.Event != "custom" {
		t.Fatalf("event = %q, want custom", w.Event)
	}
	var payload map[string]any
	if err := json.Unmarshal(w.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != "task_started" || payload["task_id"] != "task-1" || payload["description"] != "Inspect repository" || payload["subagent_type"] != "explore" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSubagentProgressProjectsToFrontendTaskRunningEvent(t *testing.T) {
	t.Parallel()
	e := runtime.MustEvent("run-1", "thread-1", runtime.EventSubagentProgress, runtime.SubagentProgress{
		TaskID:       "call-1",
		MessageID:    "run-1:call-1:2",
		MessageIndex: 3,
		Message: message.Message{
			Role:    message.RoleAssistant,
			Content: "inspecting",
			ToolCalls: []message.ToolCall{{
				ID: "tool-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`),
			}},
		},
	})
	w, err := decodeWireEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if w.Event != "custom" {
		t.Fatalf("event = %q, want custom", w.Event)
	}
	var payload struct {
		Type          string      `json:"type"`
		TaskID        string      `json:"task_id"`
		Message       wireMessage `json:"message"`
		MessageIndex  int         `json:"message_index"`
		TotalMessages int         `json:"total_messages"`
	}
	if err := json.Unmarshal(w.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Type != "task_running" || payload.TaskID != "call-1" || payload.MessageIndex != 3 || payload.TotalMessages != 3 {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.Message.ID != "run-1:call-1:2" || payload.Message.Type != "ai" || payload.Message.Content != "inspecting" {
		t.Fatalf("message = %#v", payload.Message)
	}
	if len(payload.Message.ToolCalls) != 1 || payload.Message.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %#v", payload.Message.ToolCalls)
	}
}

func TestSubagentResultProjectsToFrontendTerminalEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     string
		wantType   string
		wantResult string
	}{
		{name: "completed", status: "completed", wantType: "task_completed", wantResult: "done"},
		{name: "failed", status: "failed", wantType: "task_failed", wantResult: "child error"},
		{name: "timed out", status: "timed_out", wantType: "task_timed_out", wantResult: "deadline"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := runtime.MustEvent("run-1", "thread-1", runtime.EventSubagentResult, map[string]any{
				"task_id": "task-1",
				"status":  tc.status,
				"output":  map[string]string{"completed": "done"}[tc.status],
				"error":   map[string]string{"failed": "child error", "timed_out": "deadline"}[tc.status],
			})
			w, err := decodeWireEvent(e)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(w.Data, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["type"] != tc.wantType || payload["task_id"] != "task-1" {
				t.Fatalf("payload = %#v", payload)
			}
			key := "error"
			if tc.status == "completed" {
				key = "result"
			}
			if payload[key] != tc.wantResult {
				t.Fatalf("payload[%q] = %v, want %q", key, payload[key], tc.wantResult)
			}
		})
	}
}

func TestRawRuntimePayloadCannotMasqueradeAsWireEnvelope(t *testing.T) {
	e := runtime.MustEvent("run-1", "thread-1", runtime.EventCustom, map[string]any{
		"event": "end",
		"data":  map[string]any{"forged": true},
	})
	w, err := decodeWireEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	if w.Event != "custom" {
		t.Fatalf("event = %q, want custom", w.Event)
	}
}

func TestWireEventsKeepDistinctRuntimeTypes(t *testing.T) {
	tests := []struct {
		name string
		data any
		want runtime.EventType
	}{
		{"metadata", nil, runtime.EventRunStart},
		{"values", nil, runtime.EventStateValues},
		{"messages", nil, runtime.EventMessage},
		{"custom", map[string]any{"type": "progress"}, runtime.EventCustom},
		{"custom", map[string]any{"type": "token_usage"}, runtime.EventUsage},
		{"error", nil, runtime.EventError},
		{"end", nil, runtime.EventRunEnd},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := wireEventType(test.name, test.data); got != test.want {
				t.Fatalf("type = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCancelCompletedRunDoesNotRewriteTerminalStatus(t *testing.T) {
	store := NewMemoryStore()
	thread := newThread("thread-terminal", nil)
	_, _ = store.CreateThread(context.Background(), thread, false)
	run := Run{RunID: "run-terminal", ThreadID: thread.ThreadID, Status: "success", Created: time.Now().UTC()}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { return AgentResult{}, nil }), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	result := requestJSON(t, ts.URL+"/threads/"+thread.ThreadID+"/runs/"+run.RunID+"/cancel", http.MethodPost, "")
	if result["was_running"] != false {
		t.Fatalf("cancel response = %#v", result)
	}
	got, err := store.GetRun(context.Background(), run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" {
		t.Fatalf("status = %q, want success", got.Status)
	}
}

func TestListRunsReconcilesRunWhoseWorkerDisappeared(t *testing.T) {
	store := NewMemoryStore()
	thread := newThread("thread-orphan", nil)
	_, _ = store.CreateThread(context.Background(), thread, false)
	run := Run{RunID: "run-orphan", ThreadID: thread.ThreadID, Status: "running", Created: time.Now().Add(-time.Hour)}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { return AgentResult{}, nil }), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/threads/thread-orphan/runs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var runs []Run
	if err := json.Unmarshal(response.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "error" || runs[0].Completed == nil {
		t.Fatalf("runs=%#v", runs)
	}
}

func TestListRunsReconcilesStaleWorkerHeartbeat(t *testing.T) {
	ctx := context.Background()
	registry, err := runtime.NewRegistry(ctx, runtime.RegistryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Close)
	store := NewMemoryStore()
	thread := newThread("thread-stale-heartbeat", nil)
	_, _ = store.CreateThread(ctx, thread, false)
	started := time.Now().Add(-time.Hour)
	run := Run{RunID: "run-stale-heartbeat", ThreadID: thread.ThreadID, Status: "running", Created: started}
	if err := store.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Register(ctx, runtime.RunRecord{
		RunID: run.RunID, ThreadID: thread.ThreadID, StartedAt: started,
		OnDisconnect: runtime.DisconnectContinue,
	}); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{
		Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { return AgentResult{}, nil }),
		Store: store, Registry: registry, HeartbeatInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/threads/thread-stale-heartbeat/runs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	got, err := store.GetRun(ctx, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "error" || got.Completed == nil {
		t.Fatalf("run=%#v", got)
	}
}

func TestReconnectOrphanedRunEndsStream(t *testing.T) {
	store := NewMemoryStore()
	thread := newThread("thread-orphan-stream", nil)
	_, _ = store.CreateThread(context.Background(), thread, false)
	run := Run{RunID: "run-orphan-stream", ThreadID: thread.ThreadID, Status: "running", Created: time.Now().Add(-time.Hour)}
	if err := store.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { return AgentResult{}, nil }), Store: store})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/threads/thread-orphan-stream/runs/run-orphan-stream/stream", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: end") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type failingHistoryStore struct{ *MemoryStore }

func (s failingHistoryStore) SaveHistory(context.Context, string, []message.Message, bool) error {
	return errors.New("disk unavailable")
}

func TestHistoryPersistenceFailureProducesErrorAndFailedRun(t *testing.T) {
	store := failingHistoryStore{NewMemoryStore()}
	_, _ = store.CreateThread(context.Background(), newThread("thread-fail", nil), false)
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "answer"}}}, nil
	}), Store: store, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/threads/thread-fail/runs/stream", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, "metadata", "error", "end")
	runs, err := store.ListRuns(context.Background(), "thread-fail")
	if err != nil || len(runs) != 1 || runs[0].Status != "error" {
		t.Fatalf("runs = %#v, error = %v", runs, err)
	}
}

type failingCompletionStore struct{ *MemoryStore }

func (s failingCompletionStore) SaveRunCompletion(context.Context, RunCompletion) error {
	return errors.New("completion store unavailable")
}

func TestCompletionPersistenceFailureCannotLeaveSuccessfulRun(t *testing.T) {
	store := failingCompletionStore{NewMemoryStore()}
	_, _ = store.CreateThread(context.Background(), newThread("thread-completion-fail", nil), false)
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "answer"}}}, nil
	}), Store: store, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/threads/thread-completion-fail/runs/stream", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, "metadata", "values", "messages", "custom", "error", "end")
	runs, err := store.ListRuns(context.Background(), "thread-completion-fail")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, error = %v", runs, err)
	}
	if runs[0].Status != "error" {
		t.Fatalf("run status = %q, want error", runs[0].Status)
	}
}

type failSuccessfulTerminalStore struct {
	*MemoryStore
	mu     sync.Mutex
	failed bool
}

func (s *failSuccessfulTerminalStore) UpdateRun(ctx context.Context, id string, update RunUpdate) (Run, error) {
	s.mu.Lock()
	if update.Status == "success" && !s.failed {
		s.failed = true
		s.mu.Unlock()
		return Run{}, errors.New("terminal run store unavailable")
	}
	s.mu.Unlock()
	return s.MemoryStore.UpdateRun(ctx, id, update)
}

func TestTerminalRunFailureReconcilesCompletionAndRun(t *testing.T) {
	store := &failSuccessfulTerminalStore{MemoryStore: NewMemoryStore()}
	_, _ = store.CreateThread(context.Background(), newThread("thread-terminal-fail", nil), false)
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) {
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "answer"}}}, nil
	}), Store: store, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	resp, err := http.Post(ts.URL+"/threads/thread-terminal-fail/runs/stream", "application/json", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"hello"}]},"on_disconnect":"continue"}`))
	if err != nil {
		t.Fatal(err)
	}
	events := readSSE(t, resp.Body)
	assertEventOrder(t, events, "metadata", "values", "messages", "custom", "error", "end")
	runs, err := store.ListRuns(context.Background(), "thread-terminal-fail")
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %#v, error = %v", runs, err)
	}
	if runs[0].Status != "error" {
		t.Fatalf("run status = %q, want error", runs[0].Status)
	}
	completion, ok := store.RunCompletion(runs[0].RunID)
	if !ok || completion.Status != "error" {
		t.Fatalf("completion = %#v, ok = %t", completion, ok)
	}
}

func TestServerCloseCancelsAndWaitsForActiveRuns(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	agent := agentFunc(func(ctx context.Context, _ AgentRequest) (AgentResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return AgentResult{}, ctx.Err()
	})
	srv, err := New(Options{Agent: agent, HeartbeatInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/threads/thread-close/runs/stream", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"wait"}]},"on_disconnect":"continue"}`))
		request.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(response, request)
		requestDone <- response
	}()
	<-started

	closeDone := make(chan struct{})
	go func() {
		srv.Close()
		close(closeDone)
	}()
	<-cancelled
	select {
	case <-closeDone:
		t.Fatal("Close returned before the active run exited")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the active run exited")
	}
	response := <-requestDone
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: end") {
		t.Fatalf("run response status=%d body=%s", response.Code, response.Body.String())
	}

	rejected := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/threads/thread-close/runs/stream", strings.NewReader(`{"input":{"messages":[]}}`))
	request.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rejected, request)
	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("run admitted after Close: status=%d body=%s", rejected.Code, rejected.Body.String())
	}
}

func TestMemoryStoreSavesRunCompletion(t *testing.T) {
	store := NewMemoryStore()
	want := RunCompletion{RunID: "run-1", ThreadID: "thread-1", Status: "success", LLMCalls: 2, LeadTokens: 9}
	if err := store.SaveRunCompletion(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, ok := store.RunCompletion("run-1")
	if !ok || got.LLMCalls != 2 || got.LeadTokens != 9 {
		t.Fatalf("completion = %#v, ok = %t", got, ok)
	}
}

func TestAccountingAdvancedRejectsStaleSnapshots(t *testing.T) {
	current := runtime.Totals{LLMCalls: 2, InputTokens: 20, OutputTokens: 10, SubagentTokens: 12}
	newer := current
	newer.LLMCalls++
	newer.InputTokens += 4
	newer.SubagentTokens += 4
	if !accountingAdvanced(newer, current) {
		t.Fatal("newer accounting snapshot was rejected")
	}
	stale := current
	stale.LLMCalls--
	if accountingAdvanced(stale, current) {
		t.Fatal("stale accounting snapshot was accepted")
	}
	if accountingAdvanced(current, current) {
		t.Fatal("equal accounting snapshot was accepted")
	}
}

func TestMemoryStoreLatestRunCompletionSkipsRunsWithoutModelCalls(t *testing.T) {
	t.Parallel()
	store := NewMemoryStore()
	older := RunCompletion{RunID: "accounted", ThreadID: "thread-1", LLMCalls: 1, InputTokens: 10, CompletedAt: time.Now().Add(-time.Minute)}
	newer := RunCompletion{RunID: "failed-before-model", ThreadID: "thread-1", LLMCalls: 0, CompletedAt: time.Now()}
	if err := store.SaveRunCompletion(context.Background(), older); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunCompletion(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.LatestRunCompletion(context.Background(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got.RunID != older.RunID {
		t.Fatalf("latest completion = %+v, ok=%v", got, ok)
	}
}

func TestDisconnectContinueAndReconnect(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s := newTestServer(t, agentFunc(func(ctx context.Context, req AgentRequest) (AgentResult, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-ctx.Done():
			return AgentResult{}, ctx.Err()
		}
		return AgentResult{Messages: []message.Message{{Role: message.RoleAssistant, Content: "survived"}}}, nil
	}))
	requestJSON(t, s.URL+"/threads", http.MethodPost, `{"thread_id":"thread-2"}`)

	req, _ := http.NewRequest(http.MethodPost, s.URL+"/threads/thread-2/runs/stream", strings.NewReader(`{"input":{"messages":[{"type":"human","content":"wait"}]},"on_disconnect":"continue"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(resp.Body)
	var runID string
	for runID == "" {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data:") {
			var data map[string]any
			_ = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data)
			if id, ok := data["run_id"].(string); ok {
				runID = id
			}
		}
	}
	<-started
	_ = resp.Body.Close()
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	for {
		run := requestJSON(t, s.URL+"/threads/thread-2/runs/"+runID, http.MethodGet, "")
		if run["status"] == "success" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not continue after disconnect: %#v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}

	r2, err := http.NewRequest(http.MethodGet, s.URL+"/threads/thread-2/runs/"+runID+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	r2.Header.Set("Last-Event-ID", "1")
	reconnected, err := http.DefaultClient.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reconnected.Body.Close() }()
	events := readSSE(t, reconnected.Body)
	if countEvent(events, "end") != 1 {
		t.Fatalf("reconnect events = %#v", events)
	}
	for _, e := range events {
		if e.ID <= 1 {
			t.Fatalf("replayed event at/before cursor: %#v", e)
		}
	}
}

func TestReconnectPaginatesAndDeduplicates(t *testing.T) {
	t.Parallel()
	eventStore := runtime.NewMemoryEventStore()
	bus := runtime.NewMemoryBus(runtime.BusOptions{RingCapacity: 500})
	store := NewMemoryStore()
	srv, err := New(Options{Agent: agentFunc(func(context.Context, AgentRequest) (AgentResult, error) { return AgentResult{}, nil }), Store: store, Bus: bus, EventStore: eventStore, HeartbeatInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	run := Run{RunID: "run-many", ThreadID: "thread-many", AssistantID: "lead_agent", Status: "success", Created: time.Now().UTC()}
	_, _ = store.CreateThread(context.Background(), newThread(run.ThreadID, nil), false)
	_ = store.CreateRun(context.Background(), run)
	batch := make([]runtime.Event, 0, 1502)
	for i := 1; i <= 1500; i++ {
		e := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventContentDelta, makeWireEvent("custom", map[string]int{"n": i}))
		e.Seq = int64(i)
		batch = append(batch, e)
	}
	end := runtime.MustEvent(run.RunID, run.ThreadID, runtime.EventRunEnd, makeWireEvent("end", map[string]any{}))
	end.Seq = 1501
	batch = append(batch, end)
	if err := eventStore.PutBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/threads/thread-many/runs/run-many/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	events := readSSE(t, resp.Body)
	if got := countEvent(events, "custom"); got != 1500 {
		t.Fatalf("custom events = %d, want 1500", got)
	}
	if got := countEvent(events, "end"); got != 1 {
		t.Fatalf("end events = %d, want 1", got)
	}
}

type parsedSSE struct {
	ID    int64
	Event string
	Data  json.RawMessage
}

func readSSE(t *testing.T, body io.ReadCloser) []parsedSSE {
	t.Helper()
	defer func() { _ = body.Close() }()
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 1024), 2<<20)
	var cur parsedSSE
	var out []parsedSSE
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id:"):
			_, _ = fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "id:")), "%d", &cur.ID)
		case strings.HasPrefix(line, "event:"):
			cur.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			cur.Data = append([]byte(nil), bytes.TrimSpace([]byte(strings.TrimPrefix(line, "data:")))...)
		case line == "" && cur.Event != "":
			out = append(out, cur)
			if cur.Event == "end" {
				return out
			}
			cur = parsedSSE{}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func newTestServer(t *testing.T, agent Agent) *httptest.Server {
	t.Helper()
	srv, err := New(Options{Agent: agent, HeartbeatInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts
}
func requestJSON(t *testing.T, url, method, body string) map[string]any {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: %s: %s", method, url, resp.Status, b)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func assertEventOrder(t *testing.T, events []parsedSSE, want ...string) {
	t.Helper()
	got := make([]string, len(events))
	for i, e := range events {
		got[i] = e.Event
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", got, want)
	}
}
func countEvent(events []parsedSSE, name string) int {
	n := 0
	for _, e := range events {
		if e.Event == name {
			n++
		}
	}
	return n
}
