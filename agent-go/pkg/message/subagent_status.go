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

// ExtractSubagentStatus infers a terminal status from the legacy task result
// prefixes. It is intentionally ordered from most-specific to least-specific.
func ExtractSubagentStatus(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	switch {
	case strings.HasPrefix(trimmed, "Task Succeeded. Result:"):
		return SubagentCompleted, true
	case strings.HasPrefix(trimmed, "Task polling timed out"):
		return SubagentPollingTimedOut, true
	case strings.HasPrefix(trimmed, "Task timed out"):
		return SubagentTimedOut, true
	case strings.HasPrefix(trimmed, "Task cancelled by user"):
		return SubagentCancelled, true
	case strings.HasPrefix(trimmed, "Task failed."):
		return SubagentFailed, true
	case strings.HasPrefix(trimmed, "Error"):
		return SubagentFailed, true
	default:
		return "", false
	}
}

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

// StampSubagentStatus adds metadata when m is a task tool result. Existing
// explicit fields win; this lets task implementations provide a precise error
// while keeping a safe legacy-prefix fallback for older callers.
func StampSubagentStatus(m Message) Message {
	if m.Role != RoleTool || m.Name != "task" {
		return m
	}
	status, ok := ExtractSubagentStatus(m.Content)
	if !ok {
		return m
	}
	if _, exists := m.AdditionalKwargs[SubagentStatusKey]; exists {
		return m
	}
	errText := ""
	if status != SubagentCompleted && status != SubagentCancelled {
		errText = taskErrorText(m.Content, status)
	}
	m.AdditionalKwargs = cloneAdditionalKwargs(m.AdditionalKwargs)
	if m.AdditionalKwargs == nil {
		m.AdditionalKwargs = map[string]any{}
	}
	for key, value := range MakeSubagentAdditionalKwargs(status, errText) {
		m.AdditionalKwargs[key] = value
	}
	return m
}

func taskErrorText(content, status string) string {
	trimmed := strings.TrimSpace(content)
	for _, prefix := range []string{"Task failed. Error:", "Task timed out. Error:", "Task polling timed out"} {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
		}
	}
	if status == SubagentFailed {
		return trimmed
	}
	return ""
}
