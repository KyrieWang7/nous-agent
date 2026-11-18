package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/loop"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model/provider/faux"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

type rejectInput struct{}

func (rejectInput) Name() string { return "reject-input" }
func (rejectInput) BeforeModel(_ context.Context, st *lifecycle.State) error {
	st.Directive = lifecycle.DirectiveStop
	return nil
}

func TestRejectedOrUncommittedInputDoesNotChangeCanonicalHistory(t *testing.T) {
	for _, reject := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejected", false: "input-commit-failed"}[reject], func(t *testing.T) {
			registry := tool.NewRegistry()
			var handlers []lifecycle.Handler
			if reject {
				handlers = append(handlers, rejectInput{})
			}
			dispatcher, err := lifecycle.NewDispatcher(handlers, lifecycle.DispatcherOptions{})
			if err != nil {
				t.Fatal(err)
			}
			samples := 0
			runner, err := loop.NewRunner(loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Lifecycle: dispatcher, Sampler: samplerFunc(func(context.Context, *lifecycle.State) (*model.Response, bool, error) {
				samples++
				return nil, false, errors.New("unexpected sample")
			})})
			if err != nil {
				t.Fatal(err)
			}
			history := message.NewHistory()
			history.Append(message.Message{Role: message.RoleUser, Content: "previous"})
			watermark := history.Watermark()
			var events []runtime.EventType
			ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread", Publish: func(_ context.Context, event runtime.Event) (int64, error) {
				events = append(events, event.Type)
				if event.Type == runtime.EventModelInputCommitted {
					return 0, errors.New("journal unavailable")
				}
				return int64(len(events)), nil
			}})
			_, err = runner.Run(ctx, loop.Request{RunID: "run", ThreadID: "thread", History: history, Prompt: "proposed"})
			if reject && err != nil {
				t.Fatal(err)
			}
			if !reject && err == nil {
				t.Fatal("missing journal failure")
			}
			if samples != 0 || history.Len() != 1 || history.Watermark() != watermark {
				t.Fatalf("samples=%d history=%v watermark=%d", samples, history.All(), history.Watermark())
			}
			if reject && slices.Contains(events, runtime.EventAgentStepStart) {
				t.Fatal("rejected input spent a step")
			}
			if len(events) == 0 || events[len(events)-1] != runtime.EventAgentTurnEnd {
				t.Fatal("turn did not close", events)
			}
		})
	}
}

func TestOutputLimitNeverExecutesTruncatedToolsAndSettlesBeforeStepEnd(t *testing.T) {
	registry := tool.NewRegistry()
	executed := false
	definition := echoTool("partial")
	definition.Handler = func(context.Context, tool.Call) (*tool.Result, error) { executed = true; return &tool.Result{}, nil }
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	var events []runtime.Event
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread", Publish: func(_ context.Context, e runtime.Event) (int64, error) {
		events = append(events, e)
		return int64(len(events)), nil
	}})
	runner, err := loop.NewRunner(loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Sampler: samplerFunc(func(context.Context, *lifecycle.State) (*model.Response, bool, error) {
		return &model.Response{StopReason: model.StopReasonLength, Message: message.Message{Role: message.RoleAssistant, ToolCalls: []message.ToolCall{{ID: "partial", Name: "partial", Arguments: json.RawMessage(`{}`)}}}}, false, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	history := message.NewHistory()
	if _, err := runner.Run(ctx, loop.Request{RunID: "run", ThreadID: "thread", History: history, Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("truncated tool was executed")
	}
	msgs := history.All()
	if len(msgs) != 3 || msgs[2].Role != message.RoleTool {
		t.Fatalf("unsettled transcript=%v", msgs)
	}
	resultAt, endAt := -1, -1
	for i, e := range events {
		if e.Type == runtime.EventToolResult {
			resultAt = i
		}
		if e.Type == runtime.EventAgentStepEnd {
			endAt = i
		}
	}
	if resultAt < 0 || endAt <= resultAt {
		t.Fatal("step closed before recovery", events)
	}
	var end runtime.AgentBoundary
	if err := json.Unmarshal(events[len(events)-1].Data, &end); err != nil {
		t.Fatal(err)
	}
	if end.Reason != "max-tokens" {
		t.Fatalf("turn end=%+v", end)
	}
}

type invalidInput struct{}

func (invalidInput) Name() string { return "invalid-model-input" }
func (invalidInput) BeforeModel(_ context.Context, st *lifecycle.State) error {
	st.ModelInput.ExtraBody = map[string]any{"cannot-serialize": make(chan struct{})}
	return nil
}

func TestAdapterPreparationFailureDoesNotAdmitInputOrSpendModelCall(t *testing.T) {
	m := faux.New(faux.Text("never"))
	registry := tool.NewRegistry()
	dispatcher, err := lifecycle.NewDispatcher([]lifecycle.Handler{invalidInput{}}, lifecycle.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := loop.NewRunner(loop.Config{Registry: registry, Executor: tool.NewExecutor(registry, tool.ExecutorOptions{}), Lifecycle: dispatcher, Sampler: loop.NewDirectSampler(m)})
	if err != nil {
		t.Fatal(err)
	}
	history := message.NewHistory()
	var events []runtime.EventType
	ctx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "run", ThreadID: "thread", Publish: func(_ context.Context, event runtime.Event) (int64, error) {
		events = append(events, event.Type)
		return int64(len(events)), nil
	}})
	if _, err := runner.Run(ctx, loop.Request{RunID: "run", ThreadID: "thread", History: history, Prompt: "not admitted"}); err == nil {
		t.Fatal("expected preparation error")
	}
	if history.Len() != 0 || m.CallCount() != 0 || slices.Contains(events, runtime.EventModelInputCommitted) {
		t.Fatalf("history=%v calls=%d events=%v", history.All(), m.CallCount(), events)
	}
	if !slices.Equal(events, []runtime.EventType{runtime.EventAgentTurnStart, runtime.EventAgentStepStart, runtime.EventAgentStepEnd, runtime.EventAgentTurnEnd}) {
		t.Fatal(events)
	}
}
