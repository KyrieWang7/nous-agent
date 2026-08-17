package message

import "strings"

// Subagent status values are shared by the Go backend and the frontend
// contract. Keep the vocabulary deliberately small and stable.
const (
	SubagentStatusKey = "subagent_status"
	SubagentErrorKey  = "subagent_error"

	SubagentCompleted       = "completed"
	SubagentFailed          = "failed"
	SubagentCancelled       = "cancelled"
	SubagentTimedOut        = "timed_out"
	SubagentPollingTimedOut = "polling_timed_out"
)

// MakeSubagentAdditionalKwargs builds the structured terminal metadata. An
// empty error is omitted so the wire contract never contains a misleading
// empty subagent_error field.
func MakeSubagentAdditionalKwargs(status, errText string) map[string]any {
	if !isSubagentStatus(status) {
		return nil
	}
	out := map[string]any{SubagentStatusKey: status}
	if trimmed := strings.TrimSpace(errText); trimmed != "" {
		out[SubagentErrorKey] = trimmed
	}
	return out
}

func isSubagentStatus(status string) bool {
	switch status {
	case SubagentCompleted, SubagentFailed, SubagentCancelled, SubagentTimedOut, SubagentPollingTimedOut:
		return true
	default:
		return false
	}
}
