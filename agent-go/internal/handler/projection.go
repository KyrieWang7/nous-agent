package handler

import (
	"fmt"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/message"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime/runmanager"
)

func tokenUsagePayload(input, output, cached, lead, subagent, auxiliary int) map[string]any {
	return map[string]any{
		"type":              "token_usage",
		"input_tokens":      input,
		"output_tokens":     output,
		"cache_read_tokens": cached,
		"total_tokens":      input + output,
		"lead_tokens":       lead,
		"subagent_tokens":   subagent,
		"auxiliary_tokens":  auxiliary,
	}
}

func ProjectEvent(run runmanager.Run, projection runmanager.Projection) (runtime.Event, error) {
	name := ""
	data := projection.Data
	switch projection.Kind {
	case runmanager.ProjectionRunStart:
		name = "metadata"
	case runmanager.ProjectionValues:
		name = "values"
	case runmanager.ProjectionMessage:
		msg, ok := projection.Data.(message.Message)
		if !ok {
			return runtime.Event{}, fmt.Errorf("httpapi: message projection has type %T", projection.Data)
		}
		name = "messages"
		data = []any{toWireMessage(msg), map[string]any{"run_id": run.RunID, "thread_id": run.ThreadID}}
	case runmanager.ProjectionUsage:
		totals, ok := projection.Data.(runtime.Totals)
		if !ok {
			return runtime.Event{}, fmt.Errorf("httpapi: usage projection has type %T", projection.Data)
		}
		name = "custom"
		data = tokenUsagePayload(totals.InputTokens, totals.OutputTokens, totals.CachedInputTokens, totals.LeadTokens, totals.SubagentTokens, totals.AuxiliaryTokens)
	case runmanager.ProjectionError:
		cause, ok := projection.Data.(error)
		if !ok {
			return runtime.Event{}, fmt.Errorf("httpapi: error projection has type %T", projection.Data)
		}
		name = "error"
		data = map[string]any{"message": cause.Error(), "type": fmt.Sprintf("%T", cause)}
	case runmanager.ProjectionRunEnd:
		name = "end"
	default:
		return runtime.Event{}, fmt.Errorf("httpapi: unknown runtime projection %q", projection.Kind)
	}
	typ := wireEventType(name, data)
	e := runtime.MustEvent(run.RunID, run.ThreadID, typ, makeWireEvent(name, data))
	return e, nil
}

func wireEventType(name string, data any) runtime.EventType {
	var typ runtime.EventType
	switch name {
	case "metadata":
		typ = runtime.EventRunStart
	case "values":
		typ = runtime.EventStateValues
	case "messages":
		typ = runtime.EventMessage
	case "custom":
		typ = runtime.EventCustom
		if payload, ok := data.(map[string]any); ok && payload["type"] == "token_usage" {
			typ = runtime.EventUsage
		}
	case "error":
		typ = runtime.EventError
	case "end":
		typ = runtime.EventRunEnd
	default:
		typ = runtime.EventCustom
	}
	return typ
}
