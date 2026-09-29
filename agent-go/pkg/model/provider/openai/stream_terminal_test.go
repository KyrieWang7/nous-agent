package openai_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/openai"
)

func TestStream_RejectsTruncatedResponse(t *testing.T) {
	for _, payload := range []string{
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\"}}]}}]}\n\n",
	} {
		t.Run(payload, func(t *testing.T) {
			m, _ := stub(t, sseHandler(payload), model.ProviderConfig{})
			r, err := m.Stream(context.Background(), model.Request{})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			_, events := drain(t, r)
			resp, err := r.Result()
			if resp != nil || !errors.Is(err, io.ErrUnexpectedEOF) || !model.IsProviderUnavailable(err) {
				t.Fatalf("truncated stream returned response=%+v, error=%v", resp, err)
			}
			if len(events) == 0 || events[len(events)-1].Type != model.StreamError {
				t.Fatalf("missing terminal error event: %+v", events)
			}
			if _, ok := r.Next(); ok {
				t.Fatal("stream continued after error")
			}
		})
	}
}

func TestStream_AcceptsFinishReasonWithoutDoneMarker(t *testing.T) {
	m, _ := stub(t, sseHandler("data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\n"), model.ProviderConfig{})
	r, err := m.Stream(context.Background(), model.Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	_, events := drain(t, r)
	resp, err := r.Result()
	if err != nil || resp.Message.Content != "complete" {
		t.Fatalf("response=%+v, error=%v", resp, err)
	}
	if events[len(events)-1].Type != model.StreamDone {
		t.Fatalf("missing done event: %+v", events)
	}
}

func TestRequestInfoMatchesOutputOverridePrecedence(t *testing.T) {
	m, err := openai.New(model.ProviderConfig{Name: "test", Model: "test", MaxTokens: 100, ExtraBody: map[string]any{"max_tokens": 200}, ThinkingExtraBody: map[string]any{"max_tokens": 300}})
	if err != nil {
		t.Fatal(err)
	}
	req := model.Request{MaxTokens: 150, Thinking: true, ExtraBody: map[string]any{"max_tokens": 400}}
	if got := model.RequestInfo(m, req).MaxOutputTokens; got != 400 {
		t.Fatalf("output reservation=%d", got)
	}
}
