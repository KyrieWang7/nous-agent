package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/openai"
)

// stub 起一个打桩服务并返回构造好的客户端与最近一次请求体。
func stub(t *testing.T, handler http.HandlerFunc, cfg model.ProviderConfig) (model.Model, *capture) {
	t.Helper()

	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.body = body
		cap.auth = r.Header.Get("Authorization")
		cap.path = r.URL.Path
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	cfg.BaseURL = srv.URL
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	if cfg.Name == "" {
		cfg.Name = "stub"
	}

	m, err := openai.New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return m, cap
}

type capture struct {
	body []byte
	auth string
	path string
}

func (c *capture) payload(t *testing.T) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(c.body, &v); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, c.body)
	}
	return v
}

func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func errorResponse(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

const okResponse = `{
  "id": "chatcmpl-1",
  "model": "test-model-0613",
  "choices": [{"finish_reason": "stop", "message": {"content": "hello there"}}],
  "usage": {"prompt_tokens": 11, "completion_tokens": 3, "prompt_tokens_details": {"cached_tokens": 4}}
}`

// --- 构造与请求组装 ---

func TestNew_RequiresModel(t *testing.T) {
	t.Parallel()

	if _, err := openai.New(model.ProviderConfig{Name: "x"}); err == nil {
		t.Fatal("New() without a model succeeded")
	}
}

func TestComplete_SendsExpectedRequest(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{
		APIKey:    "secret-key",
		MaxTokens: 512,
	})

	_, err := m.Complete(context.Background(), model.Request{
		System:   "you are terse",
		Messages: []message.Message{{Role: message.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if cap.path != "/chat/completions" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.auth != "Bearer secret-key" {
		t.Errorf("Authorization = %q", cap.auth)
	}

	p := cap.payload(t)
	if p["model"] != "test-model" {
		t.Errorf("model = %v", p["model"])
	}
	if p["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v", p["max_tokens"])
	}

	msgs, _ := p["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v, want system + user", msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "you are terse" {
		t.Errorf("system message = %v", first)
	}
}

func TestComplete_ParsesResponse(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	got, err := m.Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if got.Message.Content != "hello there" {
		t.Errorf("content = %q", got.Message.Content)
	}
	if got.StopReason != model.StopReasonStop {
		t.Errorf("StopReason = %q", got.StopReason)
	}
	if got.Usage.InputTokens != 11 || got.Usage.OutputTokens != 3 || got.Usage.CachedInputTokens != 4 {
		t.Errorf("Usage = %+v", got.Usage)
	}
	if got.CallID != "chatcmpl-1" {
		t.Errorf("CallID = %q; usage attribution dedupes on it", got.CallID)
	}
	if got.ModelName != "test-model-0613" {
		t.Errorf("ModelName = %q, want the model the provider actually served", got.ModelName)
	}
}

func TestComplete_ParsesToolCalls(t *testing.T) {
	t.Parallel()

	body := `{
      "id":"c1","choices":[{"finish_reason":"tool_calls","message":{"content":"",
      "tool_calls":[{"id":"call_1","type":"function","function":{"name":"ls","arguments":"{\"path\":\"/\"}"}}]}}]}`

	m, _ := stub(t, jsonResponse(body), model.ProviderConfig{})

	got, err := m.Complete(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got.StopReason != model.StopReasonToolCalls {
		t.Errorf("StopReason = %q", got.StopReason)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", got.Message.ToolCalls)
	}
	tc := got.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "ls" || string(tc.Arguments) != `{"path":"/"}` {
		t.Errorf("tool call = %+v", tc)
	}
}

func TestComplete_SendsToolSchemas(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	_, err := m.Complete(context.Background(), model.Request{
		Tools: []model.ToolSchema{{
			Name:        "ls",
			Description: "list files",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	tools, _ := cap.payload(t)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	entry, _ := tools[0].(map[string]any)
	fn, _ := entry["function"].(map[string]any)
	if entry["type"] != "function" || fn["name"] != "ls" {
		t.Errorf("tool entry = %v", entry)
	}
}

func TestComplete_ToolSchemaWithoutParametersGetsAnEmptyObject(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	if _, err := m.Complete(context.Background(), model.Request{
		Tools: []model.ToolSchema{{Name: "noop"}},
	}); err != nil {
		t.Fatal(err)
	}

	tools, _ := cap.payload(t)["tools"].([]any)
	fn, _ := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["parameters"] == nil {
		t.Fatal("a tool without parameters must still send a schema; some endpoints reject a null")
	}
}

// DeepSeek 的 thinking.type 之类的私有字段必须能透传。
func TestComplete_PassesExtraBodyThrough(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{
		ExtraBody: map[string]any{"thinking": map[string]any{"type": "enabled"}},
	})

	if _, err := m.Complete(context.Background(), model.Request{}); err != nil {
		t.Fatal(err)
	}

	thinking, ok := cap.payload(t)["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("extra body was not passed through: %s", cap.body)
	}
}

// 配置里一个手滑的 extra_body 不该悄悄改掉整个请求的语义。
func TestComplete_ExtraBodyCannotOverrideStructuralFields(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{
		ExtraBody: map[string]any{
			"model":    "hijacked",
			"messages": []any{},
			"tools":    []any{},
		},
	})

	if _, err := m.Complete(context.Background(), model.Request{
		Messages: []message.Message{{Role: message.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}

	p := cap.payload(t)
	if p["model"] != "test-model" {
		t.Errorf("model was overridden by extra body: %v", p["model"])
	}
	if msgs, _ := p["messages"].([]any); len(msgs) == 0 {
		t.Error("messages were overridden by extra body")
	}
}

// 带 tool_calls 的 assistant 消息必须带 content 键，部分兼容端点会拒绝缺键的消息。
func TestComplete_AssistantToolCallMessageCarriesContentKey(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	if _, err := m.Complete(context.Background(), model.Request{
		Messages: []message.Message{{
			Role:      message.RoleAssistant,
			ToolCalls: []message.ToolCall{{ID: "a", Name: "ls", Arguments: json.RawMessage(`{}`)}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := cap.payload(t)["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if _, ok := first["content"]; !ok {
		t.Fatalf("assistant tool-call message is missing the content key: %v", first)
	}
}

func TestComplete_EmptyToolArgumentsBecomeEmptyObject(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	if _, err := m.Complete(context.Background(), model.Request{
		Messages: []message.Message{{
			Role:      message.RoleAssistant,
			ToolCalls: []message.ToolCall{{ID: "a", Name: "ls"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	msgs, _ := cap.payload(t)["messages"].([]any)
	calls, _ := msgs[0].(map[string]any)["tool_calls"].([]any)
	fn, _ := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["arguments"] != "{}" {
		t.Fatalf("arguments = %v, want an empty JSON object", fn["arguments"])
	}
}

func TestComplete_SendsImageBlocks(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, jsonResponse(okResponse), model.ProviderConfig{SupportsVision: true})

	if _, err := m.Complete(context.Background(), model.Request{
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: "what is this?",
			ContentBlocks: []message.ContentBlock{
				{Type: "image", MimeType: "image/jpeg", Data: "AAAA"},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(cap.body), "data:image/jpeg;base64,AAAA") {
		t.Fatalf("image block was not encoded as a data URL: %s", cap.body)
	}
}

func TestComplete_NoChoicesIsAnError(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, jsonResponse(`{"id":"c1","choices":[]}`), model.ProviderConfig{})

	if _, err := m.Complete(context.Background(), model.Request{}); err == nil {
		t.Fatal("Complete() with no choices succeeded")
	}
}

// --- 错误分类（router 的恢复策略依赖它）---

func TestComplete_ClassifiesErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
		check  func(error) bool
	}{
		{
			name: "rate limited", status: http.StatusTooManyRequests,
			body: `{"error":{"message":"slow down"}}`, check: model.IsRateLimited,
		},
		{
			name: "auth failed", status: http.StatusUnauthorized,
			body: `{"error":{"message":"bad key"}}`,
			check: func(err error) bool {
				return strings.Contains(err.Error(), "authentication")
			},
		},
		{
			name: "server error", status: http.StatusBadGateway,
			body: `upstream down`, check: model.IsProviderUnavailable,
		},
		{
			name: "service unavailable", status: http.StatusServiceUnavailable,
			body: `try later`, check: model.IsProviderUnavailable,
		},
		{
			name: "payload too large", status: http.StatusRequestEntityTooLarge,
			body: `too big`, check: model.IsContextOverflow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, _ := stub(t, errorResponse(tc.status, tc.body), model.ProviderConfig{})
			_, err := m.Complete(context.Background(), model.Request{})

			if err == nil {
				t.Fatal("Complete() succeeded on an error status")
			}
			if !tc.check(err) {
				t.Fatalf("error = %v, not classified as expected", err)
			}
		})
	}
}

// 上下文超限在兼容端点上是 400，只能靠错误体识别。
// 认错的代价是白重试一次；不认的代价是长会话彻底卡死。
func TestComplete_ClassifiesContextOverflowFromMessage(t *testing.T) {
	t.Parallel()

	bodies := []string{
		`{"error":{"message":"This model's maximum context length is 8192 tokens"}}`,
		`{"error":{"code":"context_length_exceeded"}}`,
		`{"error":{"message":"Please reduce the length of the messages"}}`,
		`{"error":{"message":"input is too long for this model"}}`,
	}

	for _, body := range bodies {
		t.Run(body[:30], func(t *testing.T) {
			t.Parallel()

			m, _ := stub(t, errorResponse(http.StatusBadRequest, body), model.ProviderConfig{})
			_, err := m.Complete(context.Background(), model.Request{})

			if !model.IsContextOverflow(err) {
				t.Fatalf("error = %v, want it classified as context overflow", err)
			}
		})
	}
}

func TestComplete_PlainBadRequestIsInvalidRequest(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, errorResponse(http.StatusBadRequest,
		`{"error":{"message":"unknown parameter foo"}}`), model.ProviderConfig{})

	_, err := m.Complete(context.Background(), model.Request{})
	if model.IsContextOverflow(err) {
		t.Fatalf("a plain bad request was misclassified as context overflow: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("error = %v, want an invalid-request classification", err)
	}
}

func TestComplete_NetworkFailureIsProviderUnavailable(t *testing.T) {
	t.Parallel()

	m, err := openai.New(model.ProviderConfig{
		Name: "dead", Model: "m", BaseURL: "http://127.0.0.1:1", Timeout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.Complete(context.Background(), model.Request{}); !model.IsProviderUnavailable(err) {
		t.Fatalf("error = %v, want provider unavailable so the router falls back", err)
	}
}

func TestComplete_ContextCancellationPropagates(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, jsonResponse(okResponse), model.ProviderConfig{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := m.Complete(ctx, model.Request{})
	if err == nil {
		t.Fatal("Complete() with a cancelled context succeeded")
	}
	if model.IsProviderUnavailable(err) {
		t.Fatalf("cancellation was misclassified as provider unavailable: %v", err)
	}
}

// --- 流式 ---

func sseHandler(chunks ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = w.Write([]byte(c))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func drain(t *testing.T, r model.StreamReader) (text string, events []model.StreamEvent) {
	t.Helper()

	var sb strings.Builder
	for {
		ev, ok := r.Next()
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Type == model.StreamTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String(), events
}

func TestStream_AssemblesText(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		"data: {\"id\":\"c1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	defer func() { _ = r.Close() }()

	text, events := drain(t, r)
	if text != "Hello world" {
		t.Fatalf("assembled text = %q", text)
	}
	if events[0].Type != model.StreamStart {
		t.Errorf("first event = %v, want start", events[0].Type)
	}
	if events[len(events)-1].Type != model.StreamDone {
		t.Errorf("last event = %v, want done", events[len(events)-1].Type)
	}

	got, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if got.Message.Content != "Hello world" || got.StopReason != model.StopReasonStop {
		t.Fatalf("Result() = %+v", got)
	}
}

// 增量里 name 只出现在第一片，arguments 分多片到达 —— 拼错就会执行错的调用。
func TestStream_AssemblesToolCallsAcrossChunks(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"write_file","arguments":"{\"pa"}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"a.txt\"}"}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	drain(t, r)

	got, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if len(got.Message.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", got.Message.ToolCalls)
	}
	tc := got.Message.ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "write_file" {
		t.Errorf("tool call identity = %+v", tc)
	}
	if string(tc.Arguments) != `{"path":"a.txt"}` {
		t.Fatalf("arguments = %q, want the reassembled JSON", tc.Arguments)
	}
	if got.StopReason != model.StopReasonToolCalls {
		t.Errorf("StopReason = %q", got.StopReason)
	}
}

func TestStream_MultipleToolCallsKeepOrder(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}}]}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{}"}}]}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()
	drain(t, r)

	got, _ := r.Result()
	if len(got.Message.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %+v", got.Message.ToolCalls)
	}
	// 顺序按首次出现，与索引一致的稳定顺序即可，关键是两个都在且身份不串
	names := []string{got.Message.ToolCalls[0].Name, got.Message.ToolCalls[1].Name}
	forward := names[0] == "second" && names[1] == "first"
	reverse := names[0] == "first" && names[1] == "second"
	if !forward && !reverse {
		t.Fatalf("tool call names = %v", names)
	}
	for _, tc := range got.Message.ToolCalls {
		if tc.ID == "" || tc.Name == "" {
			t.Fatalf("tool call lost its identity: %+v", tc)
		}
	}
}

func TestStream_ReportsUsage(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"content":"x"}}]}`+"\n\n",
		`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":2}}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()
	drain(t, r)

	got, _ := r.Result()
	if got.Usage.InputTokens != 7 || got.Usage.OutputTokens != 2 {
		t.Fatalf("Usage = %+v", got.Usage)
	}
}

func TestStream_SkipsHeartbeatsAndComments(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		": keep-alive\n\n",
		"\n",
		`data: {"id":"c1","choices":[{"delta":{"content":"ok"}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()

	text, _ := drain(t, r)
	if text != "ok" {
		t.Fatalf("text = %q", text)
	}
}

// 单个坏 chunk 不该杀掉整个流。
func TestStream_SkipsMalformedChunks(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"content":"a"}}]}`+"\n\n",
		"data: {not json\n\n",
		`data: {"choices":[{"delta":{"content":"b"}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()

	text, _ := drain(t, r)
	if text != "ab" {
		t.Fatalf("text = %q, want both good chunks", text)
	}
}

// Result 在未读完时必须先把流消费掉，否则聚合结果缺尾部内容。
func TestStream_ResultDrainsUnreadStream(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"content":"start"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"content":"-middle"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"content":"-end"}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()

	// 只读一个事件就去要结果
	if _, ok := r.Next(); !ok {
		t.Fatal("expected at least one event")
	}

	got, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if got.Message.Content != "start-middle-end" {
		t.Fatalf("Result() content = %q; the unread tail was lost", got.Message.Content)
	}
}

// 一行超过 bufio.Scanner 默认 64 KiB 时，Scanner 会静默停止，
// 表现为"回答莫名截断"。带 base64 图片或长工具参数的行很容易超。
func TestStream_HandlesVeryLongLines(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 200_000)
	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"content":"`+long+`"}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()
	drain(t, r)

	got, err := r.Result()
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if len(got.Message.Content) != len(long) {
		t.Fatalf("content = %d chars, want %d; the long line was truncated", len(got.Message.Content), len(long))
	}
}

func TestStream_ErrorStatusIsClassifiedBeforeStreaming(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, errorResponse(http.StatusTooManyRequests, `slow down`), model.ProviderConfig{})

	if _, err := m.Stream(context.Background(), model.Request{}); !model.IsRateLimited(err) {
		t.Fatalf("Stream() error = %v, want rate limited", err)
	}
}

func TestStream_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler("data: [DONE]\n\n"), model.ProviderConfig{})

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestStream_ReasoningDeltas(t *testing.T) {
	t.Parallel()

	m, _ := stub(t, sseHandler(
		`data: {"id":"c1","choices":[{"delta":{"reasoning_content":"let me think"}}]}`+"\n\n",
		`data: {"choices":[{"delta":{"content":"the answer"}}]}`+"\n\n",
		"data: [DONE]\n\n",
	), model.ProviderConfig{SupportsThinking: true})

	r, _ := m.Stream(context.Background(), model.Request{})
	defer func() { _ = r.Close() }()

	_, events := drain(t, r)
	var sawThinking bool
	for _, ev := range events {
		if ev.Type == model.StreamThinkingDelta {
			sawThinking = true
		}
	}
	if !sawThinking {
		t.Fatal("no thinking delta emitted")
	}

	got, _ := r.Result()
	if got.Message.ReasoningContent != "let me think" {
		t.Errorf("ReasoningContent = %q", got.Message.ReasoningContent)
	}
	if got.Message.Content != "the answer" {
		t.Errorf("Content = %q", got.Message.Content)
	}
}

func TestStream_RequestsUsageInStreamOptions(t *testing.T) {
	t.Parallel()

	m, cap := stub(t, sseHandler("data: [DONE]\n\n"), model.ProviderConfig{})

	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	p := cap.payload(t)
	if p["stream"] != true {
		t.Errorf("stream = %v, want true", p["stream"])
	}
	// 不带 include_usage 的流式请求拿不到 token 用量，三桶归因就永远是 0
	opts, ok := p["stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != true {
		t.Fatalf("stream_options = %v, want include_usage true", p["stream_options"])
	}
}

// --- 注册 ---

func TestRegistersInModelRegistry(t *testing.T) {
	t.Parallel()

	r := model.NewRegistry()
	if err := r.Register(openai.Name, openai.New); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := r.Get(openai.Name); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}
