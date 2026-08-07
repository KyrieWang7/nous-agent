package langgraphapi

import (
	"encoding/json"
	"fmt"
	"net/http"

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

func projectRuntimeEvent(e runtime.Event) (wireEvent, error) {
	var payload map[string]any
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &payload); err != nil {
			return wireEvent{}, err
		}
	}
	meta := map[string]any{"run_id": e.RunID, "thread_id": e.ThreadID}
	switch e.Type {
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
	default:
		custom := map[string]any{"type": string(e.Type)}
		for k, v := range payload {
			custom[k] = v
		}
		return makeWireEvent("custom", custom), nil
	}
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
