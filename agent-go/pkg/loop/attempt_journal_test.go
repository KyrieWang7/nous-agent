package loop_test

import (
	"context"
	"encoding/json"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/modelrouter"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"reflect"
	"testing"
	"time"
)

type transientInput struct{ calls int }

func (*transientInput) Name() string { return "transientInput" }
func (h *transientInput) BeforeModel(_ context.Context, st *lifecycle.State) error {
	h.calls++
	st.ModelInput.Messages = append(st.ModelInput.Messages, message.Message{Role: message.RoleSystem, Content: "inbox"})
	return nil
}
func TestRouterJournalsEveryAttemptAndToolSnapshot(t *testing.T) {
	m := faux.New(faux.Fail(model.ErrRateLimited), faux.ToolCall("echo", `{}`), faux.Text("done")).WithInfo(model.Info{Name: "actual", ContextLength: 10000, MaxOutputTokens: 500})
	router, err := modelrouter.New(modelrouter.Config{Models: map[modelrouter.Tier]model.Model{modelrouter.TierStandard: m}, Sleep: func(context.Context, time.Duration) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	if err = registry.Register(echoTool("echo")); err != nil {
		t.Fatal(err)
	}
	h := &transientInput{}
	dispatcher, err := lifecycle.NewDispatcher([]lifecycle.Handler{h}, lifecycle.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resolver := resolverFunc(func(st *lifecycle.State) ([]string, []string) {
		if st.Iteration == 0 {
			return []string{"echo"}, nil
		}
		return nil, nil
	})
	runner, err := loop.NewRunner(loop.Config{Sampler: router, Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Lifecycle: dispatcher, ToolSet: resolver, Limits: loop.Limits{MaxIterations: 3}})
	if err != nil {
		t.Fatal(err)
	}
	var inputs []runtime.ModelInputCommitted
	keys := map[string]bool{}
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread", GenerationID: "generation", Publish: func(_ context.Context, event runtime.Event) (int64, error) {
		if event.Type == runtime.EventModelInputCommitted {
			if keys[event.IdempotencyKey] {
				t.Fatal("duplicate input key")
			}
			keys[event.IdempotencyKey] = true
			var input runtime.ModelInputCommitted
			if err := json.Unmarshal(event.Data, &input); err != nil {
				t.Fatal(err)
			}
			inputs = append(inputs, input)
			if m.CallCount() != len(inputs)-1 {
				t.Fatal("input was not written before provider call")
			}
		}
		return int64(len(keys)), nil
	}})
	if _, err = runner.Run(ctx, loop.Request{RunID: "run", ThreadID: "thread", History: message.NewHistory(), Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	requests := m.Requests()
	if len(inputs) != 3 || len(requests) != 3 || h.calls != 2 {
		t.Fatalf("inputs=%d calls=%d handlers=%d", len(inputs), len(requests), h.calls)
	}
	for i, v := range inputs {
		if v.ModelName != "actual" || v.GenerationID != "generation" || v.EffectiveOutputTokens != 500 || !reflect.DeepEqual(v.Messages, requests[i].Messages) {
			t.Fatalf("input %d does not describe actual attempt: %+v", i, v)
		}
	}
	if inputs[0].Attempt != 1 || inputs[1].Attempt != 2 || inputs[2].Attempt != 1 || len(inputs[1].Tools) != 1 || len(inputs[2].Tools) != 0 {
		t.Fatalf("attempt/tool snapshots=%+v", inputs)
	}
}
