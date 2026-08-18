package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/runtime"
)

type wireEvent struct {
	Wire  bool            `json:"_wire_event,omitempty"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func makeWireEvent(name string, data any) wireEvent {
	b, err := json.Marshal(data)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"message": err.Error()})
	}
	return wireEvent{Wire: true, Event: name, Data: b}
}

func decodeWireEvent(e runtime.Event) (wireEvent, error) {
	var w wireEvent
	if err := json.Unmarshal(e.Data, &w); err == nil && w.Wire && w.Event != "" {
		return w, nil
	}
	return projectRuntimeEvent(e)
}

// clientVisibleRuntimeEvent separates the canonical model transcript from the
// HTTP projection. Transcript events still occupy the shared sequence space so
// reconnect cursors remain exact, but they are consumed by replay rather than
// serialized as frontend wire events.
func clientVisibleRuntimeEvent(e runtime.Event) bool {
	switch e.Type {
	case runtime.EventTranscriptAppend, runtime.EventTranscriptReplace,
		runtime.EventRunStateChanged, runtime.EventToolStart, runtime.EventToolResult,
		runtime.EventApprovalRequested, runtime.EventApprovalResolved, runtime.EventBudgetChanged, runtime.EventPlanModeChanged,
		runtime.EventCompactionStart, runtime.EventCompactionComplete:
		return false
	default:
		return true
	}
}

func projectRuntimeEvent(e runtime.Event) (wireEvent, error) {
	var payload map[string]any
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			return wireEvent{}, err
		}
	}
	meta := map[string]any{"run_id": e.RunID, "thread_id": e.ThreadID}
	switch e.Type {
	case runtime.EventStateValues:
		return makeWireEvent("values", payload), nil
	case runtime.EventContentDelta, runtime.EventReasoningDelta:
		id, _ := payload["message_id"].(string)
		if id == "" {
			id = e.RunID
		}
		chunk := map[string]any{"id": id, "type": "AIMessageChunk", "content": "", "additional_kwargs": map[string]any{}, "response_metadata": map[string]any{}}
		if e.Type == runtime.EventContentDelta {
			chunk["content"] = payload["delta"]
		} else {
			chunk["additional_kwargs"] = map[string]any{"reasoning_content": payload["delta"]}
		}
		return makeWireEvent("messages", []any{chunk, meta}), nil
	case runtime.EventError:
		return makeWireEvent("error", payload), nil
	case runtime.EventRunEnd:
		return makeWireEvent("end", payload), nil
	case runtime.EventRunStart:
		return makeWireEvent("metadata", meta), nil
	case runtime.EventSubagentProgress:
		return projectSubagentProgress(e)
	case runtime.EventSubagentStart, runtime.EventSubagentResult:
		return projectSubagentEvent(e, payload), nil
	default:
		custom := map[string]any{"type": string(e.Type)}
		for k, v := range payload {
			custom[k] = v
		}
		return makeWireEvent("custom", custom), nil
	}
}

func projectSubagentProgress(e runtime.Event) (wireEvent, error) {
	var progress runtime.SubagentProgress
	if err := json.Unmarshal(e.Data, &progress); err != nil {
		return wireEvent{}, err
	}
	wire := toWireMessage(progress.Message)
	if progress.MessageID != "" {
		wire.ID = progress.MessageID
	}
	return makeWireEvent("custom", map[string]any{
		"type":           "task_running",
		"task_id":        progress.TaskID,
		"message":        wire,
		"message_index":  progress.MessageIndex,
		"total_messages": progress.MessageIndex,
	}), nil
}

// projectSubagentEvent bridges the native runtime lifecycle names to the
// task_* custom events consumed by the existing frontend. The runtime event
// remains unchanged in storage/replay; only its HTTP projection is adapted.
func projectSubagentEvent(e runtime.Event, payload map[string]any) wireEvent {
	taskID, _ := payload["task_id"].(string)
	if e.Type == runtime.EventSubagentStart {
		typ, _ := payload["subagent_type"].(string)
		custom := map[string]any{
			"type":          "task_started",
			"task_id":       taskID,
			"description":   payloadString(payload, "description"),
			"subagent_type": typ,
		}
		return makeWireEvent("custom", custom)
	}

	status := strings.ToLower(payloadString(payload, "status"))
	switch status {
	case "completed", "success", "succeeded":
		return makeWireEvent("custom", map[string]any{
			"type":    "task_completed",
			"task_id": taskID,
			"result":  payloadString(payload, "output"),
		})
	case "timed_out", "timeout", "polling_timed_out":
		errText := payloadString(payload, "error")
		return makeWireEvent("custom", map[string]any{
			"type":    "task_timed_out",
			"task_id": taskID,
			"error":   errText,
		})
	case "failed", "error", "cancelled", "canceled":
		errText := payloadString(payload, "error")
		if errText == "" && (status == "cancelled" || status == "canceled") {
			errText = "Task cancelled by user."
		}
		return makeWireEvent("custom", map[string]any{
			"type":    "task_failed",
			"task_id": taskID,
			"error":   errText,
		})
	default:
		custom := map[string]any{"type": string(e.Type)}
		for key, value := range payload {
			custom[key] = value
		}
		return makeWireEvent("custom", custom)
	}
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func writeSSE(w http.ResponseWriter, id int64, event wireEvent) error {
	if id > 0 {
		if _, err := fmt.Fprintf(w, "id: %d\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event.Event); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "data: %s\n\n", event.Data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return err
}
