// Package telemetry provides OpenTelemetry spans without coupling the loop to
// a concrete exporter.
package telemetry

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const Name = "telemetry"

type Middleware struct {
	tracer trace.Tracer
	runs   sync.Map
	models sync.Map
}

type runSpan struct {
	ctx  context.Context
	span trace.Span
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
	spanCtx, span := m.tracer.Start(ctx, "agent.run", trace.WithAttributes(attribute.String("run.id", st.RunID), attribute.String("thread.id", st.ThreadID), attribute.String("assistant.id", st.AssistantID)))
	m.runs.Store(st.RunID, runSpan{ctx: spanCtx, span: span})
	return nil
}
func (m *Middleware) AfterAgent(_ context.Context, st *middleware.State) error {
	prefix := st.RunID + ":"
	m.models.Range(func(key, value any) bool {
		name, ok := key.(string)
		if ok && strings.HasPrefix(name, prefix) {
			if span, deleted := m.models.LoadAndDelete(key); deleted {
				if typed, ok := span.(trace.Span); ok {
					typed.End()
				}
			}
		}
		return true
	})
	if stored, ok := m.runs.LoadAndDelete(st.RunID); ok {
		if typed, ok := stored.(runSpan); ok {
			typed.span.End()
		}
	}
	return nil
}
func (m *Middleware) BeforeModel(ctx context.Context, st *middleware.State) error {
	key := fmt.Sprintf("%s:%d", st.RunID, st.Iteration)
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
	key := fmt.Sprintf("%s:%d", st.RunID, st.Iteration)
	if span, ok := m.models.LoadAndDelete(key); ok {
		if typed, ok := span.(trace.Span); ok {
			typed.End()
		}
	}
	return nil
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
