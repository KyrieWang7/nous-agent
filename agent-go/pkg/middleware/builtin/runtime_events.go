package builtin

import (
	"context"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
)

const NameRuntimeEvents = "runtimeEvents"

type RuntimeEvents struct{}

func NewRuntimeEvents() *RuntimeEvents        { return &RuntimeEvents{} }
func (RuntimeEvents) Name() string            { return NameRuntimeEvents }
func (RuntimeEvents) Grade() middleware.Grade { return middleware.GradeListener }
func (RuntimeEvents) BeforeTool(ctx context.Context, st *middleware.State) (tool.Decision, error) {
	if st.ToolCall != nil {
		publishRuntime(ctx, st, runtime.EventToolStart, runtime.ToolStart{ToolCallID: st.ToolCall.ID, Name: st.ToolCall.Name, Args: st.ToolCall.Args})
	}
	return tool.Decision{}, nil
}

func (RuntimeEvents) AfterTool(ctx context.Context, st *middleware.State) error {
	if st.ToolCall == nil || st.ToolResult == nil {
		return nil
	}
	publishRuntime(ctx, st, runtime.EventToolResult, runtime.ToolResult{
		ToolCallID: st.ToolCall.ID,
		Name:       st.ToolCall.Name,
		Content:    st.ToolResult.Content,
		IsError:    st.ToolResult.IsError || st.ToolExecErr != nil,
	})
	return nil
}

func publishRuntime(ctx context.Context, st *middleware.State, typ runtime.EventType, data any) {
	run, ok := runtime.RunContextFrom(ctx)
	if !ok {
		return
	}
	event := runtime.MustEvent(run.RunID, run.ThreadID, typ, data)
	if run.Publish != nil {
		run.Publish(ctx, event)
	} else if run.Bus != nil {
		run.Bus.Publish(ctx, event)
	}
}
