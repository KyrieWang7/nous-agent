package telemetry

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/model"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/lifecycle"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestAfterAgentClosesFailedModelSpanAndPreservesParentage(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	m := &Observer{tracer: provider.Tracer("test")}
	st := lifecycle.NewState(lifecycle.StateInit{RunID: "run-1", ThreadID: "thread-1"})
	if err := m.BeforeAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if err := m.BeforeModel(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	// Model sampling fails here, so AfterModel is intentionally not called.
	if err := m.AfterAgent(context.Background(), st); err != nil {
		t.Fatal(err)
	}

	if _, ok := m.runs.Load("run-1"); ok {
		t.Fatal("run span leaked")
	}
	if _, ok := m.models.Load(spanKey{runID: "run-1", id: "0"}); ok {
		t.Fatal("model span leaked")
	}

	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("ended spans = %d, want 2", len(ended))
	}
	var runSpan, modelSpan sdktrace.ReadOnlySpan
	for _, span := range ended {
		switch span.Name() {
		case "agent.run":
			runSpan = span
		case "agent.model":
			modelSpan = span
		}
	}
	if runSpan == nil || modelSpan == nil {
		t.Fatalf("spans = %#v", ended)
	}
	if modelSpan.Parent().SpanID() != runSpan.SpanContext().SpanID() {
		t.Fatalf("model parent = %s, want run span %s", modelSpan.Parent().SpanID(), runSpan.SpanContext().SpanID())
	}
}

func TestRunToolSubagentModelHierarchyAndUsageAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	m := &Observer{tracer: provider.Tracer("test")}

	parentJournal := runtime.NewJournal(nil)
	parentJournal.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "lead", Usage: model.Usage{InputTokens: 2, OutputTokens: 1}})
	parentJournal.Observe(runtime.Entry{Bucket: runtime.BucketAuxiliary, CallID: "lifecycle", Usage: model.Usage{InputTokens: 4, OutputTokens: 1}})
	childJournal := parentJournal.Child()
	childJournal.Observe(runtime.Entry{Bucket: runtime.BucketLead, CallID: "child", Usage: model.Usage{InputTokens: 1, OutputTokens: 1}})
	parentJournal.MergeSubagent("call-task", "explore", childJournal)

	parentRun := runtime.RunContext{RunID: "run-1", ThreadID: "thread-1", Journal: parentJournal}
	ctx := runtime.WithRunContext(context.Background(), parentRun)
	parentState := lifecycle.NewState(lifecycle.StateInit{RunID: "run-1", ThreadID: "thread-1", AssistantID: "lead"})
	if err := m.BeforeAgent(ctx, parentState); err != nil {
		t.Fatal(err)
	}

	call := tool.Call{ID: "call-task", Name: "task"}
	toolState := parentState.CloneForTool(call)
	if _, err := m.BeforeTool(ctx, toolState); err != nil {
		t.Fatal(err)
	}
	definition := m.InstrumentToolDefinition(tool.Definition{
		Name: "task", Group: "subagent",
		Handler: func(handlerCtx context.Context, _ tool.Call) (*tool.Result, error) {
			childRun := parentRun
			childRun.ParentRunID = parentRun.RunID
			childRun.RunID = "run-1:call-task"
			childRun.SubagentTaskID = call.ID
			childRun.Journal = childJournal
			childCtx := runtime.WithRunContext(handlerCtx, childRun)
			childState := lifecycle.NewState(lifecycle.StateInit{
				RunID: "run-1:call-task", ThreadID: "thread-1", AssistantID: "explore",
			})
			if err := m.BeforeAgent(childCtx, childState); err != nil {
				return nil, err
			}
			if err := m.BeforeModel(childCtx, childState); err != nil {
				return nil, err
			}
			childState.ModelOutput = &model.Response{
				ModelName: "model-a",
				Usage:     model.Usage{InputTokens: 7, OutputTokens: 3, CachedInputTokens: 2},
			}
			if err := m.AfterModel(childCtx, childState); err != nil {
				return nil, err
			}
			if err := m.AfterAgent(childCtx, childState); err != nil {
				return nil, err
			}
			return &tool.Result{Content: "done"}, nil
		},
	})
	result, err := definition.Handler(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	toolState.ToolResult = result
	if err := m.AfterTool(ctx, toolState); err != nil {
		t.Fatal(err)
	}
	if err := m.AfterAgent(ctx, parentState); err != nil {
		t.Fatal(err)
	}

	spans := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	for _, name := range []string{"agent.run", "agent.tool", "agent.subagent", "agent.model"} {
		if spans[name] == nil {
			t.Fatalf("missing %s span; ended=%#v", name, recorder.Ended())
		}
	}
	assertParent(t, spans["agent.tool"], spans["agent.run"])
	assertParent(t, spans["agent.subagent"], spans["agent.tool"])
	assertParent(t, spans["agent.model"], spans["agent.subagent"])
	for key, want := range map[string]int64{
		"usage.lead_tokens": 3, "usage.subagent_tokens": 2, "usage.auxiliary_tokens": 5,
	} {
		if got, ok := intAttribute(spans["agent.run"], key); !ok || got != want {
			t.Errorf("run attribute %s = %d/%v, want %d", key, got, ok, want)
		}
	}
	for key, want := range map[string]int64{
		"usage.input_tokens": 7, "usage.output_tokens": 3, "usage.cached_input_tokens": 2,
	} {
		if got, ok := intAttribute(spans["agent.model"], key); !ok || got != want {
			t.Errorf("model attribute %s = %d/%v, want %d", key, got, ok, want)
		}
	}
}

func TestInstrumentedToolKeepsLiveHandlerCancellationContext(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	m := &Observer{tracer: provider.Tracer("test")}

	run := runtime.RunContext{RunID: "run-live-context", ThreadID: "thread-1"}
	liveCtx := runtime.WithRunContext(context.Background(), run)
	state := lifecycle.NewState(lifecycle.StateInit{RunID: run.RunID, ThreadID: run.ThreadID})
	stageCtx, cancelStage := context.WithCancel(liveCtx)
	if err := m.BeforeAgent(stageCtx, state); err != nil {
		t.Fatal(err)
	}
	cancelStage()

	call := tool.Call{ID: "call-1", Name: "task"}
	toolState := state.CloneForTool(call)
	toolStageCtx, cancelToolStage := context.WithCancel(liveCtx)
	if _, err := m.BeforeTool(toolStageCtx, toolState); err != nil {
		t.Fatal(err)
	}
	cancelToolStage()

	definition := m.InstrumentToolDefinition(tool.Definition{
		Name: "task",
		Handler: func(handlerCtx context.Context, _ tool.Call) (*tool.Result, error) {
			if err := handlerCtx.Err(); err != nil {
				t.Fatalf("handler inherited cancelled lifecycle context: %v", err)
			}
			if span := trace.SpanFromContext(handlerCtx); !span.SpanContext().IsValid() {
				t.Fatal("handler did not receive the active tool span")
			}
			return &tool.Result{Content: "done"}, nil
		},
	})
	result, err := definition.Handler(liveCtx, call)
	if err != nil {
		t.Fatal(err)
	}
	toolState.ToolResult = result
	if err := m.AfterTool(liveCtx, toolState); err != nil {
		t.Fatal(err)
	}
	if err := m.AfterAgent(liveCtx, state); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentUsesExplicitParentRunIDWithoutPropagatedSpanContext(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	m := &Observer{tracer: provider.Tracer("test")}

	parentState := lifecycle.NewState(lifecycle.StateInit{RunID: "parent", ThreadID: "thread"})
	parentCtx := runtime.WithRunContext(context.Background(), runtime.RunContext{RunID: "parent", ThreadID: "thread"})
	if err := m.BeforeAgent(parentCtx, parentState); err != nil {
		t.Fatal(err)
	}

	childState := lifecycle.NewState(lifecycle.StateInit{RunID: "parent:task-1", ThreadID: "thread"})
	childCtx := runtime.WithRunContext(context.Background(), runtime.RunContext{
		RunID: "parent:task-1", ParentRunID: "parent", ThreadID: "thread", SubagentTaskID: "task-1",
	})
	if err := m.BeforeAgent(childCtx, childState); err != nil {
		t.Fatal(err)
	}
	if err := m.AfterAgent(childCtx, childState); err != nil {
		t.Fatal(err)
	}
	if err := m.AfterAgent(parentCtx, parentState); err != nil {
		t.Fatal(err)
	}

	spans := make(map[string]sdktrace.ReadOnlySpan)
	for _, span := range recorder.Ended() {
		spans[span.Name()] = span
	}
	if spans["agent.run"] == nil || spans["agent.subagent"] == nil {
		t.Fatalf("ended spans = %#v", recorder.Ended())
	}
	assertParent(t, spans["agent.subagent"], spans["agent.run"])
}

func assertParent(t *testing.T, child, parent sdktrace.ReadOnlySpan) {
	t.Helper()
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("%s parent = %s, want %s", child.Name(), child.Parent().SpanID(), parent.SpanContext().SpanID())
	}
}

func intAttribute(span sdktrace.ReadOnlySpan, key string) (int64, bool) {
	for _, value := range span.Attributes() {
		if string(value.Key) == key {
			return value.Value.AsInt64(), true
		}
	}
	return 0, false
}
