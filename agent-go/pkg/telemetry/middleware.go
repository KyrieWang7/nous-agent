// Package telemetry provides OpenTelemetry spans without coupling the loop to
// a concrete exporter.
package telemetry

import (
	"context"
	"strconv"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/tool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const Name = "telemetry"

type Middleware struct {
	tracer trace.Tracer
	runs   sync.Map
	models sync.Map
	tools  sync.Map
}

type runSpan struct {
	ctx  context.Context
	span trace.Span
}

type spanKey struct {
	runID string
	id    string
}

func New(service string) *Middleware {
	if service == "" {
		service = "nous-agent-go"
	}
	return &Middleware{tracer: otel.Tracer(service)}
}
func (*Middleware) Name() string            { return Name }
func (*Middleware) Grade() middleware.Grade { return middleware.GradeListener }
func (m *Middleware) BeforeAgent(ctx context.Context, st *middleware.State) error {
	parent := ctx
	spanName := "agent.run"
	attrs := []attribute.KeyValue{
		attribute.String("run.id", st.RunID),
		attribute.String("thread.id", st.ThreadID),
		attribute.String("assistant.id", st.AssistantID),
	}
	if run, ok := runtime.RunContextFrom(ctx); ok && run.SubagentTaskID != "" {
		spanName = "agent.subagent"
		attrs = append(attrs, attribute.String("subagent.task_id", run.SubagentTaskID))
		if !trace.SpanContextFromContext(ctx).IsValid() {
			if stored, exists := m.runs.Load(run.ParentRunID); exists {
				if typed, valid := stored.(runSpan); valid {
					parent = typed.ctx
				}
			}
		}
	}
	spanCtx, span := m.tracer.Start(parent, spanName, trace.WithAttributes(attrs...))
	m.runs.Store(st.RunID, runSpan{ctx: spanCtx, span: span})
	return nil
}
func (m *Middleware) AfterAgent(ctx context.Context, st *middleware.State) error {
	m.models.Range(func(key, value any) bool {
		name, ok := key.(spanKey)
		if ok && name.runID == st.RunID {
			if span, deleted := m.models.LoadAndDelete(key); deleted {
				if typed, ok := span.(trace.Span); ok {
					typed.End()
				}
			}
		}
		return true
	})
	m.tools.Range(func(key, value any) bool {
		name, ok := key.(spanKey)
		if ok && name.runID == st.RunID {
			if span, deleted := m.tools.LoadAndDelete(key); deleted {
				if typed, ok := span.(runSpan); ok {
					typed.span.End()
				}
			}
		}
		return true
	})
	if stored, ok := m.runs.LoadAndDelete(st.RunID); ok {
		if typed, ok := stored.(runSpan); ok {
			if run, exists := runtime.RunContextFrom(ctx); exists && run.Journal != nil {
				totals := run.Journal.Totals()
				typed.span.SetAttributes(
					attribute.Int("usage.lead_tokens", totals.LeadTokens),
					attribute.Int("usage.subagent_tokens", totals.SubagentTokens),
					attribute.Int("usage.middleware_tokens", totals.MiddlewareTokens),
					attribute.Int64("usage.cost_micros", totals.CostMicros),
				)
			}
			typed.span.End()
		}
	}
	return nil
}
func (m *Middleware) BeforeModel(ctx context.Context, st *middleware.State) error {
	key := spanKey{runID: st.RunID, id: strconv.Itoa(st.Iteration)}
	parent := ctx //nolint:contextcheck // model spans intentionally use the stored run span as parent
	if stored, ok := m.runs.Load(st.RunID); ok {
		if typed, ok := stored.(runSpan); ok {
			parent = typed.ctx
		}
	}
	_, span := m.tracer.Start(parent, "agent.model", trace.WithAttributes(attribute.Int("agent.iteration", st.Iteration)))
	m.models.Store(key, span)
	return nil
}
func (m *Middleware) AfterModel(_ context.Context, st *middleware.State) error {
	key := spanKey{runID: st.RunID, id: strconv.Itoa(st.Iteration)}
	if span, ok := m.models.LoadAndDelete(key); ok {
		if typed, ok := span.(trace.Span); ok {
			if st.ModelOutput != nil {
				typed.SetAttributes(
					attribute.String("model.name", st.ModelOutput.ModelName),
					attribute.Int("usage.input_tokens", st.ModelOutput.Usage.InputTokens),
					attribute.Int("usage.output_tokens", st.ModelOutput.Usage.OutputTokens),
					attribute.Int("usage.cached_input_tokens", st.ModelOutput.Usage.CachedInputTokens),
				)
			}
			typed.End()
		}
	}
	return nil
}

func (m *Middleware) BeforeTool(ctx context.Context, st *middleware.State) (tool.Decision, error) {
	if st.ToolCall == nil {
		return tool.Decision{}, nil
	}
	parent := ctx
	if stored, ok := m.runs.Load(st.RunID); ok {
		if typed, valid := stored.(runSpan); valid {
			parent = typed.ctx
		}
	}
	spanCtx, span := m.tracer.Start(parent, "agent.tool", trace.WithAttributes(
		attribute.String("run.id", st.RunID),
		attribute.String("tool.name", st.ToolCall.Name),
		attribute.String("tool.call_id", st.ToolCall.ID),
	))
	key := spanKey{runID: st.RunID, id: st.ToolCall.ID}
	if previous, loaded := m.tools.LoadAndDelete(key); loaded {
		if typed, ok := previous.(runSpan); ok {
			typed.span.End()
		}
	}
	m.tools.Store(key, runSpan{ctx: spanCtx, span: span})
	return tool.Decision{}, nil
}

func (m *Middleware) AfterTool(_ context.Context, st *middleware.State) error {
	if st.ToolCall == nil {
		return nil
	}
	key := spanKey{runID: st.RunID, id: st.ToolCall.ID}
	stored, ok := m.tools.LoadAndDelete(key)
	if !ok {
		return nil
	}
	active, ok := stored.(runSpan)
	if !ok {
		return nil
	}
	if st.ToolExecErr != nil {
		active.span.RecordError(st.ToolExecErr)
		active.span.SetStatus(codes.Error, st.ToolExecErr.Error())
	} else if st.ToolResult != nil && st.ToolResult.IsError {
		active.span.SetStatus(codes.Error, st.ToolResult.Content)
	}
	active.span.End()
	return nil
}

// InstrumentToolDefinition forwards the live tool span through the handler
// context. Middleware stage methods cannot replace ctx, so definitions that
// launch nested work (notably task) opt into this bridge.
func (m *Middleware) InstrumentToolDefinition(def tool.Definition) tool.Definition {
	handler := def.Handler
	if handler == nil {
		return def
	}
	def.Handler = func(ctx context.Context, call tool.Call) (*tool.Result, error) {
		run, ok := runtime.RunContextFrom(ctx)
		if !ok {
			return handler(ctx, call)
		}
		stored, exists := m.tools.Load(spanKey{runID: run.RunID, id: call.ID})
		if !exists {
			return handler(ctx, call)
		}
		active, valid := stored.(runSpan)
		if !valid {
			return handler(ctx, call)
		}
		return handler(active.ctx, call)
	}
	return def
}

// InstrumentTool wraps a tool handler with a span and is available to external
// registries that want per-call instrumentation without middleware state races.
func (m *Middleware) InstrumentTool(name string, fn func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, span := m.tracer.Start(ctx, "agent.tool", trace.WithAttributes(attribute.String("tool.name", name)))
		defer span.End()
		return fn(ctx)
	}
}
