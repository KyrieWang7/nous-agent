package telemetry

import (
	"context"
	"testing"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/middleware"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestAfterAgentClosesFailedModelSpanAndPreservesParentage(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	m := &Middleware{tracer: provider.Tracer("test")}
	st := middleware.NewState(middleware.StateInit{RunID: "run-1", ThreadID: "thread-1"})
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
	if _, ok := m.models.Load("run-1:0"); ok {
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
